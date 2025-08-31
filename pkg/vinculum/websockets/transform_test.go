package websockets

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tsarna/vinculum/pkg/vinculum"
	"go.uber.org/zap"
)

func TestConnection_applyMessageTransforms(t *testing.T) {
	logger := zap.NewNop()
	eventBus := vinculum.NewEventBus(logger)
	ctx := context.Background()

	t.Run("no transforms configured", func(t *testing.T) {
		config := NewListenerConfig().
			WithEventBus(eventBus).
			WithLogger(logger)

		conn := newConnection(ctx, nil, config, &PassthroughSubscriptionController{})

		originalMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "test/topic",
			Message: "test message",
			Fields:  map[string]string{"key": "value"},
		}

		result := conn.applyMessageTransforms(originalMsg)
		assert.Equal(t, originalMsg, result)
	})

	t.Run("single transform - passthrough", func(t *testing.T) {
		passthroughTransform := func(msg *WebSocketMessage) (*WebSocketMessage, bool) {
			return msg, true
		}

		config := NewListenerConfig().
			WithEventBus(eventBus).
			WithLogger(logger).
			WithMessageTransforms(passthroughTransform)

		conn := newConnection(ctx, nil, config, &PassthroughSubscriptionController{})

		originalMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "test/topic",
			Message: "test message",
		}

		result := conn.applyMessageTransforms(originalMsg)
		assert.Equal(t, originalMsg, result)
	})

	t.Run("single transform - modify message", func(t *testing.T) {
		modifyTransform := func(msg *WebSocketMessage) (*WebSocketMessage, bool) {
			modified := &WebSocketMessage{
				Ctx:     msg.Ctx,
				Topic:   "modified/" + msg.Topic,
				Message: "Modified: " + msg.Message.(string),
				Fields:  msg.Fields,
			}
			return modified, true
		}

		config := NewListenerConfig().
			WithEventBus(eventBus).
			WithLogger(logger).
			WithMessageTransforms(modifyTransform)

		conn := newConnection(ctx, nil, config, &PassthroughSubscriptionController{})

		originalMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "test/topic",
			Message: "test message",
		}

		result := conn.applyMessageTransforms(originalMsg)
		assert.NotEqual(t, originalMsg, result)
		assert.Equal(t, "modified/test/topic", result.Topic)
		assert.Equal(t, "Modified: test message", result.Message)
	})

	t.Run("single transform - drop message", func(t *testing.T) {
		dropTransform := func(msg *WebSocketMessage) (*WebSocketMessage, bool) {
			return nil, true
		}

		config := NewListenerConfig().
			WithEventBus(eventBus).
			WithLogger(logger).
			WithMessageTransforms(dropTransform)

		conn := newConnection(ctx, nil, config, &PassthroughSubscriptionController{})

		originalMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "test/topic",
			Message: "test message",
		}

		result := conn.applyMessageTransforms(originalMsg)
		assert.Nil(t, result)
	})

	t.Run("multiple transforms - all continue", func(t *testing.T) {
		transform1 := func(msg *WebSocketMessage) (*WebSocketMessage, bool) {
			modified := &WebSocketMessage{
				Ctx:     msg.Ctx,
				Topic:   msg.Topic,
				Message: "Step1: " + msg.Message.(string),
				Fields:  msg.Fields,
			}
			return modified, true
		}

		transform2 := func(msg *WebSocketMessage) (*WebSocketMessage, bool) {
			modified := &WebSocketMessage{
				Ctx:     msg.Ctx,
				Topic:   msg.Topic,
				Message: "Step2: " + msg.Message.(string),
				Fields:  msg.Fields,
			}
			return modified, true
		}

		config := NewListenerConfig().
			WithEventBus(eventBus).
			WithLogger(logger).
			WithMessageTransforms(transform1, transform2)

		conn := newConnection(ctx, nil, config, &PassthroughSubscriptionController{})

		originalMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "test/topic",
			Message: "original",
		}

		result := conn.applyMessageTransforms(originalMsg)
		assert.NotNil(t, result)
		assert.Equal(t, "Step2: Step1: original", result.Message)
	})

	t.Run("multiple transforms - first stops processing", func(t *testing.T) {
		transform1 := func(msg *WebSocketMessage) (*WebSocketMessage, bool) {
			modified := &WebSocketMessage{
				Ctx:     msg.Ctx,
				Topic:   msg.Topic,
				Message: "Step1: " + msg.Message.(string),
				Fields:  msg.Fields,
			}
			return modified, false // Stop processing
		}

		transform2 := func(msg *WebSocketMessage) (*WebSocketMessage, bool) {
			// This should not be called
			modified := &WebSocketMessage{
				Ctx:     msg.Ctx,
				Topic:   msg.Topic,
				Message: "Step2: " + msg.Message.(string),
				Fields:  msg.Fields,
			}
			return modified, true
		}

		config := NewListenerConfig().
			WithEventBus(eventBus).
			WithLogger(logger).
			WithMessageTransforms(transform1, transform2)

		conn := newConnection(ctx, nil, config, &PassthroughSubscriptionController{})

		originalMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "test/topic",
			Message: "original",
		}

		result := conn.applyMessageTransforms(originalMsg)
		assert.NotNil(t, result)
		assert.Equal(t, "Step1: original", result.Message) // Only first transform applied
	})

	t.Run("multiple transforms - first drops message", func(t *testing.T) {
		transform1 := func(msg *WebSocketMessage) (*WebSocketMessage, bool) {
			return nil, true // Drop message
		}

		transform2 := func(msg *WebSocketMessage) (*WebSocketMessage, bool) {
			// This should not be called
			return msg, true
		}

		config := NewListenerConfig().
			WithEventBus(eventBus).
			WithLogger(logger).
			WithMessageTransforms(transform1, transform2)

		conn := newConnection(ctx, nil, config, &PassthroughSubscriptionController{})

		originalMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "test/topic",
			Message: "original",
		}

		result := conn.applyMessageTransforms(originalMsg)
		assert.Nil(t, result)
	})

	t.Run("complex pipeline - filtering and enrichment", func(t *testing.T) {
		// Filter out messages containing "secret"
		filterTransform := func(msg *WebSocketMessage) (*WebSocketMessage, bool) {
			if msgStr, ok := msg.Message.(string); ok && msgStr == "secret" {
				return nil, true // Drop secret messages
			}
			return msg, true
		}

		// Add timestamp to all messages
		enrichTransform := func(msg *WebSocketMessage) (*WebSocketMessage, bool) {
			enriched := &WebSocketMessage{
				Ctx:   msg.Ctx,
				Topic: msg.Topic,
				Message: map[string]any{
					"original":  msg.Message,
					"timestamp": "2024-01-01T00:00:00Z",
					"server":    "test-server",
				},
				Fields: msg.Fields,
			}
			return enriched, true
		}

		config := NewListenerConfig().
			WithEventBus(eventBus).
			WithLogger(logger).
			WithMessageTransforms(filterTransform, enrichTransform)

		conn := newConnection(ctx, nil, config, &PassthroughSubscriptionController{})

		// Test normal message (should be enriched)
		normalMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "test/topic",
			Message: "normal message",
		}

		result := conn.applyMessageTransforms(normalMsg)
		assert.NotNil(t, result)
		enrichedData := result.Message.(map[string]any)
		assert.Equal(t, "normal message", enrichedData["original"])
		assert.Equal(t, "2024-01-01T00:00:00Z", enrichedData["timestamp"])
		assert.Equal(t, "test-server", enrichedData["server"])

		// Test secret message (should be dropped)
		secretMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "test/topic",
			Message: "secret",
		}

		result = conn.applyMessageTransforms(secretMsg)
		assert.Nil(t, result)
	})
}

func TestDropTopicPattern(t *testing.T) {
	transform := DropTopicPattern("secret/+")
	ctx := context.Background()

	t.Run("drops matching topic", func(t *testing.T) {
		msg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "secret/data",
			Message: "sensitive info",
		}

		result, continueProcessing := transform(msg)
		assert.Nil(t, result)
		assert.True(t, continueProcessing)
	})

	t.Run("keeps non-matching topic", func(t *testing.T) {
		msg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "public/data",
			Message: "public info",
		}

		result, continueProcessing := transform(msg)
		assert.Equal(t, msg, result)
		assert.True(t, continueProcessing)
	})

	t.Run("wildcard patterns", func(t *testing.T) {
		// Test # wildcard
		transformHash := DropTopicPattern("debug/#")

		debugMsg := &WebSocketMessage{Topic: "debug/level1/level2"}
		result, _ := transformHash(debugMsg)
		assert.Nil(t, result) // Should be dropped

		nonDebugMsg := &WebSocketMessage{Topic: "info/level1"}
		result, _ = transformHash(nonDebugMsg)
		assert.NotNil(t, result) // Should be kept
	})
}

func TestDropTopicPrefix(t *testing.T) {
	transform := DropTopicPrefix("internal/")
	ctx := context.Background()

	t.Run("drops matching prefix", func(t *testing.T) {
		msg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "internal/system",
			Message: "internal data",
		}

		result, continueProcessing := transform(msg)
		assert.Nil(t, result)
		assert.True(t, continueProcessing)
	})

	t.Run("keeps non-matching prefix", func(t *testing.T) {
		msg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "public/system",
			Message: "public data",
		}

		result, continueProcessing := transform(msg)
		assert.Equal(t, msg, result)
		assert.True(t, continueProcessing)
	})
}

func TestAddTopicPrefix(t *testing.T) {
	transform := AddTopicPrefix("client/")
	ctx := context.Background()

	msg := &WebSocketMessage{
		Ctx:     ctx,
		Topic:   "events",
		Message: "test data",
		Fields:  map[string]string{"key": "value"},
	}

	result, continueProcessing := transform(msg)
	assert.NotEqual(t, msg, result) // Should be a new message
	assert.True(t, continueProcessing)
	assert.Equal(t, "client/events", result.Topic)
	assert.Equal(t, msg.Message, result.Message)
	assert.Equal(t, msg.Fields, result.Fields)
	assert.Equal(t, msg.Ctx, result.Ctx)
}

func TestRateLimitByTopic(t *testing.T) {
	transform := RateLimitByTopic(100 * time.Millisecond)
	ctx := context.Background()

	msg1 := &WebSocketMessage{
		Ctx:     ctx,
		Topic:   "test/topic",
		Message: "message 1",
	}

	msg2 := &WebSocketMessage{
		Ctx:     ctx,
		Topic:   "test/topic",
		Message: "message 2",
	}

	differentTopicMsg := &WebSocketMessage{
		Ctx:     ctx,
		Topic:   "other/topic",
		Message: "different topic",
	}

	// First message should pass
	result, continueProcessing := transform(msg1)
	assert.Equal(t, msg1, result)
	assert.True(t, continueProcessing)

	// Second message on same topic should be dropped (too soon)
	result, continueProcessing = transform(msg2)
	assert.Nil(t, result)
	assert.True(t, continueProcessing)

	// Different topic should pass
	result, continueProcessing = transform(differentTopicMsg)
	assert.Equal(t, differentTopicMsg, result)
	assert.True(t, continueProcessing)

	// Wait for rate limit to reset
	time.Sleep(110 * time.Millisecond)

	// Now the same topic should pass again
	result, continueProcessing = transform(msg2)
	assert.Equal(t, msg2, result)
	assert.True(t, continueProcessing)
}

func TestChainTransforms(t *testing.T) {
	// Create a chain that adds prefix, then drops secrets
	chain := ChainTransforms(
		AddTopicPrefix("processed/"),
		DropTopicPrefix("processed/secret/"),
	)

	ctx := context.Background()

	t.Run("processes through chain", func(t *testing.T) {
		msg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "public",
			Message: "test data",
		}

		result, continueProcessing := chain(msg)
		assert.NotEqual(t, msg, result)
		assert.True(t, continueProcessing)

		// Should have prefix added
		assert.Equal(t, "processed/public", result.Topic)
		assert.Equal(t, "test data", result.Message)
	})

	t.Run("drops message in chain", func(t *testing.T) {
		msg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "secret/data",
			Message: "sensitive",
		}

		result, continueProcessing := chain(msg)
		assert.Nil(t, result) // Should be dropped by second transform
		assert.True(t, continueProcessing)
	})
}

func TestChainTransforms_EarlyTermination(t *testing.T) {
	// Create a chain where first transform drops the message
	chain := ChainTransforms(
		DropTopicPrefix("secret/"),
		AddTopicPrefix("processed/"), // This should not be called
	)

	ctx := context.Background()
	msg := &WebSocketMessage{
		Ctx:     ctx,
		Topic:   "secret/data",
		Message: "sensitive",
	}

	result, continueProcessing := chain(msg)
	assert.Nil(t, result) // Message should be dropped by first transform
	assert.True(t, continueProcessing)
}

func TestTransformOnPattern(t *testing.T) {
	ctx := context.Background()

	t.Run("matches pattern and transforms message", func(t *testing.T) {
		// Create a transform that enriches sensor data
		enrichSensorData := func(ctx context.Context, data any, fields map[string]string) any {
			deviceName := fields["device"]
			return map[string]any{
				"device":    deviceName,
				"data":      data,
				"timestamp": int64(1234567890), // Fixed timestamp for testing
			}
		}

		transform := TransformOnPattern("sensor/+device/data", enrichSensorData)

		msg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "sensor/temperature/data",
			Message: 23.5,
			Fields:  map[string]string{"original": "field"},
		}

		result, continueProcessing := transform(msg)
		assert.NotEqual(t, msg, result)
		assert.True(t, continueProcessing)

		// Check transformed message
		assert.Equal(t, "sensor/temperature/data", result.Topic)
		assert.Equal(t, msg.Fields, result.Fields) // Original fields preserved

		transformedData := result.Message.(map[string]any)
		assert.Equal(t, "temperature", transformedData["device"])
		assert.Equal(t, 23.5, transformedData["data"])
		assert.Equal(t, int64(1234567890), transformedData["timestamp"])
	})

	t.Run("non-matching topic passes through unchanged", func(t *testing.T) {
		enrichSensorData := func(ctx context.Context, data any, fields map[string]string) any {
			return "should not be called"
		}

		transform := TransformOnPattern("sensor/+device/data", enrichSensorData)

		msg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "weather/temperature/reading", // Different pattern
			Message: 23.5,
		}

		result, continueProcessing := transform(msg)
		assert.Equal(t, msg, result) // Should be unchanged
		assert.True(t, continueProcessing)
	})

	t.Run("multiple field extraction", func(t *testing.T) {
		// Transform that uses multiple extracted fields
		enrichUserAction := func(ctx context.Context, data any, fields map[string]string) any {
			return map[string]any{
				"user_id":   fields["userId"],
				"action":    fields["action"],
				"payload":   data,
				"processed": true,
			}
		}

		transform := TransformOnPattern("user/+userId/+action", enrichUserAction)

		msg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "user/john123/login",
			Message: map[string]any{"ip": "192.168.1.1"},
		}

		result, continueProcessing := transform(msg)
		assert.NotEqual(t, msg, result)
		assert.True(t, continueProcessing)

		transformedData := result.Message.(map[string]any)
		assert.Equal(t, "john123", transformedData["user_id"])
		assert.Equal(t, "login", transformedData["action"])
		assert.Equal(t, map[string]any{"ip": "192.168.1.1"}, transformedData["payload"])
		assert.True(t, transformedData["processed"].(bool))
	})

	t.Run("hash wildcard matching", func(t *testing.T) {
		// Transform that uses hash wildcard (# matches everything but doesn't extract)
		enrichEventData := func(ctx context.Context, data any, fields map[string]string) any {
			return map[string]any{
				"category":  fields["category"],
				"original":  data,
				"processed": true,
			}
		}

		transform := TransformOnPattern("events/+category/#", enrichEventData)

		msg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "events/system/alerts/critical/memory",
			Message: "High memory usage detected",
		}

		result, continueProcessing := transform(msg)
		assert.NotEqual(t, msg, result)
		assert.True(t, continueProcessing)

		transformedData := result.Message.(map[string]any)
		assert.Equal(t, "system", transformedData["category"])
		assert.Equal(t, "High memory usage detected", transformedData["original"])
		assert.True(t, transformedData["processed"].(bool))
	})

	t.Run("transform can return nil to drop message", func(t *testing.T) {
		// Transform that conditionally drops messages
		filterByDevice := func(ctx context.Context, data any, fields map[string]string) any {
			deviceName := fields["device"]
			if deviceName == "blocked" {
				return nil // This will cause the message to be dropped
			}
			return data
		}

		transform := TransformOnPattern("sensor/+device/data", filterByDevice)

		// Test blocked device
		blockedMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "sensor/blocked/data",
			Message: "should be dropped",
		}

		result, continueProcessing := transform(blockedMsg)
		assert.True(t, continueProcessing)
		assert.Nil(t, result) // Message should be dropped entirely when transform returns nil

		// Test allowed device
		allowedMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "sensor/allowed/data",
			Message: "should pass through",
		}

		result, continueProcessing = transform(allowedMsg)
		assert.True(t, continueProcessing)
		assert.NotNil(t, result)
		assert.Equal(t, "should pass through", result.Message)
		// Should be a new message object, not the same pointer
		assert.NotSame(t, allowedMsg, result)
	})
}

func TestIfPattern(t *testing.T) {
	ctx := context.Background()

	t.Run("applies transform when pattern matches", func(t *testing.T) {
		// Create a transform that adds a prefix
		addPrefix := AddTopicPrefix("processed/")
		conditionalTransform := IfPattern("sensor/+/data", addPrefix)

		msg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "sensor/temperature/data",
			Message: "test data",
		}

		result, continueProcessing := conditionalTransform(msg)
		assert.True(t, continueProcessing)
		assert.NotEqual(t, msg, result)
		assert.Equal(t, "processed/sensor/temperature/data", result.Topic)
		assert.Equal(t, "test data", result.Message)
	})

	t.Run("passes through unchanged when pattern doesn't match", func(t *testing.T) {
		// Create a transform that would add a prefix
		addPrefix := AddTopicPrefix("processed/")
		conditionalTransform := IfPattern("sensor/+/data", addPrefix)

		msg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "weather/temperature/reading", // Different pattern
			Message: "test data",
		}

		result, continueProcessing := conditionalTransform(msg)
		assert.True(t, continueProcessing)
		assert.Equal(t, msg, result) // Should be unchanged
	})

	t.Run("works with dropping transforms", func(t *testing.T) {
		// Create a transform that drops messages
		dropTransform := DropTopicPrefix("secret/")
		conditionalTransform := IfPattern("secret/+", dropTransform)

		// Test matching topic (should be dropped)
		secretMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "secret/data",
			Message: "confidential",
		}

		result, continueProcessing := conditionalTransform(secretMsg)
		assert.True(t, continueProcessing)
		assert.Nil(t, result) // Should be dropped

		// Test non-matching topic (should pass through)
		publicMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "public/data",
			Message: "public info",
		}

		result, continueProcessing = conditionalTransform(publicMsg)
		assert.True(t, continueProcessing)
		assert.Equal(t, publicMsg, result) // Should pass through unchanged
	})
}

func TestIfElsePattern(t *testing.T) {
	ctx := context.Background()

	t.Run("applies if-transform when pattern matches", func(t *testing.T) {
		ifTransform := AddTopicPrefix("sensor-processed/")
		elseTransform := AddTopicPrefix("other-processed/")
		conditionalTransform := IfElsePattern("sensor/+/data", ifTransform, elseTransform)

		msg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "sensor/temperature/data",
			Message: "test data",
		}

		result, continueProcessing := conditionalTransform(msg)
		assert.True(t, continueProcessing)
		assert.NotEqual(t, msg, result)
		assert.Equal(t, "sensor-processed/sensor/temperature/data", result.Topic)
		assert.Equal(t, "test data", result.Message)
	})

	t.Run("applies else-transform when pattern doesn't match", func(t *testing.T) {
		ifTransform := AddTopicPrefix("sensor-processed/")
		elseTransform := AddTopicPrefix("other-processed/")
		conditionalTransform := IfElsePattern("sensor/+/data", ifTransform, elseTransform)

		msg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "weather/temperature/reading", // Different pattern
			Message: "test data",
		}

		result, continueProcessing := conditionalTransform(msg)
		assert.True(t, continueProcessing)
		assert.NotEqual(t, msg, result)
		assert.Equal(t, "other-processed/weather/temperature/reading", result.Topic)
		assert.Equal(t, "test data", result.Message)
	})

	t.Run("passes through when pattern doesn't match and else-transform is nil", func(t *testing.T) {
		ifTransform := AddTopicPrefix("sensor-processed/")
		conditionalTransform := IfElsePattern("sensor/+/data", ifTransform, nil)

		msg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "weather/temperature/reading", // Different pattern
			Message: "test data",
		}

		result, continueProcessing := conditionalTransform(msg)
		assert.True(t, continueProcessing)
		assert.Equal(t, msg, result) // Should pass through unchanged
	})

	t.Run("works with dropping transforms", func(t *testing.T) {
		// Drop secret messages, add prefix to others
		ifTransform := DropTopicPrefix("secret/")
		elseTransform := AddTopicPrefix("processed/")
		conditionalTransform := IfElsePattern("secret/+", ifTransform, elseTransform)

		// Test secret message (should be dropped)
		secretMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "secret/data",
			Message: "confidential",
		}

		result, continueProcessing := conditionalTransform(secretMsg)
		assert.True(t, continueProcessing)
		assert.Nil(t, result) // Should be dropped

		// Test public message (should get prefix)
		publicMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "public/data",
			Message: "public info",
		}

		result, continueProcessing = conditionalTransform(publicMsg)
		assert.True(t, continueProcessing)
		assert.NotEqual(t, publicMsg, result)
		assert.Equal(t, "processed/public/data", result.Topic)
	})

	t.Run("both transforms can drop messages", func(t *testing.T) {
		// Drop secret messages, drop test messages, pass through others
		ifTransform := DropTopicPrefix("secret/")
		elseTransform := DropTopicPrefix("test/")
		conditionalTransform := IfElsePattern("secret/+", ifTransform, elseTransform)

		// Test secret message (should be dropped by if-transform)
		secretMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "secret/data",
			Message: "confidential",
		}

		result, continueProcessing := conditionalTransform(secretMsg)
		assert.True(t, continueProcessing)
		assert.Nil(t, result) // Should be dropped

		// Test test message (should be dropped by else-transform)
		testMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "test/data",
			Message: "test info",
		}

		result, continueProcessing = conditionalTransform(testMsg)
		assert.True(t, continueProcessing)
		assert.Nil(t, result) // Should be dropped

		// Test public message (should pass through)
		publicMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "public/data",
			Message: "public info",
		}

		result, continueProcessing = conditionalTransform(publicMsg)
		assert.True(t, continueProcessing)
		assert.Equal(t, publicMsg, result) // Should pass through unchanged
	})
}

func TestConditionalTransforms_WithChainTransforms(t *testing.T) {
	ctx := context.Background()

	t.Run("IfPattern works in transform chains", func(t *testing.T) {
		// Chain: Add timestamp to all, then conditionally add prefix to sensors
		chain := ChainTransforms(
			func(msg *WebSocketMessage) (*WebSocketMessage, bool) {
				// Add timestamp to all messages
				enriched := &WebSocketMessage{
					Ctx:   msg.Ctx,
					Topic: msg.Topic,
					Message: map[string]any{
						"original":  msg.Message,
						"timestamp": int64(1234567890),
					},
					Fields: msg.Fields,
				}
				return enriched, true
			},
			IfPattern("sensor/+/data", AddTopicPrefix("processed/")),
		)

		// Test sensor message
		sensorMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "sensor/temperature/data",
			Message: "23.5",
		}

		result, continueProcessing := chain(sensorMsg)
		assert.True(t, continueProcessing)
		assert.NotEqual(t, sensorMsg, result)
		assert.Equal(t, "processed/sensor/temperature/data", result.Topic)

		enrichedData := result.Message.(map[string]any)
		assert.Equal(t, "23.5", enrichedData["original"])
		assert.Equal(t, int64(1234567890), enrichedData["timestamp"])

		// Test non-sensor message
		weatherMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "weather/temperature/reading",
			Message: "25.0",
		}

		result, continueProcessing = chain(weatherMsg)
		assert.True(t, continueProcessing)
		assert.NotEqual(t, weatherMsg, result)
		assert.Equal(t, "weather/temperature/reading", result.Topic) // No prefix added

		enrichedData = result.Message.(map[string]any)
		assert.Equal(t, "25.0", enrichedData["original"])
		assert.Equal(t, int64(1234567890), enrichedData["timestamp"])
	})

	t.Run("IfElsePattern works in transform chains", func(t *testing.T) {
		// Chain: Conditionally route messages, then add common suffix
		chain := ChainTransforms(
			IfElsePattern("sensor/+/data",
				AddTopicPrefix("sensors/"),
				AddTopicPrefix("others/")),
			func(msg *WebSocketMessage) (*WebSocketMessage, bool) {
				// Add suffix to all messages
				suffixed := &WebSocketMessage{
					Ctx:     msg.Ctx,
					Topic:   msg.Topic + "/processed",
					Message: msg.Message,
					Fields:  msg.Fields,
				}
				return suffixed, true
			},
		)

		// Test sensor message
		sensorMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "sensor/temperature/data",
			Message: "23.5",
		}

		result, continueProcessing := chain(sensorMsg)
		assert.True(t, continueProcessing)
		assert.Equal(t, "sensors/sensor/temperature/data/processed", result.Topic)

		// Test other message
		weatherMsg := &WebSocketMessage{
			Ctx:     ctx,
			Topic:   "weather/temperature/reading",
			Message: "25.0",
		}

		result, continueProcessing = chain(weatherMsg)
		assert.True(t, continueProcessing)
		assert.Equal(t, "others/weather/temperature/reading/processed", result.Topic)
	})
}
