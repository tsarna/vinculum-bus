package server

import (
	"context"
	"fmt"

	"github.com/tsarna/vinculum/pkg/vinculum"
	"go.uber.org/zap"
)

// SubscriptionController controls what topic patterns clients can subscribe to
// and unsubscribe from. It can reject subscriptions, rewrite them, or split
// them into multiple subscriptions.
type SubscriptionController interface {
	// Subscribe is called when a client wants to subscribe to a topic pattern.
	// It can:
	//   - Return nil to allow the subscription as-is
	//   - Return an error to deny the subscription
	//   - Modify the subscription by calling eventBus.Subscribe with different patterns
	//
	// If this method calls eventBus.Subscribe itself, it should return an error
	// to prevent the original subscription from also being processed.
	Subscribe(ctx context.Context, subscriber vinculum.Subscriber, topicPattern string) error

	// Unsubscribe is called when a client wants to unsubscribe from a topic pattern.
	// It can:
	//   - Return nil to allow the unsubscription as-is
	//   - Return an error to deny the unsubscription
	//   - Modify the unsubscription by calling eventBus.Unsubscribe with different patterns
	//
	// If this method calls eventBus.Unsubscribe itself, it should return an error
	// to prevent the original unsubscription from also being processed.
	Unsubscribe(ctx context.Context, subscriber vinculum.Subscriber, topicPattern string) error
}

// SubscriptionControllerFactory creates a SubscriptionController instance.
// It receives the EventBus and logger that the controller can use.
type SubscriptionControllerFactory func(eventBus vinculum.EventBus, logger *zap.Logger) SubscriptionController

// Predefined subscription controllers

// PassthroughSubscriptionController is the default SubscriptionController that
// allows all subscriptions and unsubscriptions without modification.
type PassthroughSubscriptionController struct{}

// NewPassthroughSubscriptionController creates a PassthroughSubscriptionController.
// This is the default subscription controller factory.
func NewPassthroughSubscriptionController(eventBus vinculum.EventBus, logger *zap.Logger) SubscriptionController {
	return &PassthroughSubscriptionController{}
}

// Subscribe allows the subscription as-is by returning nil.
func (p *PassthroughSubscriptionController) Subscribe(ctx context.Context, subscriber vinculum.Subscriber, topicPattern string) error {
	return nil // Allow subscription as-is
}

// Unsubscribe allows the unsubscription as-is by returning nil.
func (p *PassthroughSubscriptionController) Unsubscribe(ctx context.Context, subscriber vinculum.Subscriber, topicPattern string) error {
	return nil // Allow unsubscription as-is
}

// Example subscription controller implementations

// TopicPrefixSubscriptionController only allows subscriptions to topics with a specific prefix.
// This is useful for sandboxing client subscriptions.
type TopicPrefixSubscriptionController struct {
	eventBus vinculum.EventBus
	logger   *zap.Logger
	prefix   string
}

// NewTopicPrefixSubscriptionController creates a factory for TopicPrefixSubscriptionController.
func NewTopicPrefixSubscriptionController(prefix string) SubscriptionControllerFactory {
	return func(eventBus vinculum.EventBus, logger *zap.Logger) SubscriptionController {
		return &TopicPrefixSubscriptionController{
			eventBus: eventBus,
			logger:   logger,
			prefix:   prefix,
		}
	}
}

// Subscribe only allows subscriptions to topics with the configured prefix.
func (t *TopicPrefixSubscriptionController) Subscribe(ctx context.Context, subscriber vinculum.Subscriber, topicPattern string) error {
	if len(topicPattern) >= len(t.prefix) && topicPattern[:len(t.prefix)] == t.prefix {
		return nil // Allow subscription
	}
	return fmt.Errorf("subscriptions only allowed to topics with prefix: %s", t.prefix)
}

// Unsubscribe only allows unsubscriptions from topics with the configured prefix.
func (t *TopicPrefixSubscriptionController) Unsubscribe(ctx context.Context, subscriber vinculum.Subscriber, topicPattern string) error {
	if len(topicPattern) >= len(t.prefix) && topicPattern[:len(t.prefix)] == t.prefix {
		return nil // Allow unsubscription
	}
	return fmt.Errorf("unsubscriptions only allowed from topics with prefix: %s", t.prefix)
}
