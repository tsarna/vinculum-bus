// Package otel provides OpenTelemetry implementations for vinculum observability interfaces.
package otel

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"github.com/tsarna/vinculum/pkg/vinculum/o11y"
)

// Provider implements both MetricsProvider and TracingProvider using OpenTelemetry
type Provider struct {
	meter  metric.Meter
	tracer trace.Tracer
}

// NewProvider creates a new OpenTelemetry provider for vinculum observability
func NewProvider(serviceName, serviceVersion string) *Provider {
	return &Provider{
		meter:  otel.Meter(serviceName, metric.WithInstrumentationVersion(serviceVersion)),
		tracer: otel.Tracer(serviceName, trace.WithInstrumentationVersion(serviceVersion)),
	}
}

// Counter creates an OpenTelemetry counter
func (p *Provider) Counter(name string) o11y.Counter {
	counter, _ := p.meter.Int64Counter(name)
	return &otelCounter{counter: counter}
}

// Histogram creates an OpenTelemetry histogram
func (p *Provider) Histogram(name string) o11y.Histogram {
	histogram, _ := p.meter.Float64Histogram(name)
	return &otelHistogram{histogram: histogram}
}

// Gauge creates an OpenTelemetry gauge (using UpDownCounter)
func (p *Provider) Gauge(name string) o11y.Gauge {
	gauge, _ := p.meter.Float64UpDownCounter(name)
	return &otelGauge{gauge: gauge}
}

// StartSpan creates an OpenTelemetry span
func (p *Provider) StartSpan(ctx context.Context, name string) (context.Context, o11y.Span) {
	ctx, span := p.tracer.Start(ctx, name)
	return ctx, &otelSpan{span: span}
}

// otelCounter wraps OpenTelemetry counter
type otelCounter struct {
	counter metric.Int64Counter
}

func (c *otelCounter) Add(ctx context.Context, value int64, labels ...o11y.Label) {
	attrs := make([]attribute.KeyValue, len(labels))
	for i, label := range labels {
		attrs[i] = attribute.String(label.Key, label.Value)
	}
	c.counter.Add(ctx, value, metric.WithAttributes(attrs...))
}

// otelHistogram wraps OpenTelemetry histogram
type otelHistogram struct {
	histogram metric.Float64Histogram
}

func (h *otelHistogram) Record(ctx context.Context, value float64, labels ...o11y.Label) {
	attrs := make([]attribute.KeyValue, len(labels))
	for i, label := range labels {
		attrs[i] = attribute.String(label.Key, label.Value)
	}
	h.histogram.Record(ctx, value, metric.WithAttributes(attrs...))
}

// otelGauge wraps OpenTelemetry gauge
type otelGauge struct {
	gauge metric.Float64UpDownCounter
}

func (g *otelGauge) Set(ctx context.Context, value float64, labels ...o11y.Label) {
	attrs := make([]attribute.KeyValue, len(labels))
	for i, label := range labels {
		attrs[i] = attribute.String(label.Key, label.Value)
	}
	// Note: For a true gauge, we'd need to track the previous value and add the difference
	// This is a simplified implementation using UpDownCounter
	g.gauge.Add(ctx, value, metric.WithAttributes(attrs...))
}

// otelSpan wraps OpenTelemetry span
type otelSpan struct {
	span trace.Span
}

func (s *otelSpan) SetAttributes(labels ...o11y.Label) {
	attrs := make([]attribute.KeyValue, len(labels))
	for i, label := range labels {
		attrs[i] = attribute.String(label.Key, label.Value)
	}
	s.span.SetAttributes(attrs...)
}

func (s *otelSpan) SetStatus(code o11y.SpanStatusCode, description string) {
	var otelCode codes.Code
	switch code {
	case o11y.SpanStatusOK:
		otelCode = codes.Ok
	case o11y.SpanStatusError:
		otelCode = codes.Error
	default:
		otelCode = codes.Unset
	}
	s.span.SetStatus(otelCode, description)
}

func (s *otelSpan) End() {
	s.span.End()
}
