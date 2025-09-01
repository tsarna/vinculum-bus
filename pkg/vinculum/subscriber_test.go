package vinculum

import (
	"context"
	"testing"
)

func TestBaseSubscriber(t *testing.T) {
	subscriber := &BaseSubscriber{}

	// Test that all methods can be called without panicking
	// BaseSubscriber provides default no-op implementations

	// These should not panic or cause any issues
	subscriber.OnSubscribe(context.Background(), "test/topic")
	subscriber.OnUnsubscribe(context.Background(), "test/topic")
	subscriber.OnEvent(context.Background(), "test/topic", "message", nil)
	subscriber.OnEvent(context.Background(), "test/topic", "message", map[string]string{"key": "value"})

	// Test with various message types
	subscriber.OnEvent(context.Background(), "test/topic", 123, nil)
	subscriber.OnEvent(context.Background(), "test/topic", []byte("binary data"), nil)
	subscriber.OnEvent(context.Background(), "test/topic", map[string]interface{}{"key": "value"}, nil)
}

func TestMakeMatcherExactMatch(t *testing.T) {
	// Test exact match (no wildcards)
	msg := EventBusMessage{
		MsgType: MessageTypeSubscribe,
		Topic:   "exact/topic/match",
	}

	matcher := makeMatcher(msg)

	// Should match exact topic
	matches, fields := matcher("exact/topic/match")
	if !matches {
		t.Error("Expected exact match to return true")
	}
	if fields != nil {
		t.Error("Expected exact match to return nil fields")
	}

	// Should not match different topics
	matches, fields = matcher("exact/topic/different")
	if matches {
		t.Error("Expected different topic to return false")
	}
	if fields != nil {
		t.Error("Expected non-match to return nil fields")
	}

	// Should not match partial topics
	matches, _ = matcher("exact/topic")
	if matches {
		t.Error("Expected partial topic to return false")
	}

	matches, _ = matcher("exact/topic/match/extra")
	if matches {
		t.Error("Expected longer topic to return false")
	}
}

func TestMakeMatcherSingleLevelWildcard(t *testing.T) {
	// Test single-level wildcard (+)
	msg := EventBusMessage{
		MsgType: MessageTypeSubscribe,
		Topic:   "test/+/topic",
	}

	matcher := makeMatcher(msg)

	// Should match single-level replacements
	matches, fields := matcher("test/abc/topic")
	if !matches {
		t.Error("Expected single-level wildcard match to return true")
	}
	if fields != nil {
		t.Error("Expected single-level wildcard match to return nil fields for non-extraction mode")
	}

	matches, _ = matcher("test/xyz/topic")
	if !matches {
		t.Error("Expected single-level wildcard match to return true")
	}

	matches, _ = matcher("test/123/topic")
	if !matches {
		t.Error("Expected single-level wildcard match to return true")
	}

	// Should not match multi-level
	matches, _ = matcher("test/abc/def/topic")
	if matches {
		t.Error("Expected multi-level topic to not match single-level wildcard")
	}

	// Should not match missing level
	matches, _ = matcher("test/topic")
	if matches {
		t.Error("Expected missing level to not match single-level wildcard")
	}

	// Should not match different structure
	matches, _ = matcher("different/abc/topic")
	if matches {
		t.Error("Expected different prefix to not match")
	}

	matches, _ = matcher("test/abc/different")
	if matches {
		t.Error("Expected different suffix to not match")
	}
}

func TestMakeMatcherMultiLevelWildcard(t *testing.T) {
	// Test multi-level wildcard (#)
	msg := EventBusMessage{
		MsgType: MessageTypeSubscribe,
		Topic:   "test/#",
	}

	matcher := makeMatcher(msg)

	// Should match various levels
	matches, fields := matcher("test/abc")
	if !matches {
		t.Error("Expected multi-level wildcard to match single level")
	}
	if fields != nil {
		t.Error("Expected multi-level wildcard match to return nil fields for non-extraction mode")
	}

	matches, _ = matcher("test/abc/def")
	if !matches {
		t.Error("Expected multi-level wildcard to match double level")
	}

	matches, _ = matcher("test/abc/def/ghi")
	if !matches {
		t.Error("Expected multi-level wildcard to match triple level")
	}

	// Should not match different prefix
	matches, _ = matcher("different/abc")
	if matches {
		t.Error("Expected different prefix to not match multi-level wildcard")
	}

	// Should not match just the prefix without /
	matches, _ = matcher("test")
	if matches {
		t.Error("Expected bare prefix to not match multi-level wildcard")
	}
}

func TestMakeMatcherCombinedWildcards(t *testing.T) {
	// Test combination of wildcards
	msg := EventBusMessage{
		MsgType: MessageTypeSubscribe,
		Topic:   "api/+/users/#",
	}

	matcher := makeMatcher(msg)

	// Should match various combinations
	matches, _ := matcher("api/v1/users/123")
	if !matches {
		t.Error("Expected combined wildcard to match")
	}

	matches, _ = matcher("api/v2/users/123/profile")
	if !matches {
		t.Error("Expected combined wildcard to match with extra levels")
	}

	matches, _ = matcher("api/beta/users/active/count")
	if !matches {
		t.Error("Expected combined wildcard to match complex path")
	}

	// Should not match invalid patterns
	matches, _ = matcher("api/users/123")
	if matches {
		t.Error("Expected missing middle level to not match")
	}

	matches, _ = matcher("api/v1/accounts/123")
	if matches {
		t.Error("Expected different middle section to not match")
	}
}

func TestMakeMatcherWithExtraction(t *testing.T) {
	// Test parameter extraction
	msg := EventBusMessage{
		MsgType: MessageTypeSubscribeWithExtraction,
		Topic:   "user/+userId/profile/+action",
	}

	matcher := makeMatcher(msg)

	// Should match and extract parameters
	matches, fields := matcher("user/123/profile/update")
	if !matches {
		t.Error("Expected extraction pattern to match")
	}
	if fields == nil {
		t.Fatal("Expected extraction to return fields")
	}
	if fields["userId"] != "123" {
		t.Errorf("Expected userId '123', got '%s'", fields["userId"])
	}
	if fields["action"] != "update" {
		t.Errorf("Expected action 'update', got '%s'", fields["action"])
	}

	// Test with different values
	matches, fields = matcher("user/abc/profile/delete")
	if !matches {
		t.Error("Expected extraction pattern to match different values")
	}
	if fields["userId"] != "abc" {
		t.Errorf("Expected userId 'abc', got '%s'", fields["userId"])
	}
	if fields["action"] != "delete" {
		t.Errorf("Expected action 'delete', got '%s'", fields["action"])
	}

	// Should not match different patterns
	matches, fields = matcher("user/123/settings/update")
	if matches {
		t.Error("Expected different middle section to not match extraction pattern")
	}
	if fields != nil {
		t.Error("Expected non-match to return nil fields")
	}

	matches, _ = matcher("admin/123/profile/update")
	if matches {
		t.Error("Expected different prefix to not match extraction pattern")
	}
}

func TestMakeMatcherExtractionWithWildcards(t *testing.T) {
	// Test extraction with MQTT wildcards
	msg := EventBusMessage{
		MsgType: MessageTypeSubscribeWithExtraction,
		Topic:   "sensor/+sensorId/data/+type",
	}

	matcher := makeMatcher(msg)

	// Should match and extract where possible
	matches, fields := matcher("sensor/temp01/data/temperature")
	if !matches {
		t.Error("Expected mixed wildcard/extraction pattern to match")
	}
	if fields == nil {
		t.Fatal("Expected extraction to return fields")
	}
	if fields["sensorId"] != "temp01" {
		t.Errorf("Expected sensorId 'temp01', got '%s'", fields["sensorId"])
	}
	if fields["type"] != "temperature" {
		t.Errorf("Expected type 'temperature', got '%s'", fields["type"])
	}

	// Test with different sensor ID
	matches, fields = matcher("sensor/humid02/data/humidity")
	if !matches {
		t.Error("Expected mixed pattern to match different sensor")
	}
	if fields["sensorId"] != "humid02" {
		t.Errorf("Expected sensorId 'humid02', got '%s'", fields["sensorId"])
	}
	if fields["type"] != "humidity" {
		t.Errorf("Expected type 'humidity', got '%s'", fields["type"])
	}
}

func TestMakeMatcherEmptyTopics(t *testing.T) {
	// Test with empty topic
	msg := EventBusMessage{
		MsgType: MessageTypeSubscribe,
		Topic:   "",
	}

	matcher := makeMatcher(msg)

	// Should only match empty topic
	matches, _ := matcher("")
	if !matches {
		t.Error("Expected empty pattern to match empty topic")
	}

	matches, _ = matcher("anything")
	if matches {
		t.Error("Expected empty pattern to not match non-empty topic")
	}
}

func TestMakeMatcherSpecialCharacters(t *testing.T) {
	// Test with topics containing special characters
	msg := EventBusMessage{
		MsgType: MessageTypeSubscribe,
		Topic:   "device/sensor-01/temp_data",
	}

	matcher := makeMatcher(msg)

	// Should match exact topic with special chars
	matches, _ := matcher("device/sensor-01/temp_data")
	if !matches {
		t.Error("Expected exact match with special characters to work")
	}

	// Should not match similar topics
	matches, _ = matcher("device/sensor_01/temp_data")
	if matches {
		t.Error("Expected different special character to not match")
	}

	matches, _ = matcher("device/sensor-01/temp-data")
	if matches {
		t.Error("Expected different special character in suffix to not match")
	}
}

func TestMakeMatcherPanicOnUnsupportedType(t *testing.T) {
	// Test that unsupported message types cause panic
	msg := EventBusMessage{
		MsgType: MessageType(999), // Invalid message type
		Topic:   "test/topic",
	}

	defer func() {
		if r := recover(); r == nil {
			t.Error("Expected panic for unsupported message type")
		}
	}()

	makeMatcher(msg)
}

func TestMakeMatcherEdgeCases(t *testing.T) {
	// Test edge cases with wildcard placement

	// Wildcard at beginning
	msg := EventBusMessage{
		MsgType: MessageTypeSubscribe,
		Topic:   "+/test",
	}
	matcher := makeMatcher(msg)

	matches, _ := matcher("anything/test")
	if !matches {
		t.Error("Expected wildcard at beginning to work")
	}

	// Note: MQTT + wildcard doesn't match empty segments
	// Empty segments in MQTT are not typically supported
	matches, _ = matcher("a/test")
	if !matches {
		t.Error("Expected valid segment to match wildcard")
	}

	// Wildcard at end
	msg = EventBusMessage{
		MsgType: MessageTypeSubscribe,
		Topic:   "test/+",
	}
	matcher = makeMatcher(msg)

	matches, _ = matcher("test/anything")
	if !matches {
		t.Error("Expected wildcard at end to work")
	}

	// Multiple single-level wildcards
	msg = EventBusMessage{
		MsgType: MessageTypeSubscribe,
		Topic:   "+/+/+",
	}
	matcher = makeMatcher(msg)

	matches, _ = matcher("a/b/c")
	if !matches {
		t.Error("Expected multiple wildcards to work")
	}

	matches, _ = matcher("a/b")
	if matches {
		t.Error("Expected insufficient levels to not match multiple wildcards")
	}
}
