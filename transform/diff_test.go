package transform

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tsarna/vinculum-bus"
)

func TestDiffTransform(t *testing.T) {
	// Test DiffTransform with classic "old" and "new" keys
	transform := ModifyPayload(DiffTransform("old", "new"))

	t.Run("simple diff with exactly old and new keys", func(t *testing.T) {
		oldValue := map[string]any{
			"name": "John",
			"age":  30,
			"city": "NYC",
		}
		newValue := map[string]any{
			"name": "John",
			"age":  31,
			"city": "Boston",
		}

		msg := &bus.EventBusMessage{
			Topic: "user/update",
			Payload: map[string]any{
				"old": oldValue,
				"new": newValue,
			},
		}

		result, cont := transform(msg)
		assert.True(t, cont)
		assert.NotNil(t, result)
		assert.Equal(t, "user/update", result.Topic)

		// Simple case: entire payload should be replaced with the diff
		diffMap, ok := result.Payload.(map[string]any)
		assert.True(t, ok)
		assert.Equal(t, 31, diffMap["age"])
		assert.Equal(t, "Boston", diffMap["city"])
		assert.NotContains(t, diffMap, "name") // name didn't change

		// Should not contain old, new, or diff keys since this is the simple case
		assert.NotContains(t, diffMap, "old")
		assert.NotContains(t, diffMap, "new")
		assert.NotContains(t, diffMap, "delta")
	})

	t.Run("valid diff with nested structures", func(t *testing.T) {
		oldValue := map[string]any{
			"user": map[string]any{
				"name": "John",
				"profile": map[string]any{
					"age":  30,
					"city": "NYC",
				},
			},
			"status": "active",
		}
		newValue := map[string]any{
			"user": map[string]any{
				"name": "John",
				"profile": map[string]any{
					"age":  31,
					"city": "NYC",
				},
			},
			"status": "inactive",
		}

		msg := &bus.EventBusMessage{
			Topic: "user/complex-update",
			Payload: map[string]any{
				"old": oldValue,
				"new": newValue,
			},
		}

		result, cont := transform(msg)
		assert.True(t, cont)
		assert.NotNil(t, result)

		// The diff should contain changes
		diffMap, ok := result.Payload.(map[string]any)
		assert.True(t, ok)
		assert.Contains(t, diffMap, "status")
		assert.Equal(t, "inactive", diffMap["status"])
		assert.Contains(t, diffMap, "user")
	})

	t.Run("no changes results in empty diff", func(t *testing.T) {
		sameValue := map[string]any{
			"name": "John",
			"age":  30,
			"city": "NYC",
		}

		msg := &bus.EventBusMessage{
			Topic: "user/no-change",
			Payload: map[string]any{
				"old": sameValue,
				"new": sameValue,
			},
		}

		result, cont := transform(msg)
		assert.True(t, cont)
		assert.NotNil(t, result)

		// The diff should be empty or minimal
		diffMap, ok := result.Payload.(map[string]any)
		assert.True(t, ok)
		// Empty diff should have no keys or only internal metadata
		assert.LessOrEqual(t, len(diffMap), 0)
	})

	t.Run("pass through when payload is not a map", func(t *testing.T) {
		msg := &bus.EventBusMessage{
			Topic:   "test/topic",
			Payload: "not a map",
		}

		result, cont := transform(msg)
		assert.True(t, cont)
		assert.Equal(t, msg, result) // Should be unchanged
		assert.Equal(t, "not a map", result.Payload)
	})

	t.Run("extended diff with additional metadata", func(t *testing.T) {
		msg := &bus.EventBusMessage{
			Topic: "user/audit",
			Payload: map[string]any{
				"old": map[string]any{
					"name":   "John",
					"age":    30,
					"status": "active",
				},
				"new": map[string]any{
					"name":   "John",
					"age":    31,
					"status": "inactive",
				},
				"timestamp": "2024-01-01T00:00:00Z",
				"user_id":   "123",
				"action":    "profile_update",
			},
		}

		result, cont := transform(msg)
		assert.True(t, cont)
		assert.NotNil(t, result)
		assert.Equal(t, "user/audit", result.Topic)

		// Should preserve metadata and add diff
		resultMap, ok := result.Payload.(map[string]any)
		assert.True(t, ok)

		// Check that metadata is preserved
		assert.Equal(t, "2024-01-01T00:00:00Z", resultMap["timestamp"])
		assert.Equal(t, "123", resultMap["user_id"])
		assert.Equal(t, "profile_update", resultMap["action"])

		// Check that old and new are removed
		assert.NotContains(t, resultMap, "old")
		assert.NotContains(t, resultMap, "new")

		// Check that diff is present and contains changes
		diff, exists := resultMap["delta"]
		assert.True(t, exists)
		diffMap, ok := diff.(map[string]any)
		assert.True(t, ok)
		assert.Equal(t, 31, diffMap["age"])
		assert.Equal(t, "inactive", diffMap["status"])
		assert.NotContains(t, diffMap, "name") // name didn't change
	})

	t.Run("pass through when missing required keys", func(t *testing.T) {
		testCases := []map[string]any{
			{
				"old":   "value1",
				"other": "value2",
			},
			{
				"new":   "value1",
				"other": "value2",
			},
			{
				"wrong": "value1",
				"keys":  "value2",
			},
		}

		for i, payload := range testCases {
			msg := &bus.EventBusMessage{
				Topic:   "test/topic",
				Payload: payload,
			}

			result, cont := transform(msg)
			assert.True(t, cont, "test case %d", i)
			assert.Equal(t, msg, result, "test case %d should be unchanged", i)
		}
	})

	t.Run("pass through when only one key present", func(t *testing.T) {
		msg := &bus.EventBusMessage{
			Topic: "test/topic",
			Payload: map[string]any{
				"old": "value1",
			},
		}

		result, cont := transform(msg)
		assert.True(t, cont)
		assert.Equal(t, msg, result) // Should be unchanged
	})

	t.Run("handle different data types in old/new", func(t *testing.T) {
		msg := &bus.EventBusMessage{
			Topic: "test/mixed-types",
			Payload: map[string]any{
				"old": map[string]any{"count": 5, "enabled": true},
				"new": map[string]any{"count": 10, "enabled": false, "name": "test"},
			},
		}

		result, cont := transform(msg)
		assert.True(t, cont)
		assert.NotNil(t, result)

		// Should successfully create a diff
		diffMap, ok := result.Payload.(map[string]any)
		assert.True(t, ok)
		assert.NotEmpty(t, diffMap)
	})

	t.Run("handle arrays in old/new values - now works without panic", func(t *testing.T) {
		msg := &bus.EventBusMessage{
			Topic: "test/arrays",
			Payload: map[string]any{
				"old": map[string]any{"items": []string{"a", "b", "c"}},
				"new": map[string]any{"items": []string{"a", "b", "d"}},
			},
		}

		result, cont := transform(msg)
		assert.True(t, cont)
		assert.NotNil(t, result)

		diffMap, ok := result.Payload.(map[string]any)
		assert.True(t, ok)
		assert.Contains(t, diffMap, "items")
		assert.Equal(t, []string{"a", "b", "d"}, diffMap["items"])
	})

	t.Run("extended diff with no changes", func(t *testing.T) {
		sameValue := map[string]any{"name": "John", "age": 30}

		msg := &bus.EventBusMessage{
			Topic: "user/no-change",
			Payload: map[string]any{
				"old":       sameValue,
				"new":       sameValue,
				"timestamp": "2024-01-01T00:00:00Z",
				"action":    "attempted_update",
			},
		}

		result, cont := transform(msg)
		assert.True(t, cont)
		assert.NotNil(t, result)

		resultMap, ok := result.Payload.(map[string]any)
		assert.True(t, ok)

		// Should preserve metadata
		assert.Equal(t, "2024-01-01T00:00:00Z", resultMap["timestamp"])
		assert.Equal(t, "attempted_update", resultMap["action"])

		// Should have empty or minimal diff
		diff, exists := resultMap["delta"]
		assert.True(t, exists)
		if diff != nil {
			diffMap, ok := diff.(map[string]any)
			if ok {
				assert.LessOrEqual(t, len(diffMap), 0) // Should be empty for no changes
			}
		}
	})

	t.Run("extended diff preserves complex metadata", func(t *testing.T) {
		msg := &bus.EventBusMessage{
			Topic: "complex/audit",
			Payload: map[string]any{
				"old": map[string]any{"value": 1},
				"new": map[string]any{"value": 2},
				"metadata": map[string]any{
					"source":  "api",
					"version": "1.0",
					"nested": map[string]any{
						"level": 2,
						"tags":  []string{"audit", "change"},
					},
				},
				"correlation_id": "abc-123",
				"context":        []any{"item1", "item2", 42},
			},
		}

		result, cont := transform(msg)
		assert.True(t, cont)
		assert.NotNil(t, result)

		resultMap, ok := result.Payload.(map[string]any)
		assert.True(t, ok)

		// Should preserve all complex metadata exactly
		assert.Equal(t, "abc-123", resultMap["correlation_id"])

		metadata, ok := resultMap["metadata"].(map[string]any)
		assert.True(t, ok)
		assert.Equal(t, "api", metadata["source"])
		assert.Equal(t, "1.0", metadata["version"])

		nested, ok := metadata["nested"].(map[string]any)
		assert.True(t, ok)
		assert.Equal(t, 2, nested["level"])

		context, ok := resultMap["context"].([]any)
		assert.True(t, ok)
		assert.Equal(t, []any{"item1", "item2", 42}, context)

		// Should have the diff
		diff, exists := resultMap["delta"]
		assert.True(t, exists)
		diffMap, ok := diff.(map[string]any)
		assert.True(t, ok)
		assert.Equal(t, 2, diffMap["value"])
	})

	t.Run("preserve message context and topic", func(t *testing.T) {
		msg := &bus.EventBusMessage{
			Topic: "preserve/test",
			Payload: map[string]any{
				"old": map[string]any{"value": 1},
				"new": map[string]any{"value": 2},
			},
		}

		result, cont := transform(msg)
		assert.True(t, cont)
		assert.NotNil(t, result)
		assert.Equal(t, "preserve/test", result.Topic)
		assert.NotEqual(t, msg.Payload, result.Payload) // Payload should be different (the diff)
	})
}

func TestDiffTransformWithCustomKeys(t *testing.T) {
	t.Run("custom keys - before and after", func(t *testing.T) {
		transform := ModifyPayload(DiffTransform("before", "after"))

		oldValue := map[string]any{
			"name": "John",
			"age":  30,
			"city": "NYC",
		}
		newValue := map[string]any{
			"name": "John",
			"age":  31,
			"city": "Boston",
		}

		msg := &bus.EventBusMessage{
			Topic: "user/update",
			Payload: map[string]any{
				"before": oldValue,
				"after":  newValue,
			},
		}

		result, cont := transform(msg)
		assert.True(t, cont)
		assert.NotNil(t, result)
		assert.Equal(t, "user/update", result.Topic)

		// Simple case: entire payload should be replaced with the diff
		diffMap, ok := result.Payload.(map[string]any)
		assert.True(t, ok)
		assert.Equal(t, 31, diffMap["age"])
		assert.Equal(t, "Boston", diffMap["city"])
		assert.NotContains(t, diffMap, "name") // name didn't change

		// Should not contain before, after, or delta keys since this is the simple case
		assert.NotContains(t, diffMap, "before")
		assert.NotContains(t, diffMap, "after")
		assert.NotContains(t, diffMap, "delta")
	})

	t.Run("custom keys - previous and current", func(t *testing.T) {
		transform := ModifyPayload(DiffTransform("previous", "current"))

		msg := &bus.EventBusMessage{
			Topic: "state/change",
			Payload: map[string]any{
				"previous": map[string]any{"status": "pending", "count": 5},
				"current":  map[string]any{"status": "active", "count": 10},
			},
		}

		result, cont := transform(msg)
		assert.True(t, cont)
		assert.NotNil(t, result)

		diffMap, ok := result.Payload.(map[string]any)
		assert.True(t, ok)
		assert.Equal(t, "active", diffMap["status"])
		assert.Equal(t, 10, diffMap["count"])
	})

	t.Run("custom keys with extended diff", func(t *testing.T) {
		transform := ModifyPayload(DiffTransform("source", "target"))

		msg := &bus.EventBusMessage{
			Topic: "migration/data",
			Payload: map[string]any{
				"source": map[string]any{
					"version": "1.0",
					"config":  map[string]any{"debug": false, "timeout": 30},
				},
				"target": map[string]any{
					"version": "2.0",
					"config":  map[string]any{"debug": true, "timeout": 60},
				},
				"migration_id": "mig-123",
				"timestamp":    "2024-01-01T00:00:00Z",
				"environment":  "production",
			},
		}

		result, cont := transform(msg)
		assert.True(t, cont)
		assert.NotNil(t, result)

		resultMap, ok := result.Payload.(map[string]any)
		assert.True(t, ok)

		// Check that metadata is preserved
		assert.Equal(t, "mig-123", resultMap["migration_id"])
		assert.Equal(t, "2024-01-01T00:00:00Z", resultMap["timestamp"])
		assert.Equal(t, "production", resultMap["environment"])

		// Check that source and target are removed
		assert.NotContains(t, resultMap, "source")
		assert.NotContains(t, resultMap, "target")

		// Check that diff is present and contains changes
		diff, exists := resultMap["delta"]
		assert.True(t, exists)
		diffMap, ok := diff.(map[string]any)
		assert.True(t, ok)
		assert.Equal(t, "2.0", diffMap["version"])
		assert.Contains(t, diffMap, "config")
	})

	t.Run("custom keys - pass through when missing keys", func(t *testing.T) {
		transform := ModifyPayload(DiffTransform("from", "to"))

		testCases := []map[string]any{
			{
				"from":  "value1",
				"other": "value2",
			},
			{
				"to":    "value1",
				"other": "value2",
			},
			{
				"wrong": "value1",
				"keys":  "value2",
			},
			{
				"old": "value1", // Has old/new but we're looking for from/to
				"new": "value2",
			},
		}

		for i, payload := range testCases {
			msg := &bus.EventBusMessage{
				Topic:   "test/topic",
				Payload: payload,
			}

			result, cont := transform(msg)
			assert.True(t, cont, "test case %d", i)
			assert.Equal(t, msg, result, "test case %d should be unchanged", i)
		}
	})

	t.Run("classic old/new keys work correctly", func(t *testing.T) {
		transform := ModifyPayload(DiffTransform("old", "new"))

		msg := &bus.EventBusMessage{
			Topic: "compatibility/test",
			Payload: map[string]any{
				"old":      map[string]any{"value": 1, "name": "test"},
				"new":      map[string]any{"value": 2, "name": "test"},
				"metadata": map[string]any{"source": "api"},
			},
		}

		result, cont := transform(msg)
		assert.True(t, cont)
		assert.NotNil(t, result)

		resultMap, ok := result.Payload.(map[string]any)
		assert.True(t, ok)

		// Should preserve metadata
		assert.Equal(t, map[string]any{"source": "api"}, resultMap["metadata"])

		// Should have delta with the changes
		delta, exists := resultMap["delta"]
		assert.True(t, exists)
		deltaMap, ok := delta.(map[string]any)
		assert.True(t, ok)
		assert.Equal(t, 2, deltaMap["value"])
		assert.NotContains(t, deltaMap, "name") // name didn't change
	})

	t.Run("custom keys - edge case with same key names", func(t *testing.T) {
		// Test edge case where someone uses the same key for both old and new
		transform := ModifyPayload(DiffTransform("data", "data"))

		msg := &bus.EventBusMessage{
			Topic: "edge/case",
			Payload: map[string]any{
				"data": map[string]any{"value": 42},
			},
		}

		result, cont := transform(msg)
		assert.True(t, cont)

		// When using the same key for both old and new, it will diff the value with itself
		// This results in an empty diff (no changes) in the delta key
		resultMap, ok := result.Payload.(map[string]any)
		assert.True(t, ok)

		// Should have a delta key with empty diff (since diffing same value with itself)
		delta, exists := resultMap["delta"]
		assert.True(t, exists)
		deltaMap, ok := delta.(map[string]any)
		assert.True(t, ok)
		assert.Empty(t, deltaMap) // Should be empty since no changes
	})

	t.Run("custom keys - non-string keys", func(t *testing.T) {
		transform := ModifyPayload(DiffTransform("v1", "v2"))

		msg := &bus.EventBusMessage{
			Topic: "version/diff",
			Payload: map[string]any{
				"v1": 100,
				"v2": 200,
			},
		}

		result, cont := transform(msg)
		assert.True(t, cont)
		assert.NotNil(t, result)

		// Should create a diff even for non-map values
		assert.NotEqual(t, msg.Payload, result.Payload)
	})

	t.Run("custom keys - empty string keys", func(t *testing.T) {
		transform := ModifyPayload(DiffTransform("", "new"))

		msg := &bus.EventBusMessage{
			Topic: "empty/key",
			Payload: map[string]any{
				"":    "old_value",
				"new": "new_value",
			},
		}

		result, cont := transform(msg)
		assert.True(t, cont)
		assert.NotNil(t, result)
		// Should work with empty string as key
		assert.NotEqual(t, msg.Payload, result.Payload)
	})
}
