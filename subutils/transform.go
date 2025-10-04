package subutils

import (
	"context"

	bus "github.com/tsarna/vinculum-bus"
	"github.com/tsarna/vinculum-bus/transform"
)

// TransformingSubscriber wraps another subscriber and applies message transforms
// to events before passing them to the wrapped subscriber.
type TransformingSubscriber struct {
	wrapped    bus.Subscriber
	transforms []transform.MessageTransformFunc
}

// NewTransformingSubscriber creates a new TransformingSubscriber that applies
// the given transform functions to events before passing them to the wrapped subscriber.
//
// The transforms are applied in order, and if any transform returns nil (drops the message)
// or returns false for the continue flag, the event will not be passed to the wrapped subscriber.
//
// Example:
//
//	// Create a subscriber that only receives non-debug events with added metadata
//	baseSubscriber := &MySubscriber{}
//	transformedSubscriber := subutils.NewTransformingSubscriber(
//	    baseSubscriber,
//	    transform.DropTopicPrefix("debug/"),
//	    transform.ModifyPayload(func(ctx context.Context, payload any, fields map[string]string) any {
//	        return map[string]any{
//	            "original": payload,
//	            "processed_at": time.Now().Unix(),
//	        }
//	    }),
//	)
func NewTransformingSubscriber(wrapped bus.Subscriber, transforms ...transform.MessageTransformFunc) *TransformingSubscriber {
	return &TransformingSubscriber{
		wrapped:    wrapped,
		transforms: transforms,
	}
}

// OnSubscribe passes through to the wrapped subscriber
func (t *TransformingSubscriber) OnSubscribe(ctx context.Context, topic string) error {
	return t.wrapped.OnSubscribe(ctx, topic)
}

// OnUnsubscribe passes through to the wrapped subscriber
func (t *TransformingSubscriber) OnUnsubscribe(ctx context.Context, topic string) error {
	return t.wrapped.OnUnsubscribe(ctx, topic)
}

// OnEvent applies the transform pipeline to the event before passing it to the wrapped subscriber.
// If any transform drops the message (returns nil) or stops processing (returns false),
// the event will not be passed to the wrapped subscriber.
func (t *TransformingSubscriber) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	// If no transforms are configured, pass through directly
	if len(t.transforms) == 0 {
		return t.wrapped.OnEvent(ctx, topic, message, fields)
	}

	transformed, _ := transform.ApplyTransforms(ctx, topic, message, t.transforms)

	// If we have a transformed message, pass it to the wrapped subscriber
	if transformed != nil {
		return t.wrapped.OnEvent(ctx, transformed.Topic, transformed.Payload, fields)
	}

	// Message was dropped, don't call wrapped subscriber
	return nil
}

// PassThrough passes through to the wrapped subscriber
func (t *TransformingSubscriber) PassThrough(msg bus.EventBusMessage) error {
	return t.wrapped.PassThrough(msg)
}
