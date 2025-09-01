package server

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tsarna/vinculum/pkg/vinculum"
	"go.uber.org/zap"
)

func TestSubscriptionControllers(t *testing.T) {
	eventBus := &mockEventBus{}
	logger := zap.NewNop()

	t.Run("subscription controller configuration", func(t *testing.T) {
		// Default should be PassthroughSubscriptionController
		config := NewListenerConfig()
		assert.NotNil(t, config.subscriptionController)

		// Test that we can create the default controller
		controller := config.subscriptionController(eventBus, logger)
		assert.NotNil(t, controller)

		// Test PassthroughSubscriptionController behavior
		ctx := context.Background()
		mockSubscriber := &mockSubscriber{}

		err := controller.Subscribe(ctx, mockSubscriber, "test/topic")
		assert.NoError(t, err) // Should allow all subscriptions

		err = controller.Unsubscribe(ctx, mockSubscriber, "test/topic")
		assert.NoError(t, err) // Should allow all unsubscriptions

		// Test custom subscription controller factory
		customControllerCalled := false
		customFactory := func(eb vinculum.EventBus, log *zap.Logger) SubscriptionController {
			customControllerCalled = true
			assert.Equal(t, eventBus, eb)
			assert.Equal(t, logger, log)
			return &testSubscriptionController{}
		}

		config.WithSubscriptionController(customFactory)
		controller = config.subscriptionController(eventBus, logger)
		assert.True(t, customControllerCalled)
		assert.IsType(t, &testSubscriptionController{}, controller)
	})

	t.Run("PassthroughSubscriptionController", func(t *testing.T) {
		controller := NewPassthroughSubscriptionController(eventBus, logger)
		ctx := context.Background()
		mockSubscriber := &mockSubscriber{}

		// Should allow any subscription
		err := controller.Subscribe(ctx, mockSubscriber, "any/topic/pattern")
		assert.NoError(t, err)

		err = controller.Subscribe(ctx, mockSubscriber, "sensor/+/data")
		assert.NoError(t, err)

		err = controller.Subscribe(ctx, mockSubscriber, "events/#")
		assert.NoError(t, err)

		// Should allow any unsubscription
		err = controller.Unsubscribe(ctx, mockSubscriber, "any/topic/pattern")
		assert.NoError(t, err)

		err = controller.Unsubscribe(ctx, mockSubscriber, "sensor/+/data")
		assert.NoError(t, err)

		err = controller.Unsubscribe(ctx, mockSubscriber, "events/#")
		assert.NoError(t, err)
	})

	t.Run("testSubscriptionController", func(t *testing.T) {
		controller := &testSubscriptionController{}
		ctx := context.Background()
		mockSubscriber := &mockSubscriber{}

		// Should allow normal subscriptions
		err := controller.Subscribe(ctx, mockSubscriber, "allowed/topic")
		assert.NoError(t, err)
		assert.True(t, controller.subscribeCalled)

		// Should deny specific topics
		controller.subscribeCalled = false
		err = controller.Subscribe(ctx, mockSubscriber, "denied/topic")
		assert.Error(t, err)
		assert.True(t, controller.subscribeCalled)
		assert.Contains(t, err.Error(), "subscription denied")

		// Should allow normal unsubscriptions
		err = controller.Unsubscribe(ctx, mockSubscriber, "allowed/topic")
		assert.NoError(t, err)
		assert.True(t, controller.unsubscribeCalled)

		// Should deny specific unsubscriptions
		controller.unsubscribeCalled = false
		err = controller.Unsubscribe(ctx, mockSubscriber, "denied/topic")
		assert.Error(t, err)
		assert.True(t, controller.unsubscribeCalled)
		assert.Contains(t, err.Error(), "unsubscription denied")
	})
}

// mockSubscriber implements vinculum.Subscriber for testing
type mockSubscriber struct {
	onSubscribeCalled   bool
	onUnsubscribeCalled bool
	onEventCalled       bool
}

func (m *mockSubscriber) OnSubscribe(ctx context.Context, topic string) error {
	m.onSubscribeCalled = true
	return nil
}

func (m *mockSubscriber) OnUnsubscribe(ctx context.Context, topic string) error {
	m.onUnsubscribeCalled = true
	return nil
}

func (m *mockSubscriber) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	m.onEventCalled = true
	return nil
}

func (m *mockSubscriber) PassThrough(msg vinculum.EventBusMessage) error {
	return nil
}

// testSubscriptionController is a test implementation that demonstrates advanced features
type testSubscriptionController struct {
	subscribeCalled   bool
	unsubscribeCalled bool
}

func (t *testSubscriptionController) Subscribe(ctx context.Context, subscriber vinculum.Subscriber, topicPattern string) error {
	t.subscribeCalled = true
	if topicPattern == "denied/topic" {
		return fmt.Errorf("subscription denied")
	}
	return nil
}

func (t *testSubscriptionController) Unsubscribe(ctx context.Context, subscriber vinculum.Subscriber, topicPattern string) error {
	t.unsubscribeCalled = true
	if topicPattern == "denied/topic" {
		return fmt.Errorf("unsubscription denied")
	}
	return nil
}

// mockEventBus implements vinculum.EventBus for testing
type mockEventBus struct{}

// EventBus methods
func (m *mockEventBus) Subscribe(ctx context.Context, subscriber vinculum.Subscriber, topicPattern string) error {
	return nil
}

func (m *mockEventBus) Unsubscribe(ctx context.Context, subscriber vinculum.Subscriber, topicPattern string) error {
	return nil
}

func (m *mockEventBus) Publish(ctx context.Context, topic string, message any) error {
	return nil
}

func (m *mockEventBus) UnsubscribeAll(ctx context.Context, subscriber vinculum.Subscriber) error {
	return nil
}

func (m *mockEventBus) PublishSync(ctx context.Context, topic string, message any) error {
	return nil
}

func (m *mockEventBus) Start() error {
	return nil
}

func (m *mockEventBus) Stop() error {
	return nil
}

// Subscriber methods (EventBus embeds Subscriber)
func (m *mockEventBus) OnSubscribe(ctx context.Context, topic string) error {
	return nil
}

func (m *mockEventBus) OnUnsubscribe(ctx context.Context, topic string) error {
	return nil
}

func (m *mockEventBus) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	return nil
}

func (m *mockEventBus) PassThrough(msg vinculum.EventBusMessage) error {
	return nil
}
