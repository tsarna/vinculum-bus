package bus

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zaptest"
)

// autoSettler and manualSettler name the two configurations a settle point has
// to tell apart, so the tests below read as the table in the design rather than
// as an option list.
func autoSettler(ops SettleOps) Settler   { return NewSettler(ops, AutoSettle()) }
func manualSettler(ops SettleOps) Settler { return NewSettler(ops) }

// passThroughWrapper is a wrapper that participates: it reports what it wraps,
// so Defers can see past it.
type passThroughWrapper struct{ inner Subscriber }

func (w *passThroughWrapper) OnSubscribe(ctx context.Context, topic string) error   { return nil }
func (w *passThroughWrapper) OnUnsubscribe(ctx context.Context, topic string) error { return nil }
func (w *passThroughWrapper) PassThrough(EventBusMessage) error                     { return nil }
func (w *passThroughWrapper) Unwrap() Subscriber                                    { return w.inner }

func (w *passThroughWrapper) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	return w.inner.OnEvent(ctx, topic, message, fields)
}

// opaqueWrapper is the same wrapper with Unwrap forgotten, which is the mistake
// the design says is silent. It exists so a test can show what the mistake
// costs rather than only asserting the correct case.
type opaqueWrapper struct{ inner Subscriber }

func (w *opaqueWrapper) OnSubscribe(ctx context.Context, topic string) error   { return nil }
func (w *opaqueWrapper) OnUnsubscribe(ctx context.Context, topic string) error { return nil }
func (w *opaqueWrapper) PassThrough(EventBusMessage) error                     { return nil }

func (w *opaqueWrapper) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	return w.inner.OnEvent(ctx, topic, message, fields)
}

// deferringSubscriber accepts a message for later and says so.
type deferringSubscriber struct{ BaseSubscriber }

func (d *deferringSubscriber) DeliveryDisposition() Disposition { return Deferred }

// observingSubscriber looks at the message and takes no responsibility for it —
// a debugging tap, an audit printer. It can be told to fail, because an
// observer's failure is its own business and must not reach the broker.
type observingSubscriber struct {
	BaseSubscriber
	err    error
	events chan capturedEvent
}

func newObservingSubscriber() *observingSubscriber {
	return &observingSubscriber{events: make(chan capturedEvent, 16)}
}

func (o *observingSubscriber) DeliveryDisposition() Disposition { return Observed }

func (o *observingSubscriber) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	o.events <- capturedEvent{topic: topic, payload: message, ctx: ctx}
	return o.err
}

// The ordinary case, and the one every leaf gets for free: a subscriber that
// ran the work and returned reports the outcome, and the settle point turns
// that into a broker acknowledgement.
func TestSettleOnReturnAcksASynchronousSuccess(t *testing.T) {
	ops := &fakeOps{}
	ctx := WithSettler(t.Context(), autoSettler(ops))

	SettleOnReturn(ctx, &BaseSubscriber{}, nil)

	acks, nacks, _ := ops.counts()
	assert.Equal(t, 1, acks, "a synchronous success is a handled message")
	assert.Equal(t, 0, nacks)
}

// The defect the whole design exists to remove. A subscriber that only queued
// the message must not have that enqueue reported to the broker as the work.
func TestSettleOnReturnStaysOutOfADeferringCalleesWay(t *testing.T) {
	ops := &fakeOps{}
	ctx := WithSettler(t.Context(), autoSettler(ops))

	SettleOnReturn(ctx, &deferringSubscriber{}, nil)

	acks, nacks, _ := ops.counts()
	assert.Equal(t, 0, acks, "enqueueing is not handling, and must not acknowledge")
	assert.Equal(t, 0, nacks, "nor is it a failure — something else will settle it")
}

// An error nacks whatever the mode. Under manual this looks like taking back a
// decision the configuration asked for, and is not: an unsettled delivery under
// manual is bounded by settle_timeout, whose expiry nacks. The two differ in
// latency and in whether the broker is told why, not in outcome.
func TestSettleOnReturnNacksAnErrorInEitherMode(t *testing.T) {
	for name, build := range map[string]func(SettleOps) Settler{
		"auto":   autoSettler,
		"manual": manualSettler,
	} {
		t.Run(name, func(t *testing.T) {
			ops := &fakeOps{}
			ctx := WithSettler(t.Context(), build(ops))

			SettleOnReturn(ctx, &BaseSubscriber{}, errors.New("the action threw"))

			acks, nacks, _ := ops.counts()
			assert.Equal(t, 0, acks)
			assert.Equal(t, 1, nacks, "a failure goes back to the broker in either mode")
			assert.Equal(t, []string{"the action threw"}, ops.reasons,
				"and it says what failed, rather than that a timer expired")
		})
	}
}

// The other half of manual, and the half that makes it worth having: nobody
// settles a success but the configuration.
func TestSettleOnReturnDoesNotAckUnderManual(t *testing.T) {
	ops := &fakeOps{}
	ctx := WithSettler(t.Context(), manualSettler(ops))

	SettleOnReturn(ctx, &BaseSubscriber{}, nil)

	acks, nacks, _ := ops.counts()
	assert.Equal(t, 0, acks, "manual means the configuration decides")
	assert.Equal(t, 0, nacks)
}

// Most messages never came from a broker. Settling has to be free for them, and
// safe to call from code that does not know where the message came from.
func TestSettleOnReturnDoesNothingWithoutASettler(t *testing.T) {
	assert.NotPanics(t, func() {
		SettleOnReturn(t.Context(), &BaseSubscriber{}, nil)
		SettleOnReturn(t.Context(), nil, errors.New("boom"))
		SettleRefused(t.Context(), "no subscriber")
	})
}

// A nil callee is how a subscriber that already deferred settles at its own
// completion point — an event loop after its hooks, a produce callback after
// the broker answered. There is no callee left to ask, and nothing to defer to.
func TestSettleOnReturnWithNoCalleeSettlesAtACompletionPoint(t *testing.T) {
	ops := &fakeOps{}
	ctx := WithSettler(t.Context(), autoSettler(ops))

	SettleOnReturn(ctx, nil, nil)

	acks, _, _ := ops.counts()
	assert.Equal(t, 1, acks, "a completion point with no callee is the work finishing")
}

// The silent failure. A wrapper that hides what it wraps makes a deferring
// subscriber look synchronous, and the settle point acknowledges the message at
// the moment it was queued — with no error and no log line anywhere. This test
// asserts both halves so the cost of forgetting Unwrap is written down.
func TestDispositionSeesThroughWrappersThatSayWhatTheyWrap(t *testing.T) {
	deferring := &deferringSubscriber{}
	observing := newObservingSubscriber()

	chain := &passThroughWrapper{inner: &passThroughWrapper{inner: deferring}}
	assert.Equal(t, Deferred, DispositionOf(chain),
		"a disposition must survive any depth of transparent wrapper")

	assert.Equal(t, Observed, DispositionOf(&passThroughWrapper{inner: observing}),
		"and so must an observer's, or a tap behind a transform acknowledges")

	assert.Equal(t, Handled, DispositionOf(&passThroughWrapper{inner: &BaseSubscriber{}}),
		"a wrapper around an ordinary subscriber is ordinary")

	assert.Equal(t, Handled, DispositionOf(&opaqueWrapper{inner: deferring}),
		"a wrapper that does not implement Unwrap hides what it wraps — which "+
			"is exactly why every wrapper in this module has one")

	assert.Equal(t, Handled, DispositionOf(nil), "there is nothing to ask")
	assert.Equal(t, Handled, DispositionOf(&passThroughWrapper{inner: nil}), "nor here")
}

// The rule an observer exists for. Neither half of a tap's outcome is about the
// delivery: it must not acknowledge a message it merely looked at, and it must
// not send real traffic back for redelivery because it failed to print one.
func TestAnObserverSettlesNothingEitherWay(t *testing.T) {
	t.Run("a successful observer does not acknowledge", func(t *testing.T) {
		ops := &fakeOps{}
		ctx := WithSettler(t.Context(), autoSettler(ops))

		SettleOnReturn(ctx, newObservingSubscriber(), nil)

		acks, nacks, _ := ops.counts()
		assert.Equal(t, 0, acks, "observation must not take responsibility")
		assert.Equal(t, 0, nacks)
	})

	t.Run("a failing observer does not nack", func(t *testing.T) {
		ops := &fakeOps{}
		ctx := WithSettler(t.Context(), autoSettler(ops))

		observer := newObservingSubscriber()
		observer.err = errors.New("could not format the message")
		SettleOnReturn(ctx, observer, observer.err)

		acks, nacks, _ := ops.counts()
		assert.Equal(t, 0, acks)
		assert.Equal(t, 0, nacks,
			"a broken debugging tap must not redeliver production traffic")
	})
}

// A message only observers matched has been taken up by nobody, so it takes the
// same path as one that matched nothing at all. That is what keeps attaching a
// tap from changing delivery — and from silencing the undelivered counter,
// which is the diagnostic for a topic pattern that was meant to match.
func TestAMessageOnlyObserversSawIsUndelivered(t *testing.T) {
	eventBus, err := NewEventBus().WithLogger(zaptest.NewLogger(t)).Build()
	require.NoError(t, err)
	require.NoError(t, eventBus.Start())
	defer eventBus.Stop() //nolint:errcheck

	tap := newObservingSubscriber()
	require.NoError(t, eventBus.Subscribe(context.Background(), "work/#", tap))

	ops := &fakeOps{}
	ctx := WithSettler(context.Background(), autoSettler(ops))
	require.NoError(t, eventBus.Publish(ctx, "work/one", "payload"))

	select {
	case <-tap.events:
	case <-time.After(2 * time.Second):
		t.Fatal("the tap should still see the message")
	}

	assert.Eventually(t, func() bool {
		return eventBus.UndeliveredTotal() == 1
	}, 2*time.Second, 5*time.Millisecond,
		"attaching a tap must not silence the undelivered counter")

	// undeliverable is off, so nothing wanted it and nothing asked to be told:
	// the zero-match rule acknowledges. The point is that the *tap* did not.
	assert.Eventually(t, func() bool {
		acks, _, _ := ops.counts()
		return acks == 1
	}, 2*time.Second, 5*time.Millisecond)
}

// A tap alongside a real subscriber must not change what the real subscriber's
// outcome means, in either direction.
func TestATapAlongsideARealSubscriberChangesNothing(t *testing.T) {
	eventBus, err := NewEventBus().WithLogger(zaptest.NewLogger(t)).Build()
	require.NoError(t, err)
	require.NoError(t, eventBus.Start())
	defer eventBus.Stop() //nolint:errcheck

	tap := newObservingSubscriber()
	tap.err = errors.New("the tap is broken")
	worker := newCapturingSubscriber()
	require.NoError(t, eventBus.Subscribe(context.Background(), "work/#", tap))
	require.NoError(t, eventBus.Subscribe(context.Background(), "work/#", worker))

	ops := &fakeOps{}
	ctx := WithSettler(context.Background(), autoSettler(ops))
	require.NoError(t, eventBus.Publish(ctx, "work/one", "payload"))

	worker.await(t)
	assert.Eventually(t, func() bool {
		acks, _, _ := ops.counts()
		return acks == 1
	}, 2*time.Second, 5*time.Millisecond, "the real subscriber's success settles it")

	time.Sleep(50 * time.Millisecond)
	acks, nacks, _ := ops.counts()
	assert.Equal(t, 1, acks)
	assert.Equal(t, 0, nacks, "the broken tap must not have nacked it")
	assert.Equal(t, uint64(0), eventBus.UndeliveredTotal(),
		"a real subscriber matched, so the message was delivered")
}

// Refusal is not failure. Nothing ran, so there is no author decision to
// preempt, and the message goes back in either mode.
func TestSettleRefusedNacksWhateverTheMode(t *testing.T) {
	for name, build := range map[string]func(SettleOps) Settler{
		"auto":   autoSettler,
		"manual": manualSettler,
	} {
		t.Run(name, func(t *testing.T) {
			ops := &fakeOps{}
			ctx := WithSettler(t.Context(), build(ops))

			SettleRefused(ctx, "queue full")

			_, nacks, _ := ops.counts()
			assert.Equal(t, 1, nacks)
			assert.Equal(t, []string{"queue full"}, ops.reasons)
		})
	}
}

// Deriving a new message from a delivery must not hand the new message the old
// one's responsibility, or three derived messages would race to settle it and
// the winner would be arbitrary.
func TestWithoutSettlerStopsTheHandleTravelling(t *testing.T) {
	ops := &fakeOps{}
	ctx := WithSettler(t.Context(), autoSettler(ops))
	require.NotNil(t, SettlerFromContext(ctx))

	derived := WithoutSettler(ctx)
	assert.Nil(t, SettlerFromContext(derived))

	SettleOnReturn(derived, &BaseSubscriber{}, nil)
	acks, _, _ := ops.counts()
	assert.Equal(t, 0, acks, "work on a derived message must not settle the original")

	// The original is untouched and still settles where it should.
	SettleOnReturn(ctx, &BaseSubscriber{}, nil)
	acks, _, _ = ops.counts()
	assert.Equal(t, 1, acks)
}

// A settle point hands the nack whatever error the work returned, and an
// expression failure renders as a multi-line diagnostic quoting the source. The
// reason reaches a dead-letter header, so it is bounded once here rather than
// once per protocol — and after truncation it is still valid UTF-8.
func TestNackReasonIsBounded(t *testing.T) {
	ops := &fakeOps{}
	s := NewSettler(ops)

	_, err := s.Nack(t.Context(), strings.Repeat("é", 2000))
	require.NoError(t, err)

	require.Len(t, ops.reasons, 1)
	got := ops.reasons[0]
	assert.LessOrEqual(t, len(got), maxNackReasonBytes)
	assert.True(t, strings.HasSuffix(got, "…"), "a truncated reason should say it was cut")
	assert.True(t, utf8ValidString(got), "truncation must not cut a rune in half")

	short := &fakeOps{}
	_, err = NewSettler(short).Nack(t.Context(), "brief")
	require.NoError(t, err)
	assert.Equal(t, []string{"brief"}, short.reasons, "a reason that fits is untouched")
}

func utf8ValidString(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

// The bus row of the design's table, and the reason a bus declares itself
// deferring: the acknowledgement must follow the subscriber's work, not the
// hop that handed the message to it.
func TestSettleFollowsTheWorkAcrossABusHop(t *testing.T) {
	eventBus, err := NewEventBus().WithLogger(zaptest.NewLogger(t)).Build()
	require.NoError(t, err)
	require.NoError(t, eventBus.Start())
	defer eventBus.Stop() //nolint:errcheck

	blocker := newBlockingSubscriber()
	require.NoError(t, eventBus.Subscribe(context.Background(), "work/#", blocker))

	ops := &fakeOps{}
	ctx := WithSettler(context.Background(), autoSettler(ops))
	require.NoError(t, eventBus.Publish(ctx, "work/one", "payload"))

	<-blocker.entered
	acks, _, _ := ops.counts()
	assert.Equal(t, 0, acks, "the subscriber is still working; nothing has been handled yet")

	close(blocker.release)
	assert.Eventually(t, func() bool {
		acks, _, _ := ops.counts()
		return acks == 1
	}, 2*time.Second, 5*time.Millisecond, "the ack should follow the work, not the hop")
}

// A bus subscribed to another bus is where DefersDelivery earns its keep. The
// upstream bus hands the message to the downstream one, which returns as soon
// as the message is on its channel — so the settle has to wait for the leaf at
// the far end, however many hops away it is.
func TestSettleWaitsForTheLeafAcrossTwoBuses(t *testing.T) {
	upstream, err := NewEventBus().WithLogger(zaptest.NewLogger(t)).Build()
	require.NoError(t, err)
	require.NoError(t, upstream.Start())
	defer upstream.Stop() //nolint:errcheck

	downstream, err := NewEventBus().WithLogger(zaptest.NewLogger(t)).Build()
	require.NoError(t, err)
	require.NoError(t, downstream.Start())
	defer downstream.Stop() //nolint:errcheck

	require.NoError(t, upstream.Subscribe(context.Background(), "work/#", downstream))

	blocker := newBlockingSubscriber()
	require.NoError(t, downstream.Subscribe(context.Background(), "work/#", blocker))

	ops := &fakeOps{}
	ctx := WithSettler(context.Background(), autoSettler(ops))
	require.NoError(t, upstream.Publish(ctx, "work/one", "payload"))

	<-blocker.entered
	acks, _, _ := ops.counts()
	assert.Equal(t, 0, acks,
		"handing the message to the downstream bus is not handling it")

	close(blocker.release)
	assert.Eventually(t, func() bool {
		acks, _, _ := ops.counts()
		return acks == 1
	}, 2*time.Second, 5*time.Millisecond, "the leaf finishing is what settles it")
}

// Publish returns nil whether or not the message was accepted, so a refusal
// here is invisible to everything upstream. Nacking is the only way the bus can
// say it dropped something that was acknowledged elsewhere.
func TestAFullBusChannelNacks(t *testing.T) {
	eventBus, err := NewEventBus().
		WithLogger(zaptest.NewLogger(t)).
		WithBufferSize(1).
		Build()
	require.NoError(t, err)
	require.NoError(t, eventBus.Start())
	defer eventBus.Stop() //nolint:errcheck

	blocker := newBlockingSubscriber()
	// Released before the bus is stopped, and by a defer rather than at the end
	// of the test: a failing assertion below would otherwise leave the delivery
	// goroutine parked inside OnEvent, and Stop waits for it. A test that hangs
	// on failure reports a timeout instead of the assertion that caught the bug.
	defer close(blocker.release)
	require.NoError(t, eventBus.Subscribe(context.Background(), "work/#", blocker))

	require.NoError(t, eventBus.Publish(context.Background(), "work/first", "in flight"))
	<-blocker.entered
	require.NoError(t, eventBus.Publish(context.Background(), "work/second", "buffered"))

	ops := &fakeOps{}
	ctx := WithSettler(context.Background(), autoSettler(ops))
	require.NoError(t, eventBus.Publish(ctx, "work/refused", "nowhere to go"))

	_, nacks, _ := ops.counts()
	assert.Equal(t, 1, nacks, "a refused message goes back to the broker rather than being lost")
	if assert.Len(t, ops.reasons, 1) {
		assert.Contains(t, ops.reasons[0], "queue full")
	}
}

// Nobody asked to hear about undeliverable messages, so a topic matching no
// subscription is a routing outcome the configuration chose. Nacking instead
// would turn an unsubscribed topic into a redelivery loop.
func TestZeroMatchAcksWhenUndeliverableIsOff(t *testing.T) {
	eventBus, err := NewEventBus().WithLogger(zaptest.NewLogger(t)).Build()
	require.NoError(t, err)
	require.NoError(t, eventBus.Start())
	defer eventBus.Stop() //nolint:errcheck

	ops := &fakeOps{}
	ctx := WithSettler(context.Background(), autoSettler(ops))
	require.NoError(t, eventBus.Publish(ctx, "sensor/nobody-wants-this", "payload"))

	assert.Eventually(t, func() bool {
		acks, _, _ := ops.counts()
		return acks == 1
	}, 2*time.Second, 5*time.Millisecond, "nothing wanted it, and nothing asked to be told")

	_, nacks, _ := ops.counts()
	assert.Equal(t, 0, nacks)
	assert.Equal(t, uint64(1), eventBus.UndeliveredTotal(), "the counter is still the diagnostic")
}

// With undeliverable on, the republished message reaches a real subscription
// and that subscription decides — which is how a configuration says "nothing
// wanted this and that is fine" or "send it again", without new vocabulary.
func TestZeroMatchLetsTheUndeliverableHandlerDecide(t *testing.T) {
	t.Run("a handler that returns acknowledges", func(t *testing.T) {
		eventBus, err := NewEventBus().
			WithLogger(zaptest.NewLogger(t)).
			WithUndeliverable(true).
			Build()
		require.NoError(t, err)
		require.NoError(t, eventBus.Start())
		defer eventBus.Stop() //nolint:errcheck

		catcher := newCapturingSubscriber()
		require.NoError(t, eventBus.Subscribe(context.Background(), UndeliverableTopic, catcher))

		ops := &fakeOps{}
		ctx := WithSettler(context.Background(), autoSettler(ops))
		require.NoError(t, eventBus.Publish(ctx, "sensor/typo", "payload"))

		catcher.await(t)
		assert.Eventually(t, func() bool {
			acks, _, _ := ops.counts()
			return acks == 1
		}, 2*time.Second, 5*time.Millisecond)

		_, nacks, _ := ops.counts()
		assert.Equal(t, 0, nacks, "the handler ran and returned; that is a handled message")
	})

	t.Run("a handler that refuses asks for redelivery", func(t *testing.T) {
		eventBus, err := NewEventBus().
			WithLogger(zaptest.NewLogger(t)).
			WithUndeliverable(true).
			Build()
		require.NoError(t, err)
		require.NoError(t, eventBus.Start())
		defer eventBus.Stop() //nolint:errcheck

		refuser := NewEventReceiver(func(ctx context.Context, topic string, message any, fields map[string]string) error {
			SettleRefused(ctx, "no handler for this topic")
			return nil
		})
		require.NoError(t, eventBus.Subscribe(context.Background(), UndeliverableTopic, refuser))

		ops := &fakeOps{}
		ctx := WithSettler(context.Background(), autoSettler(ops))
		require.NoError(t, eventBus.Publish(ctx, "sensor/typo", "payload"))

		assert.Eventually(t, func() bool {
			_, nacks, _ := ops.counts()
			return nacks == 1
		}, 2*time.Second, 5*time.Millisecond)

		acks, _, _ := ops.counts()
		assert.Equal(t, 0, acks, "the handler settled first, and settle-once means it wins")
	})
}

// The author turned undeliverable on — tell me about these — and then left
// $undeliverable itself unhandled. That is the one zero-match that is a mistake
// rather than a configuration, and it is the end of the line: the $-prefix
// guard means there is nowhere further to republish.
func TestUndeliverableThatItselfMatchesNothingNacks(t *testing.T) {
	eventBus, err := NewEventBus().
		WithLogger(zaptest.NewLogger(t)).
		WithUndeliverable(true).
		Build()
	require.NoError(t, err)
	require.NoError(t, eventBus.Start())
	defer eventBus.Stop() //nolint:errcheck

	ops := &fakeOps{}
	ctx := WithSettler(context.Background(), autoSettler(ops))
	require.NoError(t, eventBus.Publish(ctx, "sensor/typo", "payload"))

	assert.Eventually(t, func() bool {
		_, nacks, _ := ops.counts()
		return nacks == 1
	}, 2*time.Second, 5*time.Millisecond, "asking to be told and not listening is a mistake")

	acks, _, _ := ops.counts()
	assert.Equal(t, 0, acks)
}

// An acknowledgement means someone took responsibility, not that everyone
// finished. Two subscribers both handle the message; the broker hears once.
func TestFanOutSettlesOnce(t *testing.T) {
	eventBus, err := NewEventBus().WithLogger(zaptest.NewLogger(t)).Build()
	require.NoError(t, err)
	require.NoError(t, eventBus.Start())
	defer eventBus.Stop() //nolint:errcheck

	first := newCapturingSubscriber()
	second := newCapturingSubscriber()
	require.NoError(t, eventBus.Subscribe(context.Background(), "work/#", first))
	require.NoError(t, eventBus.Subscribe(context.Background(), "work/#", second))

	ops := &fakeOps{}
	ctx := WithSettler(context.Background(), autoSettler(ops))
	require.NoError(t, eventBus.Publish(ctx, "work/one", "payload"))

	first.await(t)
	second.await(t)

	assert.Eventually(t, func() bool {
		acks, _, _ := ops.counts()
		return acks == 1
	}, 2*time.Second, 5*time.Millisecond)

	// Give the second delivery's settle attempt time to be the no-op it should
	// be, rather than asserting on a race it has not yet lost.
	time.Sleep(50 * time.Millisecond)
	acks, nacks, _ := ops.counts()
	assert.Equal(t, 1, acks, "two subscribers, one broker acknowledgement")
	assert.Equal(t, 0, nacks)
}

// A delivery under auto that the configuration also settles by hand settles
// once, and the framework's later call reports that it was not the one.
func TestAnExplicitSettleOutranksTheFrameworks(t *testing.T) {
	ops := &fakeOps{}
	settler := autoSettler(ops)
	ctx := WithSettler(t.Context(), settler)

	settledByHand, err := settler.Ack(ctx)
	require.NoError(t, err)
	require.True(t, settledByHand)

	SettleOnReturn(ctx, &BaseSubscriber{}, nil)

	acks, _, _ := ops.counts()
	assert.Equal(t, 1, acks, "the framework's call is a no-op once the config has settled")
}
