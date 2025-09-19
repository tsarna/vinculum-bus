package config

import (
	"context"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/tsarna/vinculum/pkg/vinculum/bus"
	"github.com/zclconf/go-cty/cty"
	"go.uber.org/zap"
)

type SubscriptionDefinition struct {
	Name    string         `hcl:",label"`
	BusExpr hcl.Expression `hcl:"bus,optional"`
	Topics  []string       `hcl:"topics"`
	// QueueSize  *int                  `hcl:"queue_size,optional"`
	ActionExpr hcl.Expression `hcl:"action,optional"`
}

type SubscriptionBlockHandler struct {
	BlockHandlerBase
}

func NewSubscriptionBlockHandler() *SubscriptionBlockHandler {
	return &SubscriptionBlockHandler{}
}

func (h *SubscriptionBlockHandler) GetBlockDependencyId(block *hcl.Block) (string, hcl.Diagnostics) {
	return "subscription." + block.Labels[0], nil
}

func (h *SubscriptionBlockHandler) Process(config *Config, block *hcl.Block) hcl.Diagnostics {
	subscriptionDef := SubscriptionDefinition{}
	diags := gohcl.DecodeBody(block.Body, config.evalCtx, &subscriptionDef)
	if diags.HasErrors() {
		return diags
	}

	// Manually set the name from the block label since DecodeBody doesn't handle labels
	if len(block.Labels) > 0 {
		subscriptionDef.Name = block.Labels[0]
	}

	bus, diags := GetEventBusFromExpression(config, subscriptionDef.BusExpr)
	if diags.HasErrors() {
		return diags
	}

	subscriber, diags := CreateSubscriber(config, &subscriptionDef)
	if diags.HasErrors() {
		return diags
	}

	for _, topic := range subscriptionDef.Topics {
		err := bus.Subscribe(context.Background(), subscriber, topic) // TODO: context for otel
		if err != nil {
			diags = diags.Append(
				&hcl.Diagnostic{
					Severity: hcl.DiagError,
					Summary:  "Failed to subscribe to bus",
					Detail:   err.Error(),
					Subject:  &block.DefRange,
				})
		}
	}

	return diags
}

func CreateSubscriber(config *Config, subscriptionDef *SubscriptionDefinition) (bus.Subscriber, hcl.Diagnostics) {
	config.Logger.Info("CreateSubscriber called", zap.String("subscription", subscriptionDef.Name))
	return &ActionSubscriber{
		Config:     config,
		ActionExpr: subscriptionDef.ActionExpr,
	}, nil
}

type ActionSubscriber struct {
	bus.BaseSubscriber
	Config     *Config
	ActionExpr hcl.Expression
}

func (a *ActionSubscriber) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	ctyMessage, err := AnyToCty(message)
	if err != nil {
		return err
	}

	evalCtxBuilder := NewContext(ctx).
		WithStringAttribute("topic", topic).
		WithAttribute("msg", ctyMessage)

	if len(fields) > 0 {
		ctyFields := make(map[string]cty.Value)
		for key, value := range fields {
			ctyFields[key] = cty.StringVal(value)
		}
		evalCtxBuilder = evalCtxBuilder.WithAttribute("fields", cty.ObjectVal(ctyFields))
	}

	evalCtx, diags := evalCtxBuilder.BuildEvalContext(a.Config.evalCtx)
	if diags.HasErrors() {
		return diags
	}

	a.Config.Logger.Info("OnEvent called", zap.String("topic", topic), zap.Any("message", message), zap.Any("fields", fields))

	_, diags = a.ActionExpr.Value(evalCtx)
	if diags.HasErrors() {
		return diags
	}

	return nil
}
