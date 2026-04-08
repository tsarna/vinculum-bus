package bus

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tsarna/vinculum-bus/o11y"
	"go.opentelemetry.io/otel/metric/noop"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
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

// newTestMeterProvider creates an sdkmetric.MeterProvider with a ManualReader for testing.
func newTestMeterProvider() (*sdkmetric.MeterProvider, *sdkmetric.ManualReader) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	return mp, reader
}


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

func TestEventBusBuilder_WithMeterProvider(t *testing.T) {
	logger := zaptest.NewLogger(t)
	mp := noop.NewMeterProvider()

	eventBus, err := NewEventBus().
		WithLogger(logger).
		WithMeterProvider(mp).
		WithServiceInfo("test-service", "1.0.0").
		Build()

	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}

	b := eventBus.(*basicEventBus)
	if !b.hasMetrics {
		t.Error("Expected hasMetrics to be true")
	}
}

func TestEventBusBuilder_WithTracerProvider(t *testing.T) {
	logger := zaptest.NewLogger(t)
	tp := sdktrace.NewTracerProvider()
	defer tp.Shutdown(context.Background()) //nolint:errcheck

	eventBus, err := NewEventBus().
		WithLogger(logger).
		WithTracerProvider(tp).
		WithName("mybus").
		Build()

	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}

	b := eventBus.(*basicEventBus)
	if b.tracer == nil {
		t.Error("Expected tracer to be set")
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

	mp := noop.NewMeterProvider()
	result3 := builder.WithMeterProvider(mp)
	if result3 != builder {
		t.Error("WithMeterProvider should return the same builder instance")
	}

	tp := sdktrace.NewTracerProvider()
	defer tp.Shutdown(context.Background()) //nolint:errcheck
	result4 := builder.WithTracerProvider(tp)
	if result4 != builder {
		t.Error("WithTracerProvider should return the same builder instance")
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
	eventBus.Subscribe(context.Background(), "test/topic", subscriber)

	// Give time for the subscription to be processed
	time.Sleep(10 * time.Millisecond)

	subscriptions := subscriber.GetSubscriptions()
	if len(subscriptions) != 1 || subscriptions[0] != "test/topic" {
		t.Errorf("Expected subscription to 'test/topic', got %v", subscriptions)
	}

	// Test unsubscription
	eventBus.Unsubscribe(context.Background(), "test/topic", subscriber)

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
	eventBus.Subscribe(context.Background(), "test/topic1", subscriber)
	eventBus.Subscribe(context.Background(), "test/topic2", subscriber)

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
	eventBus.Subscribe(context.Background(), "test/topic", subscriber)

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
	eventBus.Subscribe(context.Background(), "exact/topic", subscriber)

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
	eventBus.Subscribe(context.Background(), "test/+/topic", subscriber)

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
	eventBus.Subscribe(context.Background(), "test/#", subscriber)

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
	eventBus.Subscribe(context.Background(), "user/+userId/profile/+action", subscriber)

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
	eventBus.Subscribe(context.Background(), "shared/topic", subscriber1)
	eventBus.Subscribe(context.Background(), "shared/topic", subscriber2)

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
	eventBus.Subscribe(context.Background(), "device/+/data", subscriber)   // Matches device/123/data
	eventBus.Subscribe(context.Background(), "device/123/+", subscriber)    // Also matches device/123/data
	eventBus.Subscribe(context.Background(), "device/123/data", subscriber) // Exact match for device/123/data
	eventBus.Subscribe(context.Background(), "device/#", subscriber)        // Also matches device/123/data

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
	eventBus.Subscribe(context.Background(), "test/topic", subscriber)

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
		eventBus.Subscribe(context.Background(), "concurrent/test", subscribers[i])
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
	eventBus.Subscribe(context.Background(), "buffer/test", subscriber)

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
			eventBus.Subscribe(context.Background(), "config/test", subscriber)
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
	eventBus.Subscribe(context.Background(), "obs/test", subscriber)
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
	eventBus.Subscribe(context.Background(), "stop/test", subscriber)

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
	eventBus.Subscribe(context.Background(), "api/+version/users/+userId", subscriber)

	// Test that patterns without extractions work normally
	eventBus.Subscribe(context.Background(), "simple/topic", subscriber)
	eventBus.Subscribe(context.Background(), "wildcard/+/pattern", subscriber)

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
	err = eventBus.Subscribe(context.Background(), "test/sync", mockSub)
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
	err = eventBus.Subscribe(context.Background(), "test/error", errorSub)
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
	mp, reader := newTestMeterProvider()
	defer mp.Shutdown(context.Background()) //nolint:errcheck

	eventBus, err := NewEventBus().
		WithLogger(zaptest.NewLogger(t)).
		WithMeterProvider(mp).
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
	err = eventBus.Subscribe(context.Background(), "test/metrics", mockSub)
	if err != nil {
		t.Fatalf("Subscribe failed: %v", err)
	}

	// Test that metrics are recorded
	eventBus.Publish(context.Background(), "test/metrics", "test message")
	eventBus.PublishSync(context.Background(), "test/metrics", "sync test message")

	// Allow async processing
	time.Sleep(50 * time.Millisecond)

	// Collect metrics
	var rm metricdata.ResourceMetrics
	err = reader.Collect(context.Background(), &rm)
	if err != nil {
		t.Fatalf("Failed to collect metrics: %v", err)
	}

	// Build a map of metric name -> metric for easy lookup
	metrics := make(map[string]metricdata.Metrics)
	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			metrics[m.Name] = m
		}
	}

	// Verify publish counter exists
	if _, ok := metrics["messaging.client.sent.messages"]; !ok {
		t.Error("Expected messaging.client.sent.messages metric to be created")
	}

	// Verify operation duration histogram exists
	if _, ok := metrics["messaging.client.operation.duration"]; !ok {
		t.Error("Expected messaging.client.operation.duration metric to be created")
	}

	// Verify subscriber gauge exists
	if _, ok := metrics["eventbus.active_subscribers"]; !ok {
		t.Error("Expected eventbus.active_subscribers metric to be created")
	}
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

func TestStandaloneMeterProvider(t *testing.T) {
	eventBus, err := NewEventBus().WithLogger(zaptest.NewLogger(t)).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}
	err = eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start EventBus: %v", err)
	}
	defer eventBus.Stop()

	// Create standalone meter provider with fast interval for testing
	mp, _ := o11y.NewStandaloneMeterProvider(eventBus, &o11y.StandaloneMetricsConfig{
		Interval:     50 * time.Millisecond, // Fast for testing
		MetricsTopic: "$metrics",
		ServiceName:  "test-service",
	})
	defer mp.Shutdown(context.Background()) //nolint:errcheck

	// Create observable EventBus using the standalone provider
	observableEventBus, err := NewEventBus().
		WithLogger(zaptest.NewLogger(t)).
		WithMeterProvider(mp).
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

	eventBus.Subscribe(context.Background(), "$metrics", metricsSubscriber)

	// Generate some metrics
	testSub := &MockSubscriber{}
	observableEventBus.Subscribe(context.Background(), "test/topic", testSub)
	observableEventBus.Publish(context.Background(), "test/topic", "test message")
	observableEventBus.PublishSync(context.Background(), "test/topic", "sync test message")

	// Wait a bit for operations to complete
	time.Sleep(100 * time.Millisecond)

	// Wait for metrics to be published (at least one cycle)
	deadline := time.Now().Add(500 * time.Millisecond)
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

	// Check for specific metrics (now using OTel semconv names)
	if publishCount, exists := receivedMetrics.Counters["messaging.client.sent.messages"]; !exists || publishCount < 1 {
		t.Errorf("Expected messaging.client.sent.messages counter >= 1, got %f", publishCount)
	}

	if _, exists := receivedMetrics.Histograms["messaging.client.operation.duration"]; !exists {
		t.Error("Expected messaging.client.operation.duration histogram to be present")
	}

	if subscriberCount, exists := receivedMetrics.Gauges["eventbus.active_subscribers"]; !exists || subscriberCount < 1 {
		t.Errorf("Expected eventbus.active_subscribers gauge >= 1, got %f", subscriberCount)
	}
}

func TestEventBusSubscribeFunc(t *testing.T) {
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

	// Test basic SubscribeFunc functionality
	t.Run("basic functionality", func(t *testing.T) {
		var receivedEvents []Event
		var mu sync.Mutex

		// Define a simple receiver function
		receiver := func(ctx context.Context, topic string, message any, fields map[string]string) error {
			mu.Lock()
			defer mu.Unlock()
			receivedEvents = append(receivedEvents, Event{
				Topic:   topic,
				Message: message,
				Fields:  fields,
			})
			return nil
		}

		// Subscribe using SubscribeFunc
		subscriber, err := eventBus.SubscribeFunc(context.Background(), "test/func", receiver)
		if err != nil {
			t.Fatalf("SubscribeFunc failed: %v", err)
		}

		if subscriber == nil {
			t.Fatal("SubscribeFunc should return a non-nil Subscriber")
		}

		// Give time for subscription to be processed
		time.Sleep(10 * time.Millisecond)

		// Publish an event
		testMessage := "function subscriber test"
		err = eventBus.Publish(context.Background(), "test/func", testMessage)
		if err != nil {
			t.Fatalf("Publish failed: %v", err)
		}

		// Give time for event to be processed
		time.Sleep(10 * time.Millisecond)

		mu.Lock()
		events := make([]Event, len(receivedEvents))
		copy(events, receivedEvents)
		mu.Unlock()

		if len(events) != 1 {
			t.Fatalf("Expected 1 event, got %d", len(events))
		}

		event := events[0]
		if event.Topic != "test/func" {
			t.Errorf("Expected topic 'test/func', got '%s'", event.Topic)
		}
		if event.Message != testMessage {
			t.Errorf("Expected message '%s', got '%v'", testMessage, event.Message)
		}
		if event.Fields != nil {
			t.Errorf("Expected nil fields for exact match, got %v", event.Fields)
		}
	})

	// Test that returned subscriber can be used for unsubscription
	t.Run("unsubscribe returned subscriber", func(t *testing.T) {
		var eventCount int32
		receiver := func(ctx context.Context, topic string, message any, fields map[string]string) error {
			atomic.AddInt32(&eventCount, 1)
			return nil
		}

		// Subscribe using SubscribeFunc
		subscriber, err := eventBus.SubscribeFunc(context.Background(), "test/unsub", receiver)
		if err != nil {
			t.Fatalf("SubscribeFunc failed: %v", err)
		}

		// Give time for subscription to be processed
		time.Sleep(10 * time.Millisecond)

		// Publish an event - should be received
		err = eventBus.Publish(context.Background(), "test/unsub", "message1")
		if err != nil {
			t.Fatalf("Publish failed: %v", err)
		}

		time.Sleep(10 * time.Millisecond)

		if atomic.LoadInt32(&eventCount) != 1 {
			t.Errorf("Expected 1 event before unsubscribe, got %d", atomic.LoadInt32(&eventCount))
		}

		// Unsubscribe using the returned subscriber
		err = eventBus.Unsubscribe(context.Background(), "test/unsub", subscriber)
		if err != nil {
			t.Fatalf("Unsubscribe failed: %v", err)
		}

		time.Sleep(10 * time.Millisecond)

		// Publish another event - should not be received
		err = eventBus.Publish(context.Background(), "test/unsub", "message2")
		if err != nil {
			t.Fatalf("Publish failed: %v", err)
		}

		time.Sleep(10 * time.Millisecond)

		if atomic.LoadInt32(&eventCount) != 1 {
			t.Errorf("Expected still 1 event after unsubscribe, got %d", atomic.LoadInt32(&eventCount))
		}
	})

	// Test SubscribeFunc with parameter extraction
	t.Run("parameter extraction", func(t *testing.T) {
		var receivedEvent Event
		var mu sync.Mutex
		var eventReceived bool

		receiver := func(ctx context.Context, topic string, message any, fields map[string]string) error {
			mu.Lock()
			defer mu.Unlock()
			receivedEvent = Event{
				Topic:   topic,
				Message: message,
				Fields:  fields,
			}
			eventReceived = true
			return nil
		}

		// Subscribe with parameter extraction pattern
		subscriber, err := eventBus.SubscribeFunc(context.Background(), "user/+userId/action/+action", receiver)
		if err != nil {
			t.Fatalf("SubscribeFunc failed: %v", err)
		}

		time.Sleep(10 * time.Millisecond)

		// Publish event with parameters
		err = eventBus.Publish(context.Background(), "user/123/action/login", "user login data")
		if err != nil {
			t.Fatalf("Publish failed: %v", err)
		}

		time.Sleep(10 * time.Millisecond)

		mu.Lock()
		defer mu.Unlock()

		if !eventReceived {
			t.Fatal("Expected to receive event")
		}

		if receivedEvent.Fields == nil {
			t.Fatal("Expected extracted fields, got nil")
		}

		if receivedEvent.Fields["userId"] != "123" {
			t.Errorf("Expected userId '123', got '%s'", receivedEvent.Fields["userId"])
		}

		if receivedEvent.Fields["action"] != "login" {
			t.Errorf("Expected action 'login', got '%s'", receivedEvent.Fields["action"])
		}

		// Clean up
		eventBus.Unsubscribe(context.Background(), "user/+userId/action/+action", subscriber)
	})

	// Test error handling when receiver function returns error
	t.Run("error handling", func(t *testing.T) {
		var errorCount int32
		receiver := func(ctx context.Context, topic string, message any, fields map[string]string) error {
			atomic.AddInt32(&errorCount, 1)
			return fmt.Errorf("receiver error")
		}

		subscriber, err := eventBus.SubscribeFunc(context.Background(), "test/error", receiver)
		if err != nil {
			t.Fatalf("SubscribeFunc failed: %v", err)
		}

		time.Sleep(10 * time.Millisecond)

		// Publish event - receiver will return error but event bus should continue
		err = eventBus.Publish(context.Background(), "test/error", "error test")
		if err != nil {
			t.Fatalf("Publish failed: %v", err)
		}

		time.Sleep(10 * time.Millisecond)

		if atomic.LoadInt32(&errorCount) != 1 {
			t.Errorf("Expected receiver to be called once, got %d", atomic.LoadInt32(&errorCount))
		}

		// Clean up
		eventBus.Unsubscribe(context.Background(), "test/error", subscriber)
	})

	// Test SubscribeFunc with wildcard patterns
	t.Run("wildcard patterns", func(t *testing.T) {
		var receivedTopics []string
		var mu sync.Mutex

		receiver := func(ctx context.Context, topic string, message any, fields map[string]string) error {
			mu.Lock()
			defer mu.Unlock()
			receivedTopics = append(receivedTopics, topic)
			return nil
		}

		// Subscribe with single-level wildcard
		subscriber, err := eventBus.SubscribeFunc(context.Background(), "wildcard/+/test", receiver)
		if err != nil {
			t.Fatalf("SubscribeFunc failed: %v", err)
		}

		time.Sleep(10 * time.Millisecond)

		// Publish events that should match
		eventBus.Publish(context.Background(), "wildcard/abc/test", "message1")
		eventBus.Publish(context.Background(), "wildcard/xyz/test", "message2")

		// Publish event that should not match
		eventBus.Publish(context.Background(), "wildcard/abc/xyz/test", "message3")

		time.Sleep(10 * time.Millisecond)

		mu.Lock()
		topics := make([]string, len(receivedTopics))
		copy(topics, receivedTopics)
		mu.Unlock()

		if len(topics) != 2 {
			t.Errorf("Expected 2 matching topics, got %d: %v", len(topics), topics)
		}

		expectedTopics := []string{"wildcard/abc/test", "wildcard/xyz/test"}
		for _, expected := range expectedTopics {
			found := false
			for _, received := range topics {
				if received == expected {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Expected to receive topic '%s', but didn't", expected)
			}
		}

		// Clean up
		eventBus.Unsubscribe(context.Background(), "wildcard/+/test", subscriber)
	})

	// Test SubscribeFunc before event bus is started
	t.Run("subscribe before start", func(t *testing.T) {
		// Create a new event bus but don't start it
		stoppedBus, err := NewEventBus().WithLogger(logger).Build()
		if err != nil {
			t.Fatalf("Build() returned error: %v", err)
		}

		receiver := func(ctx context.Context, topic string, message any, fields map[string]string) error {
			return nil
		}

		// SubscribeFunc should return error when bus is not started
		subscriber, err := stoppedBus.SubscribeFunc(context.Background(), "test/stopped", receiver)
		if err == nil {
			t.Error("Expected SubscribeFunc to return error when bus is not started")
		}
		if subscriber != nil {
			t.Error("Expected nil subscriber when SubscribeFunc fails")
		}
	})
}

// ── Tracing tests ─────────────────────────────────────────────────────────────

func setupTestTracer(t *testing.T) (*tracetest.InMemoryExporter, *sdktrace.TracerProvider) {
	t.Helper()
	exporter := tracetest.NewInMemoryExporter()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	t.Cleanup(func() { tp.Shutdown(context.Background()) }) //nolint:errcheck
	return exporter, tp
}

func makeTracedBus(t *testing.T, tp *sdktrace.TracerProvider) EventBus {
	t.Helper()
	eb, err := NewEventBus().
		WithLogger(zaptest.NewLogger(t)).
		WithTracerProvider(tp).
		WithName("testbus").
		Build()
	require.NoError(t, err)
	require.NoError(t, eb.Start())
	t.Cleanup(func() { eb.Stop() })
	return eb
}

func TestTracing_PublishCreatesProducerSpan(t *testing.T) {
	exporter, tp := setupTestTracer(t)
	eb := makeTracedBus(t, tp)

	err := eb.Publish(context.Background(), "a/b", "msg")
	require.NoError(t, err)
	time.Sleep(20 * time.Millisecond) // let the goroutine flush

	spans := exporter.GetSpans()
	var publishSpan *tracetest.SpanStub
	for i := range spans {
		if spans[i].Name == "publish a/b" {
			publishSpan = &spans[i]
		}
	}
	require.NotNil(t, publishSpan, "expected a publish span")
	assert.Equal(t, "producer", publishSpan.SpanKind.String())
}

func TestTracing_PublishSyncCreatesProducerAndChildConsumerSpans(t *testing.T) {
	exporter, tp := setupTestTracer(t)
	eb := makeTracedBus(t, tp)

	target := &MockSubscriber{}
	require.NoError(t, eb.Subscribe(context.Background(), "a/b", target))
	require.NoError(t, eb.PublishSync(context.Background(), "a/b", "msg"))

	spans := exporter.GetSpans()

	var publishSpan, deliverSpan *tracetest.SpanStub
	for i := range spans {
		switch spans[i].Name {
		case "publish a/b":
			publishSpan = &spans[i]
		case "process a/b":
			deliverSpan = &spans[i]
		}
	}
	require.NotNil(t, publishSpan, "expected a publish span")
	require.NotNil(t, deliverSpan, "expected a process span")

	assert.Equal(t, "producer", publishSpan.SpanKind.String())
	assert.Equal(t, "consumer", deliverSpan.SpanKind.String())

	// delivery span is a child of the publish span (same trace, parent = publish)
	assert.Equal(t, publishSpan.SpanContext.TraceID(), deliverSpan.SpanContext.TraceID(),
		"deliver span should be in the same trace as publish span")
	assert.Equal(t, publishSpan.SpanContext.SpanID(), deliverSpan.Parent.SpanID(),
		"deliver span parent should be the publish span")
}

func TestTracing_AsyncPublishCreatesLinkedConsumerSpan(t *testing.T) {
	exporter, tp := setupTestTracer(t)
	eb := makeTracedBus(t, tp)

	target := &MockSubscriber{}
	require.NoError(t, eb.Subscribe(context.Background(), "a/b", target))
	require.NoError(t, eb.Publish(context.Background(), "a/b", "msg"))

	// Wait for async delivery
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		target.mu.RLock()
		n := len(target.events)
		target.mu.RUnlock()
		if n > 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	spans := exporter.GetSpans()
	var publishSpan, deliverSpan *tracetest.SpanStub
	for i := range spans {
		switch spans[i].Name {
		case "publish a/b":
			publishSpan = &spans[i]
		case "process a/b":
			deliverSpan = &spans[i]
		}
	}
	require.NotNil(t, publishSpan, "expected a publish span")
	require.NotNil(t, deliverSpan, "expected a process span")

	assert.Equal(t, "consumer", deliverSpan.SpanKind.String())

	// delivery span is a new root (different trace from publish)
	assert.NotEqual(t, publishSpan.SpanContext.TraceID(), deliverSpan.SpanContext.TraceID(),
		"async deliver span should be a new root trace, not a child of publish")

	// delivery span has a link to the publish span
	require.Len(t, deliverSpan.Links, 1, "expected one link on the deliver span")
	assert.Equal(t, publishSpan.SpanContext.TraceID(), deliverSpan.Links[0].SpanContext.TraceID(),
		"link should point to the publish span's trace")
	assert.Equal(t, publishSpan.SpanContext.SpanID(), deliverSpan.Links[0].SpanContext.SpanID(),
		"link should point to the publish span")
}
