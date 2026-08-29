package bus

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.uber.org/zap/zaptest"
)

// blockingSubscriber holds the delivery goroutine inside OnEvent until it is
// released, which is the only way to make the bus's queue fill up on purpose.
type blockingSubscriber struct {
	BaseSubscriber
	entered chan struct{}
	release chan struct{}
}

func newBlockingSubscriber() *blockingSubscriber {
	return &blockingSubscriber{
		entered: make(chan struct{}, 1),
		release: make(chan struct{}),
	}
}

func (s *blockingSubscriber) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	select {
	case s.entered <- struct{}{}:
	default:
	}
	<-s.release
	return nil
}

// capturingSubscriber records every delivery along with the context it arrived
// in, so a test can assert what rode along with the message.
type capturingSubscriber struct {
	BaseSubscriber
	events chan capturedEvent
}

type capturedEvent struct {
	topic   string
	payload any
	ctx     context.Context
}

func newCapturingSubscriber() *capturingSubscriber {
	return &capturingSubscriber{events: make(chan capturedEvent, 16)}
}

func (s *capturingSubscriber) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	s.events <- capturedEvent{topic: topic, payload: message, ctx: ctx}
	return nil
}

func (s *capturingSubscriber) await(t *testing.T) capturedEvent {
	t.Helper()
	select {
	case e := <-s.events:
		return e
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for a delivery")
		return capturedEvent{}
	}
}

func TestQueueDepthAndCapacity(t *testing.T) {
	eventBus, err := NewEventBus().WithLogger(zaptest.NewLogger(t)).Build()
	require.NoError(t, err)

	assert.Equal(t, 1000, eventBus.QueueCapacity(), "default buffer size")
	assert.Equal(t, 0, eventBus.QueueDepth(), "an idle bus holds nothing")

	sized, err := NewEventBus().WithLogger(zaptest.NewLogger(t)).WithBufferSize(7).Build()
	require.NoError(t, err)
	assert.Equal(t, 7, sized.QueueCapacity())
}

func TestQueueDepthReflectsUndispatchedMessages(t *testing.T) {
	eventBus, err := NewEventBus().WithLogger(zaptest.NewLogger(t)).WithBufferSize(4).Build()
	require.NoError(t, err)
	require.NoError(t, eventBus.Start())
	defer eventBus.Stop() //nolint:errcheck

	blocker := newBlockingSubscriber()
	require.NoError(t, eventBus.Subscribe(context.Background(), "work/#", blocker))

	// The first publish is picked up and stalls the delivery goroutine; the
	// next two have nowhere to go but the buffer.
	require.NoError(t, eventBus.Publish(context.Background(), "work/one", "1"))
	<-blocker.entered
	require.NoError(t, eventBus.Publish(context.Background(), "work/two", "2"))
	require.NoError(t, eventBus.Publish(context.Background(), "work/three", "3"))

	assert.Equal(t, 2, eventBus.QueueDepth())
	assert.Equal(t, uint64(0), eventBus.DroppedTotal(), "nothing was refused yet")

	close(blocker.release)
}

func TestDroppedTotalCountsQueueFullDrops(t *testing.T) {
	mp, reader := newTestMeterProvider()

	eventBus, err := NewEventBus().
		WithLogger(zaptest.NewLogger(t)).
		WithBufferSize(1).
		WithMeterProvider(mp).
		Build()
	require.NoError(t, err)
	require.NoError(t, eventBus.Start())
	defer eventBus.Stop() //nolint:errcheck

	blocker := newBlockingSubscriber()
	require.NoError(t, eventBus.Subscribe(context.Background(), "work/#", blocker))

	require.NoError(t, eventBus.Publish(context.Background(), "work/first", "in flight"))
	<-blocker.entered
	require.NoError(t, eventBus.Publish(context.Background(), "work/second", "buffered"))

	// The queue holds one and the delivery goroutine is stalled, so everything
	// from here is refused.
	for i := 0; i < 3; i++ {
		require.NoError(t, eventBus.Publish(context.Background(), "work/overflow", i))
	}

	assert.Equal(t, uint64(3), eventBus.DroppedTotal())

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	assert.True(t, hasMetric(rm, "messaging.client.dropped.messages"),
		"a drop must be a signal, not only a log line")

	close(blocker.release)
}

func TestUndeliveredTotalCountsZeroMatch(t *testing.T) {
	eventBus, err := NewEventBus().WithLogger(zaptest.NewLogger(t)).Build()
	require.NoError(t, err)
	require.NoError(t, eventBus.Start())
	defer eventBus.Stop() //nolint:errcheck

	sub := newCapturingSubscriber()
	require.NoError(t, eventBus.Subscribe(context.Background(), "wanted/#", sub))

	require.NoError(t, eventBus.Publish(context.Background(), "wanted/thing", "matched"))
	sub.await(t)
	assert.Equal(t, uint64(0), eventBus.UndeliveredTotal(), "a matched publish is not undelivered")

	require.NoError(t, eventBus.Publish(context.Background(), "senosr/typo", "async"))
	require.NoError(t, eventBus.PublishSync(context.Background(), "senosr/typo", "sync"))

	assert.Eventually(t, func() bool {
		return eventBus.UndeliveredTotal() == 2
	}, time.Second, 5*time.Millisecond, "both the async and the sync path must count")
}

func TestPublishSyncToNobodyStillReturnsNil(t *testing.T) {
	eventBus, err := NewEventBus().
		WithLogger(zaptest.NewLogger(t)).
		WithUndeliverable(true).
		Build()
	require.NoError(t, err)
	require.NoError(t, eventBus.Start())
	defer eventBus.Stop() //nolint:errcheck

	catcher := newCapturingSubscriber()
	require.NoError(t, eventBus.Subscribe(context.Background(), UndeliverableTopic, catcher))

	// Publishing to nobody is legitimate, so the caller who never asked to be
	// told about it is not told about it — the counter and $undeliverable are
	// where the signal goes.
	assert.NoError(t, eventBus.PublishSync(context.Background(), "nobody/wants/this", "payload"))
	assert.Equal(t, uint64(1), eventBus.UndeliveredTotal())
	catcher.await(t)
}

type republishKey struct{}

func TestUndeliverableRepublishesWithOriginalContext(t *testing.T) {
	eventBus, err := NewEventBus().
		WithLogger(zaptest.NewLogger(t)).
		WithUndeliverable(true).
		Build()
	require.NoError(t, err)
	require.NoError(t, eventBus.Start())
	defer eventBus.Stop() //nolint:errcheck

	catcher := newCapturingSubscriber()
	require.NoError(t, eventBus.Subscribe(context.Background(), UndeliverableTopic, catcher))

	// A value stands in for the settler a receiver puts in the context: what
	// makes this feature worth building is that it survives the republish.
	ctx := context.WithValue(context.Background(), republishKey{}, "settler")
	require.NoError(t, eventBus.Publish(ctx, "sensor/typo", "the payload"))

	got := catcher.await(t)
	assert.Equal(t, UndeliverableTopic, got.topic)
	assert.Equal(t, "the payload", got.payload)
	assert.Equal(t, "settler", got.ctx.Value(republishKey{}))

	original, ok := UndeliverableTopicFromContext(got.ctx)
	assert.True(t, ok, "the handler must be able to see what failed to route")
	assert.Equal(t, "sensor/typo", original)
}

func TestUndeliverableRepublishesFromSyncPublish(t *testing.T) {
	eventBus, err := NewEventBus().
		WithLogger(zaptest.NewLogger(t)).
		WithUndeliverable(true).
		Build()
	require.NoError(t, err)
	require.NoError(t, eventBus.Start())
	defer eventBus.Stop() //nolint:errcheck

	catcher := newCapturingSubscriber()
	require.NoError(t, eventBus.Subscribe(context.Background(), UndeliverableTopic, catcher))

	require.NoError(t, eventBus.PublishSync(context.Background(), "sensor/typo", "sync payload"))

	got := catcher.await(t)
	assert.Equal(t, "sync payload", got.payload)
	original, _ := UndeliverableTopicFromContext(got.ctx)
	assert.Equal(t, "sensor/typo", original)
}

func TestUndeliverableNeverRepublishesDollarTopics(t *testing.T) {
	eventBus, err := NewEventBus().
		WithLogger(zaptest.NewLogger(t)).
		WithUndeliverable(true).
		Build()
	require.NoError(t, err)
	require.NoError(t, eventBus.Start())
	defer eventBus.Stop() //nolint:errcheck

	// Nothing consumes $undeliverable — the normal state for anyone who turns
	// the attribute on and forgets the subscription. The original publish is
	// undelivered, its republish is undelivered too, and there it stops.
	require.NoError(t, eventBus.Publish(context.Background(), "sensor/typo", "payload"))

	assert.Eventually(t, func() bool {
		return eventBus.UndeliveredTotal() == 2
	}, time.Second, 5*time.Millisecond)

	time.Sleep(100 * time.Millisecond)
	assert.Equal(t, uint64(2), eventBus.UndeliveredTotal(),
		"a $-topic must never be republished, or the bus feeds itself forever")
}

func TestUndeliverableIsOffByDefault(t *testing.T) {
	eventBus, err := NewEventBus().WithLogger(zaptest.NewLogger(t)).Build()
	require.NoError(t, err)
	require.NoError(t, eventBus.Start())
	defer eventBus.Stop() //nolint:errcheck

	catcher := newCapturingSubscriber()
	require.NoError(t, eventBus.Subscribe(context.Background(), UndeliverableTopic, catcher))

	require.NoError(t, eventBus.Publish(context.Background(), "sensor/typo", "payload"))

	assert.Eventually(t, func() bool {
		return eventBus.UndeliveredTotal() == 1
	}, time.Second, 5*time.Millisecond, "the counter is always kept")

	select {
	case e := <-catcher.events:
		t.Fatalf("republished %v without WithUndeliverable(true)", e)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestUndeliveredMetricIsRecorded(t *testing.T) {
	mp, reader := newTestMeterProvider()

	eventBus, err := NewEventBus().
		WithLogger(zaptest.NewLogger(t)).
		WithMeterProvider(mp).
		Build()
	require.NoError(t, err)
	require.NoError(t, eventBus.Start())
	defer eventBus.Stop() //nolint:errcheck

	require.NoError(t, eventBus.Publish(context.Background(), "sensor/typo", "payload"))
	assert.Eventually(t, func() bool {
		return eventBus.UndeliveredTotal() == 1
	}, time.Second, 5*time.Millisecond)

	var rm metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(context.Background(), &rm))
	assert.True(t, hasMetric(rm, "messaging.client.undelivered.messages"))
}

// hasMetric reports whether a collected snapshot carries an instrument by name.
func hasMetric(rm metricdata.ResourceMetrics, name string) bool {
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			if m.Name == name {
				return true
			}
		}
	}
	return false
}
