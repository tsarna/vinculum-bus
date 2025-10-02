package subutils

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/tsarna/vinculum-bus"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

func TestNewLoggingSubscriber(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Test basic constructor with nil wrapped subscriber (standalone mode)
	subscriber := NewLoggingSubscriber(nil, logger, zap.InfoLevel)
	if subscriber == nil {
		t.Fatal("NewLoggingSubscriber returned nil")
	}

	if subscriber.wrapped != nil {
		t.Error("Expected wrapped subscriber to be nil")
	}

	if subscriber.logger != logger {
		t.Error("Logger not set correctly")
	}

	if subscriber.logLevel != zap.InfoLevel {
		t.Error("Log level not set correctly")
	}

	if subscriber.name != "LoggingSubscriber" {
		t.Error("Default name not set correctly")
	}
}

func TestNewNamedLoggingSubscriber(t *testing.T) {
	logger := zaptest.NewLogger(t)
	customName := "MyCustomSubscriber"

	subscriber := NewNamedLoggingSubscriber(nil, logger, zap.DebugLevel, customName)
	if subscriber == nil {
		t.Fatal("NewNamedLoggingSubscriber returned nil")
	}

	if subscriber.wrapped != nil {
		t.Error("Expected wrapped subscriber to be nil")
	}

	if subscriber.logger != logger {
		t.Error("Logger not set correctly")
	}

	if subscriber.logLevel != zap.DebugLevel {
		t.Error("Log level not set correctly")
	}

	if subscriber.name != customName {
		t.Error("Custom name not set correctly")
	}
}

func TestLoggingSubscriberOnSubscribe(t *testing.T) {
	logger := zaptest.NewLogger(t)
	subscriber := NewNamedLoggingSubscriber(nil, logger, zap.InfoLevel, "TestSubscriber")

	// This should log without error
	subscriber.OnSubscribe(context.Background(), "test/topic")
	subscriber.OnSubscribe(context.Background(), "user/+userId/profile")
	subscriber.OnSubscribe(context.Background(), "device/#")

	// Test doesn't crash and methods can be called multiple times
}

func TestLoggingSubscriberOnUnsubscribe(t *testing.T) {
	logger := zaptest.NewLogger(t)
	subscriber := NewNamedLoggingSubscriber(nil, logger, zap.WarnLevel, "TestSubscriber")

	// This should log without error
	subscriber.OnUnsubscribe(context.Background(), "test/topic")
	subscriber.OnUnsubscribe(context.Background(), "user/+userId/profile")
	subscriber.OnUnsubscribe(context.Background(), "")

	// Test doesn't crash and methods can be called multiple times
}

func TestLoggingSubscriberOnEvent(t *testing.T) {
	logger := zaptest.NewLogger(t)
	subscriber := NewNamedLoggingSubscriber(nil, logger, zap.DebugLevel, "TestSubscriber")

	// Test with string message
	subscriber.OnEvent(context.Background(), "test/topic", "string message", nil)

	// Test with map message
	mapMessage := map[string]interface{}{
		"key1": "value1",
		"key2": 42,
	}
	subscriber.OnEvent(context.Background(), "api/data", mapMessage, nil)

	// Test with extracted fields
	fields := map[string]string{
		"userId": "123",
		"action": "login",
	}
	subscriber.OnEvent(context.Background(), "user/123/login", "User logged in", fields)

	// Test with nil message
	subscriber.OnEvent(context.Background(), "empty/topic", nil, nil)

	// Test with byte slice message
	subscriber.OnEvent(context.Background(), "binary/data", []byte("binary data"), nil)

	// Test with various field combinations
	manyFields := map[string]string{
		"field1": "value1",
		"field2": "value2",
		"field3": "value3",
	}
	subscriber.OnEvent(context.Background(), "complex/topic", "complex message", manyFields)

	// Test doesn't crash and methods can be called multiple times
}

func TestLoggingSubscriberDifferentLogLevels(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Test each log level
	levels := []zap.AtomicLevel{
		zap.NewAtomicLevelAt(zap.DebugLevel),
		zap.NewAtomicLevelAt(zap.InfoLevel),
		zap.NewAtomicLevelAt(zap.WarnLevel),
		zap.NewAtomicLevelAt(zap.ErrorLevel),
	}

	for i, level := range levels {
		subscriber := NewNamedLoggingSubscriber(nil, logger, level.Level(), fmt.Sprintf("Subscriber%d", i))

		subscriber.OnSubscribe(context.Background(), "test/topic")
		subscriber.OnEvent(context.Background(), "test/topic", "test message", map[string]string{"param": "value"})
		subscriber.OnUnsubscribe(context.Background(), "test/topic")
	}
}

func TestLoggingSubscriberWithEventBus(t *testing.T) {
	logger := zaptest.NewLogger(t)
	eventBus, err := bus.NewEventBus().WithLogger(logger).Build()
	if err != nil {
		t.Fatalf("Build() returned error: %v", err)
	}

	err = eventBus.Start()
	if err != nil {
		t.Fatalf("Failed to start event bus: %v", err)
	}
	defer eventBus.Stop()

	// Create logging subscribers with different log levels
	debugSubscriber := NewNamedLoggingSubscriber(nil, logger, zap.DebugLevel, "DebugSubscriber")
	infoSubscriber := NewNamedLoggingSubscriber(nil, logger, zap.InfoLevel, "InfoSubscriber")

	// Subscribe to topics
	eventBus.Subscribe(context.Background(), debugSubscriber, "debug/+level/topic")
	eventBus.Subscribe(context.Background(), infoSubscriber, "info/events/#")

	// Give time for subscriptions to be processed
	time.Sleep(10 * time.Millisecond)

	// Publish events
	eventBus.Publish(context.Background(), "debug/high/topic", "Debug message")
	eventBus.Publish(context.Background(), "info/events/user/login", map[string]interface{}{
		"userId":    "123",
		"timestamp": "2024-01-01T12:00:00Z",
	})

	// Give time for events to be processed
	time.Sleep(10 * time.Millisecond)

	// Test unsubscribe
	eventBus.UnsubscribeAll(context.Background(), debugSubscriber)

	// Give time for unsubscription to be processed
	time.Sleep(10 * time.Millisecond)
}

// MockSubscriber is a simple mock subscriber for testing wrapper functionality
type MockSubscriber struct {
	onSubscribeCalled   bool
	onUnsubscribeCalled bool
	onEventCalled       bool
	lastTopic           string
	lastMessage         any
	lastFields          map[string]string
	returnError         error
}

func (m *MockSubscriber) OnSubscribe(ctx context.Context, topic string) error {
	m.onSubscribeCalled = true
	m.lastTopic = topic
	return m.returnError
}

func (m *MockSubscriber) OnUnsubscribe(ctx context.Context, topic string) error {
	m.onUnsubscribeCalled = true
	m.lastTopic = topic
	return m.returnError
}

func (m *MockSubscriber) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	m.onEventCalled = true
	m.lastTopic = topic
	m.lastMessage = message
	m.lastFields = fields
	return m.returnError
}

func (m *MockSubscriber) PassThrough(msg bus.EventBusMessage) error {
	return nil
}

func TestLoggingSubscriberWithWrappedSubscriber(t *testing.T) {
	logger := zaptest.NewLogger(t)
	mockSub := &MockSubscriber{}

	// Test with wrapped subscriber
	subscriber := NewNamedLoggingSubscriber(mockSub, logger, zap.InfoLevel, "WrapperTest")

	if subscriber.wrapped != mockSub {
		t.Error("Wrapped subscriber not set correctly")
	}

	// Test OnSubscribe calls wrapped subscriber
	err := subscriber.OnSubscribe(context.Background(), "test/topic")
	if err != nil {
		t.Errorf("OnSubscribe returned error: %v", err)
	}
	if !mockSub.onSubscribeCalled {
		t.Error("Wrapped subscriber OnSubscribe was not called")
	}
	if mockSub.lastTopic != "test/topic" {
		t.Error("Wrapped subscriber received wrong topic")
	}

	// Test OnUnsubscribe calls wrapped subscriber
	err = subscriber.OnUnsubscribe(context.Background(), "test/unsubscribe")
	if err != nil {
		t.Errorf("OnUnsubscribe returned error: %v", err)
	}
	if !mockSub.onUnsubscribeCalled {
		t.Error("Wrapped subscriber OnUnsubscribe was not called")
	}
	if mockSub.lastTopic != "test/unsubscribe" {
		t.Error("Wrapped subscriber received wrong topic for unsubscribe")
	}

	// Test OnEvent calls wrapped subscriber
	testMessage := "test message"
	testFields := map[string]string{"key": "value"}
	err = subscriber.OnEvent(context.Background(), "test/event", testMessage, testFields)
	if err != nil {
		t.Errorf("OnEvent returned error: %v", err)
	}
	if !mockSub.onEventCalled {
		t.Error("Wrapped subscriber OnEvent was not called")
	}
	if mockSub.lastTopic != "test/event" {
		t.Error("Wrapped subscriber received wrong topic for event")
	}
	if mockSub.lastMessage != testMessage {
		t.Error("Wrapped subscriber received wrong message")
	}
	if len(mockSub.lastFields) != 1 || mockSub.lastFields["key"] != "value" {
		t.Error("Wrapped subscriber received wrong fields")
	}
}

func TestLoggingSubscriberErrorPropagation(t *testing.T) {
	logger := zaptest.NewLogger(t)
	mockSub := &MockSubscriber{
		returnError: fmt.Errorf("mock error"),
	}

	subscriber := NewNamedLoggingSubscriber(mockSub, logger, zap.InfoLevel, "ErrorTest")

	// Test error propagation from OnSubscribe
	err := subscriber.OnSubscribe(context.Background(), "test/topic")
	if err == nil {
		t.Error("Expected error from OnSubscribe")
	}
	if err.Error() != "mock error" {
		t.Errorf("Expected 'mock error', got '%v'", err)
	}

	// Test error propagation from OnUnsubscribe
	err = subscriber.OnUnsubscribe(context.Background(), "test/topic")
	if err == nil {
		t.Error("Expected error from OnUnsubscribe")
	}
	if err.Error() != "mock error" {
		t.Errorf("Expected 'mock error', got '%v'", err)
	}

	// Test error propagation from OnEvent
	err = subscriber.OnEvent(context.Background(), "test/topic", "message", nil)
	if err == nil {
		t.Error("Expected error from OnEvent")
	}
	if err.Error() != "mock error" {
		t.Errorf("Expected 'mock error', got '%v'", err)
	}
}

func TestLoggingSubscriberStandaloneVsWrapped(t *testing.T) {
	logger := zaptest.NewLogger(t)

	// Test standalone (nil wrapped)
	standalone := NewNamedLoggingSubscriber(nil, logger, zap.InfoLevel, "Standalone")

	// These should not return errors even with nil wrapped subscriber
	err := standalone.OnSubscribe(context.Background(), "test/topic")
	if err != nil {
		t.Errorf("Standalone OnSubscribe returned error: %v", err)
	}

	err = standalone.OnUnsubscribe(context.Background(), "test/topic")
	if err != nil {
		t.Errorf("Standalone OnUnsubscribe returned error: %v", err)
	}

	err = standalone.OnEvent(context.Background(), "test/topic", "message", nil)
	if err != nil {
		t.Errorf("Standalone OnEvent returned error: %v", err)
	}

	// Test with wrapped subscriber
	mockSub := &MockSubscriber{}
	wrapped := NewNamedLoggingSubscriber(mockSub, logger, zap.InfoLevel, "Wrapped")

	// These should call the wrapped subscriber
	wrapped.OnSubscribe(context.Background(), "test/topic")
	wrapped.OnUnsubscribe(context.Background(), "test/topic")
	wrapped.OnEvent(context.Background(), "test/topic", "message", nil)

	if !mockSub.onSubscribeCalled || !mockSub.onUnsubscribeCalled || !mockSub.onEventCalled {
		t.Error("Wrapped subscriber methods were not called")
	}
}
