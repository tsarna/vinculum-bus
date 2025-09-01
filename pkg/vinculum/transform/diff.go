package transform

import (
	"context"

	"github.com/tsarna/go-structdiff"
)

// DiffTransform is a SimpleMessageTransformFunc that generates structural diffs between "old" and "new" values.
//
// This transform looks for message payloads that are maps containing both "old" and "new" keys.
// When found, it uses go-structdiff to compute the differences between the old and new values.
//
// Behavior depends on the payload structure:
//
// 1. Map with exactly "old" and "new" keys:
//   - Replaces the entire payload with the diff result
//
// 2. Map with "old" and "new" keys plus additional keys:
//   - Creates a copy of the map with "old" and "new" removed
//   - Adds a "delta" key containing the structural differences
//   - Preserves all other keys and their values
//
// Example 1 - Simple diff (exactly old/new):
//
//	Input:  map[string]any{"old": {...}, "new": {...}}
//	Output: map[string]any{"age": 31, "city": "Boston"}  // the diff
//
// Example 2 - Extended diff (old/new + metadata):
//
//	Input:  map[string]any{
//	    "old": map[string]any{"name": "John", "age": 30},
//	    "new": map[string]any{"name": "John", "age": 31},
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
func DiffTransform(ctx context.Context, payload any, fields map[string]string) any {
	// Check if payload is a map
	payloadMap, ok := payload.(map[string]any)
	if !ok {
		return payload // Not a map, pass through
	}

	// Check if map contains required "old" and "new" keys
	oldValue, hasOld := payloadMap["old"]
	newValue, hasNew := payloadMap["new"]
	if !hasOld || !hasNew {
		return payload // Missing required keys, pass through
	}

	// Determine if this is a simple diff (exactly old/new) or extended diff (old/new + metadata)
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
		// Extended case: preserve other keys and add "diff" key
		newPayload := make(map[string]any)

		// Copy all keys except "old" and "new"
		for key, value := range payloadMap {
			if key != "old" && key != "new" {
				newPayload[key] = value
			}
		}

		// Add the diff
		newPayload["delta"] = diff

		return newPayload
	}
}
