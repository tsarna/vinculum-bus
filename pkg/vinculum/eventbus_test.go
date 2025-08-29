package vinculum

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"
)

// MockSubscriber implements the Subscriber interface for testing
type MockSubscriber struct {
	BaseSubscriber
	subscriptions   []string
	unsubscriptions []string
	events          []Event
	mu              sync.RWMutex
	simulateError   bool
}

type Event struct {
	Topic   string
	Message any
	Fields  map[string]string
}

func NewMockSubscriber() *MockSubscriber {
	return &MockSubscriber{
		subscriptions:   make([]string, 0),
		unsubscriptions: make([]string, 0),
		events:          make([]Event, 0),
	}
}

func (m *MockSubscriber) OnSubscribe(topic string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subscriptions = append(m.subscriptions, topic)
	return nil
}

func (m *MockSubscriber) OnUnsubscribe(topic string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unsubscriptions = append(m.unsubscriptions, topic)
	return nil
}

func (m *MockSubscriber) OnEvent(topic string, message any, fields map[string]string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, Event{
		Topic:   topic,
		Message: message,
		Fields:  fields,
	})

	if m.simulateError {
		return fmt.Errorf("simulated error")
	}
	return nil
}

func (m *MockSubscriber) GetSubscriptions() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]string, len(m.subscriptions))
	copy(result, m.subscriptions)
	return result
}

func (m *MockSubscriber) GetUnsubscriptions() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]string, len(m.unsubscriptions))
	copy(result, m.unsubscriptions)
	return result
}

func (m *MockSubscriber) GetEvents() []Event {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Event, len(m.events))
	copy(result, m.events)
	return result
}

func (m *MockSubscriber) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subscriptions = m.subscriptions[:0]
	m.unsubscriptions = m.unsubscriptions[:0]
	m.events = m.events[:0]
}

func TestNewEventBus(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBusInstance := NewEventBus(logger)
	if eventBusInstance == nil {
		t.Fatal("NewEventBus returned nil")
	}

	// Verify the event bus is not started initially
	b, ok := eventBusInstance.(*basicEventBus)
	if !ok {
		t.Fatal("NewEventBus should return *basicEventBus type")
	}
	if atomic.LoadInt32(&b.started) != 0 {
		t.Error("EventBus should not be started initially")
	}
}

func TestEventBusStartStop(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBus := NewEventBus(logger)

	// Test starting the event bus
	err := eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}

	// Give the goroutine time to start
	time.Sleep(10 * time.Millisecond)

	// Test double start should fail
	err = eventBus.Start()
	if err == nil {
		t.Error("Expected error when starting already started event bus")
	}

	// Test stopping the event bus
	err = eventBus.Stop()
	if err != nil {
		t.Fatalf("Failed to stop event bus: %v", err)
	}

	// Test double stop should fail
	err = eventBus.Stop()
	if err == nil {
		t.Error("Expected error when stopping already stopped event bus")
	}
}

func TestEventBusSubscribeUnsubscribe(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBus := NewEventBus(logger)
	err := eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	subscriber := NewMockSubscriber()

	// Test subscription
	eventBus.Subscribe(subscriber, "test/topic")

	// Give time for the subscription to be processed
	time.Sleep(10 * time.Millisecond)

	subscriptions := subscriber.GetSubscriptions()
	if len(subscriptions) != 1 || subscriptions[0] != "test/topic" {
		t.Errorf("Expected subscription to 'test/topic', got %v", subscriptions)
	}

	// Test unsubscription
	eventBus.Unsubscribe(subscriber, "test/topic")

	// Give time for the unsubscription to be processed
	time.Sleep(10 * time.Millisecond)

	unsubscriptions := subscriber.GetUnsubscriptions()
	if len(unsubscriptions) != 1 || unsubscriptions[0] != "test/topic" {
		t.Errorf("Expected unsubscription from 'test/topic', got %v", unsubscriptions)
	}
}

func TestEventBusUnsubscribeAll(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBus := NewEventBus(logger)
	err := eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	subscriber := NewMockSubscriber()

	// Subscribe to multiple topics
	eventBus.Subscribe(subscriber, "test/topic1")
	eventBus.Subscribe(subscriber, "test/topic2")

	// Give time for subscriptions to be processed
	time.Sleep(10 * time.Millisecond)

	// Unsubscribe from all
	eventBus.UnsubscribeAll(subscriber)

	// Give time for unsubscription to be processed
	time.Sleep(10 * time.Millisecond)

	unsubscriptions := subscriber.GetUnsubscriptions()
	// UnsubscribeAll directly removes from map and calls OnUnsubscribe with empty string
	// It doesn't trigger individual unsubscriptions for each topic
	if len(unsubscriptions) != 1 { // Just 1 unsubscribe all (empty string)
		t.Errorf("Expected 1 unsubscription event, got %d", len(unsubscriptions))
	}

	// Check that the unsubscription is the "unsubscribe all" event
	if len(unsubscriptions) > 0 && unsubscriptions[0] != "" {
		t.Error("Expected empty string for unsubscribe all event")
	}
}

func TestEventBusPublishEvent(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBus := NewEventBus(logger)
	err := eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	subscriber := NewMockSubscriber()
	eventBus.Subscribe(subscriber, "test/topic")

	// Give time for subscription to be processed
	time.Sleep(10 * time.Millisecond)

	// Publish an event
	testMessage := "test message"
	eventBus.Publish("test/topic", testMessage)

	// Give time for event to be processed
	time.Sleep(10 * time.Millisecond)

	events := subscriber.GetEvents()
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	event := events[0]
	if event.Topic != "test/topic" {
		t.Errorf("Expected topic 'test/topic', got '%s'", event.Topic)
	}
	if event.Message != testMessage {
		t.Errorf("Expected message '%s', got '%v'", testMessage, event.Message)
	}
	if event.Fields != nil {
		t.Errorf("Expected nil fields for exact match, got %v", event.Fields)
	}
}

func TestEventBusExactTopicMatching(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBus := NewEventBus(logger)
	err := eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	subscriber := NewMockSubscriber()
	eventBus.Subscribe(subscriber, "exact/topic")

	// Give time for subscription to be processed
	time.Sleep(10 * time.Millisecond)

	// Publish to exact topic
	eventBus.Publish("exact/topic", "message1")

	// Publish to different topic
	eventBus.Publish("exact/different", "message2")
	eventBus.Publish("different/topic", "message3")

	// Give time for events to be processed
	time.Sleep(10 * time.Millisecond)

	events := subscriber.GetEvents()
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	if events[0].Message != "message1" {
		t.Errorf("Expected 'message1', got '%v'", events[0].Message)
	}
}

func TestEventBusWildcardMatching(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBus := NewEventBus(logger)
	err := eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	subscriber := NewMockSubscriber()

	// Subscribe with single-level wildcard
	eventBus.Subscribe(subscriber, "test/+/topic")

	// Give time for subscription to be processed
	time.Sleep(10 * time.Millisecond)

	// Publish events
	eventBus.Publish("test/abc/topic", "message1")     // Should match
	eventBus.Publish("test/xyz/topic", "message2")     // Should match
	eventBus.Publish("test/abc/xyz/topic", "message3") // Should not match (multi-level)
	eventBus.Publish("test/topic", "message4")         // Should not match (no middle part)

	// Give time for events to be processed
	time.Sleep(10 * time.Millisecond)

	events := subscriber.GetEvents()
	if len(events) != 2 {
		t.Fatalf("Expected 2 events, got %d", len(events))
	}
}

func TestEventBusMultilevelWildcardMatching(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBus := NewEventBus(logger)
	err := eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	subscriber := NewMockSubscriber()

	// Subscribe with multi-level wildcard
	eventBus.Subscribe(subscriber, "test/#")

	// Give time for subscription to be processed
	time.Sleep(10 * time.Millisecond)

	// Publish events
	eventBus.Publish("test/abc", "message1")         // Should match
	eventBus.Publish("test/abc/def", "message2")     // Should match
	eventBus.Publish("test/abc/def/ghi", "message3") // Should match
	eventBus.Publish("other/abc", "message4")        // Should not match

	// Give time for events to be processed
	time.Sleep(10 * time.Millisecond)

	events := subscriber.GetEvents()
	if len(events) != 3 {
		t.Fatalf("Expected 3 events, got %d", len(events))
	}
}

func TestEventBusParameterExtraction(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBus := NewEventBus(logger)
	err := eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	subscriber := NewMockSubscriber()

	// Subscribe with parameter extraction (automatically detected)
	eventBus.Subscribe(subscriber, "user/+userId/profile/+action")

	// Give time for subscription to be processed
	time.Sleep(10 * time.Millisecond)

	// Publish event with parameters
	eventBus.Publish("user/123/profile/update", "profile data")

	// Give time for event to be processed
	time.Sleep(10 * time.Millisecond)

	events := subscriber.GetEvents()
	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	event := events[0]
	if event.Fields == nil {
		t.Fatal("Expected extracted fields, got nil")
	}

	if event.Fields["userId"] != "123" {
		t.Errorf("Expected userId '123', got '%s'", event.Fields["userId"])
	}

	if event.Fields["action"] != "update" {
		t.Errorf("Expected action 'update', got '%s'", event.Fields["action"])
	}
}

func TestEventBusMultipleSubscribers(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBus := NewEventBus(logger)
	err := eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	subscriber1 := NewMockSubscriber()
	subscriber2 := NewMockSubscriber()

	// Both subscribe to the same topic
	eventBus.Subscribe(subscriber1, "shared/topic")
	eventBus.Subscribe(subscriber2, "shared/topic")

	// Give time for subscriptions to be processed
	time.Sleep(10 * time.Millisecond)

	// Publish event
	eventBus.Publish("shared/topic", "broadcast message")

	// Give time for event to be processed
	time.Sleep(10 * time.Millisecond)

	// Both subscribers should receive the event
	events1 := subscriber1.GetEvents()
	events2 := subscriber2.GetEvents()

	if len(events1) != 1 {
		t.Errorf("Subscriber1 expected 1 event, got %d", len(events1))
	}

	if len(events2) != 1 {
		t.Errorf("Subscriber2 expected 1 event, got %d", len(events2))
	}
}

func TestEventBusMultipleMatchingPatterns(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBus := NewEventBus(logger)
	err := eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	subscriber := NewMockSubscriber()

	// Subscribe to multiple patterns that could match the same topic
	eventBus.Subscribe(subscriber, "device/+/data")   // Matches device/123/data
	eventBus.Subscribe(subscriber, "device/123/+")    // Also matches device/123/data
	eventBus.Subscribe(subscriber, "device/123/data") // Exact match for device/123/data
	eventBus.Subscribe(subscriber, "device/#")        // Also matches device/123/data

	// Give time for subscriptions to be processed
	time.Sleep(10 * time.Millisecond)

	// Publish an event that matches all patterns
	eventBus.Publish("device/123/data", "sensor reading")

	// Give time for event to be processed
	time.Sleep(10 * time.Millisecond)

	events := subscriber.GetEvents()
	// Subscriber should only receive the event once, even though multiple patterns match
	// The event bus should break after the first match for each subscriber
	if len(events) != 1 {
		t.Errorf("Expected subscriber to receive event only once, got %d events", len(events))
	}

	if len(events) > 0 {
		event := events[0]
		if event.Topic != "device/123/data" {
			t.Errorf("Expected topic 'device/123/data', got '%s'", event.Topic)
		}
		if event.Message != "sensor reading" {
			t.Errorf("Expected message 'sensor reading', got '%v'", event.Message)
		}
	}
}

func TestEventBusMessageBeforeStart(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBus := NewEventBus(logger)
	subscriber := NewMockSubscriber()

	// Try to subscribe before starting event bus
	eventBus.Subscribe(subscriber, "test/topic")

	// Try to publish before starting event bus
	eventBus.Publish("test/topic", "message")

	// Give time for any potential processing
	time.Sleep(10 * time.Millisecond)

	// Subscriber should not have received anything
	subscriptions := subscriber.GetSubscriptions()
	events := subscriber.GetEvents()

	if len(subscriptions) != 0 {
		t.Errorf("Expected no subscriptions before start, got %d", len(subscriptions))
	}

	if len(events) != 0 {
		t.Errorf("Expected no events before start, got %d", len(events))
	}
}

func TestEventBusConcurrentOperations(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBus := NewEventBus(logger)
	err := eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	const numSubscribers = 10
	const numMessages = 100

	subscribers := make([]*MockSubscriber, numSubscribers)
	for i := 0; i < numSubscribers; i++ {
		subscribers[i] = NewMockSubscriber()
		eventBus.Subscribe(subscribers[i], "concurrent/test")
	}

	// Give time for subscriptions to be processed
	time.Sleep(50 * time.Millisecond)

	// Publish messages concurrently
	var wg sync.WaitGroup
	for i := 0; i < numMessages; i++ {
		wg.Add(1)
		go func(msg int) {
			defer wg.Done()
			eventBus.Publish("concurrent/test", msg)
		}(i)
	}

	wg.Wait()

	// Give time for all events to be processed
	time.Sleep(100 * time.Millisecond)

	// Verify all subscribers received all messages
	for i, subscriber := range subscribers {
		events := subscriber.GetEvents()
		if len(events) != numMessages {
			t.Errorf("Subscriber %d expected %d events, got %d", i, numMessages, len(events))
		}
	}
}

func TestEventBusChannelBuffer(t *testing.T) {
	// Create a event bus and start it
	logger := zaptest.NewLogger(t)
	eventBus := NewEventBus(logger)
	err := eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	// Create a slow subscriber that takes time to process events
	subscriber := NewMockSubscriber()
	eventBus.Subscribe(subscriber, "buffer/test")

	// Give time for subscription to be processed
	time.Sleep(10 * time.Millisecond)

	// Send many messages quickly to test buffering
	const numMessages = 150 // More than the buffer size of 100
	for i := 0; i < numMessages; i++ {
		eventBus.Publish("buffer/test", i)
	}

	// Give time for events to be processed
	time.Sleep(100 * time.Millisecond)

	events := subscriber.GetEvents()
	// Due to channel buffering and potential drops, we should receive some messages
	// but maybe not all if the channel was full
	if len(events) == 0 {
		t.Error("Expected at least some events to be processed")
	}

	t.Logf("Processed %d out of %d messages", len(events), numMessages)
}

func TestEventBusStopWithPendingMessages(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBus := NewEventBus(logger)
	err := eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}

	subscriber := NewMockSubscriber()
	eventBus.Subscribe(subscriber, "stop/test")

	// Give time for subscription to be processed
	time.Sleep(10 * time.Millisecond)

	// Send some messages
	for i := 0; i < 10; i++ {
		eventBus.Publish("stop/test", i)
	}

	// Stop immediately without waiting for processing
	err = eventBus.Stop()
	if err != nil {
		t.Fatalf("Failed to stop event bus: %v", err)
	}

	// EventBus should stop gracefully even with pending messages
	// This test mainly verifies no deadlock occurs
}

func TestEventBusContextCancellation(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBusInstance := NewEventBus(logger)
	b := eventBusInstance.(*basicEventBus)

	err := eventBusInstance.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}

	// Cancel the context directly
	b.cancel()

	// Give time for the goroutine to stop
	time.Sleep(50 * time.Millisecond)

	// The started flag might not be reset by context cancellation alone
	// Context cancellation just stops the goroutine, Stop() resets the flag
	// This test verifies that context cancellation doesn't cause deadlock

	// Clean up - this should not cause issues even though context is cancelled
	eventBusInstance.Stop()
}

func TestEventBusAutomaticParameterDetection(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBus := NewEventBus(logger)
	err := eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	subscriber := NewMockSubscriber()

	// Test that patterns with extractions automatically get parameter extraction
	eventBus.Subscribe(subscriber, "api/+version/users/+userId")

	// Test that patterns without extractions work normally
	eventBus.Subscribe(subscriber, "simple/topic")
	eventBus.Subscribe(subscriber, "wildcard/+/pattern")

	// Give time for subscriptions to be processed
	time.Sleep(10 * time.Millisecond)

	// Publish event that should extract parameters
	eventBus.Publish("api/v1/users/123", "user data")

	// Publish event to simple topic
	eventBus.Publish("simple/topic", "simple message")

	// Publish event to wildcard pattern (no extraction)
	eventBus.Publish("wildcard/anything/pattern", "wildcard message")

	// Give time for events to be processed
	time.Sleep(10 * time.Millisecond)

	events := subscriber.GetEvents()
	if len(events) != 3 {
		t.Fatalf("Expected 3 events, got %d", len(events))
	}

	// Check parameter extraction worked automatically
	foundExtractionEvent := false
	for _, event := range events {
		if event.Topic == "api/v1/users/123" {
			foundExtractionEvent = true
			if event.Fields == nil {
				t.Error("Expected extracted fields for parameterized pattern")
			} else {
				if event.Fields["version"] != "v1" {
					t.Errorf("Expected version 'v1', got '%s'", event.Fields["version"])
				}
				if event.Fields["userId"] != "123" {
					t.Errorf("Expected userId '123', got '%s'", event.Fields["userId"])
				}
			}
			break
		}
	}

	if !foundExtractionEvent {
		t.Error("Expected to find event with parameter extraction")
	}
}

func TestEventBusPublishSync(t *testing.T) {
	eventBus := NewEventBus(zaptest.NewLogger(t))
	err := eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start EventBus: %v", err)
	}
	defer eventBus.Stop()

	mockSub := &MockSubscriber{}
	err = eventBus.Subscribe(mockSub, "test/sync")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Test successful PublishSync
	err = eventBus.PublishSync("test/sync", "sync message")
	if err != nil {
		t.Errorf("PublishSync should not return error, got: %v", err)
	}

	// Check that the message was delivered
	if len(mockSub.events) != 1 {
		t.Errorf("Expected 1 event, got %d", len(mockSub.events))
	} else {
		event := mockSub.events[0]
		if event.Topic != "test/sync" || event.Message != "sync message" {
			t.Errorf("Expected topic='test/sync' and message='sync message', got topic='%s' and message='%v'",
				event.Topic, event.Message)
		}
	}

	// Test PublishSync with no matching subscribers
	err = eventBus.PublishSync("test/nomatch", "no subscribers")
	if err != nil {
		t.Errorf("PublishSync with no subscribers should not return error, got: %v", err)
	}

	// Should still only have 1 event
	if len(mockSub.events) != 1 {
		t.Errorf("Expected still 1 event after no-match publish, got %d", len(mockSub.events))
	}
}

func TestEventBusPublishSyncWithError(t *testing.T) {
	eventBus := NewEventBus(zaptest.NewLogger(t))
	err := eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start EventBus: %v", err)
	}
	defer eventBus.Stop()

	// Create a subscriber that returns an error
	errorSub := &MockSubscriber{simulateError: true}
	err = eventBus.Subscribe(errorSub, "test/error")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Test PublishSync with error
	err = eventBus.PublishSync("test/error", "error message")
	if err == nil {
		t.Error("PublishSync should return error when subscriber fails")
	}

	expectedError := "simulated error"
	if err.Error() != expectedError {
		t.Errorf("Expected error '%s', got '%s'", expectedError, err.Error())
	}
}

func TestEventBusPublishSyncBeforeStart(t *testing.T) {
	eventBus := NewEventBus(zaptest.NewLogger(t))
	// Don't start the EventBus

	// Test PublishSync on stopped EventBus
	err := eventBus.PublishSync("test/topic", "message")
	if err == nil {
		t.Error("PublishSync should return error when EventBus is not started")
	}

	expectedError := "event bus not started"
	if err.Error() != expectedError {
		t.Errorf("Expected error '%s', got '%s'", expectedError, err.Error())
	}
}

func TestEventBusWithObservability(t *testing.T) {
	// Create a simple test metrics provider
	metrics := &testMetricsProvider{
		counters:   make(map[string]*testCounter),
		histograms: make(map[string]*testHistogram),
		gauges:     make(map[string]*testGauge),
	}

	eventBus := NewEventBusWithObservability(zaptest.NewLogger(t), &ObservabilityConfig{
		MetricsProvider: metrics,
		ServiceName:     "test-service",
		ServiceVersion:  "v1.0.0",
	})

	err := eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start EventBus: %v", err)
	}
	defer eventBus.Stop()

	mockSub := &MockSubscriber{}
	err = eventBus.Subscribe(mockSub, "test/metrics")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Test that metrics are recorded
	eventBus.Publish("test/metrics", "test message")
	eventBus.PublishSync("test/metrics", "sync test message")

	// Verify metrics were recorded
	if publishCounter := metrics.counters["eventbus_messages_published_total"]; publishCounter == nil {
		t.Error("Expected publish counter to be created")
	} else if publishCounter.value != 1 {
		t.Errorf("Expected publish counter value to be 1, got %d", publishCounter.value)
	}

	if syncCounter := metrics.counters["eventbus_messages_published_sync_total"]; syncCounter == nil {
		t.Error("Expected sync publish counter to be created")
	} else if syncCounter.value != 1 {
		t.Errorf("Expected sync publish counter value to be 1, got %d", syncCounter.value)
	}

	if subscriberGauge := metrics.gauges["eventbus_active_subscribers"]; subscriberGauge == nil {
		t.Error("Expected subscriber gauge to be created")
	} else if subscriberGauge.value != 1.0 {
		t.Errorf("Expected subscriber gauge value to be 1.0, got %f", subscriberGauge.value)
	}
}

// Test implementations for observability
type testMetricsProvider struct {
	counters   map[string]*testCounter
	histograms map[string]*testHistogram
	gauges     map[string]*testGauge
}

func (p *testMetricsProvider) Counter(name string) Counter {
	if p.counters[name] == nil {
		p.counters[name] = &testCounter{}
	}
	return p.counters[name]
}

func (p *testMetricsProvider) Histogram(name string) Histogram {
	if p.histograms[name] == nil {
		p.histograms[name] = &testHistogram{}
	}
	return p.histograms[name]
}

func (p *testMetricsProvider) Gauge(name string) Gauge {
	if p.gauges[name] == nil {
		p.gauges[name] = &testGauge{}
	}
	return p.gauges[name]
}

type testCounter struct {
	value int64
}

func (c *testCounter) Add(ctx context.Context, value int64, labels ...Label) {
	c.value += value
}

type testHistogram struct {
	values []float64
}

func (h *testHistogram) Record(ctx context.Context, value float64, labels ...Label) {
	h.values = append(h.values, value)
}

type testGauge struct {
	value float64
}

func (g *testGauge) Set(ctx context.Context, value float64, labels ...Label) {
	g.value = value
}
