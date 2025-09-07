package vinculum

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// mockClient is a test implementation of the Client interface
type mockClient struct {
	connectCalled        bool
	disconnectCalled     bool
	subscribeCalled      bool
	unsubscribeCalled    bool
	unsubscribeAllCalled bool
	publishCalled        bool
	publishSyncCalled    bool
	onSubscribeCalled    bool
	onUnsubscribeCalled  bool
	onEventCalled        bool
	passThroughCalled    bool

	connectShouldFail bool
	connectFailCount  int
	connectAttempts   int
}

func (m *mockClient) Connect(ctx context.Context) error {
	m.connectCalled = true
	m.connectAttempts++

	if m.connectShouldFail && m.connectAttempts <= m.connectFailCount {
		return fmt.Errorf("mock connect failure")
	}

	return nil
}

func (m *mockClient) Disconnect() error {
	m.disconnectCalled = true
	return nil
}

func (m *mockClient) Subscribe(ctx context.Context, topicPattern string) error {
	m.subscribeCalled = true
	return nil
}

func (m *mockClient) Unsubscribe(ctx context.Context, topicPattern string) error {
	m.unsubscribeCalled = true
	return nil
}

func (m *mockClient) UnsubscribeAll(ctx context.Context) error {
	m.unsubscribeAllCalled = true
	return nil
}

func (m *mockClient) Publish(ctx context.Context, topic string, payload any) error {
	m.publishCalled = true
	return nil
}

func (m *mockClient) PublishSync(ctx context.Context, topic string, payload any) error {
	m.publishSyncCalled = true
	return nil
}

func (m *mockClient) OnSubscribe(ctx context.Context, topicPattern string) error {
	m.onSubscribeCalled = true
	return nil
}

func (m *mockClient) OnUnsubscribe(ctx context.Context, topicPattern string) error {
	m.onUnsubscribeCalled = true
	return nil
}

func (m *mockClient) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	m.onEventCalled = true
	return nil
}

func (m *mockClient) PassThrough(msg EventBusMessage) error {
	m.passThroughCalled = true
	return nil
}

func TestSubscriptionTracker(t *testing.T) {
	tracker := NewSubscriptionTracker()
	mockClient := &mockClient{}

	t.Run("initial state", func(t *testing.T) {
		assert.False(t, tracker.IsConnected())
		assert.Zero(t, tracker.GetConnectionTime())
		assert.Zero(t, tracker.GetDisconnectionTime())
		assert.Nil(t, tracker.GetLastError())
		assert.Equal(t, 0, tracker.GetSubscriptionCount())
		assert.Empty(t, tracker.GetSubscriptionTopics())
		assert.Empty(t, tracker.GetSubscriptions())
		assert.False(t, tracker.IsSubscribedTo("test/topic"))
		assert.Zero(t, tracker.GetSubscriptionTime("test/topic"))
	})

	t.Run("connection lifecycle", func(t *testing.T) {
		ctx := context.Background()
		beforeConnect := time.Now()

		// Test connect
		tracker.OnConnect(ctx, mockClient)
		assert.True(t, tracker.IsConnected())
		assert.True(t, tracker.GetConnectionTime().After(beforeConnect))
		assert.Nil(t, tracker.GetLastError())

		// Test graceful disconnect
		beforeDisconnect := time.Now()
		tracker.OnDisconnect(ctx, mockClient, nil)
		assert.False(t, tracker.IsConnected())
		assert.True(t, tracker.GetDisconnectionTime().After(beforeDisconnect))
		assert.Nil(t, tracker.GetLastError())

		// Test error disconnect
		testErr := assert.AnError
		tracker.OnDisconnect(ctx, mockClient, testErr)
		assert.False(t, tracker.IsConnected())
		assert.Equal(t, testErr, tracker.GetLastError())
	})

	t.Run("subscription tracking", func(t *testing.T) {
		ctx := context.Background()
		beforeSub := time.Now()

		// Test subscribe
		tracker.OnSubscribe(ctx, mockClient, "sensor/+/temperature")
		assert.Equal(t, 1, tracker.GetSubscriptionCount())
		assert.True(t, tracker.IsSubscribedTo("sensor/+/temperature"))
		assert.True(t, tracker.GetSubscriptionTime("sensor/+/temperature").After(beforeSub))
		assert.Contains(t, tracker.GetSubscriptionTopics(), "sensor/+/temperature")

		// Test multiple subscriptions
		tracker.OnSubscribe(ctx, mockClient, "alerts/#")
		tracker.OnSubscribe(ctx, mockClient, "events/user/+")
		assert.Equal(t, 3, tracker.GetSubscriptionCount())
		assert.True(t, tracker.IsSubscribedTo("alerts/#"))
		assert.True(t, tracker.IsSubscribedTo("events/user/+"))

		subscriptions := tracker.GetSubscriptions()
		assert.Len(t, subscriptions, 3)
		assert.Contains(t, subscriptions, "sensor/+/temperature")
		assert.Contains(t, subscriptions, "alerts/#")
		assert.Contains(t, subscriptions, "events/user/+")

		topics := tracker.GetSubscriptionTopics()
		assert.Len(t, topics, 3)
		assert.Contains(t, topics, "sensor/+/temperature")
		assert.Contains(t, topics, "alerts/#")
		assert.Contains(t, topics, "events/user/+")

		// Test unsubscribe
		tracker.OnUnsubscribe(ctx, mockClient, "alerts/#")
		assert.Equal(t, 2, tracker.GetSubscriptionCount())
		assert.False(t, tracker.IsSubscribedTo("alerts/#"))
		assert.True(t, tracker.IsSubscribedTo("sensor/+/temperature"))
		assert.True(t, tracker.IsSubscribedTo("events/user/+"))

		// Test unsubscribe all
		tracker.OnUnsubscribeAll(ctx, mockClient)
		assert.Equal(t, 0, tracker.GetSubscriptionCount())
		assert.False(t, tracker.IsSubscribedTo("sensor/+/temperature"))
		assert.False(t, tracker.IsSubscribedTo("events/user/+"))
		assert.Empty(t, tracker.GetSubscriptionTopics())
		assert.Empty(t, tracker.GetSubscriptions())
	})

	t.Run("subscription persistence across disconnects", func(t *testing.T) {
		ctx := context.Background()

		// Subscribe to topics
		tracker.OnSubscribe(ctx, mockClient, "persistent/topic1")
		tracker.OnSubscribe(ctx, mockClient, "persistent/topic2")
		assert.Equal(t, 2, tracker.GetSubscriptionCount())

		// Disconnect - subscriptions should persist
		tracker.OnDisconnect(ctx, mockClient, nil)
		assert.False(t, tracker.IsConnected())
		assert.Equal(t, 2, tracker.GetSubscriptionCount())
		assert.True(t, tracker.IsSubscribedTo("persistent/topic1"))
		assert.True(t, tracker.IsSubscribedTo("persistent/topic2"))

		// Reconnect - subscriptions still there
		tracker.OnConnect(ctx, mockClient)
		assert.True(t, tracker.IsConnected())
		assert.Equal(t, 2, tracker.GetSubscriptionCount())
	})

	t.Run("thread safety", func(t *testing.T) {
		// This test verifies that concurrent access doesn't panic
		// More comprehensive race testing would require -race flag
		ctx := context.Background()

		done := make(chan bool, 2)

		// Goroutine 1: Connect/disconnect
		go func() {
			defer func() { done <- true }()
			for i := 0; i < 100; i++ {
				tracker.OnConnect(ctx, mockClient)
				tracker.OnDisconnect(ctx, mockClient, nil)
			}
		}()

		// Goroutine 2: Subscribe/unsubscribe
		go func() {
			defer func() { done <- true }()
			for i := 0; i < 100; i++ {
				tracker.OnSubscribe(ctx, mockClient, "test/topic")
				tracker.OnUnsubscribe(ctx, mockClient, "test/topic")
			}
		}()

		// Wait for both goroutines
		<-done
		<-done

		// Should not panic and should be in a consistent state
		assert.NotPanics(t, func() {
			tracker.IsConnected()
			tracker.GetSubscriptionCount()
			tracker.GetSubscriptions()
		})
	})
}

func TestNewSubscriptionTracker(t *testing.T) {
	tracker := NewSubscriptionTracker()
	require.NotNil(t, tracker)
	assert.NotNil(t, tracker.subscriptions)
	assert.Equal(t, 0, len(tracker.subscriptions))
}

func TestAutoReconnector(t *testing.T) {
	t.Run("creation and configuration", func(t *testing.T) {
		// Test default configuration
		reconnector := NewAutoReconnector().Build()
		assert.NotNil(t, reconnector)
		assert.NotNil(t, reconnector.SubscriptionTracker)
		assert.True(t, reconnector.IsEnabled())
		assert.Equal(t, 0, reconnector.GetReconnectCount())
		assert.Zero(t, reconnector.GetLastReconnectTime())

		// Test custom configuration with builder
		reconnectorCustom := NewAutoReconnector().
			WithInitialDelay(500 * time.Millisecond).
			WithMaxDelay(10 * time.Second).
			WithBackoffFactor(1.5).
			WithMaxRetries(5).
			Build()
		assert.NotNil(t, reconnectorCustom)
		assert.Equal(t, 500*time.Millisecond, reconnectorCustom.initialDelay)
		assert.Equal(t, 10*time.Second, reconnectorCustom.maxDelay)
		assert.Equal(t, 1.5, reconnectorCustom.backoffFactor)
		assert.Equal(t, 5, reconnectorCustom.maxRetries)

		// Test builder with all options set
		logger := zap.NewNop()
		reconnectorFull := NewAutoReconnector().
			WithInitialDelay(200 * time.Millisecond).
			WithMaxDelay(5 * time.Second).
			WithBackoffFactor(3.0).
			WithMaxRetries(3).
			WithEnabled(false).
			WithLogger(logger).
			Build()
		assert.NotNil(t, reconnectorFull)
		assert.Equal(t, 200*time.Millisecond, reconnectorFull.initialDelay)
		assert.Equal(t, 5*time.Second, reconnectorFull.maxDelay)
		assert.Equal(t, 3.0, reconnectorFull.backoffFactor)
		assert.Equal(t, 3, reconnectorFull.maxRetries)
		assert.False(t, reconnectorFull.IsEnabled())
		assert.Equal(t, logger, reconnectorFull.logger)
	})

	t.Run("enable/disable", func(t *testing.T) {
		reconnector := NewAutoReconnector().Build()

		// Initially enabled
		assert.True(t, reconnector.IsEnabled())

		// Disable
		reconnector.SetEnabled(false)
		assert.False(t, reconnector.IsEnabled())

		// Enable
		reconnector.SetEnabled(true)
		assert.True(t, reconnector.IsEnabled())
	})

	t.Run("delegation to SubscriptionTracker", func(t *testing.T) {
		reconnector := NewAutoReconnector().Build()
		mockClient := &mockClient{}
		ctx := context.Background()

		// Test OnConnect delegation
		reconnector.OnConnect(ctx, mockClient)
		assert.True(t, reconnector.IsConnected())

		// Test OnSubscribe delegation
		reconnector.OnSubscribe(ctx, mockClient, "test/topic")
		assert.True(t, reconnector.IsSubscribedTo("test/topic"))
		assert.Equal(t, 1, reconnector.GetSubscriptionCount())

		// Test OnUnsubscribe delegation
		reconnector.OnUnsubscribe(ctx, mockClient, "test/topic")
		assert.False(t, reconnector.IsSubscribedTo("test/topic"))
		assert.Equal(t, 0, reconnector.GetSubscriptionCount())

		// Test OnUnsubscribeAll delegation
		reconnector.OnSubscribe(ctx, mockClient, "topic1")
		reconnector.OnSubscribe(ctx, mockClient, "topic2")
		assert.Equal(t, 2, reconnector.GetSubscriptionCount())

		reconnector.OnUnsubscribeAll(ctx, mockClient)
		assert.Equal(t, 0, reconnector.GetSubscriptionCount())

		// Test OnDisconnect delegation
		testErr := fmt.Errorf("connection lost")
		reconnector.OnDisconnect(ctx, mockClient, testErr)
		assert.False(t, reconnector.IsConnected())
		assert.Equal(t, testErr, reconnector.GetLastError())
	})

	t.Run("no reconnection on clean disconnect", func(t *testing.T) {
		reconnector := NewAutoReconnector().Build()
		mockClient := &mockClient{}
		ctx := context.Background()

		// Simulate clean disconnect (err = nil)
		reconnector.OnDisconnect(ctx, mockClient, nil)

		// Should not attempt reconnection
		time.Sleep(50 * time.Millisecond) // Give time for any potential reconnection
		assert.Equal(t, 0, reconnector.GetReconnectCount())
		assert.False(t, mockClient.connectCalled)
	})

	t.Run("no reconnection when disabled", func(t *testing.T) {
		reconnector := NewAutoReconnector().WithEnabled(false).Build()
		mockClient := &mockClient{}
		ctx := context.Background()

		// Simulate error disconnect
		disconnectErr := fmt.Errorf("connection lost")
		reconnector.OnDisconnect(ctx, mockClient, disconnectErr)

		// Should not attempt reconnection because it's disabled
		time.Sleep(50 * time.Millisecond)
		assert.Equal(t, 0, reconnector.GetReconnectCount())
		assert.False(t, mockClient.connectCalled)
	})

	t.Run("reconnection on error disconnect", func(t *testing.T) {
		reconnector := NewAutoReconnector().
			WithInitialDelay(10 * time.Millisecond). // Very short for testing
			WithMaxDelay(50 * time.Millisecond).
			WithBackoffFactor(2.0).
			WithMaxRetries(3).
			Build()

		mockClient := &mockClient{
			connectShouldFail: true,
			connectFailCount:  2, // Fail first 2 attempts, then succeed
		}
		ctx := context.Background()

		// Add some subscriptions to test resubscription
		reconnector.OnSubscribe(ctx, mockClient, "topic1")
		reconnector.OnSubscribe(ctx, mockClient, "topic2")

		// Simulate error disconnect
		disconnectErr := fmt.Errorf("connection lost")
		reconnector.OnDisconnect(ctx, mockClient, disconnectErr)

		// Wait for reconnection attempts
		time.Sleep(200 * time.Millisecond)

		// Should have attempted reconnection
		assert.Greater(t, reconnector.GetReconnectCount(), 0)
		assert.True(t, mockClient.connectCalled)

		// Should eventually succeed and resubscribe
		assert.True(t, mockClient.subscribeCalled)
	})

	t.Run("max retries respected", func(t *testing.T) {
		reconnector := NewAutoReconnector().
			WithInitialDelay(5 * time.Millisecond).
			WithMaxDelay(20 * time.Millisecond).
			WithBackoffFactor(2.0).
			WithMaxRetries(3). // Limited retries
			Build()

		mockClient := &mockClient{
			connectShouldFail: true,
			connectFailCount:  10, // Always fail
		}
		ctx := context.Background()

		// Simulate error disconnect
		disconnectErr := fmt.Errorf("connection lost")
		reconnector.OnDisconnect(ctx, mockClient, disconnectErr)

		// Wait for all retry attempts
		time.Sleep(200 * time.Millisecond)

		// Should have attempted exactly maxRetries
		assert.LessOrEqual(t, reconnector.GetReconnectCount(), 3)
	})

	t.Run("builder pattern validation", func(t *testing.T) {
		// Test builder ignores invalid values
		reconnector := NewAutoReconnector().
			WithInitialDelay(-1 * time.Second). // Should be ignored (negative)
			WithMaxDelay(0).                    // Should be ignored (zero)
			WithBackoffFactor(0.5).             // Should be ignored (< 1.0)
			WithMaxRetries(-2).                 // Should be ignored (< -1)
			Build()

		// Should use default values since invalid ones were ignored
		assert.Equal(t, 1*time.Second, reconnector.initialDelay) // Default: 1 second
		assert.Equal(t, 30*time.Second, reconnector.maxDelay)    // Default: 30 seconds
		assert.Equal(t, 2.0, reconnector.backoffFactor)          // Default: 2.0
		assert.Equal(t, -1, reconnector.maxRetries)              // Default: -1 (unlimited)

		// Test valid edge cases
		reconnectorEdge := NewAutoReconnector().
			WithInitialDelay(1 * time.Nanosecond). // Minimum valid value
			WithMaxDelay(1 * time.Nanosecond).     // Minimum valid value
			WithBackoffFactor(1.0001).             // Just above 1.0
			WithMaxRetries(-1).                    // Unlimited retries
			WithEnabled(false).                    // Disabled
			Build()

		assert.Equal(t, 1*time.Nanosecond, reconnectorEdge.initialDelay)
		assert.Equal(t, 1*time.Nanosecond, reconnectorEdge.maxDelay)
		assert.Equal(t, 1.0001, reconnectorEdge.backoffFactor)
		assert.Equal(t, -1, reconnectorEdge.maxRetries)
		assert.False(t, reconnectorEdge.IsEnabled())
	})

	t.Run("builder fluent interface", func(t *testing.T) {
		// Test that all methods return the builder for chaining
		builder := NewAutoReconnector()

		// Each method should return the same builder instance
		assert.Same(t, builder, builder.WithInitialDelay(1*time.Second))
		assert.Same(t, builder, builder.WithMaxDelay(10*time.Second))
		assert.Same(t, builder, builder.WithBackoffFactor(2.0))
		assert.Same(t, builder, builder.WithMaxRetries(5))
		assert.Same(t, builder, builder.WithEnabled(true))

		// Build should return a different object
		reconnector := builder.Build()
		assert.NotSame(t, builder, reconnector)
		assert.IsType(t, &AutoReconnector{}, reconnector)
	})

	t.Run("logger configuration", func(t *testing.T) {
		// Test default no-op logger
		reconnector := NewAutoReconnector().Build()
		assert.NotNil(t, reconnector.logger)

		// Test custom logger
		logger := zap.NewNop()
		reconnectorWithLogger := NewAutoReconnector().
			WithLogger(logger).
			Build()
		assert.Equal(t, logger, reconnectorWithLogger.logger)

		// Test nil logger is ignored
		reconnectorNilLogger := NewAutoReconnector().
			WithLogger(nil).
			Build()
		assert.NotNil(t, reconnectorNilLogger.logger) // Should still have default no-op logger
	})
}
