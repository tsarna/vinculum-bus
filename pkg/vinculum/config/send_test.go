package config

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tsarna/vinculum/pkg/vinculum/bus"
	"github.com/zclconf/go-cty/cty"
	"go.uber.org/zap"
)

const (
	timeout  = 1 * time.Second
	interval = 10 * time.Millisecond
)

// mockSubscriber captures published messages for testing
type mockSubscriber struct {
	bus.BaseSubscriber
	messages []mockMessage
}

type mockMessage struct {
	topic   string
	payload any
}

func (m *mockSubscriber) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	m.messages = append(m.messages, mockMessage{
		topic:   topic,
		payload: message,
	})
	return nil
}

func TestSendFunctions(t *testing.T) {
	logger, err := zap.NewDevelopment()
	require.NoError(t, err)

	// Create a test config with a bus
	config := &Config{
		Logger:         logger,
		Buses:          make(map[string]bus.EventBus),
		CtyBusMap:      make(map[string]cty.Value),
		Constants:      make(map[string]cty.Value),
		evalCtx:        &hcl.EvalContext{},
		BusCapsuleType: EventBusCapsuleType,
	}

	// Create a test bus
	eventBus, err := bus.NewEventBus().WithLogger(logger).WithName("test").Build()
	require.NoError(t, err)

	err = eventBus.Start()
	require.NoError(t, err)
	defer eventBus.Stop()

	config.Buses["test"] = eventBus
	config.CtyBusMap["test"] = NewEventBusCapsule(eventBus)

	// Create mock subscriber
	subscriber := &mockSubscriber{}
	err = eventBus.Subscribe(context.Background(), subscriber, "test/topic")
	require.NoError(t, err)

	// Test data - using cty values directly to avoid conversion issues
	testCtyValue := cty.ObjectVal(map[string]cty.Value{
		"message": cty.StringVal("hello world"),
		"count":   cty.NumberIntVal(42),
		"active":  cty.BoolVal(true),
	})

	// Create context object
	ctx := context.Background()
	ctxValue, diags := NewContext(ctx).Build()
	require.False(t, diags.HasErrors(), "Context build should not have errors")
	busValue := config.CtyBusMap["test"]
	topicValue := cty.StringVal("test/topic")

	t.Run("send function (original)", func(t *testing.T) {
		subscriber.messages = nil // Reset messages

		sendFunc := SendFunction(config)
		result, err := sendFunc.Call([]cty.Value{ctxValue, busValue, topicValue, testCtyValue})

		require.NoError(t, err)
		assert.True(t, result.True())

		// Give some time for async processing
		assert.Eventually(t, func() bool {
			return len(subscriber.messages) > 0
		}, timeout, interval)

		// The original send should pass the cty.Value as-is
		assert.Equal(t, "test/topic", subscriber.messages[0].topic)
		assert.Equal(t, testCtyValue, subscriber.messages[0].payload)
	})

	t.Run("sendjson function", func(t *testing.T) {
		subscriber.messages = nil // Reset messages

		sendJSONFunc := SendJSONFunction(config)
		result, err := sendJSONFunc.Call([]cty.Value{ctxValue, busValue, topicValue, testCtyValue})

		require.NoError(t, err)
		assert.True(t, result.True())

		// Give some time for async processing
		assert.Eventually(t, func() bool {
			return len(subscriber.messages) > 0
		}, timeout, interval)

		// The sendjson should convert to JSON string
		assert.Equal(t, "test/topic", subscriber.messages[0].topic)
		jsonStr, ok := subscriber.messages[0].payload.(string)
		require.True(t, ok, "Expected payload to be a JSON string")

		// Verify it's valid JSON by unmarshaling
		var unmarshaled any
		err = json.Unmarshal([]byte(jsonStr), &unmarshaled)
		require.NoError(t, err)

		// Verify the content matches our expected structure
		expected := map[string]any{
			"message": "hello world",
			"count":   float64(42), // JSON numbers become float64
			"active":  true,
		}
		assert.Equal(t, expected, unmarshaled)
	})

	t.Run("sendgo function", func(t *testing.T) {
		subscriber.messages = nil // Reset messages

		sendGoFunc := SendGoFunction(config)
		result, err := sendGoFunc.Call([]cty.Value{ctxValue, busValue, topicValue, testCtyValue})

		require.NoError(t, err)
		assert.True(t, result.True())

		// Give some time for async processing
		assert.Eventually(t, func() bool {
			return len(subscriber.messages) > 0
		}, timeout, interval)

		// The sendgo should convert to Go native types
		assert.Equal(t, "test/topic", subscriber.messages[0].topic)
		goValue, ok := subscriber.messages[0].payload.(map[string]any)
		require.True(t, ok, "Expected payload to be a Go map")

		// Verify the content matches our expected structure
		expectedGo := map[string]any{
			"message": "hello world",
			"count":   int64(42), // Our custom converter returns int64 for exact integers
			"active":  true,
		}
		assert.Equal(t, expectedGo, goValue)
	})

	t.Run("sendgo function with nested objects", func(t *testing.T) {
		subscriber.messages = nil // Reset messages

		// Create a complex nested structure
		nestedCtyValue := cty.ObjectVal(map[string]cty.Value{
			"user": cty.ObjectVal(map[string]cty.Value{
				"name": cty.StringVal("Alice"),
				"age":  cty.NumberIntVal(30),
				"profile": cty.ObjectVal(map[string]cty.Value{
					"email":    cty.StringVal("alice@example.com"),
					"verified": cty.BoolVal(true),
				}),
			}),
			"tags": cty.ListVal([]cty.Value{
				cty.StringVal("admin"),
				cty.StringVal("premium"),
			}),
			"scores": cty.TupleVal([]cty.Value{
				cty.NumberIntVal(95),
				cty.NumberFloatVal(87.5),
			}),
			"metadata": cty.ObjectVal(map[string]cty.Value{
				"source":    cty.StringVal("api"),
				"timestamp": cty.NumberIntVal(1234567890),
			}),
		})

		sendGoFunc := SendGoFunction(config)
		result, err := sendGoFunc.Call([]cty.Value{ctxValue, busValue, topicValue, nestedCtyValue})

		require.NoError(t, err)
		assert.True(t, result.True())

		// Give some time for async processing
		assert.Eventually(t, func() bool {
			return len(subscriber.messages) > 0
		}, timeout, interval)

		// The sendgo should convert to Go native types recursively
		assert.Equal(t, "test/topic", subscriber.messages[0].topic)
		goValue, ok := subscriber.messages[0].payload.(map[string]any)
		require.True(t, ok, "Expected payload to be a Go map")

		// Verify the nested structure
		expectedNested := map[string]any{
			"user": map[string]any{
				"name": "Alice",
				"age":  int64(30),
				"profile": map[string]any{
					"email":    "alice@example.com",
					"verified": true,
				},
			},
			"tags":   []any{"admin", "premium"},
			"scores": []any{int64(95), 87.5},
			"metadata": map[string]any{
				"source":    "api",
				"timestamp": int64(1234567890),
			},
		}
		assert.Equal(t, expectedNested, goValue)
	})

	t.Run("sendgo function with capsule types", func(t *testing.T) {
		subscriber.messages = nil // Reset messages

		// Create a structure containing a capsule (context)
		capsuleValue := cty.ObjectVal(map[string]cty.Value{
			"context": NewContextCapsule(context.Background()),
			"message": cty.StringVal("test with capsule"),
		})

		sendGoFunc := SendGoFunction(config)
		result, err := sendGoFunc.Call([]cty.Value{ctxValue, busValue, topicValue, capsuleValue})

		require.NoError(t, err)
		assert.True(t, result.True())

		// Give some time for async processing
		assert.Eventually(t, func() bool {
			return len(subscriber.messages) > 0
		}, timeout, interval)

		// The sendgo should unwrap capsule types
		assert.Equal(t, "test/topic", subscriber.messages[0].topic)
		goValue, ok := subscriber.messages[0].payload.(map[string]any)
		require.True(t, ok, "Expected payload to be a Go map")

		// Verify the capsule was unwrapped to the actual context pointer
		assert.Equal(t, "test with capsule", goValue["message"])
		contextPtr, ok := goValue["context"].(*context.Context)
		require.True(t, ok, "Expected context to be unwrapped to *context.Context")
		assert.NotNil(t, contextPtr, "Context pointer should not be nil")
	})

	t.Run("error handling - invalid context", func(t *testing.T) {
		sendFunc := SendFunction(config)
		invalidCtx := cty.StringVal("invalid")

		_, err := sendFunc.Call([]cty.Value{invalidCtx, busValue, topicValue, testCtyValue})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "context error")
	})

	t.Run("error handling - invalid bus", func(t *testing.T) {
		sendFunc := SendFunction(config)
		invalidBus := cty.StringVal("invalid")

		_, err := sendFunc.Call([]cty.Value{ctxValue, invalidBus, topicValue, testCtyValue})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected EventBus capsule")
	})
}
