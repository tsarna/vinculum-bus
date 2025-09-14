package config

import (
	_ "embed"
	"testing"
)

//go:embed testdata/env.vcl
var envtest []byte

func TestEnv(t *testing.T) {
	_, diags := NewConfig().WithSources(envtest).Build()
	if diags.HasErrors() {
		t.Fatalf("failed to build config: %v", diags)
	}
}
