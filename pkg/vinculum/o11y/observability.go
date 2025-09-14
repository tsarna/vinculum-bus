package o11y

import (
	"context"
)

// MetricsPublisher defines the minimal interface needed by StandaloneMetricsProvider
// to publish metrics events. This avoids circular dependencies with the bus package.
type MetricsPublisher interface {
	Publish(ctx context.Context, topic string, message any) error
}

// ObservabilityConfig holds optional observability providers
type ObservabilityConfig struct {
	MetricsProvider MetricsProvider
	TracingProvider TracingProvider
	ServiceName     string
	ServiceVersion  string
}

// MetricsProvider abstracts metrics collection (can be implemented with OpenTelemetry, Prometheus, etc.)
type MetricsProvider interface {
	Counter(name string) Counter
	Histogram(name string) Histogram
	Gauge(name string) Gauge
}

// TracingProvider abstracts distributed tracing (can be implemented with OpenTelemetry, Jaeger, etc.)
type TracingProvider interface {
	StartSpan(ctx context.Context, name string) (context.Context, Span)
}

// Counter represents a monotonically increasing metric
type Counter interface {
	Add(ctx context.Context, value int64, labels ...Label)
}

// Histogram records distribution of values
type Histogram interface {
	Record(ctx context.Context, value float64, labels ...Label)
}

// Gauge represents a value that can go up and down
type Gauge interface {
	Set(ctx context.Context, value float64, labels ...Label)
}

// Span represents a unit of work in a trace
type Span interface {
	SetAttributes(labels ...Label)
	SetStatus(code SpanStatusCode, description string)
	End()
}

// Label represents a key-value pair for metrics and tracing
type Label struct {
	Key   string
	Value string
}

// SpanStatusCode represents the status of a span
type SpanStatusCode int

const (
	SpanStatusUnset SpanStatusCode = iota
	SpanStatusOK
	SpanStatusError
)
