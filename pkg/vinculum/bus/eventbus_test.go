package bus

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tsarna/vinculum/pkg/vinculum/o11y"
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

func (m *MockSubscriber) OnSubscribe(ctx context.Context, topic string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subscriptions = append(m.subscriptions, topic)
	return nil
}

func (m *MockSubscriber) OnUnsubscribe(ctx context.Context, topic string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unsubscriptions = append(m.unsubscriptions, topic)
	return nil
}

func (m *MockSubscriber) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
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

// Mock implementations for testing observability
type mockMetricsProvider struct{}

func (m *mockMetricsProvider) Counter(name string) o11y.Counter     { return &mockCounter{} }
func (m *mockMetricsProvider) Histogram(name string) o11y.Histogram { return &mockHistogram{} }
func (m *mockMetricsProvider) Gauge(name string) o11y.Gauge         { return &mockGauge{} }

type mockCounter struct{}

func (m *mockCounter) Add(ctx context.Context, value int64, labels ...o11y.Label) {}

type mockHistogram struct{}

func (m *mockHistogram) Record(ctx context.Context, value float64, labels ...o11y.Label) {}

type mockGauge struct{}

func (m *mockGauge) Set(ctx context.Context, value float64, labels ...o11y.Label) {}

type mockTracingProvider struct{}

func (m *mockTracingProvider) StartSpan(ctx context.Context, name string) (context.Context, o11y.Span) {
	return ctx, &mockSpan{}
}

type mockSpan struct{}

func (m *mockSpan) SetAttributes(labels ...o11y.Label)                     {}
func (m *mockSpan) SetStatus(code o11y.SpanStatusCode, description string) {}
func (m *mockSpan) End()                                                   {}

func TestNewEventBus(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBusInstance, err := NewEventBus().WithLogger(logger).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	if eventBusInstance == nil {
		t.Fatal("Builder returned nil")
	}

	// Verify the event bus is not started initially
	b, ok := eventBusInstance.(*basicEventBus)
	if !ok {
		t.Fatal("Builder should return *basicEventBus type")
	}
	if atomic.LoadInt32(&b.started) != 0 {
		t.Error("EventBus should not be started initially")
	}
}

func TestEventBusBuilder_Basic(t *testing.T) {
	logger := zaptest.NewLogger(t)

	eventBus, err := NewEventBus().
		WithLogger(logger).
		Build()

	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	if eventBus == nil {
		t.Fatal("Builder returned nil")
	}

	// Verify the event bus is not started initially
	b, ok := eventBus.(*basicEventBus)
	if !ok {
		t.Fatal("Builder should return *basicEventBus type")
	}
	if atomic.LoadInt32(&b.started) != 0 {
		t.Error("EventBus should not be started initially")
	}

	// Verify default buffer size
	if cap(b.ch) != 1000 {
		t.Errorf("Expected default buffer size 1000, got %d", cap(b.ch))
	}
}

func TestEventBusBuilder_WithBufferSize(t *testing.T) {
	logger := zaptest.NewLogger(t)

	eventBus, err := NewEventBus().
		WithLogger(logger).
		WithBufferSize(500).
		Build()

	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}

	b := eventBus.(*basicEventBus)
	if cap(b.ch) != 500 {
		t.Errorf("Expected buffer size 500, got %d", cap(b.ch))
	}
}

func TestEventBusBuilder_WithObservability(t *testing.T) {
	logger := zaptest.NewLogger(t)
	mockMetrics := &mockMetricsProvider{}
	mockTracing := &mockTracingProvider{}

	eventBus, err := NewEventBus().
		WithLogger(logger).
		WithObservability(mockMetrics, mockTracing).
		WithServiceInfo("test-service", "1.0.0").
		Build()

	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}

	b := eventBus.(*basicEventBus)
	if b.metricsProvider != mockMetrics {
		t.Error("Expected metrics provider to be set")
	}
	if b.tracingProvider != mockTracing {
		t.Error("Expected tracing provider to be set")
	}
}

func TestEventBusBuilder_WithMetricsAndTracing(t *testing.T) {
	logger := zaptest.NewLogger(t)
	mockMetrics := &mockMetricsProvider{}
	mockTracing := &mockTracingProvider{}

	eventBus, err := NewEventBus().
		WithLogger(logger).
		WithMetrics(mockMetrics).
		WithTracing(mockTracing).
		Build()

	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}

	b := eventBus.(*basicEventBus)
	if b.metricsProvider != mockMetrics {
		t.Error("Expected metrics provider to be set")
	}
	if b.tracingProvider != mockTracing {
		t.Error("Expected tracing provider to be set")
	}
}

func TestEventBusBuilder_BuildWithoutLogger(t *testing.T) {
	// Should not return error and should use nop logger
	eventBus, err := NewEventBus().Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	if eventBus == nil {
		t.Error("Expected EventBus instance but got nil")
	}

	// Verify it uses nop logger by checking the internal structure
	b, ok := eventBus.(*basicEventBus)
	if !ok {
		t.Fatal("Expected *basicEventBus type")
	}
	if b.logger == nil {
		t.Error("Expected logger to be set (should be nop logger)")
	}
}

func TestEventBusBuilder_FluentInterface(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Test that all methods return the builder for chaining
	builder := NewEventBus()

	result1 := builder.WithLogger(logger)
	if result1 != builder {
		t.Error("WithLogger should return the same builder instance")
	}

	result2 := builder.WithBufferSize(200)
	if result2 != builder {
		t.Error("WithBufferSize should return the same builder instance")
	}

	mockMetrics := &mockMetricsProvider{}
	result3 := builder.WithMetrics(mockMetrics)
	if result3 != builder {
		t.Error("WithMetrics should return the same builder instance")
	}

	mockTracing := &mockTracingProvider{}
	result4 := builder.WithTracing(mockTracing)
	if result4 != builder {
		t.Error("WithTracing should return the same builder instance")
	}

	result5 := builder.WithServiceInfo("test", "1.0")
	if result5 != builder {
		t.Error("WithServiceInfo should return the same builder instance")
	}
}

func TestEventBusBuilder_IsValid(t *testing.T) {
	tests := []struct {
		name          string
		setupBuilder  func() *EventBusBuilder
		expectError   bool
		errorContains string
	}{
		{
			name: "valid configuration",
			setupBuilder: func() *EventBusBuilder {
				return NewEventBus().WithLogger(zaptest.NewLogger(t))
			},
			expectError: false,
		},
		{
			name: "missing logger is valid (uses nop logger)",
			setupBuilder: func() *EventBusBuilder {
				return NewEventBus()
			},
			expectError: false,
		},
		{
			name: "zero buffer size",
			setupBuilder: func() *EventBusBuilder {
				return NewEventBus().
					WithLogger(zaptest.NewLogger(t)).
					WithBufferSize(0)
			},
			expectError:   true,
			errorContains: "buffer size must be positive",
		},
		{
			name: "negative buffer size",
			setupBuilder: func() *EventBusBuilder {
				return NewEventBus().
					WithLogger(zaptest.NewLogger(t)).
					WithBufferSize(-100)
			},
			expectError:   true,
			errorContains: "buffer size must be positive",
		},
		{
			name: "service name without version",
			setupBuilder: func() *EventBusBuilder {
				return NewEventBus().
					WithLogger(zaptest.NewLogger(t)).
					WithServiceInfo("my-service", "")
			},
			expectError:   true,
			errorContains: "both service name and version must be provided together",
		},
		{
			name: "service version without name",
			setupBuilder: func() *EventBusBuilder {
				return NewEventBus().
					WithLogger(zaptest.NewLogger(t)).
					WithServiceInfo("", "1.0.0")
			},
			expectError:   true,
			errorContains: "both service name and version must be provided together",
		},
		{
			name: "valid service info",
			setupBuilder: func() *EventBusBuilder {
				return NewEventBus().
					WithLogger(zaptest.NewLogger(t)).
					WithServiceInfo("my-service", "1.0.0")
			},
			expectError: false,
		},
		{
			name: "empty service info is valid",
			setupBuilder: func() *EventBusBuilder {
				return NewEventBus().
					WithLogger(zaptest.NewLogger(t)).
					WithServiceInfo("", "")
			},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			builder := tt.setupBuilder()
			err := builder.IsValid()

			if tt.expectError {
				if err == nil {
					t.Error("Expected validation error but got none")
				} else if tt.errorContains != "" && !strings.Contains(err.Error(), tt.errorContains) {
					t.Errorf("Expected error to contain '%s', got: %s", tt.errorContains, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected no validation error but got: %s", err.Error())
				}
			}
		})
	}
}

func TestEventBusBuilder_Build_WithErrors(t *testing.T) {
	t.Run("valid configuration", func(t *testing.T) {
		logger := zaptest.NewLogger(t)
		eventBus, err := NewEventBus().
			WithLogger(logger).
			Build()

		if err != nil {
			t.Errorf("Expected no error but got: %s", err.Error())
		}
		if eventBus == nil {
			t.Error("Expected EventBus instance but got nil")
		}
	})

	t.Run("invalid configuration", func(t *testing.T) {
		eventBus, err := NewEventBus().
			WithBufferSize(-1).
			Build()

		if err == nil {
			t.Error("Expected validation error but got none")
		}
		if eventBus != nil {
			t.Error("Expected nil EventBus but got instance")
		}
	})
}

func TestEventBusStartStop(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBus, err := NewEventBus().WithLogger(logger).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}

	// Test starting the event bus
	err = eventBus.Start()
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
	eventBus, err := NewEventBus().WithLogger(logger).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	err = eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	subscriber := NewMockSubscriber()

	// Test subscription
	eventBus.Subscribe(context.Background(), subscriber, "test/topic")

	// Give time for the subscription to be processed
	time.Sleep(10 * time.Millisecond)

	subscriptions := subscriber.GetSubscriptions()
	if len(subscriptions) != 1 || subscriptions[0] != "test/topic" {
		t.Errorf("Expected subscription to 'test/topic', got %v", subscriptions)
	}

	// Test unsubscription
	eventBus.Unsubscribe(context.Background(), subscriber, "test/topic")

	// Give time for the unsubscription to be processed
	time.Sleep(10 * time.Millisecond)

	unsubscriptions := subscriber.GetUnsubscriptions()
	if len(unsubscriptions) != 1 || unsubscriptions[0] != "test/topic" {
		t.Errorf("Expected unsubscription from 'test/topic', got %v", unsubscriptions)
	}
}

func TestEventBusUnsubscribeAll(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBus, err := NewEventBus().WithLogger(logger).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	err = eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	subscriber := NewMockSubscriber()

	// Subscribe to multiple topics
	eventBus.Subscribe(context.Background(), subscriber, "test/topic1")
	eventBus.Subscribe(context.Background(), subscriber, "test/topic2")

	// Give time for subscriptions to be processed
	time.Sleep(10 * time.Millisecond)

	// Unsubscribe from all
	eventBus.UnsubscribeAll(context.Background(), subscriber)

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
	eventBus, err := NewEventBus().WithLogger(logger).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	err = eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	subscriber := NewMockSubscriber()
	eventBus.Subscribe(context.Background(), subscriber, "test/topic")

	// Give time for subscription to be processed
	time.Sleep(10 * time.Millisecond)

	// Publish an event
	testMessage := "test message"
	eventBus.Publish(context.Background(), "test/topic", testMessage)

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
	eventBus, err := NewEventBus().WithLogger(logger).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	err = eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	subscriber := NewMockSubscriber()
	eventBus.Subscribe(context.Background(), subscriber, "exact/topic")

	// Give time for subscription to be processed
	time.Sleep(10 * time.Millisecond)

	// Publish to exact topic
	eventBus.Publish(context.Background(), "exact/topic", "message1")

	// Publish to different topic
	eventBus.Publish(context.Background(), "exact/different", "message2")
	eventBus.Publish(context.Background(), "different/topic", "message3")

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
	eventBus, err := NewEventBus().WithLogger(logger).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	err = eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	subscriber := NewMockSubscriber()

	// Subscribe with single-level wildcard
	eventBus.Subscribe(context.Background(), subscriber, "test/+/topic")

	// Give time for subscription to be processed
	time.Sleep(10 * time.Millisecond)

	// Publish events
	eventBus.Publish(context.Background(), "test/abc/topic", "message1")     // Should match
	eventBus.Publish(context.Background(), "test/xyz/topic", "message2")     // Should match
	eventBus.Publish(context.Background(), "test/abc/xyz/topic", "message3") // Should not match (multi-level)
	eventBus.Publish(context.Background(), "test/topic", "message4")         // Should not match (no middle part)

	// Give time for events to be processed
	time.Sleep(10 * time.Millisecond)

	events := subscriber.GetEvents()
	if len(events) != 2 {
		t.Fatalf("Expected 2 events, got %d", len(events))
	}
}

func TestEventBusMultilevelWildcardMatching(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBus, err := NewEventBus().WithLogger(logger).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	err = eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	subscriber := NewMockSubscriber()

	// Subscribe with multi-level wildcard
	eventBus.Subscribe(context.Background(), subscriber, "test/#")

	// Give time for subscription to be processed
	time.Sleep(10 * time.Millisecond)

	// Publish events
	eventBus.Publish(context.Background(), "test/abc", "message1")         // Should match
	eventBus.Publish(context.Background(), "test/abc/def", "message2")     // Should match
	eventBus.Publish(context.Background(), "test/abc/def/ghi", "message3") // Should match
	eventBus.Publish(context.Background(), "other/abc", "message4")        // Should not match

	// Give time for events to be processed
	time.Sleep(10 * time.Millisecond)

	events := subscriber.GetEvents()
	if len(events) != 3 {
		t.Fatalf("Expected 3 events, got %d", len(events))
	}
}

func TestEventBusParameterExtraction(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBus, err := NewEventBus().WithLogger(logger).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	err = eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	subscriber := NewMockSubscriber()

	// Subscribe with parameter extraction (automatically detected)
	eventBus.Subscribe(context.Background(), subscriber, "user/+userId/profile/+action")

	// Give time for subscription to be processed
	time.Sleep(10 * time.Millisecond)

	// Publish event with parameters
	eventBus.Publish(context.Background(), "user/123/profile/update", "profile data")

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
	eventBus, err := NewEventBus().WithLogger(logger).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	err = eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	subscriber1 := NewMockSubscriber()
	subscriber2 := NewMockSubscriber()

	// Both subscribe to the same topic
	eventBus.Subscribe(context.Background(), subscriber1, "shared/topic")
	eventBus.Subscribe(context.Background(), subscriber2, "shared/topic")

	// Give time for subscriptions to be processed
	time.Sleep(10 * time.Millisecond)

	// Publish event
	eventBus.Publish(context.Background(), "shared/topic", "broadcast message")

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
	eventBus, err := NewEventBus().WithLogger(logger).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	err = eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	subscriber := NewMockSubscriber()

	// Subscribe to multiple patterns that could match the same topic
	eventBus.Subscribe(context.Background(), subscriber, "device/+/data")   // Matches device/123/data
	eventBus.Subscribe(context.Background(), subscriber, "device/123/+")    // Also matches device/123/data
	eventBus.Subscribe(context.Background(), subscriber, "device/123/data") // Exact match for device/123/data
	eventBus.Subscribe(context.Background(), subscriber, "device/#")        // Also matches device/123/data

	// Give time for subscriptions to be processed
	time.Sleep(10 * time.Millisecond)

	// Publish an event that matches all patterns
	eventBus.Publish(context.Background(), "device/123/data", "sensor reading")

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
	eventBus, err := NewEventBus().WithLogger(logger).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	subscriber := NewMockSubscriber()

	// Try to subscribe before starting event bus
	eventBus.Subscribe(context.Background(), subscriber, "test/topic")

	// Try to publish before starting event bus
	eventBus.Publish(context.Background(), "test/topic", "message")

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
	eventBus, err := NewEventBus().WithLogger(logger).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	err = eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	const numSubscribers = 10
	const numMessages = 100

	subscribers := make([]*MockSubscriber, numSubscribers)
	for i := 0; i < numSubscribers; i++ {
		subscribers[i] = NewMockSubscriber()
		eventBus.Subscribe(context.Background(), subscribers[i], "concurrent/test")
	}

	// Give time for subscriptions to be processed
	time.Sleep(50 * time.Millisecond)

	// Publish messages concurrently
	var wg sync.WaitGroup
	for i := 0; i < numMessages; i++ {
		wg.Add(1)
		go func(msg int) {
			defer wg.Done()
			eventBus.Publish(context.Background(), "concurrent/test", msg)
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
	eventBus, err := NewEventBus().WithLogger(logger).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	err = eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	// Create a slow subscriber that takes time to process events
	subscriber := NewMockSubscriber()
	eventBus.Subscribe(context.Background(), subscriber, "buffer/test")

	// Give time for subscription to be processed
	time.Sleep(10 * time.Millisecond)

	// Send many messages quickly to test buffering
	const numMessages = 1500 // More than the default buffer size of 1000
	for i := 0; i < numMessages; i++ {
		eventBus.Publish(context.Background(), "buffer/test", i)
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

func TestEventBusConfigurableBuffer(t *testing.T) {
	tests := []struct {
		name       string
		bufferSize int
		expected   int
	}{
		{"Default buffer", 0, 1000},
		{"Custom small buffer", 50, 50},
		{"Custom large buffer", 2000, 2000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zaptest.NewLogger(t)

			// Test with builder pattern
			builder := NewEventBus().WithLogger(logger)

			// Only set buffer size if it's positive (builder validates this)
			if tt.bufferSize > 0 {
				builder = builder.WithBufferSize(tt.bufferSize)
			}

			eventBus, err := builder.Build()
			if err != nil {
				t.Fatalf("Build() returned error: %v", err)
			}
			eventBus.Start()
			defer eventBus.Stop()

			subscriber := NewMockSubscriber()
			eventBus.Subscribe(context.Background(), subscriber, "config/test")
			time.Sleep(10 * time.Millisecond)

			// Send messages equal to expected buffer size + 10
			numMessages := tt.expected + 10
			for i := 0; i < numMessages; i++ {
				eventBus.Publish(context.Background(), "config/test", i)
			}

			time.Sleep(50 * time.Millisecond)

			events := subscriber.GetEvents()
			t.Logf("Buffer size %d: Processed %d out of %d messages", tt.expected, len(events), numMessages)

			// Should receive at least some messages
			if len(events) == 0 {
				t.Error("Expected to receive at least some messages")
			}
		})
	}
}

func TestEventBusObservabilityConfigurableBuffer(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Test with builder pattern for observability buffer size
	bufferSize := 500
	eventBus, err := NewEventBus().WithLogger(logger).WithBufferSize(bufferSize).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	eventBus.Start()
	defer eventBus.Stop()

	subscriber := NewMockSubscriber()
	eventBus.Subscribe(context.Background(), subscriber, "obs/test")
	time.Sleep(10 * time.Millisecond)

	// Send messages equal to buffer size + 10
	numMessages := 510
	for i := 0; i < numMessages; i++ {
		eventBus.Publish(context.Background(), "obs/test", i)
	}

	time.Sleep(50 * time.Millisecond)

	events := subscriber.GetEvents()
	t.Logf("Observability buffer %d: Processed %d out of %d messages", bufferSize, len(events), numMessages)

	// Should receive at least some messages
	if len(events) == 0 {
		t.Error("Expected to receive at least some messages")
	}
}

func TestEventBusStopWithPendingMessages(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBus, err := NewEventBus().WithLogger(logger).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	err = eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}

	subscriber := NewMockSubscriber()
	eventBus.Subscribe(context.Background(), subscriber, "stop/test")

	// Give time for subscription to be processed
	time.Sleep(10 * time.Millisecond)

	// Send some messages
	for i := 0; i < 10; i++ {
		eventBus.Publish(context.Background(), "stop/test", i)
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
	eventBusInstance, err := NewEventBus().WithLogger(logger).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	b := eventBusInstance.(*basicEventBus)

	err = eventBusInstance.Start()
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
	eventBus, err := NewEventBus().WithLogger(logger).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	err = eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	subscriber := NewMockSubscriber()

	// Test that patterns with extractions automatically get parameter extraction
	eventBus.Subscribe(context.Background(), subscriber, "api/+version/users/+userId")

	// Test that patterns without extractions work normally
	eventBus.Subscribe(context.Background(), subscriber, "simple/topic")
	eventBus.Subscribe(context.Background(), subscriber, "wildcard/+/pattern")

	// Give time for subscriptions to be processed
	time.Sleep(10 * time.Millisecond)

	// Publish event that should extract parameters
	eventBus.Publish(context.Background(), "api/v1/users/123", "user data")

	// Publish event to simple topic
	eventBus.Publish(context.Background(), "simple/topic", "simple message")

	// Publish event to wildcard pattern (no extraction)
	eventBus.Publish(context.Background(), "wildcard/anything/pattern", "wildcard message")

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
	eventBus, err := NewEventBus().WithLogger(zaptest.NewLogger(t)).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	err = eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start EventBus: %v", err)
	}
	defer eventBus.Stop()

	mockSub := &MockSubscriber{}
	err = eventBus.Subscribe(context.Background(), mockSub, "test/sync")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Test successful PublishSync
	err = eventBus.PublishSync(context.Background(), "test/sync", "sync message")
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
	err = eventBus.PublishSync(context.Background(), "test/nomatch", "no subscribers")
	if err != nil {
		t.Errorf("PublishSync with no subscribers should not return error, got: %v", err)
	}

	// Should still only have 1 event
	if len(mockSub.events) != 1 {
		t.Errorf("Expected still 1 event after no-match publish, got %d", len(mockSub.events))
	}
}

func TestEventBusPublishSyncWithError(t *testing.T) {
	eventBus, err := NewEventBus().WithLogger(zaptest.NewLogger(t)).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	err = eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start EventBus: %v", err)
	}
	defer eventBus.Stop()

	// Create a subscriber that returns an error
	errorSub := &MockSubscriber{simulateError: true}
	err = eventBus.Subscribe(context.Background(), errorSub, "test/error")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Test PublishSync with error
	err = eventBus.PublishSync(context.Background(), "test/error", "error message")
	if err == nil {
		t.Error("PublishSync should return error when subscriber fails")
	}

	expectedError := "simulated error"
	if err.Error() != expectedError {
		t.Errorf("Expected error '%s', got '%s'", expectedError, err.Error())
	}
}

func TestEventBusPublishSyncBeforeStart(t *testing.T) {
	eventBus, err := NewEventBus().WithLogger(zaptest.NewLogger(t)).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	// Don't start the EventBus

	// Test PublishSync on stopped EventBus
	err = eventBus.PublishSync(context.Background(), "test/topic", "message")
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

	eventBus, err := NewEventBus().
		WithLogger(zaptest.NewLogger(t)).
		WithMetrics(metrics).
		WithServiceInfo("test-service", "v1.0.0").
		Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}

	err = eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start EventBus: %v", err)
	}
	defer eventBus.Stop()

	mockSub := &MockSubscriber{}
	err = eventBus.Subscribe(context.Background(), mockSub, "test/metrics")
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Test that metrics are recorded
	eventBus.Publish(context.Background(), "test/metrics", "test message")
	eventBus.PublishSync(context.Background(), "test/metrics", "sync test message")

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

func (p *testMetricsProvider) Counter(name string) o11y.Counter {
	if p.counters[name] == nil {
		p.counters[name] = &testCounter{}
	}
	return p.counters[name]
}

func (p *testMetricsProvider) Histogram(name string) o11y.Histogram {
	if p.histograms[name] == nil {
		p.histograms[name] = &testHistogram{}
	}
	return p.histograms[name]
}

func (p *testMetricsProvider) Gauge(name string) o11y.Gauge {
	if p.gauges[name] == nil {
		p.gauges[name] = &testGauge{}
	}
	return p.gauges[name]
}

type testCounter struct {
	value int64
}

func (c *testCounter) Add(ctx context.Context, value int64, labels ...o11y.Label) {
	c.value += value
}

type testHistogram struct {
	values []float64
}

func (h *testHistogram) Record(ctx context.Context, value float64, labels ...o11y.Label) {
	h.values = append(h.values, value)
}

type testGauge struct {
	value float64
}

func (g *testGauge) Set(ctx context.Context, value float64, labels ...o11y.Label) {
	g.value = value
}

// testMetricsSubscriber captures metrics snapshots for testing
type testMetricsSubscriber struct {
	BaseSubscriber
	receivedMetrics *o11y.MetricsSnapshot
	metricsMutex    *sync.Mutex
	metricsReceived *bool
}

func (s *testMetricsSubscriber) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	if topic == "$metrics" {
		if snapshot, ok := message.(o11y.MetricsSnapshot); ok {
			s.metricsMutex.Lock()
			*s.receivedMetrics = snapshot
			*s.metricsReceived = true
			s.metricsMutex.Unlock()
		}
	}
	return nil
}

func TestStandaloneMetricsProvider(t *testing.T) {
	eventBus, err := NewEventBus().WithLogger(zaptest.NewLogger(t)).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	err = eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start EventBus: %v", err)
	}
	defer eventBus.Stop()

	// Create standalone metrics provider with fast interval for testing
	metricsProvider := o11y.NewStandaloneMetricsProvider(eventBus, &o11y.StandaloneMetricsConfig{
		Interval:     50 * time.Millisecond, // Fast for testing
		MetricsTopic: "$metrics",
		ServiceName:  "test-service",
	})

	err = metricsProvider.Start()
	if err != nil {
		t.Fatalf("Failed to start metrics provider: %v", err)
	}
	defer metricsProvider.Stop()

	// Create observable EventBus using the standalone provider
	observableEventBus, err := NewEventBus().
		WithLogger(zaptest.NewLogger(t)).
		WithMetrics(metricsProvider).
		WithServiceInfo("test-service", "v1.0.0").
		Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}

	err = observableEventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start observable EventBus: %v", err)
	}
	defer observableEventBus.Stop()

	// Subscribe to metrics topic
	var receivedMetrics o11y.MetricsSnapshot
	var metricsMutex sync.Mutex
	metricsReceived := false

	// Custom subscriber to capture metrics
	metricsSubscriber := &testMetricsSubscriber{
		receivedMetrics: &receivedMetrics,
		metricsMutex:    &metricsMutex,
		metricsReceived: &metricsReceived,
	}

	eventBus.Subscribe(context.Background(), metricsSubscriber, "$metrics")

	// Generate some metrics
	testSub := &MockSubscriber{}
	observableEventBus.Subscribe(context.Background(), testSub, "test/topic")
	observableEventBus.Publish(context.Background(), "test/topic", "test message")
	observableEventBus.PublishSync(context.Background(), "test/topic", "sync test message")

	// Wait a bit for operations to complete
	time.Sleep(100 * time.Millisecond)

	// Wait for metrics to be published (at least one cycle)
	deadline := time.Now().Add(200 * time.Millisecond) // Wait for at least 4 cycles
	for time.Now().Before(deadline) {
		metricsMutex.Lock()
		received := metricsReceived
		metricsMutex.Unlock()

		if received {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	metricsMutex.Lock()
	defer metricsMutex.Unlock()

	if !metricsReceived {
		t.Fatal("Expected to receive metrics snapshot")
	}

	// Verify metrics content
	if receivedMetrics.ServiceName != "test-service" {
		t.Errorf("Expected service name 'test-service', got '%s'", receivedMetrics.ServiceName)
	}

	if receivedMetrics.Counters == nil {
		t.Error("Expected counters to be present")
	}

	if receivedMetrics.Histograms == nil {
		t.Error("Expected histograms to be present")
	}

	if receivedMetrics.Gauges == nil {
		t.Error("Expected gauges to be present")
	}

	// Check for specific metrics
	if publishCount, exists := receivedMetrics.Counters["eventbus_messages_published_total"]; !exists || publishCount < 1 {
		t.Errorf("Expected published messages counter >= 1, got %d", publishCount)
	}

	if syncCount, exists := receivedMetrics.Counters["eventbus_messages_published_sync_total"]; !exists || syncCount < 1 {
		t.Errorf("Expected sync published messages counter >= 1, got %d", syncCount)
	}

	if subscriberCount, exists := receivedMetrics.Gauges["eventbus_active_subscribers"]; !exists || subscriberCount < 1 {
		t.Errorf("Expected active subscribers gauge >= 1, got %f", subscriberCount)
	}
}
