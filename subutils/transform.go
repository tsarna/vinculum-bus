package subutils

import (
	"context"

	"github.com/tsarna/vinculum-bus"
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

	// Create an EventBusMessage for the transform pipeline
	eventMsg := &bus.EventBusMessage{
		Ctx:     ctx,
		MsgType: bus.MessageTypeEvent,
		Topic:   topic,
		Payload: message,
	}

	// Apply transforms in order
	current := eventMsg
	for _, transformFunc := range t.transforms {
		if current == nil {
			// Previous transform dropped the message, stop processing
			return nil
		}

		transformed, continueProcessing := transformFunc(current)
		current = transformed

		if !continueProcessing {
			// Transform requested to stop processing, but we have a message
			// So we'll pass it to the wrapped subscriber and stop
			break
		}
	}

	// If we have a transformed message, pass it to the wrapped subscriber
	if current != nil {
		return t.wrapped.OnEvent(ctx, current.Topic, current.Payload, fields)
	}

	// Message was dropped, don't call wrapped subscriber
	return nil
}

// PassThrough passes through to the wrapped subscriber
func (t *TransformingSubscriber) PassThrough(msg bus.EventBusMessage) error {
	return t.wrapped.PassThrough(msg)
}
