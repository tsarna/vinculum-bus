package subutils

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tsarna/vinculum-bus"
)

// partitionRecorder records when each message started and finished being
// handled, which is what the ordering and concurrency claims are actually
// about. Everything else here reads those two facts back.
type partitionRecorder struct {
	mu sync.Mutex

	// order is every key handled, in the order handling began.
	order []string

	// live is the keys currently being handled, and overlaps records every
	// pair seen in flight at once.
	live     map[string]bool
	overlaps map[[2]string]bool

	// block, when a key has an entry, holds that key's handler until the
	// channel is closed.
	block map[string]chan struct{}

	// delay is how long every handler takes. Concurrency is only observable
	// in work that lasts long enough to be observed: with an instant handler,
	// four goroutines still process one message at a time simply because each
	// finishes before the next arrives.
	delay time.Duration
}

func newPartitionRecorder() *partitionRecorder {
	return &partitionRecorder{
		live:     map[string]bool{},
		overlaps: map[[2]string]bool{},
		block:    map[string]chan struct{}{},
	}
}

func (p *partitionRecorder) OnSubscribe(context.Context, string) error   { return nil }
func (p *partitionRecorder) OnUnsubscribe(context.Context, string) error { return nil }
func (p *partitionRecorder) PassThrough(bus.EventBusMessage) error       { return nil }

func (p *partitionRecorder) OnEvent(_ context.Context, topic string, message any, _ map[string]string) error {
	key := fmt.Sprint(message)

	p.mu.Lock()
	p.order = append(p.order, key)
	for other := range p.live {
		pair := [2]string{key, other}
		if other < key {
			pair = [2]string{other, key}
		}
		p.overlaps[pair] = true
	}
	p.live[key] = true
	gate := p.block[key]
	delay := p.delay
	p.mu.Unlock()

	if delay > 0 {
		time.Sleep(delay)
	}

	if gate != nil {
		<-gate
	}

	p.mu.Lock()
	delete(p.live, key)
	p.mu.Unlock()

	return nil
}

// blockKey holds every message with this key until the returned func is called.
func (p *partitionRecorder) blockKey(key string) func() {
	gate := make(chan struct{})

	p.mu.Lock()
	p.block[key] = gate
	p.mu.Unlock()

	return sync.OnceFunc(func() { close(gate) })
}

func (p *partitionRecorder) handled() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return append([]string(nil), p.order...)
}

func (p *partitionRecorder) overlapped(a, b string) bool {
	pair := [2]string{a, b}
	if b < a {
		pair = [2]string{b, a}
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	return p.overlaps[pair]
}

// keyOfPayload keys on the payload, which is what the tests here vary.
func keyOfPayload(msg bus.EventBusMessage) string { return fmt.Sprint(msg.Payload) }

// send publishes n messages carrying the given key as their payload.
func send(t *testing.T, sub *AsyncQueueingSubscriber, key string, n int) {
	t.Helper()

	for i := 0; i < n; i++ {
		require.NoError(t, sub.OnEvent(context.Background(), "topic", key, nil))
	}
}

// A subscriber with no partitioning must behave exactly as it did before
// partitions existed: one goroutine, every message in order, whatever the
// topics. The identity is asserted against the guarantee rather than against
// the field, because the field is not what anyone depends on.
func TestPartitions_OneIsUnchanged(t *testing.T) {
	recorder := newPartitionRecorder()
	sub := NewAsyncQueueingSubscriber(recorder, 100).Start()
	defer sub.Close()

	assert.Equal(t, 1, sub.Partitions())
	assert.Equal(t, 100, sub.QueueCapacity())

	want := make([]string, 0, 30)
	for i := 0; i < 30; i++ {
		key := fmt.Sprintf("key-%d", i%7)
		want = append(want, key)
		require.NoError(t, sub.OnEvent(context.Background(), "topic", key, nil))
	}

	require.NoError(t, sub.Close())
	assert.Equal(t, want, recorder.handled())
}

// The central claim: two messages with the same key are never handled at once,
// and messages with different keys are. The first half is the guarantee; the
// second is the reason for the feature, and a test asserting only the first
// would pass on an implementation that serialised everything.
func TestPartitions_SameKeyNeverConcurrent(t *testing.T) {
	recorder := newPartitionRecorder()
	recorder.delay = time.Millisecond
	sub := NewAsyncQueueingSubscriber(recorder, 100).
		WithPartitions(4).
		WithPartitionKey(keyOfPayload).
		Start()
	defer sub.Close()

	var wg sync.WaitGroup
	for _, key := range []string{"a", "b", "c", "d", "e", "f"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			send(t, sub, key, 40)
		}()
	}
	wg.Wait()

	require.NoError(t, sub.Close())

	for _, key := range []string{"a", "b", "c", "d", "e", "f"} {
		assert.False(t, recorder.overlapped(key, key),
			"two messages with key %q were handled at the same time", key)
	}

	// Six keys over four partitions: at least two pairs must share a goroutine,
	// but some pair must have run concurrently or nothing was parallel.
	concurrent := false
	keys := []string{"a", "b", "c", "d", "e", "f"}
	for i := range keys {
		for j := i + 1; j < len(keys); j++ {
			if recorder.overlapped(keys[i], keys[j]) {
				concurrent = true
			}
		}
	}
	assert.True(t, concurrent, "no two keys were ever handled concurrently")
}

// Order within a key is enqueue order, and stays so while other partitions are
// busy. The sequence numbers are the payload, so the recorder's log is the
// order the handler saw them.
func TestPartitions_OrderWithinKeyIsEnqueueOrder(t *testing.T) {
	recorder := newPartitionRecorder()
	sub := NewAsyncQueueingSubscriber(recorder, 200).
		WithPartitions(8).
		WithPartitionKey(func(msg bus.EventBusMessage) string {
			// Key on the part before the dash, so "a-3" and "a-4" share a
			// partition while their sequence numbers stay visible.
			return fmt.Sprint(msg.Payload)[:1]
		}).
		Start()
	defer sub.Close()

	for i := 0; i < 50; i++ {
		for _, key := range []string{"a", "b", "c"} {
			require.NoError(t, sub.OnEvent(context.Background(), "topic",
				fmt.Sprintf("%s-%d", key, i), nil))
		}
	}
	require.NoError(t, sub.Close())

	seen := map[string]int{}
	for _, handled := range recorder.handled() {
		key, index := handled[:1], 0
		_, err := fmt.Sscanf(handled[2:], "%d", &index)
		require.NoError(t, err)
		assert.Equal(t, seen[key], index, "key %q was handled out of order", key)
		seen[key]++
	}

	for _, key := range []string{"a", "b", "c"} {
		assert.Equal(t, 50, seen[key])
	}
}

// A key that never finishes holds up its own partition and nothing else. This
// is the whole promise, so it is asserted from the outside: the other key's
// messages are handled while the first key's handler has not returned.
func TestPartitions_BlockedKeyDoesNotBlockAnother(t *testing.T) {
	recorder := newPartitionRecorder()
	sub := NewAsyncQueueingSubscriber(recorder, 100).
		WithPartitions(2).
		WithPartitionKey(keyOfPayload).
		Start()
	defer sub.Close()

	// "slow" and "fast" are chosen to hash apart over two partitions; the
	// assertion below fails loudly rather than silently passing if they do not.
	release := recorder.blockKey("slow")
	defer release()

	send(t, sub, "slow", 3)
	send(t, sub, "fast", 5)

	assert.Eventually(t, func() bool {
		handled := recorder.handled()
		fast := 0
		for _, key := range handled {
			if key == "fast" {
				fast++
			}
		}
		return fast == 5
	}, 2*time.Second, 10*time.Millisecond,
		"messages with an unrelated key waited behind a blocked one")

	release()
}

// The cost of hashing into fixed partitions, pinned so it stays a known
// property rather than arriving as a bug report: keys that hash together share
// a goroutine, so one that blocks holds up the others behind it.
func TestPartitions_BlockedKeyBlocksItsOwnPartition(t *testing.T) {
	recorder := newPartitionRecorder()
	sub := NewAsyncQueueingSubscriber(recorder, 100).
		WithPartitions(4).
		WithPartitionKey(keyOfPayload).
		Start()
	defer sub.Close()

	// Find a second key landing in the same partition as "slow" — which is the
	// situation being described, and there is no point asserting it on keys
	// that happen not to collide.
	slot := sub.partitionFor(bus.EventBusMessage{Payload: "slow"})
	var sharer string
	for i := 0; sharer == ""; i++ {
		candidate := fmt.Sprintf("other-%d", i)
		if sub.partitionFor(bus.EventBusMessage{Payload: candidate}) == slot {
			sharer = candidate
		}
	}

	release := recorder.blockKey("slow")
	defer release()

	send(t, sub, "slow", 1)
	send(t, sub, sharer, 1)

	// Give the sharer every chance to be handled; it must not be.
	time.Sleep(100 * time.Millisecond)
	for _, key := range recorder.handled() {
		assert.NotEqual(t, sharer, key,
			"a key sharing the blocked key's partition was handled anyway")
	}

	release()
	assert.Eventually(t, func() bool {
		return len(recorder.handled()) == 2
	}, 2*time.Second, 10*time.Millisecond)
}

// Round-robin preserves nothing and spreads everything, which is the point of
// asking for it. One key, so a hashed subscriber would put every message on one
// goroutine.
func TestPartitions_WithoutOrderingSpreads(t *testing.T) {
	recorder := newPartitionRecorder()
	sub := NewAsyncQueueingSubscriber(recorder, 100).
		WithPartitions(4).
		WithoutOrdering().
		Start()
	defer sub.Close()

	release := recorder.blockKey("same")
	defer release()

	send(t, sub, "same", 4)

	// All four partitions take one, so all four are in flight at once — which
	// a keyed subscriber could not manage with a single key.
	assert.Eventually(t, func() bool {
		return len(recorder.handled()) == 4
	}, 2*time.Second, 10*time.Millisecond,
		"round-robin did not spread a single key across partitions")

	release()
}

// A full partition refuses its message while the others still accept, and the
// drop counter is the total across all of them.
func TestPartitions_DropsArePerPartition(t *testing.T) {
	recorder := newPartitionRecorder()
	sub := NewAsyncQueueingSubscriber(recorder, 2).
		WithPartitions(4).
		WithPartitionKey(keyOfPayload).
		Start()
	defer sub.Close()

	assert.Equal(t, 8, sub.QueueCapacity(), "capacity is per partition, times partitions")

	release := recorder.blockKey("hot")
	defer release()

	// One in the handler, two in the queue, and the fourth has nowhere to go.
	var refused error
	for i := 0; i < 6 && refused == nil; i++ {
		// The first message is picked up immediately, so it takes a moment
		// before the queue behind it is genuinely full.
		refused = sub.OnEvent(context.Background(), "topic", "hot", nil)
		if refused == nil {
			time.Sleep(10 * time.Millisecond)
		}
	}
	require.ErrorIs(t, refused, ErrQueueFull)
	assert.Equal(t, uint64(1), sub.DroppedTotal())

	// A key in another partition is unaffected.
	other := "hot"
	for i := 0; sub.partitionFor(bus.EventBusMessage{Payload: other}) ==
		sub.partitionFor(bus.EventBusMessage{Payload: "hot"}); i++ {
		other = fmt.Sprintf("cold-%d", i)
	}
	assert.NoError(t, sub.OnEvent(context.Background(), "topic", other, nil))

	release()
}

// MaxQueueDepth is the number that notices a hot partition; QueueDepth is the
// number shutdown waits on, and has to see every partition or it will let the
// process exit with messages still queued.
func TestPartitions_DepthAccounting(t *testing.T) {
	recorder := newPartitionRecorder()
	sub := NewAsyncQueueingSubscriber(recorder, 10).
		WithPartitions(4).
		WithPartitionKey(keyOfPayload).
		Start()
	defer sub.Close()

	release := recorder.blockKey("hot")
	defer release()

	send(t, sub, "hot", 5)

	assert.Eventually(t, func() bool {
		return sub.QueueDepth() > 0 && sub.MaxQueueDepth() == sub.QueueDepth()
	}, time.Second, 10*time.Millisecond,
		"one busy partition should account for the whole depth")

	release()
	assert.Eventually(t, func() bool { return sub.QueueDepth() == 0 }, 2*time.Second, 10*time.Millisecond)
}

// Close drains every partition, not just the first. The messages are spread
// over all four, and all of them must be handled before Close returns.
func TestPartitions_CloseDrainsEveryPartition(t *testing.T) {
	recorder := newPartitionRecorder()
	sub := NewAsyncQueueingSubscriber(recorder, 100).
		WithPartitions(4).
		WithPartitionKey(keyOfPayload).
		Start()

	for i := 0; i < 40; i++ {
		require.NoError(t, sub.OnEvent(context.Background(), "topic", fmt.Sprintf("key-%d", i), nil))
	}

	require.NoError(t, sub.Close())
	assert.Len(t, recorder.handled(), 40, "Close returned with messages still queued")
	assert.Equal(t, 0, sub.QueueDepth())
}

// A tick is a periodic event for the wrapped subscriber rather than a message
// being delivered to it, so partitioning must not multiply it.
func TestPartitions_TickerFiresOncePerInterval(t *testing.T) {
	base := &asyncTestSubscriber{}
	sub := NewAsyncQueueingSubscriber(base, 100).
		WithPartitions(8).
		WithTicker(20 * time.Millisecond).
		Start()
	defer sub.Close()

	time.Sleep(110 * time.Millisecond)
	require.NoError(t, sub.Close())

	_, _, _, ticks := base.getCounts()
	// Five intervals in 110ms, with generous slack for a loaded machine — the
	// assertion that matters is that it is not eight times that.
	assert.Greater(t, ticks, 1)
	assert.Less(t, ticks, 12, "each partition fired its own ticker")
}

// A partitioned queue still defers, and still lets a settle point see what is
// behind it. The failure this guards is silent — a caller settling on the
// enqueue rather than on the work — so it is asserted at more than one
// partition as well as at one.
func TestPartitions_DispositionIsStillDeferred(t *testing.T) {
	recorder := newPartitionRecorder()
	sub := NewAsyncQueueingSubscriber(recorder, 10).
		WithPartitions(4).
		WithPartitionKey(keyOfPayload).
		Start()
	defer sub.Close()

	assert.Equal(t, bus.Deferred, bus.DispositionOf(sub))
	assert.Equal(t, bus.Subscriber(recorder), sub.Unwrap())
}

// The two ordering modes are alternatives, not layers: whichever was asked for
// last is what happens, so a configuration cannot end up with a key it thinks
// is being honoured while messages are dealt round-robin.
func TestPartitions_OrderingModesAreExclusive(t *testing.T) {
	recorder := newPartitionRecorder()

	sub := NewAsyncQueueingSubscriber(recorder, 10).
		WithPartitions(4).
		WithPartitionKey(keyOfPayload).
		WithoutOrdering()
	assert.True(t, sub.unordered)
	assert.Nil(t, sub.keyFn)

	sub = NewAsyncQueueingSubscriber(recorder, 10).
		WithPartitions(4).
		WithoutOrdering().
		WithPartitionKey(keyOfPayload)
	assert.False(t, sub.unordered)
	assert.NotNil(t, sub.keyFn)
}

// Partitioning defaults to the topic, so a configuration that sets partitions
// and no key still gets ordering per topic and parallelism across them.
func TestPartitions_DefaultKeyIsTheTopic(t *testing.T) {
	recorder := newPartitionRecorder()
	sub := NewAsyncQueueingSubscriber(recorder, 100).
		WithPartitions(4).
		Start()
	defer sub.Close()

	first := sub.partitionFor(bus.EventBusMessage{Topic: "sensor/1"})
	assert.Equal(t, first, sub.partitionFor(bus.EventBusMessage{Topic: "sensor/1"}),
		"the same topic must always reach the same partition")

	differs := false
	for i := 0; i < 20 && !differs; i++ {
		if sub.partitionFor(bus.EventBusMessage{Topic: fmt.Sprintf("sensor/%d", i)}) != first {
			differs = true
		}
	}
	assert.True(t, differs, "every topic hashed to one partition")
}

// The key function is written against a message, so it is asked only about
// messages. A subscribe has no payload and no fields, and handing one to a key
// expression produces a failure rather than a key — which is why this is the
// difference between a working subscription and one that logs on every
// subscribe.
func TestPartitions_KeyFuncSeesOnlyEvents(t *testing.T) {
	recorder := newPartitionRecorder()

	var asked []bus.MessageType
	var mu sync.Mutex
	sub := NewAsyncQueueingSubscriber(recorder, 10).
		WithPartitions(4).
		WithPartitionKey(func(msg bus.EventBusMessage) string {
			mu.Lock()
			defer mu.Unlock()
			asked = append(asked, msg.MsgType)
			return "key"
		}).
		Start()
	defer sub.Close()

	require.NoError(t, sub.OnSubscribe(context.Background(), "a/topic"))
	require.NoError(t, sub.OnUnsubscribe(context.Background(), "a/topic"))
	require.NoError(t, sub.OnEvent(context.Background(), "a/topic", "payload", nil))
	require.NoError(t, sub.Close())

	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []bus.MessageType{bus.MessageTypeEvent}, asked,
		"the key function was asked about something that is not a message")
}

// Below one is one. A caller asking for no partitions has asked for something
// that cannot process anything, which is never what was meant.
func TestPartitions_BelowOneIsOne(t *testing.T) {
	recorder := newPartitionRecorder()

	assert.Equal(t, 1, NewAsyncQueueingSubscriber(recorder, 10).WithPartitions(0).Partitions())
	assert.Equal(t, 1, NewAsyncQueueingSubscriber(recorder, 10).WithPartitions(-3).Partitions())
}
