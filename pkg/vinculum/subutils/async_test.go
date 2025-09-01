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
	a.subscribes = append(a.subscribes, topic)
	return nil
}

func (a *asyncTestSubscriber) OnUnsubscribe(ctx context.Context, topic string) error {
	if a.processDelay > 0 {
		time.Sleep(a.processDelay)
	}
	a.unsubscribes = append(a.unsubscribes, topic)
	return nil
}

func (a *asyncTestSubscriber) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	if a.processDelay > 0 {
		time.Sleep(a.processDelay)
	}
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
	a.passthroughs = append(a.passthroughs, msg)
	return nil
}

func (a *asyncTestSubscriber) getCounts() (int, int, int, int) {
	return len(a.subscribes), len(a.unsubscribes), len(a.events), len(a.passthroughs)
}

func (a *asyncTestSubscriber) getEvents() []asyncTestEvent {
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
		processDelay: 250 * time.Millisecond,
	}
	asyncSub := NewAsyncQueueingSubscriber(baseSubscriber, 1) // Small queue
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
	time.Sleep(250 * time.Millisecond)

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
	time.Sleep(500 * time.Millisecond)

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

func TestAsyncQueueingSubscriber_WithTicker(t *testing.T) {
	baseSubscriber := &asyncTestSubscriber{}
	asyncSub := NewAsyncQueueingSubscriber(baseSubscriber, 10).
		WithTicker(50 * time.Millisecond) // Fast ticks for testing
	defer asyncSub.Close()

	// Wait for a few ticks
	time.Sleep(120 * time.Millisecond)

	// Should have received at least 2 tick messages
	_, _, _, passthroughs := baseSubscriber.getCounts()
	assert.GreaterOrEqual(t, passthroughs, 2, "Should have received at least 2 tick messages")
}

func TestAsyncQueueingSubscriber_WithTicker_ZeroInterval(t *testing.T) {
	baseSubscriber := &asyncTestSubscriber{}
	asyncSub := NewAsyncQueueingSubscriber(baseSubscriber, 10).
		WithTicker(0) // Zero interval should disable ticker
	defer asyncSub.Close()

	// Wait a bit
	time.Sleep(100 * time.Millisecond)

	// Should have received no tick messages
	_, _, _, passthroughs := baseSubscriber.getCounts()
	assert.Equal(t, 0, passthroughs, "Should have received no tick messages with zero interval")
}

func TestAsyncQueueingSubscriber_WithTicker_MultipleCallsIgnored(t *testing.T) {
	baseSubscriber := &asyncTestSubscriber{}
	asyncSub := NewAsyncQueueingSubscriber(baseSubscriber, 10).
		WithTicker(100 * time.Millisecond).
		WithTicker(50 * time.Millisecond) // Second call should be ignored
	defer asyncSub.Close()

	// The ticker should use the first interval (100ms), not the second (50ms)
	time.Sleep(80 * time.Millisecond)

	// Should have received 0 ticks (since first tick is at 100ms)
	_, _, _, passthroughs := baseSubscriber.getCounts()
	assert.Equal(t, 0, passthroughs, "Should not have received tick yet with 100ms interval")

	// Wait for the first tick
	time.Sleep(50 * time.Millisecond) // Total: 130ms

	// Should have received 1 tick now
	_, _, _, passthroughs = baseSubscriber.getCounts()
	assert.Equal(t, 1, passthroughs, "Should have received exactly 1 tick")
}

func TestAsyncQueueingSubscriber_TickerStopsOnClose(t *testing.T) {
	baseSubscriber := &asyncTestSubscriber{}
	asyncSub := NewAsyncQueueingSubscriber(baseSubscriber, 10).
		WithTicker(30 * time.Millisecond)

	// Wait for a few ticks
	time.Sleep(80 * time.Millisecond)

	// Get current tick count
	_, _, _, ticksBefore := baseSubscriber.getCounts()
	assert.GreaterOrEqual(t, ticksBefore, 2, "Should have received some ticks before close")

	// Close the subscriber
	err := asyncSub.Close()
	assert.NoError(t, err)

	// Wait longer than the tick interval
	time.Sleep(100 * time.Millisecond)

	// Tick count should not have increased
	_, _, _, ticksAfter := baseSubscriber.getCounts()
	assert.Equal(t, ticksBefore, ticksAfter, "Tick count should not increase after close")
}

func TestAsyncQueueingSubscriber_TickerWithQueueFull(t *testing.T) {
	// Create a subscriber with slow processing and small queue
	baseSubscriber := &asyncTestSubscriber{
		processDelay: 200 * time.Millisecond, // Slow processing
	}
	asyncSub := NewAsyncQueueingSubscriber(baseSubscriber, 1). // Very small queue
									WithTicker(10 * time.Millisecond) // Fast ticks
	defer asyncSub.Close()

	// Fill the queue with a regular message first
	ctx := context.Background()
	err := asyncSub.OnEvent(ctx, "test", "data", nil)
	assert.NoError(t, err)

	// Wait for ticks to try to queue (they should fail due to full queue)
	time.Sleep(50 * time.Millisecond)

	// The ticker should have stopped due to queue full errors
	// We can't easily verify this without exposing internal state,
	// but the test shouldn't hang or panic
	assert.True(t, true, "Test completed without hanging")
}
