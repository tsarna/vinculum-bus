package subutils

import (
	"context"
	"errors"
	"sync"

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

	// Start the background processor
	subscriber.wg.Add(1)
	go subscriber.processQueue()

	return subscriber
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

	for {
		select {
		case msg := <-a.queue:
			a.processMessage(msg)
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
func (a *AsyncQueueingSubscriber) Close() error {
	a.closeOnce.Do(func() {
		close(a.done)
		a.wg.Wait() // Wait for the background goroutine to finish processing
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
