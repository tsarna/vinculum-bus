package subutils

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tsarna/vinculum-bus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

// Error definitions for AsyncQueueingSubscriber
var (
	ErrQueueFull        = errors.New("subscriber queue is full")
	ErrSubscriberClosed = errors.New("subscriber is closed")
)

// PartitionKeyFunc computes the ordering key for a message.
//
// Messages with equal keys go to the same queue and are therefore processed by
// the same goroutine, in the order they were enqueued; messages with different
// keys may be processed concurrently. Choosing the key is choosing what must
// stay in order.
//
// Two properties matter, and neither is checkable here:
//
// It must be cheap. This is called on the *enqueueing* goroutine — the poll
// loop or bus dispatch that a queue exists to keep moving — so its cost is
// charged to the thing the queue was protecting.
//
// It must be a pure function of the message. A key drawn from the clock or a
// random source puts successive messages about one thing in different queues,
// which is the ordering guarantee saying it holds while it does not.
type PartitionKeyFunc func(msg bus.EventBusMessage) string

// AsyncQueueingSubscriber wraps another subscriber and processes events asynchronously
// through a buffered channel queue. This allows the calling thread to return immediately
// while events are processed in a background goroutine.
//
// With more than one partition (see WithPartitions) there are that many queues
// and that many goroutines, and a message picks its queue by hashing a key.
// Equal keys therefore keep their order and different keys run concurrently.
type AsyncQueueingSubscriber struct {
	wrapped bus.Subscriber

	// queues holds one channel per partition, and always at least one. An
	// unpartitioned subscriber is the same code over a slice of length one
	// rather than a second path through it.
	queues    []chan bus.EventBusMessage
	done      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
	ticker    *time.Ticker // Optional ticker for periodic operations
	tracer    trace.Tracer // Optional tracer for per-message consumer spans
	name      string       // Optional name for span attributes

	// keyFn computes the partition key, or is nil to key on the topic.
	keyFn PartitionKeyFunc

	// unordered abandons keying entirely and deals messages round-robin over
	// next, for work with no ordering requirement at all. Set only by
	// WithoutOrdering, which is what makes it distinguishable from a key
	// function that happens to return the same thing every time.
	unordered bool
	next      uint32

	// dropped counts messages refused because the queue was full. A subscription
	// with a queue is a second, independent backpressure point: counting only
	// the bus's drops would miss the one-slow-handler case, which is the common
	// shape.
	//
	// It is a total across partitions. Which partition refused a message is a
	// question for a span, not a counter: what a configuration acts on is that
	// something was refused at all.
	dropped uint64
}

// NewAsyncQueueingSubscriber creates a new AsyncQueueingSubscriber that processes
// events asynchronously through a buffered channel of the specified size.
//
// The subscriber starts a background goroutine that processes events from the queue.
// All Subscriber interface methods return immediately after queuing the operation.
//
// Example:
//
//	// Create an async subscriber with a queue size of 100
//	baseSubscriber := &MySubscriber{}
//	asyncSubscriber := subutils.NewAsyncQueueingSubscriber(baseSubscriber, 100)
//	defer asyncSubscriber.Close() // Important: call Close() to properly shutdown
//
// Note: You must call Close() to properly shutdown the background goroutine and
// ensure all queued messages are processed.
func NewAsyncQueueingSubscriber(wrapped bus.Subscriber, queueSize int) *AsyncQueueingSubscriber {
	if queueSize <= 0 {
		queueSize = 100 // Default queue size
	}

	subscriber := &AsyncQueueingSubscriber{
		wrapped: wrapped,
		queues:  []chan bus.EventBusMessage{make(chan bus.EventBusMessage, queueSize)},
		done:    make(chan struct{}),
	}

	return subscriber
}

// WithPartitions runs n queues instead of one, each drained by its own
// goroutine, so that n messages can be processed at once.
//
// Order is preserved within a partition and not across partitions, and which
// partition a message lands in is decided by hashing its key — the topic, or
// whatever WithPartitionKey supplies. So the key is the configuration of what
// must stay in order, and n is how much parallelism is available to everything
// that need not.
//
// Two consequences worth knowing before setting it:
//
// The queue size passed to NewAsyncQueueingSubscriber is *per partition*, so
// the memory and the in-flight count are both multiplied by n.
//
// Keys that hash together share a queue, so one slow key holds up the unrelated
// keys behind it — a 1/n share of the traffic rather than all of it, which is
// the trade this shape makes for having no shared state between partitions.
//
// n below 1 is treated as 1. Must be called before Start().
func (a *AsyncQueueingSubscriber) WithPartitions(n int) *AsyncQueueingSubscriber {
	if n < 1 {
		n = 1
	}

	size := cap(a.queues[0])
	a.queues = make([]chan bus.EventBusMessage, n)
	for i := range a.queues {
		a.queues[i] = make(chan bus.EventBusMessage, size)
	}

	return a
}

// WithPartitionKey sets the function deciding which messages must stay in order
// relative to each other. Without it, that is the message's topic.
//
// It has no effect with a single partition, where everything is already
// ordered. Calling it clears WithoutOrdering, so the two can be called in
// either order and the last one wins rather than blending. Must be called
// before Start().
func (a *AsyncQueueingSubscriber) WithPartitionKey(fn PartitionKeyFunc) *AsyncQueueingSubscriber {
	a.keyFn = fn
	a.unordered = false
	return a
}

// WithoutOrdering deals messages to partitions round-robin, preserving no order
// at all, for work where none is required — every message independent of every
// other.
//
// It is worth having as its own spelling because the alternative a configuration
// would otherwise reach for is a key that varies per message, and a random or
// clock-derived key is both slower and less evenly spread than counting.
//
// Calling it clears WithPartitionKey. Must be called before Start().
func (a *AsyncQueueingSubscriber) WithoutOrdering() *AsyncQueueingSubscriber {
	a.unordered = true
	a.keyFn = nil
	return a
}

// WithTicker enables periodic tick messages at the specified interval.
// Returns the same AsyncQueueingSubscriber instance for method chaining.
//
// When enabled, the subscriber will periodically send MessageTypeTick messages
// to the wrapped subscriber's PassThrough method. This is useful for implementing
// periodic operations like connection health checks, cleanup tasks, etc.
//
// Example:
//
//	asyncSub := subutils.NewAsyncQueueingSubscriber(baseSubscriber, 100).
//		WithTicker(30 * time.Second).
//		Start() // Must call Start() to begin processing
//
// Note: The ticker is automatically cleaned up when Close() is called.
// This method must be called before Start() to avoid race conditions.
func (a *AsyncQueueingSubscriber) WithTicker(interval time.Duration) *AsyncQueueingSubscriber {
	if interval > 0 && a.ticker == nil {
		a.ticker = time.NewTicker(interval)
	}
	return a
}

// WithTracerProvider enables OTel tracing for message processing. When set,
// each message processed in the background goroutine is wrapped in a new-root
// SpanKindConsumer span linked to the caller's span context (if valid). The
// new-root link pattern follows OTel messaging semantic conventions for async
// pub/sub boundaries: the producer span has already ended by the time we
// process the message, so a child span would be misleading.
//
// Returns the same AsyncQueueingSubscriber instance for method chaining. Must
// be called before Start() to avoid racing with the processing goroutine.
func (a *AsyncQueueingSubscriber) WithTracerProvider(tp trace.TracerProvider) *AsyncQueueingSubscriber {
	if tp != nil {
		scope := "github.com/tsarna/vinculum-bus/subutils"
		if a.name != "" {
			scope = scope + "/" + a.name
		}
		a.tracer = tp.Tracer(scope)
	}
	return a
}

// WithName sets a name used in the tracer instrumentation scope and as a
// span attribute. Must be called before WithTracerProvider to affect the
// tracer scope. Returns the same AsyncQueueingSubscriber instance for
// method chaining.
func (a *AsyncQueueingSubscriber) WithName(name string) *AsyncQueueingSubscriber {
	a.name = name
	return a
}

// Start begins processing messages in a background goroutine per partition.
// This method must be called after configuration (WithTicker, etc.) to start processing.
// Returns the same AsyncQueueingSubscriber instance for method chaining.
func (a *AsyncQueueingSubscriber) Start() *AsyncQueueingSubscriber {
	for i := range a.queues {
		a.wg.Add(1)
		go a.processQueue(i)
	}
	return a
}

// partitionFor picks the queue a message belongs to.
//
// The single-partition case answers without hashing anything, which is what
// keeps an unpartitioned subscriber exactly as cheap as it was before
// partitioning existed.
//
// FNV-1a because only within-process agreement is required: the queues live and
// die with this object, so there is nothing to be consistent with across a
// restart or across a fleet. What a stable, unseeded hash does buy is a key that
// lands in the same place every time within a run, which is the difference
// between a reproducible report and one that moves while being read.
func (a *AsyncQueueingSubscriber) partitionFor(msg bus.EventBusMessage) int {
	n := len(a.queues)
	if n == 1 {
		return 0
	}

	if a.unordered {
		return int(atomic.AddUint32(&a.next, 1) % uint32(n))
	}

	// Only an event has a key to compute. A subscribe, unsubscribe or
	// pass-through partitions on its topic, because the key function is written
	// against a message — asking it about a subscribe would hand it a nil
	// payload and no fields, and get a failure rather than an answer.
	key := msg.Topic
	if a.keyFn != nil && msg.MsgType == bus.MessageTypeEvent {
		key = a.keyFn(msg)
	}

	h := fnv.New32a()
	// Hash.Write never returns an error, per the hash.Hash contract.
	_, _ = h.Write([]byte(key))

	return int(h.Sum32() % uint32(n))
}

// enqueue offers a message to its partition, or reports why it could not.
//
// Every entry point funnels through here — the three Subscriber methods and
// PassThrough — because they differ only in the message they build. What
// happens to it afterwards is one policy, and a policy in four copies is a
// policy that will be three after the next change.
//
// A full queue refuses rather than blocks. The caller learns of it through
// ErrQueueFull, and for a delivery that carries a settler that error is what
// nacks it: a queue refusing a message is a refusal to have accepted it, not a
// failure while handling it.
func (a *AsyncQueueingSubscriber) enqueue(msg bus.EventBusMessage) error {
	if a.IsClosed() {
		return ErrSubscriberClosed
	}

	select {
	case a.queues[a.partitionFor(msg)] <- msg:
		return nil
	default:
		a.recordDrop()
		return ErrQueueFull
	}
}

// processMessage handles a single message by dispatching it to the appropriate
// wrapped subscriber method. The original context is detached from cancellation
// via context.WithoutCancel because the producer may have already returned and
// canceled its context by the time async processing occurs. Context values
// (including trace spans) are preserved.
//
// When a tracer is configured (see WithTracerProvider), the dispatch is wrapped
// in a new-root SpanKindConsumer span linked to the caller's span context. This
// preserves the causal link to the upstream work without tying the async
// processing span to the producer's lifecycle.
// Settling is confined to event messages. A subscribe, unsubscribe, tick or
// pass-through is not a delivery — nothing acknowledged it to a broker and
// nothing is waiting on its outcome — so even where one carries a context with
// a settler on it, that settler belongs to some other message's delivery and
// this is not the place to decide it.
func (a *AsyncQueueingSubscriber) processMessage(msg bus.EventBusMessage, partition int) {
	ctx := context.WithoutCancel(msg.Ctx)

	var span trace.Span
	if a.tracer != nil {
		ctx, span = a.startConsumerSpan(ctx, msg, partition)
		defer span.End()
	}

	// A panic mid-work must not leave the delivery unsettled. Nothing upstream
	// is waiting on this goroutine, so the message would simply sit until its
	// broker lease lapsed, with nothing anywhere saying why. Telling the broker
	// first changes what it hears, not what the process then does: the panic
	// continues on its way.
	defer func() {
		if r := recover(); r != nil {
			if msg.MsgType == bus.MessageTypeEvent {
				bus.SettleRefused(ctx, fmt.Sprintf("panic while handling %s: %v", msg.Topic, r))
			}
			panic(r)
		}
	}()

	err := a.dispatch(ctx, msg)
	if err != nil && span != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}

	// This is a settle point, and it is the one this queue exists to move. The
	// enqueue that put the message here returned long ago; the work happened
	// just now, on this goroutine, and its outcome is err.
	if msg.MsgType == bus.MessageTypeEvent {
		bus.SettleOnReturn(ctx, a.wrapped, err)
	}
}

// Unwrap returns the subscriber this queue feeds, so a caller asking whether
// delivery is deferred can see past the queue to what is behind it.
func (a *AsyncQueueingSubscriber) Unwrap() bus.Subscriber { return a.wrapped }

// DeliveryDisposition reports that a queued message is not a handled one.
//
// OnEvent below puts the message on the queue and returns; processMessage above
// runs it later, on another goroutine, and settles there. A caller settling on
// the enqueue instead would acknowledge every message the instant it was
// accepted — which is the whole defect a queue in front of a broker receiver
// used to introduce.
//
// This is true whatever the queue feeds. A queue in front of an observer really
// does defer — the message is on a channel and nothing has looked at it yet —
// and the drain asks the question again of what it wraps.
func (a *AsyncQueueingSubscriber) DeliveryDisposition() bus.Disposition {
	return bus.Deferred
}

// dispatch routes a message to the appropriate wrapped subscriber method.
func (a *AsyncQueueingSubscriber) dispatch(ctx context.Context, msg bus.EventBusMessage) error {
	switch msg.MsgType {
	case bus.MessageTypeSubscribe:
		return a.wrapped.OnSubscribe(ctx, msg.Topic)
	case bus.MessageTypeUnsubscribe:
		return a.wrapped.OnUnsubscribe(ctx, msg.Topic)
	case bus.MessageTypeEvent:
		return a.wrapped.OnEvent(ctx, msg.Topic, msg.Payload, msg.Fields)
	default:
		msg.Ctx = ctx
		return a.wrapped.PassThrough(msg)
	}
}

// startConsumerSpan starts a new-root consumer span for processing a queued
// message. The span is linked to the caller's span (if valid) so traces
// remain causally connected across the async boundary.
func (a *AsyncQueueingSubscriber) startConsumerSpan(ctx context.Context, msg bus.EventBusMessage, partition int) (context.Context, trace.Span) {
	attrs := []attribute.KeyValue{
		semconv.MessagingSystemKey.String("vinculum"),
		semconv.MessagingOperationTypeDeliver,
		semconv.MessagingOperationNameKey.String("process"),
	}
	if msg.Topic != "" {
		attrs = append(attrs, semconv.MessagingDestinationNameKey.String(msg.Topic))
	}
	if a.name != "" {
		attrs = append(attrs, attribute.String("vinculum.subscriber.name", a.name))
	}
	// Only where there is a choice of partition. On an unpartitioned subscriber
	// the attribute would be zero on every span, which is a column of noise
	// rather than a fact about the message.
	if len(a.queues) > 1 {
		attrs = append(attrs, semconv.MessagingDestinationPartitionID(strconv.Itoa(partition)))
	}

	opts := []trace.SpanStartOption{
		trace.WithNewRoot(),
		trace.WithSpanKind(trace.SpanKindConsumer),
		trace.WithAttributes(attrs...),
	}
	if linkCtx := trace.SpanFromContext(msg.Ctx).SpanContext(); linkCtx.IsValid() {
		opts = append(opts, trace.WithLinks(trace.Link{SpanContext: linkCtx}))
	}
	return a.tracer.Start(ctx, spanNameFor(msg), opts...)
}

// spanNameFor returns the OTel span name for a queued message, following the
// "<operation> <destination>" convention used by the event bus.
func spanNameFor(msg bus.EventBusMessage) string {
	switch msg.MsgType {
	case bus.MessageTypeEvent:
		return "process " + msg.Topic
	case bus.MessageTypeSubscribe:
		return "on_subscribe " + msg.Topic
	case bus.MessageTypeUnsubscribe:
		return "on_unsubscribe " + msg.Topic
	case bus.MessageTypeTick:
		return "tick"
	default:
		if msg.Topic != "" {
			return "passthrough " + msg.Topic
		}
		return "passthrough"
	}
}

// processQueue runs in a background goroutine and processes messages from one
// partition's queue.
//
// Only partition 0 ticks. A tick is a periodic event for the wrapped
// subscriber, not a message being delivered to it, so it should arrive once per
// interval however many partitions are draining alongside — a keepalive sent
// eight times is eight keepalives.
func (a *AsyncQueueingSubscriber) processQueue(partition int) {
	defer a.wg.Done()

	queue := a.queues[partition]

	// Set up ticker channel if ticker is configured
	var tickerChan <-chan time.Time
	if a.ticker != nil && partition == 0 {
		tickerChan = a.ticker.C
	}

	for {
		select {
		case msg := <-queue:
			a.processMessage(msg, partition)
		case <-tickerChan:
			a.processMessage(bus.EventBusMessage{
				Ctx:     context.Background(),
				MsgType: bus.MessageTypeTick,
			}, partition)
		case <-a.done:
			// Shutdown signal received, drain remaining messages
			a.drainQueue(queue, partition)
			return
		}
	}
}

// drainQueue processes any remaining messages in one partition's queue during
// shutdown. Each goroutine drains its own, so the order within a partition
// survives shutdown exactly as it does in normal running.
func (a *AsyncQueueingSubscriber) drainQueue(queue chan bus.EventBusMessage, partition int) {
	for {
		select {
		case msg := <-queue:
			a.processMessage(msg, partition)
		default:
			// No more messages to drain
			return
		}
	}
}

// OnSubscribe queues a subscribe operation and returns immediately.
//
// A subscribe carries no payload, so it partitions on its topic whatever the
// key function is. Where a custom key puts that topic's events in a different
// partition, this is no longer ordered before them — a subscriber that depends
// on seeing its subscribe first should not be partitioned.
func (a *AsyncQueueingSubscriber) OnSubscribe(ctx context.Context, topic string) error {
	return a.enqueue(bus.EventBusMessage{
		Ctx:     ctx,
		MsgType: bus.MessageTypeSubscribe,
		Topic:   topic,
	})
}

// OnUnsubscribe queues an unsubscribe operation and returns immediately. Like
// OnSubscribe, it partitions on its topic.
func (a *AsyncQueueingSubscriber) OnUnsubscribe(ctx context.Context, topic string) error {
	return a.enqueue(bus.EventBusMessage{
		Ctx:     ctx,
		MsgType: bus.MessageTypeUnsubscribe,
		Topic:   topic,
	})
}

// OnEvent queues an event and returns immediately
func (a *AsyncQueueingSubscriber) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	return a.enqueue(bus.EventBusMessage{
		Ctx:     ctx,
		MsgType: bus.MessageTypeEvent,
		Topic:   topic,
		Payload: message,
		Fields:  fields,
	})
}

// PassThrough queues a pass-through operation and returns immediately.
//
// IMPORTANT: This method should NOT be called with MessageTypeEvent, MessageTypeSubscribe,
// or MessageTypeUnsubscribe as these have dedicated handler methods (OnEvent, OnSubscribe, OnUnsubscribe).
// Use PassThrough only for message types that don't have specific handlers, or for forwarding
// messages that should bypass the normal processing logic.
func (a *AsyncQueueingSubscriber) PassThrough(msg bus.EventBusMessage) error {
	return a.enqueue(msg)
}

// Close gracefully shuts down the async subscriber, ensuring all queued messages
// are processed before returning. This method should be called to properly clean up
// the background goroutine.
//
// The shutdown process:
// 1. Stop the ticker (if running) to prevent new tick events
// 2. Signal the message processor goroutine to stop via the done channel
// 3. Wait for the message processor goroutine to complete
// 4. Any remaining messages in the queue are processed during drainQueue()
func (a *AsyncQueueingSubscriber) Close() error {
	a.closeOnce.Do(func() {
		// Stop ticker first to prevent new tick events during shutdown
		if a.ticker != nil {
			a.ticker.Stop()
		}

		// Signal shutdown to the message processor goroutine
		close(a.done)

		// Wait for the message processor goroutine to finish
		a.wg.Wait()

		// Don't close the queue channel here as the background goroutine might still be reading from it
	})
	return nil
}

// QueueDepth returns the current number of messages waiting, across every
// partition.
//
// A total rather than a per-partition figure because of what asks: a shutdown
// waiting for the queue to empty needs to know whether *anything* is still
// waiting, and a maximum or a single partition's depth would let it proceed
// with messages queued elsewhere.
func (a *AsyncQueueingSubscriber) QueueDepth() int {
	depth := 0
	for _, queue := range a.queues {
		depth += len(queue)
	}

	return depth
}

// QueueCapacity returns the total capacity across every partition — the queue
// size that was configured, multiplied by the partition count.
func (a *AsyncQueueingSubscriber) QueueCapacity() int {
	return cap(a.queues[0]) * len(a.queues)
}

// MaxQueueDepth returns the depth of the fullest partition.
//
// This is the number that says whether the queue is in trouble, where
// QueueDepth says how much is outstanding. One hot key can fill its own
// partition and start refusing messages while seven others sit empty, and the
// total across the eight reads as comfortable for exactly as long as that is
// happening.
func (a *AsyncQueueingSubscriber) MaxQueueDepth() int {
	max := 0
	for _, queue := range a.queues {
		if depth := len(queue); depth > max {
			max = depth
		}
	}

	return max
}

// Partitions returns how many queues, and therefore how many goroutines, this
// subscriber is running. Always at least one.
func (a *AsyncQueueingSubscriber) Partitions() int {
	return len(a.queues)
}

// DroppedTotal returns the number of messages refused because the queue was
// full.
//
// The overflow policy itself is unchanged and deliberately so: this queue's
// remaining users are outbound senders and subscription actions, for whom
// drop-newest under sustained overload is defensible. What was missing was the
// number saying it had happened.
func (a *AsyncQueueingSubscriber) DroppedTotal() uint64 {
	return atomic.LoadUint64(&a.dropped)
}

// recordDrop accounts for a message the queue could not accept.
func (a *AsyncQueueingSubscriber) recordDrop() {
	atomic.AddUint64(&a.dropped, 1)
}

// IsClosed returns true if the subscriber has been closed
func (a *AsyncQueueingSubscriber) IsClosed() bool {
	select {
	case <-a.done:
		return true
	default:
		return false
	}
}
