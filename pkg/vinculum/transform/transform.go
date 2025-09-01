package transform

import (
	"context"
	"strings"
	"time"

	"github.com/amir-yaghoubi/mqttpattern"
	"github.com/tsarna/vinculum/pkg/vinculum"
)

// MessageTransformFunc is a function that transforms EventBus messages.
// It receives a message from the EventBus and can modify, replace, or drop it
// before further processing.
//
// Parameters:
//   - msg: The EventBus message to transform
//
// Returns:
//   - *vinculum.EventBusMessage: The transformed message (nil to drop the message)
//   - bool: Whether to continue calling subsequent transform functions (ignored if msg is nil)
//
// If a function returns nil, the message is dropped and no further transforms in a chain are called.
// If a function returns false for the continue flag, no further transforms are called.
type MessageTransformFunc func(msg *vinculum.EventBusMessage) (*vinculum.EventBusMessage, bool)

// Common MessageTransformFunc implementations for reuse

// DropTopicPattern returns a MessageTransformFunc that drops messages
// whose topics match the given MQTT-style pattern.
//
// Pattern examples:
//   - "secret/+" - drops all topics starting with "secret/"
//   - "+/internal" - drops all topics ending with "/internal"
//   - "debug/#" - drops all topics starting with "debug/"
//   - "temp/+/data" - drops topics like "temp/sensor1/data", "temp/sensor2/data"
//
// Example:
//
//	transforms := []MessageTransformFunc{
//	    DropTopicPattern("secret/+"),
//	    DropTopicPattern("debug/#"),
//	}
func DropTopicPattern(pattern string) MessageTransformFunc {
	return func(msg *vinculum.EventBusMessage) (*vinculum.EventBusMessage, bool) {
		if mqttpattern.Matches(pattern, msg.Topic) {
			return nil, false // Drop message, continue pipeline (though it will stop due to nil)
		}
		return msg, true // Keep message, continue pipeline
	}
}

// DropTopicPrefix returns a MessageTransformFunc that drops messages
// whose topics start with the given prefix. You probably want the prefix to end with a slash.
//
// Example:
//
//	transforms := []MessageTransformFunc{
//	    DropTopicPrefix("internal/"),
//	    DropTopicPrefix("debug/"),
//	}
func DropTopicPrefix(prefix string) MessageTransformFunc {
	return func(msg *vinculum.EventBusMessage) (*vinculum.EventBusMessage, bool) {
		if strings.HasPrefix(msg.Topic, prefix) {
			return nil, false // Drop message
		}
		return msg, true // Keep message
	}
}

// AddTopicPrefix returns a MessageTransformFunc that adds a prefix
// to all message topics.
//
// Example:
//
//	transforms := []MessageTransformFunc{
//	    AddTopicPrefix("processed/"), // "events" becomes "processed/events"
//	}
func AddTopicPrefix(prefix string) MessageTransformFunc {
	return func(msg *vinculum.EventBusMessage) (*vinculum.EventBusMessage, bool) {
		modified := &vinculum.EventBusMessage{
			Ctx:     msg.Ctx,
			MsgType: msg.MsgType,
			Topic:   prefix + msg.Topic,
			Payload: msg.Payload,
		}
		return modified, true
	}
}

// RateLimitByTopic returns a MessageTransformFunc that implements
// simple rate limiting per topic pattern.
//
// Note: This is a basic implementation. For production use, consider
// something more sophisticated.
//
// Example:
//
//	transforms := []MessageTransformFunc{
//	    RateLimitByTopic(time.Second), // Max 1 message per second per topic
//	}
func RateLimitByTopic(minInterval time.Duration) MessageTransformFunc {
	lastSent := make(map[string]time.Time)

	return func(msg *vinculum.EventBusMessage) (*vinculum.EventBusMessage, bool) {
		now := time.Now()
		if last, exists := lastSent[msg.Topic]; exists {
			if now.Sub(last) < minInterval {
				return nil, false // Drop message due to rate limit
			}
		}
		lastSent[msg.Topic] = now
		return msg, true // Allow message
	}
}

// ChainTransforms combines multiple MessageTransformFunc into a single function.
// This is useful for creating reusable transform pipelines.
//
// Example:
//
//	securityPipeline := ChainTransforms(
//	    DropTopicPattern("secret/+"),
//	    DropTopicPrefix("internal/"),
//	)
//	transforms := []MessageTransformFunc{securityPipeline, otherTransform}
func ChainTransforms(transforms ...MessageTransformFunc) MessageTransformFunc {
	return func(msg *vinculum.EventBusMessage) (*vinculum.EventBusMessage, bool) {
		current := msg
		for _, transform := range transforms {
			if current == nil {
				return nil, true // Previous transform dropped message
			}

			transformed, continueProcessing := transform(current)
			current = transformed

			if current == nil || !continueProcessing {
				return current, continueProcessing
			}
		}
		return current, true
	}
}

// SimpleMessageTransformFunc is a simple function that transforms the message payload.
// It receives the message payload and extracted fields from topic pattern matching,
// and returns the transformed message payload.
type SimpleMessageTransformFunc func(ctx context.Context, payload any, fields map[string]string) any

// TransformOnPattern returns a MessageTransformFunc that applies a SimpleMessageTransformFunc
// to messages whose topics match the given MQTT-style pattern. If the pattern matches,
// it extracts fields from the topic and passes them to the transform function.
//
// If the transform function returns nil, the message is dropped entirely.
//
// Pattern examples with field extraction:
//   - "sensor/+device/data" - extracts device name as "device" field
//   - "user/+userId/+action" - extracts userId and action as fields
//   - "events/+category/#" - extracts category, remaining path as fields
//
// Example:
//
//	enrichSensorData := func(ctx context.Context, payload any, fields map[string]string) any {
//	    deviceName := fields["device"]
//	    if deviceName == "blocked" {
//	        return nil // Drop the message
//	    }
//	    return map[string]any{
//	        "device":    deviceName,
//	        "data":      payload,
//	        "timestamp": time.Now().Unix(),
//	    }
//	}
//
//	transforms := []MessageTransformFunc{
//	    TransformOnPattern("sensor/+device/data", enrichSensorData),
//	}
func TransformOnPattern(pattern string, transform SimpleMessageTransformFunc) MessageTransformFunc {
	return func(msg *vinculum.EventBusMessage) (*vinculum.EventBusMessage, bool) {
		// Check if topic matches pattern
		if !mqttpattern.Matches(pattern, msg.Topic) {
			return msg, true // Topic doesn't match, pass through unchanged
		}

		// Extract fields from the topic using the pattern
		extractedFields := mqttpattern.Extract(pattern, msg.Topic)

		// Apply the simple transform function with extracted fields
		transformedPayload := transform(msg.Ctx, msg.Payload, extractedFields)

		// If transform function returns nil, drop the message
		if transformedPayload == nil {
			return nil, true
		}

		// Create new message with transformed payload
		transformed := &vinculum.EventBusMessage{
			Ctx:     msg.Ctx,
			MsgType: msg.MsgType,
			Topic:   msg.Topic,
			Payload: transformedPayload,
		}

		return transformed, true
	}
}

// IfPattern returns a MessageTransformFunc that applies a transform only if the topic
// matches the given MQTT-style pattern. If the pattern doesn't match, the message
// passes through unchanged.
//
// Example:
//
//	// Only add prefix to sensor messages
//	transforms := []MessageTransformFunc{
//	    IfPattern("sensor/+/data", AddTopicPrefix("processed/")),
//	}
func IfPattern(pattern string, transform MessageTransformFunc) MessageTransformFunc {
	return func(msg *vinculum.EventBusMessage) (*vinculum.EventBusMessage, bool) {
		if mqttpattern.Matches(pattern, msg.Topic) {
			return transform(msg)
		}
		return msg, true // Pass through unchanged if pattern doesn't match
	}
}

// IfElsePattern returns a MessageTransformFunc that applies one transform if the topic
// matches the given MQTT-style pattern, or another transform if it doesn't match.
//
// Example:
//
//	// Add different prefixes based on topic pattern
//	transforms := []MessageTransformFunc{
//	    IfElsePattern("sensor/+/data",
//	        AddTopicPrefix("sensor-processed/"),
//	        AddTopicPrefix("other-processed/")),
//	}
func IfElsePattern(pattern string, ifTransform, elseTransform MessageTransformFunc) MessageTransformFunc {
	return func(msg *vinculum.EventBusMessage) (*vinculum.EventBusMessage, bool) {
		if mqttpattern.Matches(pattern, msg.Topic) {
			return ifTransform(msg)
		}
		return elseTransform(msg)
	}
}

// ModifyPayload returns a MessageTransformFunc that applies a transformation
// function to the message payload.
//
// Example:
//
//	// Add metadata to all payloads
//	addMetadata := func(ctx context.Context, payload any) any {
//	    return map[string]any{
//	        "original": payload,
//	        "timestamp": time.Now().Unix(),
//	        "source": "eventbus",
//	    }
//	}
//
//	transforms := []MessageTransformFunc{
//	    ModifyPayload(addMetadata),
//	}
func ModifyPayload(transform SimpleMessageTransformFunc) MessageTransformFunc {
	return func(msg *vinculum.EventBusMessage) (*vinculum.EventBusMessage, bool) {
		// Pass empty fields map since this is not pattern-based
		transformedPayload := transform(msg.Ctx, msg.Payload, make(map[string]string))

		// If transform function returns nil, drop the message
		if transformedPayload == nil {
			return nil, true
		}

		// Create new message with transformed payload
		modified := &vinculum.EventBusMessage{
			Ctx:     msg.Ctx,
			MsgType: msg.MsgType,
			Topic:   msg.Topic,
			Payload: transformedPayload,
		}

		return modified, true
	}
}
