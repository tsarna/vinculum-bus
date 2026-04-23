// Package topicmatch wraps github.com/amir-yaghoubi/mqttpattern to enforce the
// MQTT 5.0 §4.7.2 rule: a topic filter starting with a wildcard character
// (+ or #) MUST NOT match a topic name beginning with $. This reserves
// $-prefixed topics for server/system use (e.g. $metrics, $server/mcp/...).
//
// All topic pattern matching across vinculum and its bridges should go
// through this package rather than mqttpattern directly.
package topicmatch

import "github.com/amir-yaghoubi/mqttpattern"

// blocked reports whether the MQTT $-topic rule prevents this pattern from
// matching this topic: pattern begins with + or #, and topic begins with $.
func blocked(pattern, topic string) bool {
	return len(topic) > 0 && topic[0] == '$' &&
		len(pattern) > 0 && (pattern[0] == '+' || pattern[0] == '#')
}

// Matches reports whether topic matches pattern, honoring the $-topic rule.
func Matches(pattern, topic string) bool {
	if blocked(pattern, topic) {
		return false
	}
	return mqttpattern.Matches(pattern, topic)
}

// Extract returns the named parameters extracted from topic using pattern.
// Returns an empty map if the $-topic rule blocks the match (matching the
// upstream convention of an empty map on no-match).
func Extract(pattern, topic string) map[string]string {
	if blocked(pattern, topic) {
		return map[string]string{}
	}
	return mqttpattern.Extract(pattern, topic)
}

// Exec matches topic against pattern and, if it matches, returns extracted
// parameters. Returns nil on no-match (matching the upstream convention).
func Exec(pattern, topic string) map[string]string {
	if blocked(pattern, topic) {
		return nil
	}
	return mqttpattern.Exec(pattern, topic)
}

// HasExtractions reports whether pattern contains any named wildcards
// (+name or #name). Pattern-only inspection, unaffected by the $-topic rule.
func HasExtractions(pattern string) bool {
	return mqttpattern.HasExtractions(pattern)
}
