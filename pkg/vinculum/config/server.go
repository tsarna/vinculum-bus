package config

import (
	"fmt"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
)

type ServerDefinition struct {
	Type string `hcl:",label"`
	Name string `hcl:",label"`

	Disabled      bool     `hcl:"disabled,optional"`
	RemainingBody hcl.Body `hcl:",remain"`
}

type ServerBlockHandler struct {
	BlockHandlerBase
}

func NewServerBlockHandler() *ServerBlockHandler {
	return &ServerBlockHandler{}
}

func (h *ServerBlockHandler) GetBlockDependencyId(block *hcl.Block) (string, hcl.Diagnostics) {
	return "server." + block.Labels[0] + "." + block.Labels[1], nil
}

func (h *ServerBlockHandler) Process(config *Config, block *hcl.Block) hcl.Diagnostics {
	if config.Servers == nil {
		config.Servers = make(map[string]map[string]Server)
	}

	serverDef := ServerDefinition{}
	diags := gohcl.DecodeBody(block.Body, config.evalCtx, &serverDef)
	if diags.HasErrors() {
		return diags
	}

	if serverDef.Disabled {
		return nil
	}

	switch block.Labels[0] {
	case "http":
		return ProcessHttpServerBlock(config, block, serverDef.RemainingBody)

	default:
		return hcl.Diagnostics{
			&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Invalid server type",
				Detail:   fmt.Sprintf("Invalid server type: %s", block.Labels[0]),
				Subject:  &block.DefRange,
			},
		}
	}
}

type Server interface {
	GetName() string
	GetDefRange() hcl.Range
}
