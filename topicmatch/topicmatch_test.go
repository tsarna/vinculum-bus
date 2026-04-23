package topicmatch

import (
	"reflect"
	"testing"
)

func TestMatches(t *testing.T) {
	cases := []struct {
		pattern string
		topic   string
		want    bool
	}{
		// $-topic rule: wildcard-prefixed patterns must not match $-topics.
		{"#", "$metrics", false},
		{"+", "$metrics", false},
		{"+/x", "$sys/x", false},
		{"#", "$", false},

		// $-topic rule: patterns starting with $ (even containing wildcards) match.
		{"$metrics", "$metrics", true},
		{"$sys/#", "$sys/broker/foo", true},
		{"$sys/+", "$sys/foo", true},

		// Regression: normal matching unaffected.
		{"foo/+", "foo/bar", true},
		{"#", "foo/bar", true},
		{"+/bar", "foo/bar", true},
		{"foo/bar", "foo/bar", true},
		{"foo/bar", "foo/baz", false},

		// Boundary: empty pattern is passed through to mqttpattern unchanged
		// (the $-rule only fires when pattern is non-empty and starts with + or #).
		{"", "", true},
		{"#", "", true},
		{"", "$x", true},
		{"", "x", true},
	}

	for _, c := range cases {
		got := Matches(c.pattern, c.topic)
		if got != c.want {
			t.Errorf("Matches(%q, %q) = %v, want %v", c.pattern, c.topic, got, c.want)
		}
	}
}

func TestExtract(t *testing.T) {
	cases := []struct {
		pattern string
		topic   string
		want    map[string]string
	}{
		// $-topic rule: blocked match returns empty map.
		{"#", "$metrics", map[string]string{}},
		{"+name", "$metrics", map[string]string{}},

		// Patterns starting with $ work normally.
		{"$sys/+name", "$sys/foo", map[string]string{"name": "foo"}},

		// Normal extraction.
		{"sensor/+id/data", "sensor/42/data", map[string]string{"id": "42"}},
		{"foo/+", "foo/bar", map[string]string{}},
	}

	for _, c := range cases {
		got := Extract(c.pattern, c.topic)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Extract(%q, %q) = %v, want %v", c.pattern, c.topic, got, c.want)
		}
	}
}

func TestExec(t *testing.T) {
	cases := []struct {
		pattern string
		topic   string
		want    map[string]string
	}{
		// $-topic rule: blocked match returns nil.
		{"#", "$metrics", nil},
		{"+name", "$metrics", nil},

		// Patterns starting with $ work normally.
		{"$sys/+name", "$sys/foo", map[string]string{"name": "foo"}},

		// Normal exec: no-match returns nil, match returns extracted params.
		{"foo/bar", "foo/baz", nil},
		{"sensor/+id/data", "sensor/42/data", map[string]string{"id": "42"}},
	}

	for _, c := range cases {
		got := Exec(c.pattern, c.topic)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Exec(%q, %q) = %v, want %v", c.pattern, c.topic, got, c.want)
		}
	}
}

func TestHasExtractions(t *testing.T) {
	cases := []struct {
		pattern string
		want    bool
	}{
		{"foo/+id", true},
		{"foo/#all", true},
		{"foo/+", false},
		{"foo/#", false},
		{"foo/bar", false},
	}

	for _, c := range cases {
		got := HasExtractions(c.pattern)
		if got != c.want {
			t.Errorf("HasExtractions(%q) = %v, want %v", c.pattern, got, c.want)
		}
	}
}
