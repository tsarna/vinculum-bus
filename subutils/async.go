package subutils

import (
	"context"
	"errors"
	"sync"
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

// AsyncQueueingSubscriber wraps another subscriber and processes events asynchronously
// through a buffered channel queue. This allows the calling thread to return immediately
// while events are processed in a background goroutine.
type AsyncQueueingSubscriber struct {
	wrapped   bus.Subscriber
	queue     chan bus.EventBusMessage
	done      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
	ticker    *time.Ticker // Optional ticker for periodic operations
	tracer    trace.Tracer // Optional tracer for per-message consumer spans
	name      string       // Optional name for span attributes
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
		queue:   make(chan bus.EventBusMessage, queueSize),
		done:    make(chan struct{}),
	}

	return subscriber
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

// Start begins processing messages in a background goroutine.
// This method must be called after configuration (WithTicker, etc.) to start processing.
// Returns the same AsyncQueueingSubscriber instance for method chaining.
func (a *AsyncQueueingSubscriber) Start() *AsyncQueueingSubscriber {
	a.wg.Add(1)
	go a.processQueue()
	return a
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
func (a *AsyncQueueingSubscriber) processMessage(msg bus.EventBusMessage) {
	ctx := context.WithoutCancel(msg.Ctx)

	var span trace.Span
	if a.tracer != nil {
		ctx, span = a.startConsumerSpan(ctx, msg)
		defer span.End()
	}

	err := a.dispatch(ctx, msg)
	if err != nil && span != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
	}
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
func (a *AsyncQueueingSubscriber) startConsumerSpan(ctx context.Context, msg bus.EventBusMessage) (context.Context, trace.Span) {
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

// processQueue runs in a background goroutine and processes messages from the queue
func (a *AsyncQueueingSubscriber) processQueue() {
	defer a.wg.Done()

	// Set up ticker channel if ticker is configured
	var tickerChan <-chan time.Time
	if a.ticker != nil {
		tickerChan = a.ticker.C
	}

	for {
		select {
		case msg := <-a.queue:
			a.processMessage(msg)
		case <-tickerChan:
			a.processMessage(bus.EventBusMessage{
				Ctx:     context.Background(),
				MsgType: bus.MessageTypeTick,
			})
		case <-a.done:
			// Shutdown signal received, drain remaining messages
			a.drainQueue()
			return
		}
	}
}

// drainQueue processes any remaining messages in the queue during shutdown
func (a *AsyncQueueingSubscriber) drainQueue() {
	for {
		select {
		case msg := <-a.queue:
			a.processMessage(msg)
		default:
			// No more messages to drain
			return
		}
	}
}

// OnSubscribe queues a subscribe operation and returns immediately
func (a *AsyncQueueingSubscriber) OnSubscribe(ctx context.Context, topic string) error {
	// Check if closed first
	if a.IsClosed() {
		return ErrSubscriberClosed
	}

	msg := bus.EventBusMessage{
		Ctx:     ctx,
		MsgType: bus.MessageTypeSubscribe,
		Topic:   topic,
	}

	select {
	case a.queue <- msg:
		return nil
	default:
		return ErrQueueFull
	}
}

// OnUnsubscribe queues an unsubscribe operation and returns immediately
func (a *AsyncQueueingSubscriber) OnUnsubscribe(ctx context.Context, topic string) error {
	// Check if closed first
	if a.IsClosed() {
		return ErrSubscriberClosed
	}

	msg := bus.EventBusMessage{
		Ctx:     ctx,
		MsgType: bus.MessageTypeUnsubscribe,
		Topic:   topic,
	}

	select {
	case a.queue <- msg:
		return nil
	default:
		return ErrQueueFull
	}
}

// OnEvent queues an event and returns immediately
func (a *AsyncQueueingSubscriber) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	// Check if closed first
	if a.IsClosed() {
		return ErrSubscriberClosed
	}

	msg := bus.EventBusMessage{
		Ctx:     ctx,
		MsgType: bus.MessageTypeEvent,
		Topic:   topic,
		Payload: message,
		Fields:  fields,
	}

	select {
	case a.queue <- msg:
		return nil
	default:
		return ErrQueueFull
	}
}

// PassThrough queues a pass-through operation and returns immediately.
//
// IMPORTANT: This method should NOT be called with MessageTypeEvent, MessageTypeSubscribe,
// or MessageTypeUnsubscribe as these have dedicated handler methods (OnEvent, OnSubscribe, OnUnsubscribe).
// Use PassThrough only for message types that don't have specific handlers, or for forwarding
// messages that should bypass the normal processing logic.
func (a *AsyncQueueingSubscriber) PassThrough(msg bus.EventBusMessage) error {
	// Check if closed first
	if a.IsClosed() {
		return ErrSubscriberClosed
	}

	select {
	case a.queue <- msg:
		return nil
	default:
		return ErrQueueFull
	}
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

// QueueSize returns the current number of messages in the queue
func (a *AsyncQueueingSubscriber) QueueSize() int {
	return len(a.queue)
}

// QueueCapacity returns the maximum capacity of the queue
func (a *AsyncQueueingSubscriber) QueueCapacity() int {
	return cap(a.queue)
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
