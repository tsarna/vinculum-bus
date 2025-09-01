package subutils

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/tsarna/vinculum/pkg/vinculum"
)

// Error definitions for AsyncQueueingSubscriber
var (
	ErrQueueFull        = errors.New("subscriber queue is full")
	ErrSubscriberClosed = errors.New("subscriber is closed")
)

type asyncMessage struct {
	vinculum.EventBusMessage
	Fields map[string]string
}

// AsyncQueueingSubscriber wraps another subscriber and processes events asynchronously
// through a buffered channel queue. This allows the calling thread to return immediately
// while events are processed in a background goroutine.
type AsyncQueueingSubscriber struct {
	wrapped   vinculum.Subscriber
	queue     chan asyncMessage
	done      chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
	ticker    *time.Ticker // Optional ticker for periodic operations
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
func NewAsyncQueueingSubscriber(wrapped vinculum.Subscriber, queueSize int) *AsyncQueueingSubscriber {
	if queueSize <= 0 {
		queueSize = 100 // Default queue size
	}

	subscriber := &AsyncQueueingSubscriber{
		wrapped: wrapped,
		queue:   make(chan asyncMessage, queueSize),
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

// Start begins processing messages in a background goroutine.
// This method must be called after configuration (WithTicker, etc.) to start processing.
// Returns the same AsyncQueueingSubscriber instance for method chaining.
func (a *AsyncQueueingSubscriber) Start() *AsyncQueueingSubscriber {
	a.wg.Add(1)
	go a.processQueue()
	return a
}

// processMessage handles a single message by dispatching it to the appropriate wrapped subscriber method
func (a *AsyncQueueingSubscriber) processMessage(msg asyncMessage) {
	switch msg.MsgType {
	case vinculum.MessageTypeSubscribe:
		a.wrapped.OnSubscribe(msg.Ctx, msg.Topic)
	case vinculum.MessageTypeUnsubscribe:
		a.wrapped.OnUnsubscribe(msg.Ctx, msg.Topic)
	case vinculum.MessageTypeEvent:
		a.wrapped.OnEvent(msg.Ctx, msg.Topic, msg.Payload, msg.Fields)
	default:
		a.wrapped.PassThrough(msg.EventBusMessage)
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
			a.wrapped.PassThrough(vinculum.EventBusMessage{
				Ctx:     context.Background(), // TODO can we use the connection's context here somehow?
				MsgType: vinculum.MessageTypeTick,
				Topic:   "",
				Payload: nil,
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

	msg := asyncMessage{
		EventBusMessage: vinculum.EventBusMessage{
			Ctx:     ctx,
			MsgType: vinculum.MessageTypeSubscribe,
			Topic:   topic,
		},
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

	msg := asyncMessage{
		EventBusMessage: vinculum.EventBusMessage{
			Ctx:     ctx,
			MsgType: vinculum.MessageTypeUnsubscribe,
			Topic:   topic,
		},
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

	msg := asyncMessage{
		EventBusMessage: vinculum.EventBusMessage{
			Ctx:     ctx,
			MsgType: vinculum.MessageTypeEvent,
			Topic:   topic,
			Payload: message,
		},
		Fields: fields,
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
func (a *AsyncQueueingSubscriber) PassThrough(msg vinculum.EventBusMessage) error {
	// Check if closed first
	if a.IsClosed() {
		return ErrSubscriberClosed
	}

	select {
	case a.queue <- asyncMessage{EventBusMessage: msg}:
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
