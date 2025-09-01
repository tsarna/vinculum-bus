package server

import (
	"context"
	"fmt"

	"github.com/amir-yaghoubi/mqttpattern"
	"github.com/tsarna/vinculum/pkg/vinculum/websockets"
)

// An EventAuthFunc is called to authorize and potentially modify client-published events.
// It can:
//   - Return (nil, nil) to silently drop the event (no error response sent)
//   - Return (nil, error) to deny the event with an error response
//   - Return (originalMsg, nil) to allow the event unchanged
//   - Return (modifiedMsg, nil) to allow the event with modifications
type EventAuthFunc func(ctx context.Context, msg *websockets.WireMessage) (*websockets.WireMessage, error)

// Predefined event authorization policies

// AllowAllEvents is an EventAuthFunc that allows all events without modification.
func AllowAllEvents(ctx context.Context, msg *websockets.WireMessage) (*websockets.WireMessage, error) {
	return msg, nil // Return original message unchanged
}

// DenyAllEvents is an EventAuthFunc that denies all events with a generic error.
// This is the default policy for security.
func DenyAllEvents(ctx context.Context, msg *websockets.WireMessage) (*websockets.WireMessage, error) {
	return nil, fmt.Errorf("client event publishing is not allowed")
}

// DropAllEvents is an EventAuthFunc that silently drops all events.
// The client receives an ACK response but the event is not published to the EventBus.
// This is useful for temporarily disabling event publishing without breaking clients.
func DropAllEvents(ctx context.Context, msg *websockets.WireMessage) (*websockets.WireMessage, error) {
	return nil, nil // nil message with nil error means silently drop
}

// AllowTopicPrefix returns an EventAuthFunc that only allows events to topics
// with the specified prefix. This is useful for sandboxing client events.
// You probably want the prefix to end with a slash.
func AllowTopicPrefix(prefix string) EventAuthFunc {
	return func(ctx context.Context, msg *websockets.WireMessage) (*websockets.WireMessage, error) {
		if len(msg.Topic) >= len(prefix) && msg.Topic[:len(prefix)] == prefix {
			return msg, nil // Allow with original message
		}
		return nil, fmt.Errorf("events only allowed to topics with prefix: %s", prefix)
	}
}

// AllowTopicPattern returns an EventAuthFunc that only allows events to topics
// that match the specified MQTT-style pattern. This provides more flexible
// topic filtering than simple prefix matching.
//
// Pattern examples:
//   - "sensor/+/data" - allows sensor/temperature/data, sensor/humidity/data, etc.
//   - "events/+category/#" - allows events/user/login, events/system/alerts/critical, etc.
//   - "client/+userId/actions/+" - allows client/john/actions/login, client/jane/actions/logout, etc.
func AllowTopicPattern(pattern string) EventAuthFunc {
	return func(ctx context.Context, msg *websockets.WireMessage) (*websockets.WireMessage, error) {
		if mqttpattern.Matches(pattern, msg.Topic) {
			return msg, nil // Allow with original message
		}
		return nil, fmt.Errorf("event not allowed")
	}
}

// ChainEventAuth returns an EventAuthFunc that applies multiple EventAuthFunc functions
// in sequence. The first function that returns an error or drops the message (nil, nil)
// will stop the chain. If all functions allow the message, the final modified message
// is returned.
//
// This allows combining multiple authorization policies:
//
//	// Allow only client events, but drop test clients
//	chainedAuth := ChainEventAuth(
//	    AllowTopicPrefix("client/"),
//	    func(ctx context.Context, msg *websockets.WireMessage) (*websockets.WireMessage, error) {
//	        if strings.Contains(msg.Topic, "/test/") {
//	            return nil, nil // Silently drop test events
//	        }
//	        return msg, nil
//	    },
//	)
func ChainEventAuth(funcs ...EventAuthFunc) EventAuthFunc {
	return func(ctx context.Context, msg *websockets.WireMessage) (*websockets.WireMessage, error) {
		current := msg
		for _, authFunc := range funcs {
			result, err := authFunc(ctx, current)
			if err != nil {
				// Function denied the event
				return nil, err
			}
			if result == nil {
				// Function silently dropped the event
				return nil, nil
			}
			// Function allowed/modified the event, continue with the result
			current = result
		}
		return current, nil
	}
}
