package websockets

import (
	"context"
	"strings"
	"time"

	"github.com/amir-yaghoubi/mqttpattern"
)

// MessageTransformFunc is a function that transforms outbound WebSocket messages.
// It receives a message from the EventBus and can modify, replace, or drop it
// before sending to the WebSocket client.
//
// Parameters:
//   - msg: The WebSocket message to transform (may be nil if previous transform dropped it)
//
// Returns:
//   - *WebSocketMessage: The transformed message (nil to drop the message)
//   - bool: Whether to continue calling subsequent transform functions (ignored if msg is nil)
//
// Transform functions are called in the order they were added to the configuration.
// If a function returns nil, the message is dropped and no further transforms are called.
// If a function returns false for the continue flag, no further transforms are called.
type MessageTransformFunc func(msg *WebSocketMessage) (*WebSocketMessage, bool)

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
//	config.WithMessageTransforms(
//	    DropTopicPattern("secret/+"),
//	    DropTopicPattern("debug/#"),
//	)
func DropTopicPattern(pattern string) MessageTransformFunc {
	return func(msg *WebSocketMessage) (*WebSocketMessage, bool) {
		if mqttpattern.Matches(pattern, msg.Topic) {
			return nil, true // Drop message, continue pipeline (though it will stop due to nil)
		}
		return msg, true // Keep message, continue pipeline
	}
}

// DropTopicPrefix returns a MessageTransformFunc that drops messages
// whose topics start with the given prefix.
//
// Example:
//
//	config.WithMessageTransforms(
//	    DropTopicPrefix("internal/"),
//	    DropTopicPrefix("debug/"),
//	)
func DropTopicPrefix(prefix string) MessageTransformFunc {
	return func(msg *WebSocketMessage) (*WebSocketMessage, bool) {
		if strings.HasPrefix(msg.Topic, prefix) {
			return nil, true // Drop message
		}
		return msg, true // Keep message
	}
}

// AddTopicPrefix returns a MessageTransformFunc that adds a prefix
// to all message topics.
//
// Example:
//
//	config.WithMessageTransforms(
//	    AddTopicPrefix("client/"), // "events" becomes "client/events"
//	)
func AddTopicPrefix(prefix string) MessageTransformFunc {
	return func(msg *WebSocketMessage) (*WebSocketMessage, bool) {
		modified := &WebSocketMessage{
			Ctx:     msg.Ctx,
			Topic:   prefix + msg.Topic,
			Message: msg.Message,
			Fields:  msg.Fields,
		}
		return modified, true
	}
}

// RateLimitByTopic returns a MessageTransformFunc that implements
// simple rate limiting per topic pattern.
//
// Note: This is a basic implementation. For production use, consider
// more sophisticated rate limiting with sliding windows, Redis, etc.
//
// Example:
//
//	config.WithMessageTransforms(
//	    RateLimitByTopic(time.Second), // Max 1 message per second per topic
//	)
func RateLimitByTopic(minInterval time.Duration) MessageTransformFunc {
	lastSent := make(map[string]time.Time)

	return func(msg *WebSocketMessage) (*WebSocketMessage, bool) {
		now := time.Now()
		if last, exists := lastSent[msg.Topic]; exists {
			if now.Sub(last) < minInterval {
				return nil, true // Drop message due to rate limit
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
//	config.WithMessageTransforms(securityPipeline, otherTransform)
func ChainTransforms(transforms ...MessageTransformFunc) MessageTransformFunc {
	return func(msg *WebSocketMessage) (*WebSocketMessage, bool) {
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

// SimpleMessageTransformFunc is a simple function that transforms the message data.
// It receives the message data and extracted fields from topic pattern matching,
// and returns the transformed message data.
type SimpleMessageTransformFunc func(ctx context.Context, msgData any, fields map[string]string) any

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
//	enrichSensorData := func(ctx context.Context, data any, fields map[string]string) any {
//	    deviceName := fields["device"]
//	    if deviceName == "blocked" {
//	        return nil // Drop the message
//	    }
//	    return map[string]any{
//	        "device":    deviceName,
//	        "data":      data,
//	        "timestamp": time.Now().Unix(),
//	    }
//	}
//
//	config.WithMessageTransforms(
//	    TransformOnPattern("sensor/+device/data", enrichSensorData),
//	)
func TransformOnPattern(pattern string, transform SimpleMessageTransformFunc) MessageTransformFunc {
	return func(msg *WebSocketMessage) (*WebSocketMessage, bool) {
		// Check if topic matches pattern
		if !mqttpattern.Matches(pattern, msg.Topic) {
			return msg, true // Topic doesn't match, pass through unchanged
		}

		// Extract fields from the topic using the pattern
		extractedFields := mqttpattern.Extract(pattern, msg.Topic)

		// Apply the simple transform function with extracted fields
		transformedData := transform(msg.Ctx, msg.Message, extractedFields)

		// If transform function returns nil, drop the message
		if transformedData == nil {
			return nil, true
		}

		// Create new message with transformed data
		transformed := &WebSocketMessage{
			Ctx:     msg.Ctx,
			Topic:   msg.Topic,
			Message: transformedData,
			Fields:  msg.Fields, // Preserve original fields
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
//	config.WithMessageTransforms(
//	    IfPattern("sensor/+/data", AddTopicPrefix("processed/")),
//	)
func IfPattern(pattern string, transform MessageTransformFunc) MessageTransformFunc {
	return func(msg *WebSocketMessage) (*WebSocketMessage, bool) {
		if mqttpattern.Matches(pattern, msg.Topic) {
			return transform(msg)
		}
		return msg, true // Pass through unchanged if pattern doesn't match
	}
}

// IfElsePattern returns a MessageTransformFunc that applies one transform if the topic
// matches the given MQTT-style pattern, or another transform if it doesn't match.
// The elseTransform parameter is optional - pass nil to use pass-through behavior
// when the pattern doesn't match.
//
// Example:
//
//	// Add different prefixes based on topic pattern
//	config.WithMessageTransforms(
//	    IfElsePattern("sensor/+/data",
//	        AddTopicPrefix("sensor-processed/"),
//	        AddTopicPrefix("other-processed/")),
//	)
//
//	// Drop secret messages, pass through others
//	config.WithMessageTransforms(
//	    IfElsePattern("secret/+",
//	        DropTopicPattern("secret/+"),
//	        nil), // Pass through non-secret messages
//	)
func IfElsePattern(pattern string, ifTransform, elseTransform MessageTransformFunc) MessageTransformFunc {
	return func(msg *WebSocketMessage) (*WebSocketMessage, bool) {
		if mqttpattern.Matches(pattern, msg.Topic) {
			return ifTransform(msg)
		}
		if elseTransform != nil {
			return elseTransform(msg)
		}
		return msg, true // Pass through unchanged if no else transform
	}
}
