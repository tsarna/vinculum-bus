package transform

import (
	"context"

	"github.com/tsarna/go-structdiff"
)

// DiffTransform returns a SimpleMessageTransformFunc that generates structural diffs
// between values at the specified keys.
//
// This function allows you to specify which keys to compare, making it flexible
// for different payload structures.
//
// Parameters:
//   - oldKey: The key containing the "before" value
//   - newKey: The key containing the "after" value
//
// Behavior depends on the payload structure:
//
// 1. Map with exactly the two specified keys:
//   - Replaces the entire payload with the diff result
//
// 2. Map with the two specified keys plus additional keys:
//   - Creates a copy of the map with the specified keys removed
//   - Adds a "delta" key containing the structural differences
//   - Preserves all other keys and their values
//
// Example usage:
//
//	// Create a transform that diffs "old" and "new" keys (classic usage)
//	oldNewDiff := DiffTransform("old", "new")
//
//	// Create a transform that diffs "before" and "after" keys
//	beforeAfterDiff := DiffTransform("before", "after")
//
//	// Use in a transform pipeline
//	transforms := []MessageTransformFunc{
//	    TransformOnPattern("audit/+/changes", beforeAfterDiff),
//	}
//
// Example 1 - Simple diff (exactly two keys):
//
//	Input:  map[string]any{"before": {...}, "after": {...}}
//	Output: map[string]any{"age": 31, "city": "Boston"}  // the diff
//
// Example 2 - Extended diff (two keys + metadata):
//
//	Input:  map[string]any{
//	    "before": map[string]any{"name": "John", "age": 30},
//	    "after": map[string]any{"name": "John", "age": 31},
//	    "timestamp": "2024-01-01T00:00:00Z",
//	    "user_id": "123",
//	}
//	Output: map[string]any{
//	    "delta": map[string]any{"age": 31},  // the diff
//	    "timestamp": "2024-01-01T00:00:00Z",
//	    "user_id": "123",
//	}
//
// If the payload doesn't match the expected structure (not a map, missing keys, etc.),
// the payload is passed through unchanged.
//
// Use cases:
//   - Change tracking and audit logs
//   - Efficient delta synchronization
//   - Event sourcing with state changes
//   - Reducing payload size for incremental updates
func DiffTransform(oldKey, newKey string) SimpleMessageTransformFunc {
	return func(ctx context.Context, payload any, fields map[string]string) any {
		// Check if payload is a map
		payloadMap, ok := payload.(map[string]any)
		if !ok {
			return payload // Not a map, pass through
		}

		// Check if map contains required keys
		oldValue, hasOld := payloadMap[oldKey]
		newValue, hasNew := payloadMap[newKey]
		if !hasOld || !hasNew {
			return payload // Missing required keys, pass through
		}

		// Determine if this is a simple diff (exactly the two keys) or extended diff (keys + metadata)
		isSimpleDiff := len(payloadMap) == 2

		diff, err := structdiff.Diff(oldValue, newValue)
		if err != nil {
			// If diff computation failed, pass through original payload
			return payload
		}

		if isSimpleDiff {
			// Simple case: replace entire payload with the diff
			return diff
		} else {
			// Extended case: preserve other keys and add "delta" key
			newPayload := make(map[string]any)

			// Copy all keys except the specified old and new keys
			for key, value := range payloadMap {
				if key != oldKey && key != newKey {
					newPayload[key] = value
				}
			}

			// Add the diff
			newPayload["delta"] = diff

			return newPayload
		}
	}
}
