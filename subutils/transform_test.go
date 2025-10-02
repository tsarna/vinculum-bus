package subutils

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/tsarna/vinculum-bus"
	"github.com/tsarna/vinculum-bus/transform"
)

// testSubscriber is a simple subscriber for testing
type testSubscriber struct {
	events []testEvent
}

type testEvent struct {
	topic   string
	message any
	fields  map[string]string
}

func (t *testSubscriber) OnSubscribe(ctx context.Context, topic string) error {
	return nil
}

func (t *testSubscriber) OnUnsubscribe(ctx context.Context, topic string) error {
	return nil
}

func (t *testSubscriber) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	t.events = append(t.events, testEvent{
		topic:   topic,
		message: message,
		fields:  fields,
	})
	return nil
}

func (t *testSubscriber) PassThrough(msg bus.EventBusMessage) error {
	return nil
}

func TestNewTransformingSubscriber(t *testing.T) {
	baseSubscriber := &testSubscriber{}
	transformingSubscriber := NewTransformingSubscriber(baseSubscriber)

	assert.NotNil(t, transformingSubscriber)
	assert.Equal(t, baseSubscriber, transformingSubscriber.wrapped)
	assert.Empty(t, transformingSubscriber.transforms)
}

func TestTransformingSubscriber_PassThroughMethods(t *testing.T) {
	baseSubscriber := &testSubscriber{}
	transformingSubscriber := NewTransformingSubscriber(baseSubscriber)
	ctx := context.Background()

	// Test OnSubscribe
	err := transformingSubscriber.OnSubscribe(ctx, "test/topic")
	assert.NoError(t, err)

	// Test OnUnsubscribe
	err = transformingSubscriber.OnUnsubscribe(ctx, "test/topic")
	assert.NoError(t, err)

	// Test PassThrough
	msg := bus.EventBusMessage{
		Ctx:     ctx,
		MsgType: bus.MessageTypeEvent,
		Topic:   "test/topic",
		Payload: "test data",
	}
	err = transformingSubscriber.PassThrough(msg)
	assert.NoError(t, err)
}

func TestTransformingSubscriber_OnEventNoTransforms(t *testing.T) {
	baseSubscriber := &testSubscriber{}
	transformingSubscriber := NewTransformingSubscriber(baseSubscriber)
	ctx := context.Background()

	// Call OnEvent with no transforms - should pass through directly
	err := transformingSubscriber.OnEvent(ctx, "test/topic", "test message", map[string]string{"key": "value"})
	assert.NoError(t, err)

	// Verify the event was passed to the wrapped subscriber
	assert.Len(t, baseSubscriber.events, 1)
	assert.Equal(t, "test/topic", baseSubscriber.events[0].topic)
	assert.Equal(t, "test message", baseSubscriber.events[0].message)
	assert.Equal(t, map[string]string{"key": "value"}, baseSubscriber.events[0].fields)
}

func TestTransformingSubscriber_OnEventWithTransforms(t *testing.T) {
	baseSubscriber := &testSubscriber{}

	// Create a transform that adds a prefix to the topic
	addPrefix := transform.AddTopicPrefix("processed/")

	// Create a transform that modifies the payload
	addMetadata := transform.ModifyPayload(func(ctx context.Context, payload any, fields map[string]string) any {
		return map[string]any{
			"original":     payload,
			"processed_at": time.Now().Unix(),
		}
	})

	transformingSubscriber := NewTransformingSubscriber(baseSubscriber, addPrefix, addMetadata)
	ctx := context.Background()

	// Call OnEvent
	err := transformingSubscriber.OnEvent(ctx, "events/user/login", "user logged in", nil)
	assert.NoError(t, err)

	// Verify the event was transformed and passed to the wrapped subscriber
	assert.Len(t, baseSubscriber.events, 1)

	event := baseSubscriber.events[0]
	assert.Equal(t, "processed/events/user/login", event.topic)

	// Check that the payload was transformed
	transformedPayload, ok := event.message.(map[string]any)
	assert.True(t, ok, "Payload should be transformed to map[string]any")
	assert.Equal(t, "user logged in", transformedPayload["original"])
	assert.Contains(t, transformedPayload, "processed_at")
}

func TestTransformingSubscriber_OnEventDropMessage(t *testing.T) {
	baseSubscriber := &testSubscriber{}

	// Create a transform that drops messages with "debug" prefix
	dropDebug := transform.DropTopicPrefix("debug/")

	transformingSubscriber := NewTransformingSubscriber(baseSubscriber, dropDebug)
	ctx := context.Background()

	// Call OnEvent with a debug topic - should be dropped
	err := transformingSubscriber.OnEvent(ctx, "debug/info", "debug message", nil)
	assert.NoError(t, err)

	// Verify no event was passed to the wrapped subscriber
	assert.Len(t, baseSubscriber.events, 0)

	// Call OnEvent with a non-debug topic - should pass through
	err = transformingSubscriber.OnEvent(ctx, "info/data", "info message", nil)
	assert.NoError(t, err)

	// Verify the non-debug event was passed to the wrapped subscriber
	assert.Len(t, baseSubscriber.events, 1)
	assert.Equal(t, "info/data", baseSubscriber.events[0].topic)
	assert.Equal(t, "info message", baseSubscriber.events[0].message)
}

func TestTransformingSubscriber_OnEventWithPatternTransform(t *testing.T) {
	baseSubscriber := &testSubscriber{}

	// Create a transform that enriches sensor data
	enrichSensorData := transform.TransformOnPattern("sensor/+device/data",
		func(ctx context.Context, payload any, fields map[string]string) any {
			deviceName := fields["device"]
			return map[string]any{
				"device":    deviceName,
				"data":      payload,
				"timestamp": time.Now().Unix(),
			}
		})

	transformingSubscriber := NewTransformingSubscriber(baseSubscriber, enrichSensorData)
	ctx := context.Background()

	// Call OnEvent with a matching sensor topic
	err := transformingSubscriber.OnEvent(ctx, "sensor/temp1/data", 25.5, nil)
	assert.NoError(t, err)

	// Verify the event was transformed
	assert.Len(t, baseSubscriber.events, 1)

	event := baseSubscriber.events[0]
	assert.Equal(t, "sensor/temp1/data", event.topic)

	transformedPayload, ok := event.message.(map[string]any)
	assert.True(t, ok, "Payload should be transformed to map[string]any")
	assert.Equal(t, "temp1", transformedPayload["device"])
	assert.Equal(t, 25.5, transformedPayload["data"])
	assert.Contains(t, transformedPayload, "timestamp")

	// Reset and test with non-matching topic
	baseSubscriber.events = nil
	err = transformingSubscriber.OnEvent(ctx, "other/topic", "other data", nil)
	assert.NoError(t, err)

	// Verify the non-matching event passed through unchanged
	assert.Len(t, baseSubscriber.events, 1)
	assert.Equal(t, "other/topic", baseSubscriber.events[0].topic)
	assert.Equal(t, "other data", baseSubscriber.events[0].message)
}

func TestTransformingSubscriber_ChainedTransforms(t *testing.T) {
	baseSubscriber := &testSubscriber{}

	// Create a chain of transforms
	transforms := []transform.MessageTransformFunc{
		// First, drop any debug messages
		transform.DropTopicPrefix("debug/"),
		// Then, add a prefix to remaining messages
		transform.AddTopicPrefix("processed/"),
		// Finally, enrich the payload
		transform.ModifyPayload(func(ctx context.Context, payload any, fields map[string]string) any {
			return map[string]any{
				"enriched": true,
				"data":     payload,
			}
		}),
	}

	transformingSubscriber := NewTransformingSubscriber(baseSubscriber, transforms...)
	ctx := context.Background()

	// Test with debug message - should be dropped
	err := transformingSubscriber.OnEvent(ctx, "debug/test", "debug info", nil)
	assert.NoError(t, err)
	assert.Len(t, baseSubscriber.events, 0)

	// Test with regular message - should be transformed
	err = transformingSubscriber.OnEvent(ctx, "events/user", "user data", nil)
	assert.NoError(t, err)

	assert.Len(t, baseSubscriber.events, 1)
	event := baseSubscriber.events[0]

	// Verify topic was prefixed
	assert.Equal(t, "processed/events/user", event.topic)

	// Verify payload was enriched
	transformedPayload, ok := event.message.(map[string]any)
	assert.True(t, ok)
	assert.True(t, transformedPayload["enriched"].(bool))
	assert.Equal(t, "user data", transformedPayload["data"])
}
