package transform

import (
	"context"
	"testing"
	"time"

	"github.com/tsarna/vinculum-bus"
)

func TestMessageTransformFunc(t *testing.T) {
	ctx := context.Background()

	// Create a sample EventBusMessage
	originalMsg := &bus.EventBusMessage{
		Ctx:     ctx,
		MsgType: bus.MessageTypeEvent,
		Topic:   "sensor/temp1/data",
		Payload: map[string]any{"temperature": 25.5},
	}

	t.Run("DropTopicPattern", func(t *testing.T) {
		transform := DropTopicPattern("sensor/+/data")
		result, cont := transform(originalMsg)

		if result != nil {
			t.Error("Expected message to be dropped, but got result")
		}
		if cont {
			t.Error("Expected continue to be false when message is dropped")
		}
	})

	t.Run("DropTopicPrefix", func(t *testing.T) {
		transform := DropTopicPrefix("sensor/")
		result, cont := transform(originalMsg)

		if result != nil {
			t.Error("Expected message to be dropped, but got result")
		}
		if cont {
			t.Error("Expected continue to be false when message is dropped")
		}
	})

	t.Run("AddTopicPrefix", func(t *testing.T) {
		transform := AddTopicPrefix("processed/")
		result, cont := transform(originalMsg)

		if result == nil {
			t.Fatal("Expected transformed message, but got nil")
		}
		if !cont {
			t.Error("Expected continue to be true")
		}

		expectedTopic := "processed/sensor/temp1/data"
		if result.Topic != expectedTopic {
			t.Errorf("Expected topic %q, got %q", expectedTopic, result.Topic)
		}

		// Verify other fields are preserved
		if result.MsgType != originalMsg.MsgType {
			t.Error("MsgType should be preserved")
		}
		if result.Ctx != originalMsg.Ctx {
			t.Error("Context should be preserved")
		}
		// Note: We can't directly compare map payloads, so we'll check the content
		resultPayload, ok := result.Payload.(map[string]any)
		if !ok {
			t.Error("Payload should be a map")
		} else {
			originalPayload := originalMsg.Payload.(map[string]any)
			if resultPayload["temperature"] != originalPayload["temperature"] {
				t.Error("Payload content should be preserved")
			}
		}
	})

	t.Run("ModifyPayload", func(t *testing.T) {
		addTimestamp := func(ctx context.Context, payload any, fields map[string]string) any {
			return map[string]any{
				"original":  payload,
				"timestamp": time.Now().Unix(),
			}
		}

		transform := ModifyPayload(addTimestamp)
		result, cont := transform(originalMsg)

		if result == nil {
			t.Fatal("Expected transformed message, but got nil")
		}
		if !cont {
			t.Error("Expected continue to be true")
		}

		// Check that payload was transformed
		transformedPayload, ok := result.Payload.(map[string]any)
		if !ok {
			t.Fatal("Expected payload to be map[string]any")
		}

		// Compare the original payload content instead of direct comparison
		originalPayload := originalMsg.Payload.(map[string]any)
		preservedPayload := transformedPayload["original"].(map[string]any)
		if preservedPayload["temperature"] != originalPayload["temperature"] {
			t.Error("Original payload should be preserved in 'original' field")
		}

		if _, exists := transformedPayload["timestamp"]; !exists {
			t.Error("Expected timestamp field to be added")
		}
	})

	t.Run("ChainTransforms", func(t *testing.T) {
		// Create a chain that adds prefix and modifies payload
		addPrefix := AddTopicPrefix("processed/")
		addMetadata := ModifyPayload(func(ctx context.Context, payload any, fields map[string]string) any {
			return map[string]any{
				"data":      payload,
				"processed": true,
			}
		})

		chain := ChainTransforms(addPrefix, addMetadata)
		result, cont := chain(originalMsg)

		if result == nil {
			t.Fatal("Expected transformed message, but got nil")
		}
		if !cont {
			t.Error("Expected continue to be true")
		}

		// Check topic was prefixed
		expectedTopic := "processed/sensor/temp1/data"
		if result.Topic != expectedTopic {
			t.Errorf("Expected topic %q, got %q", expectedTopic, result.Topic)
		}

		// Check payload was modified
		transformedPayload, ok := result.Payload.(map[string]any)
		if !ok {
			t.Fatal("Expected payload to be map[string]any")
		}

		// Compare the original payload content instead of direct comparison
		originalPayload := originalMsg.Payload.(map[string]any)
		preservedPayload := transformedPayload["data"].(map[string]any)
		if preservedPayload["temperature"] != originalPayload["temperature"] {
			t.Error("Original payload should be preserved in 'data' field")
		}

		if processed, exists := transformedPayload["processed"]; !exists || processed != true {
			t.Error("Expected processed field to be true")
		}
	})

	t.Run("IfPattern", func(t *testing.T) {
		// Transform should only apply to matching pattern
		transform := IfPattern("sensor/+/data", AddTopicPrefix("matched/"))

		// Test matching topic
		result, cont := transform(originalMsg)
		if result == nil {
			t.Fatal("Expected transformed message for matching topic")
		}
		if result.Topic != "matched/sensor/temp1/data" {
			t.Errorf("Expected topic to be transformed for matching pattern")
		}
		if !cont {
			t.Error("Expected continue to be true")
		}

		// Test non-matching topic
		nonMatchingMsg := &bus.EventBusMessage{
			Ctx:     ctx,
			MsgType: bus.MessageTypeEvent,
			Topic:   "other/topic",
			Payload: "test",
		}

		result, cont = transform(nonMatchingMsg)
		if result == nil {
			t.Fatal("Expected message to pass through for non-matching topic")
		}
		if result.Topic != "other/topic" {
			t.Error("Expected topic to remain unchanged for non-matching pattern")
		}
		if !cont {
			t.Error("Expected continue to be true")
		}
	})

	t.Run("IfElsePattern", func(t *testing.T) {
		// Test different transforms based on pattern matching
		transform := IfElsePattern("sensor/+/data",
			AddTopicPrefix("sensor-processed/"),
			AddTopicPrefix("other-processed/"))

		// Test matching topic
		result, cont := transform(originalMsg)
		if result == nil {
			t.Fatal("Expected transformed message for matching topic")
		}
		if result.Topic != "sensor-processed/sensor/temp1/data" {
			t.Errorf("Expected topic to be transformed with sensor prefix")
		}
		if !cont {
			t.Error("Expected continue to be true")
		}

		// Test non-matching topic
		nonMatchingMsg := &bus.EventBusMessage{
			Ctx:     ctx,
			MsgType: bus.MessageTypeEvent,
			Topic:   "other/topic",
			Payload: "test",
		}

		result, cont = transform(nonMatchingMsg)
		if result == nil {
			t.Fatal("Expected transformed message for non-matching topic")
		}
		if result.Topic != "other-processed/other/topic" {
			t.Error("Expected topic to be transformed with other prefix")
		}
		if !cont {
			t.Error("Expected continue to be true")
		}
	})

	t.Run("TransformOnPattern", func(t *testing.T) {
		// Test field extraction from topic pattern
		enrichWithFields := func(ctx context.Context, payload any, fields map[string]string) any {
			deviceName := fields["device"]
			if deviceName == "" {
				t.Error("Expected device field to be extracted")
			}
			return map[string]any{
				"device":    deviceName,
				"data":      payload,
				"timestamp": time.Now().Unix(),
			}
		}

		transform := TransformOnPattern("sensor/+device/data", enrichWithFields)
		result, cont := transform(originalMsg)

		if result == nil {
			t.Fatal("Expected transformed message for matching pattern")
		}
		if !cont {
			t.Error("Expected continue to be true")
		}

		// Check that payload was transformed with extracted fields
		transformedPayload, ok := result.Payload.(map[string]any)
		if !ok {
			t.Fatal("Expected payload to be map[string]any")
		}

		if transformedPayload["device"] != "temp1" {
			t.Errorf("Expected device field to be 'temp1', got %v", transformedPayload["device"])
		}

		// Test non-matching pattern
		nonMatchingMsg := &bus.EventBusMessage{
			Ctx:     ctx,
			MsgType: bus.MessageTypeEvent,
			Topic:   "other/topic",
			Payload: "test",
		}

		result, cont = transform(nonMatchingMsg)
		if result == nil {
			t.Fatal("Expected message to pass through for non-matching topic")
		}
		if result.Topic != "other/topic" {
			t.Error("Expected topic to remain unchanged for non-matching pattern")
		}
		if result.Payload != "test" {
			t.Error("Expected payload to remain unchanged for non-matching pattern")
		}
		if !cont {
			t.Error("Expected continue to be true")
		}
	})
}

func TestRateLimitByTopic(t *testing.T) {
	ctx := context.Background()
	interval := 100 * time.Millisecond

	transform := RateLimitByTopic(interval)

	msg := &bus.EventBusMessage{
		Ctx:     ctx,
		MsgType: bus.MessageTypeEvent,
		Topic:   "test/topic",
		Payload: "test data",
	}

	// First message should pass through
	result, cont := transform(msg)
	if result == nil {
		t.Error("First message should pass through")
	}
	if !cont {
		t.Error("Expected continue to be true")
	}

	// Second message immediately should be dropped
	result, cont = transform(msg)
	if result != nil {
		t.Error("Second immediate message should be dropped")
	}
	if cont {
		t.Error("Expected continue to be false when message is dropped")
	}

	// Wait for rate limit to reset
	time.Sleep(interval + 10*time.Millisecond)

	// Third message should pass through after waiting
	result, cont = transform(msg)
	if result == nil {
		t.Error("Message should pass through after rate limit reset")
	}
	if !cont {
		t.Error("Expected continue to be true")
	}
}
