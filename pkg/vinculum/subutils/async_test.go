package subutils

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tsarna/vinculum/pkg/vinculum"
)

// asyncTestSubscriber is a subscriber for testing that tracks all operations
type asyncTestSubscriber struct {
	mu           sync.Mutex
	subscribes   []string
	unsubscribes []string
	events       []asyncTestEvent
	passthroughs []vinculum.EventBusMessage
	processDelay time.Duration // Artificial delay for testing
}

type asyncTestEvent struct {
	topic   string
	message any
	fields  map[string]string
}

func (a *asyncTestSubscriber) OnSubscribe(ctx context.Context, topic string) error {
	if a.processDelay > 0 {
		time.Sleep(a.processDelay)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.subscribes = append(a.subscribes, topic)
	return nil
}

func (a *asyncTestSubscriber) OnUnsubscribe(ctx context.Context, topic string) error {
	if a.processDelay > 0 {
		time.Sleep(a.processDelay)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.unsubscribes = append(a.unsubscribes, topic)
	return nil
}

func (a *asyncTestSubscriber) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	if a.processDelay > 0 {
		time.Sleep(a.processDelay)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.events = append(a.events, asyncTestEvent{
		topic:   topic,
		message: message,
		fields:  fields,
	})
	return nil
}

func (a *asyncTestSubscriber) PassThrough(msg vinculum.EventBusMessage) error {
	if a.processDelay > 0 {
		time.Sleep(a.processDelay)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.passthroughs = append(a.passthroughs, msg)
	return nil
}

func (a *asyncTestSubscriber) getCounts() (int, int, int, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.subscribes), len(a.unsubscribes), len(a.events), len(a.passthroughs)
}

func (a *asyncTestSubscriber) getEvents() []asyncTestEvent {
	a.mu.Lock()
	defer a.mu.Unlock()
	events := make([]asyncTestEvent, len(a.events))
	copy(events, a.events)
	return events
}

func TestNewAsyncQueueingSubscriber(t *testing.T) {
	baseSubscriber := &asyncTestSubscriber{}
	asyncSub := NewAsyncQueueingSubscriber(baseSubscriber, 10)
	defer asyncSub.Close()

	assert.NotNil(t, asyncSub)
	assert.Equal(t, baseSubscriber, asyncSub.wrapped)
	assert.Equal(t, 10, asyncSub.QueueCapacity())
	assert.Equal(t, 0, asyncSub.QueueSize())
	assert.False(t, asyncSub.IsClosed())
}

func TestNewAsyncQueueingSubscriber_DefaultQueueSize(t *testing.T) {
	baseSubscriber := &asyncTestSubscriber{}
	asyncSub := NewAsyncQueueingSubscriber(baseSubscriber, 0) // Should use default
	defer asyncSub.Close()

	assert.Equal(t, 100, asyncSub.QueueCapacity()) // Default size
}

func TestAsyncQueueingSubscriber_OnSubscribe(t *testing.T) {
	baseSubscriber := &asyncTestSubscriber{}
	asyncSub := NewAsyncQueueingSubscriber(baseSubscriber, 10)
	defer asyncSub.Close()

	ctx := context.Background()

	// Send subscribe operations
	err := asyncSub.OnSubscribe(ctx, "topic1")
	assert.NoError(t, err)

	err = asyncSub.OnSubscribe(ctx, "topic2")
	assert.NoError(t, err)

	// Wait for processing
	time.Sleep(50 * time.Millisecond)

	// Verify operations were processed
	subscribes, _, _, _ := baseSubscriber.getCounts()
	assert.Equal(t, 2, subscribes)
}

func TestAsyncQueueingSubscriber_OnUnsubscribe(t *testing.T) {
	baseSubscriber := &asyncTestSubscriber{}
	asyncSub := NewAsyncQueueingSubscriber(baseSubscriber, 10)
	defer asyncSub.Close()

	ctx := context.Background()

	// Send unsubscribe operations
	err := asyncSub.OnUnsubscribe(ctx, "topic1")
	assert.NoError(t, err)

	err = asyncSub.OnUnsubscribe(ctx, "topic2")
	assert.NoError(t, err)

	// Wait for processing
	time.Sleep(50 * time.Millisecond)

	// Verify operations were processed
	_, unsubscribes, _, _ := baseSubscriber.getCounts()
	assert.Equal(t, 2, unsubscribes)
}

func TestAsyncQueueingSubscriber_OnEvent(t *testing.T) {
	baseSubscriber := &asyncTestSubscriber{}
	asyncSub := NewAsyncQueueingSubscriber(baseSubscriber, 10)
	defer asyncSub.Close()

	ctx := context.Background()

	// Send events
	err := asyncSub.OnEvent(ctx, "topic1", "message1", map[string]string{"key": "value1"})
	assert.NoError(t, err)

	err = asyncSub.OnEvent(ctx, "topic2", "message2", map[string]string{"key": "value2"})
	assert.NoError(t, err)

	// Wait for processing
	time.Sleep(50 * time.Millisecond)

	// Verify events were processed
	events := baseSubscriber.getEvents()
	assert.Len(t, events, 2)

	assert.Equal(t, "topic1", events[0].topic)
	assert.Equal(t, "message1", events[0].message)
	assert.Equal(t, map[string]string{"key": "value1"}, events[0].fields)

	assert.Equal(t, "topic2", events[1].topic)
	assert.Equal(t, "message2", events[1].message)
	assert.Equal(t, map[string]string{"key": "value2"}, events[1].fields)
}

func TestAsyncQueueingSubscriber_PassThrough(t *testing.T) {
	baseSubscriber := &asyncTestSubscriber{}
	asyncSub := NewAsyncQueueingSubscriber(baseSubscriber, 10)
	defer asyncSub.Close()

	ctx := context.Background()

	// Send PassThrough messages (using a message type that goes to PassThrough, not a specific handler)
	msg1 := vinculum.EventBusMessage{
		Ctx:     ctx,
		MsgType: vinculum.MessageTypePassThrough,
		Topic:   "test/topic",
		Payload: "test data",
	}

	err := asyncSub.PassThrough(msg1)
	assert.NoError(t, err)

	// Wait for processing
	time.Sleep(50 * time.Millisecond)

	// Verify PassThrough was processed
	_, _, _, passthroughs := baseSubscriber.getCounts()
	assert.Equal(t, 1, passthroughs)
}

func TestAsyncQueueingSubscriber_QueueFull(t *testing.T) {
	// Create a subscriber with slow processing to fill the queue
	baseSubscriber := &asyncTestSubscriber{
		processDelay: 100 * time.Millisecond,
	}
	asyncSub := NewAsyncQueueingSubscriber(baseSubscriber, 2) // Small queue
	defer asyncSub.Close()

	ctx := context.Background()

	// Fill the queue
	err := asyncSub.OnEvent(ctx, "topic1", "message1", nil)
	assert.NoError(t, err)

	err = asyncSub.OnEvent(ctx, "topic2", "message2", nil)
	assert.NoError(t, err)

	// Next message should fail because queue is full and processing is slow
	err = asyncSub.OnEvent(ctx, "topic3", "message3", nil)
	assert.Error(t, err)
	assert.Equal(t, ErrQueueFull, err)
}

func TestAsyncQueueingSubscriber_CloseAndDrain(t *testing.T) {
	baseSubscriber := &asyncTestSubscriber{}
	asyncSub := NewAsyncQueueingSubscriber(baseSubscriber, 10)

	ctx := context.Background()

	// Send several operations
	err := asyncSub.OnEvent(ctx, "topic1", "message1", nil)
	assert.NoError(t, err)

	err = asyncSub.OnEvent(ctx, "topic2", "message2", nil)
	assert.NoError(t, err)

	err = asyncSub.OnSubscribe(ctx, "topic3")
	assert.NoError(t, err)

	// Close should process all remaining messages
	err = asyncSub.Close()
	assert.NoError(t, err)

	// Verify all messages were processed
	subscribes, _, events, _ := baseSubscriber.getCounts()
	assert.Equal(t, 1, subscribes)
	assert.Equal(t, 2, events)

	// Verify subscriber is marked as closed
	assert.True(t, asyncSub.IsClosed())

	// Operations after close should fail
	err = asyncSub.OnEvent(ctx, "topic4", "message4", nil)
	assert.Error(t, err)
	assert.Equal(t, ErrSubscriberClosed, err)
}

func TestAsyncQueueingSubscriber_ConcurrentOperations(t *testing.T) {
	baseSubscriber := &asyncTestSubscriber{}
	asyncSub := NewAsyncQueueingSubscriber(baseSubscriber, 100)
	defer asyncSub.Close()

	ctx := context.Background()
	numGoroutines := 10
	operationsPerGoroutine := 10

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Launch multiple goroutines sending operations concurrently
	for i := 0; i < numGoroutines; i++ {
		go func(goroutineID int) {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				err := asyncSub.OnEvent(ctx, "topic", "message", map[string]string{
					"goroutine": string(rune(goroutineID)),
					"operation": string(rune(j)),
				})
				assert.NoError(t, err)
			}
		}(i)
	}

	wg.Wait()

	// Wait for all operations to be processed
	time.Sleep(100 * time.Millisecond)

	// Verify all events were processed
	_, _, events, _ := baseSubscriber.getCounts()
	expectedEvents := numGoroutines * operationsPerGoroutine
	assert.Equal(t, expectedEvents, events)
}

func TestAsyncQueueingSubscriber_QueueSizeTracking(t *testing.T) {
	// Create a subscriber with slow processing
	baseSubscriber := &asyncTestSubscriber{
		processDelay: 50 * time.Millisecond,
	}
	asyncSub := NewAsyncQueueingSubscriber(baseSubscriber, 10)
	defer asyncSub.Close()

	ctx := context.Background()

	// Initial queue should be empty
	assert.Equal(t, 0, asyncSub.QueueSize())

	// Add some messages quickly
	for i := 0; i < 5; i++ {
		err := asyncSub.OnEvent(ctx, "topic", "message", nil)
		require.NoError(t, err)
	}

	// Queue size should increase (though it may start decreasing as processing begins)
	queueSize := asyncSub.QueueSize()
	assert.True(t, queueSize > 0 && queueSize <= 5)

	// Wait for processing to complete
	time.Sleep(300 * time.Millisecond)

	// Queue should be empty again
	assert.Equal(t, 0, asyncSub.QueueSize())
}

func TestAsyncQueueingSubscriber_CloseMultipleTimes(t *testing.T) {
	baseSubscriber := &asyncTestSubscriber{}
	asyncSub := NewAsyncQueueingSubscriber(baseSubscriber, 10)

	// Close multiple times should not panic or cause issues
	err1 := asyncSub.Close()
	err2 := asyncSub.Close()
	err3 := asyncSub.Close()

	assert.NoError(t, err1)
	assert.NoError(t, err2)
	assert.NoError(t, err3)

	assert.True(t, asyncSub.IsClosed())
}
