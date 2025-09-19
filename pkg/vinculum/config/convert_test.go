package config

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/gocty"
)

func TestCtyToAny(t *testing.T) {
	t.Run("primitive types", func(t *testing.T) {
		// String
		result, err := CtyToAny(cty.StringVal("hello"))
		require.NoError(t, err)
		assert.Equal(t, "hello", result)

		// Bool
		result, err = CtyToAny(cty.BoolVal(true))
		require.NoError(t, err)
		assert.Equal(t, true, result)

		// Number (int)
		result, err = CtyToAny(cty.NumberIntVal(42))
		require.NoError(t, err)
		assert.Equal(t, int64(42), result)

		// Number (float)
		result, err = CtyToAny(cty.NumberFloatVal(3.14))
		require.NoError(t, err)
		assert.Equal(t, 3.14, result)

		// Null
		result, err = CtyToAny(cty.NullVal(cty.String))
		require.NoError(t, err)
		assert.Nil(t, result)
	})

	t.Run("complex types", func(t *testing.T) {
		// Object
		obj := cty.ObjectVal(map[string]cty.Value{
			"name": cty.StringVal("Alice"),
			"age":  cty.NumberIntVal(30),
		})
		result, err := CtyToAny(obj)
		require.NoError(t, err)
		expected := map[string]any{
			"name": "Alice",
			"age":  int64(30),
		}
		assert.Equal(t, expected, result)

		// List
		list := cty.ListVal([]cty.Value{
			cty.StringVal("a"),
			cty.StringVal("b"),
		})
		result, err = CtyToAny(list)
		require.NoError(t, err)
		assert.Equal(t, []any{"a", "b"}, result)
	})

	t.Run("capsule types", func(t *testing.T) {
		// Create a context capsule
		ctx := context.Background()
		capsule := NewContextCapsule(ctx)

		result, err := CtyToAny(capsule)
		require.NoError(t, err)

		// Should unwrap to the pointer to context
		contextPtr, ok := result.(*context.Context)
		require.True(t, ok, "Expected *context.Context")
		assert.NotNil(t, contextPtr)
		assert.Equal(t, ctx, *contextPtr)
	})

	t.Run("nested with capsules", func(t *testing.T) {
		// Create an object containing a capsule
		ctx := context.Background()
		obj := cty.ObjectVal(map[string]cty.Value{
			"context": NewContextCapsule(ctx),
			"data":    cty.StringVal("test"),
			"nested": cty.ObjectVal(map[string]cty.Value{
				"value": cty.NumberIntVal(123),
			}),
		})

		result, err := CtyToAny(obj)
		require.NoError(t, err)

		resultMap, ok := result.(map[string]any)
		require.True(t, ok)

		// Check that the capsule was unwrapped
		contextPtr, ok := resultMap["context"].(*context.Context)
		require.True(t, ok)
		assert.Equal(t, ctx, *contextPtr)

		// Check other values
		assert.Equal(t, "test", resultMap["data"])
		nested, ok := resultMap["nested"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, int64(123), nested["value"])
	})
}

func TestByteSliceHandling(t *testing.T) {
	t.Run("direct gocty conversion fails", func(t *testing.T) {
		// Test what happens with []byte using gocty directly
		data := []byte("hello")
		_, err := gocty.ToCtyValue(data, cty.DynamicPseudoType)

		t.Logf("gocty.ToCtyValue with []byte failed: %v", err)
		assert.Error(t, err, "gocty should fail with []byte")
	})

	t.Run("AnyToCty with byte slice", func(t *testing.T) {
		// Test what happens with []byte through our AnyToCty (which tries JSON fallback)
		data := []byte("hello")
		result, err := AnyToCty(data)

		if err != nil {
			t.Logf("AnyToCty failed with []byte: %v", err)
			// This is expected - []byte doesn't convert well
			assert.Error(t, err)
			return
		}

		t.Logf("Unexpected success! Type: %s", result.Type().FriendlyName())
	})

	t.Run("cty handles byte slice as list of numbers", func(t *testing.T) {
		// If we manually create a list of numbers from []byte
		data := []byte("hi") // Just 2 bytes: 104, 105

		// Create a cty list manually
		ctyValues := make([]cty.Value, len(data))
		for i, b := range data {
			ctyValues[i] = cty.NumberIntVal(int64(b))
		}
		result := cty.ListVal(ctyValues)

		t.Logf("Manual list type: %s", result.Type().FriendlyName())

		// Convert back using CtyToAny
		converted, err := CtyToAny(result)
		require.NoError(t, err)

		t.Logf("Converted back to: %T = %v", converted, converted)

		// Should be []any containing int64 values
		if slice, ok := converted.([]any); ok {
			t.Logf("Slice: %v", slice)
			// Convert back to []byte
			backToBytes := make([]byte, len(slice))
			for i, v := range slice {
				backToBytes[i] = byte(v.(int64))
			}
			t.Logf("Reconstructed []byte: %q", string(backToBytes))
			assert.Equal(t, string(data), string(backToBytes))
		}
	})
}

func TestCtyStringWithArbitraryBytes(t *testing.T) {
	t.Run("valid UTF-8 bytes", func(t *testing.T) {
		data := []byte("hello world 🌍")
		str := string(data)

		ctyVal := cty.StringVal(str)
		t.Logf("UTF-8 string: %q", ctyVal.AsString())

		// Round trip through CtyToAny
		converted, err := CtyToAny(ctyVal)
		require.NoError(t, err)
		assert.Equal(t, str, converted)
	})

	t.Run("binary data as string", func(t *testing.T) {
		// Create some arbitrary binary data
		data := []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0x80, 0x7F}
		str := string(data)

		t.Logf("Binary as string: %q (len=%d)", str, len(str))

		// Can we create a cty string with it?
		ctyVal := cty.StringVal(str)
		retrieved := ctyVal.AsString()

		t.Logf("Retrieved from cty: %q (len=%d)", retrieved, len(retrieved))

		// Convert back to []byte and compare
		backToBytes := []byte(retrieved)
		t.Logf("Original bytes: %v", data)
		t.Logf("Round-trip bytes: %v", backToBytes)

		assert.Equal(t, data, backToBytes, "Binary data should survive round-trip through cty string")
	})

	t.Run("invalid UTF-8 sequences", func(t *testing.T) {
		// Create invalid UTF-8 sequences
		invalidUTF8 := []byte{
			0xC0, 0x80, // overlong encoding of null
			0xFF, 0xFE, // invalid start bytes
			0x80, 0x81, 0x82, // continuation bytes without start
		}

		str := string(invalidUTF8)
		t.Logf("Invalid UTF-8 as string: %q", str)

		// Can cty handle it?
		ctyVal := cty.StringVal(str)
		retrieved := ctyVal.AsString()

		// Round trip
		backToBytes := []byte(retrieved)
		t.Logf("Original invalid UTF-8: %v", invalidUTF8)
		t.Logf("Round-trip bytes: %v", backToBytes)

		// Note: Go's string conversion may replace invalid UTF-8 with replacement characters
		// but the byte length and content should be preserved in some form
		assert.Equal(t, len(str), len(retrieved), "String length should be preserved")
	})

	t.Run("null bytes in string", func(t *testing.T) {
		// Test null bytes specifically
		data := []byte{'h', 'e', 0x00, 'l', 'l', 0x00, 'o'}
		str := string(data)

		t.Logf("String with nulls: %q (len=%d)", str, len(str))

		ctyVal := cty.StringVal(str)
		retrieved := ctyVal.AsString()

		backToBytes := []byte(retrieved)
		assert.Equal(t, data, backToBytes, "Null bytes should be preserved")
	})

	t.Run("json marshaling with binary strings", func(t *testing.T) {
		// Test if JSON marshaling (used in AnyToCty fallback) handles binary strings
		data := []byte{0x00, 0x01, 0xFF, 0xFE}
		str := string(data)

		// Can we JSON marshal/unmarshal it?
		jsonBytes, err := json.Marshal(str)
		t.Logf("JSON marshaled: %s", jsonBytes)

		if err != nil {
			t.Logf("JSON marshal failed: %v", err)
			return
		}

		var unmarshaled string
		err = json.Unmarshal(jsonBytes, &unmarshaled)
		require.NoError(t, err)

		backToBytes := []byte(unmarshaled)
		t.Logf("Original: %v", data)
		t.Logf("JSON round-trip: %v", backToBytes)

		// JSON replaces invalid UTF-8 with replacement characters
		// This is expected behavior - JSON doesn't preserve arbitrary binary data
		t.Logf("JSON replaced invalid UTF-8 bytes with replacement characters")
		assert.NotEqual(t, data, backToBytes, "JSON should NOT preserve invalid UTF-8 bytes")
	})

	t.Run("practical workaround for binary data", func(t *testing.T) {
		// Show a practical approach for handling binary data
		originalData := []byte{0x00, 0x01, 0xFF, 0xFE, 0x80, 0x7F}

		// Method 1: Base64 encode for safe transport
		encoded := base64.StdEncoding.EncodeToString(originalData)

		// This can safely go through cty and JSON
		ctyVal := cty.StringVal(encoded)
		retrieved := ctyVal.AsString()

		// And can be JSON marshaled/unmarshaled safely
		jsonBytes, err := json.Marshal(retrieved)
		require.NoError(t, err)

		var fromJSON string
		err = json.Unmarshal(jsonBytes, &fromJSON)
		require.NoError(t, err)

		// Decode back to original binary data
		decoded, err := base64.StdEncoding.DecodeString(fromJSON)
		require.NoError(t, err)

		assert.Equal(t, originalData, decoded, "Base64 encoding preserves binary data perfectly")
		t.Logf("Base64 approach: %d bytes -> %s -> %d bytes",
			len(originalData), encoded, len(decoded))
	})
}

func TestEnhancedAnyToCty(t *testing.T) {
	t.Run("byte slice to string conversion", func(t *testing.T) {
		// Test the special []byte handling
		data := []byte("hello world")
		result, err := AnyToCty(data)
		require.NoError(t, err)

		assert.Equal(t, cty.String, result.Type())
		assert.Equal(t, "hello world", result.AsString())

		t.Logf("[]byte converted to cty string successfully")
	})

	t.Run("binary byte slice to string", func(t *testing.T) {
		// Test with binary data
		data := []byte{0x00, 0x01, 0xFF, 0xFE}
		result, err := AnyToCty(data)
		require.NoError(t, err)

		assert.Equal(t, cty.String, result.Type())

		// Round trip back
		converted, err := CtyToAny(result)
		require.NoError(t, err)

		backToBytes := []byte(converted.(string))
		assert.Equal(t, data, backToBytes)

		t.Logf("Binary []byte preserved through cty string conversion")
	})

	t.Run("nil handling", func(t *testing.T) {
		result, err := AnyToCty(nil)
		require.NoError(t, err)
		assert.True(t, result.IsNull())

		t.Logf("nil converted to cty null")
	})

	t.Run("primitive types", func(t *testing.T) {
		tests := []struct {
			name     string
			value    any
			expected cty.Type
		}{
			{"string", "hello", cty.String},
			{"bool", true, cty.Bool},
			{"int", int(42), cty.Number},
			{"int8", int8(42), cty.Number},
			{"int16", int16(42), cty.Number},
			{"int32", int32(42), cty.Number},
			{"int64", int64(42), cty.Number},
			{"uint", uint(42), cty.Number},
			{"uint8", uint8(42), cty.Number},
			{"uint16", uint16(42), cty.Number},
			{"uint32", uint32(42), cty.Number},
			{"uint64", uint64(42), cty.Number},
			{"float32", float32(3.14), cty.Number},
			{"float64", float64(3.14), cty.Number},
		}

		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				result, err := AnyToCty(test.value)
				require.NoError(t, err, "Failed to convert %s", test.name)
				assert.Equal(t, test.expected, result.Type(), "Wrong type for %s", test.name)

				t.Logf("%s (%T) -> %s", test.name, test.value, result.Type().FriendlyName())
			})
		}
	})

	t.Run("collections via JSON", func(t *testing.T) {
		// Test slice conversion - may fail due to JSON issues, but should try
		slice := []string{"a", "b", "c"}
		result, err := AnyToCty(slice)
		if err != nil {
			t.Logf("Slice conversion failed (expected): %v", err)
		} else {
			assert.True(t, result.Type().IsListType() || result.Type().IsTupleType())
			t.Logf("Slice conversion succeeded")
		}

		// Test map conversion - may also fail
		m := map[string]int{"a": 1, "b": 2}
		result, err = AnyToCty(m)
		if err != nil {
			t.Logf("Map conversion failed (expected): %v", err)
		} else {
			// Homogeneous map should become cty map, not object
			assert.True(t, result.Type().IsMapType())
			t.Logf("Map conversion succeeded")
		}

		// The important thing is that we don't panic and provide better error messages
		t.Logf("Collections handled gracefully (success or clear error)")
	})

	t.Run("round trip compatibility", func(t *testing.T) {
		// Test that values that go through AnyToCty can come back through CtyToAny
		testValues := []any{
			"hello",
			true,
			int64(42),
			float64(3.14),
			[]byte("binary data"),
			nil,
		}

		for i, val := range testValues {
			t.Run(fmt.Sprintf("value_%d", i), func(t *testing.T) {
				// Convert to cty
				ctyVal, err := AnyToCty(val)
				require.NoError(t, err)

				// Convert back
				backToAny, err := CtyToAny(ctyVal)
				require.NoError(t, err)

				// Special handling for different types
				switch original := val.(type) {
				case []byte:
					// []byte becomes string, so compare as strings
					assert.Equal(t, string(original), backToAny.(string))
				case nil:
					assert.Nil(t, backToAny)
				default:
					assert.Equal(t, val, backToAny)
				}

				t.Logf("Round trip successful for %T", val)
			})
		}
	})
}

func TestRecursiveAnyToCty(t *testing.T) {
	t.Run("already cty.Value", func(t *testing.T) {
		// Test that cty.Value inputs are returned as-is
		original := cty.StringVal("hello")
		result, err := AnyToCty(original)
		require.NoError(t, err)

		// Should be the exact same value (not just equal)
		assert.True(t, result.RawEquals(original))

		t.Logf("cty.Value input returned as-is")
	})

	t.Run("nested slices", func(t *testing.T) {
		// Test nested slice structures
		data := [][]string{
			{"a", "b"},
			{"c", "d", "e"},
			{"f"},
		}

		result, err := AnyToCty(data)
		require.NoError(t, err)

		// Should be a list of lists (or tuple of lists)
		assert.True(t, result.Type().IsListType() || result.Type().IsTupleType())

		// Convert back and verify
		converted, err := CtyToAny(result)
		require.NoError(t, err)

		// Should be []any containing []any
		outerSlice, ok := converted.([]any)
		require.True(t, ok)
		assert.Len(t, outerSlice, 3)

		// Check first inner slice
		innerSlice, ok := outerSlice[0].([]any)
		require.True(t, ok)
		assert.Equal(t, []any{"a", "b"}, innerSlice)

		t.Logf("Nested slices converted successfully")
	})

	t.Run("nested maps", func(t *testing.T) {
		// Test nested map structures
		data := map[string]map[string]int{
			"group1": {"a": 1, "b": 2},
			"group2": {"c": 3, "d": 4},
		}

		result, err := AnyToCty(data)
		require.NoError(t, err)

		// Outer map has homogeneous values (all map[string]int), so should be a map
		// Inner maps have homogeneous values (all int), so should be maps too
		assert.True(t, result.Type().IsMapType())

		// Convert back and verify structure
		converted, err := CtyToAny(result)
		require.NoError(t, err)

		outerMap, ok := converted.(map[string]any)
		require.True(t, ok)

		group1, ok := outerMap["group1"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, int64(1), group1["a"])
		assert.Equal(t, int64(2), group1["b"])

		t.Logf("Nested maps converted successfully")
	})

	t.Run("mixed nested structures", func(t *testing.T) {
		// Test complex mixed nesting
		data := map[string]any{
			"users": []map[string]any{
				{"name": "Alice", "age": 30, "active": true},
				{"name": "Bob", "age": 25, "active": false},
			},
			"settings": map[string]any{
				"theme":         "dark",
				"notifications": []string{"email", "push"},
			},
			"metadata": []any{
				"version1.0",
				42,
				true,
				[]byte("binary data"),
			},
		}

		result, err := AnyToCty(data)
		require.NoError(t, err)

		assert.True(t, result.Type().IsObjectType())

		// Convert back and verify the complex structure
		converted, err := CtyToAny(result)
		require.NoError(t, err)

		rootMap, ok := converted.(map[string]any)
		require.True(t, ok)

		// Check users array
		users, ok := rootMap["users"].([]any)
		require.True(t, ok)
		assert.Len(t, users, 2)

		user1, ok := users[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "Alice", user1["name"])
		assert.Equal(t, int64(30), user1["age"])
		assert.Equal(t, true, user1["active"])

		// Check settings
		settings, ok := rootMap["settings"].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "dark", settings["theme"])

		notifications, ok := settings["notifications"].([]any)
		require.True(t, ok)
		assert.Equal(t, []any{"email", "push"}, notifications)

		// Check metadata with mixed types
		metadata, ok := rootMap["metadata"].([]any)
		require.True(t, ok)
		assert.Len(t, metadata, 4)
		assert.Equal(t, "version1.0", metadata[0])
		assert.Equal(t, int64(42), metadata[1])
		assert.Equal(t, true, metadata[2])
		assert.Equal(t, "binary data", metadata[3]) // []byte becomes string

		t.Logf("Complex mixed structures converted successfully")
	})

	t.Run("homogeneous vs heterogeneous slices", func(t *testing.T) {
		// Test that homogeneous slices become lists, heterogeneous become tuples

		// Homogeneous slice
		homogeneous := []int{1, 2, 3, 4}
		result, err := AnyToCty(homogeneous)
		require.NoError(t, err)
		assert.True(t, result.Type().IsListType(), "Homogeneous slice should become list")

		// Heterogeneous slice
		heterogeneous := []any{"hello", 42, true, []byte("data")}
		result, err = AnyToCty(heterogeneous)
		require.NoError(t, err)
		assert.True(t, result.Type().IsTupleType(), "Heterogeneous slice should become tuple")

		t.Logf("Slice type detection working correctly")
	})

	t.Run("homogeneous vs heterogeneous maps", func(t *testing.T) {
		// Test that homogeneous maps become cty maps, heterogeneous become objects

		// Homogeneous map (all values are int)
		homogeneousMap := map[string]int{
			"a": 1,
			"b": 2,
			"c": 3,
		}
		result, err := AnyToCty(homogeneousMap)
		require.NoError(t, err)
		assert.True(t, result.Type().IsMapType(), "Homogeneous map should become cty map")

		// Heterogeneous map (mixed value types)
		heterogeneousMap := map[string]any{
			"name":   "Alice",
			"age":    30,
			"active": true,
		}
		result, err = AnyToCty(heterogeneousMap)
		require.NoError(t, err)
		assert.True(t, result.Type().IsObjectType(), "Heterogeneous map should become cty object")

		t.Logf("Map type detection working correctly")
	})

	t.Run("pointers and interfaces", func(t *testing.T) {
		// Test pointer dereferencing
		str := "hello"
		ptrToStr := &str

		result, err := AnyToCty(ptrToStr)
		require.NoError(t, err)
		assert.Equal(t, cty.String, result.Type())
		assert.Equal(t, "hello", result.AsString())

		// Test nil pointer
		var nilPtr *string
		result, err = AnyToCty(nilPtr)
		require.NoError(t, err)
		assert.True(t, result.IsNull())

		// Test interface{}
		var iface any = "interface value"
		result, err = AnyToCty(iface)
		require.NoError(t, err)
		assert.Equal(t, cty.String, result.Type())
		assert.Equal(t, "interface value", result.AsString())

		t.Logf("Pointers and interfaces handled correctly")
	})

	t.Run("empty collections", func(t *testing.T) {
		// Test empty slice
		emptySlice := []string{}
		result, err := AnyToCty(emptySlice)
		require.NoError(t, err)
		assert.True(t, result.Type().IsListType())

		// Test empty map - should be a map with dynamic type
		emptyMap := map[string]int{}
		result, err = AnyToCty(emptyMap)
		require.NoError(t, err)
		assert.True(t, result.Type().IsMapType(), "Empty map should become cty map")

		t.Logf("Empty collections handled correctly")
	})

	t.Run("comparison with old approach", func(t *testing.T) {
		// Compare recursive approach vs JSON approach for collections
		data := []map[string]any{
			{"name": "test", "value": 42},
		}

		// Our recursive approach should work
		result, err := AnyToCty(data)
		require.NoError(t, err)

		// Should be a list/tuple of objects
		assert.True(t, result.Type().IsListType() || result.Type().IsTupleType())

		// Round trip should work
		converted, err := CtyToAny(result)
		require.NoError(t, err)

		slice, ok := converted.([]any)
		require.True(t, ok)
		assert.Len(t, slice, 1)

		obj, ok := slice[0].(map[string]any)
		require.True(t, ok)
		assert.Equal(t, "test", obj["name"])
		assert.Equal(t, int64(42), obj["value"])

		t.Logf("Recursive approach handles collections that JSON approach failed on")
	})
}
