# Vinculum Observability (o11y)

Observability interfaces and implementations for Vinculum EventBus - providing metrics, tracing, and monitoring capabilities with minimal overhead.

## ✨ Features

### 🔌 **Interface-Based Design**
- **Pluggable providers** - Easy to swap between different observability backends
- **Zero dependencies** - Core interfaces have no external dependencies
- **Optional integration** - No overhead when observability is disabled

### 📊 **Metrics**
- **Counters** - Monotonically increasing values (events published, errors, etc.)
- **Histograms** - Distribution of values (latency, message sizes)
- **Gauges** - Current state values (active connections, queue sizes)
- **Labels** - Key-value pairs for metric dimensions

### 🔍 **Tracing**
- **Distributed tracing** support with context propagation
- **Span lifecycle** management (start, attributes, status, end)
- **Status tracking** (OK, Error, Unset)

### 🏗️ **Standalone Metrics Provider**
- **Zero external dependencies** - Self-contained metrics collection
- **EventBus integration** - Publishes metrics as events
- **JSON format** - Human-readable metrics snapshots
- **Configurable intervals** - Control metrics publishing frequency

## 🚀 Quick Start

### Using Standalone Metrics

```go
import (
    "github.com/tsarna/vinculum-bus"
    "github.com/tsarna/vinculum-bus/o11y"
)

// Create EventBus
eventBus, _ := bus.NewEventBus().Build()
eventBus.Start()

// Create standalone metrics provider
metricsProvider := o11y.NewStandaloneMetricsProvider(eventBus, &o11y.StandaloneMetricsConfig{
    Interval:     30 * time.Second,
    MetricsTopic: "$metrics",
    ServiceName:  "my-service",
})

// Start metrics collection
metricsProvider.Start()
defer metricsProvider.Stop()

// Create EventBus with metrics
observableEventBus, _ := bus.NewEventBus().
    WithMetrics(metricsProvider).
    Build()
```

### Using with OpenTelemetry

```go
import (
    "github.com/tsarna/vinculum-bus"
    "github.com/tsarna/vinculum-bus/otel"
)

// Create OpenTelemetry provider
otelProvider := otel.NewProvider("my-service", "v1.0.0")

// Create EventBus with full observability
eventBus, _ := bus.NewEventBus().
    WithObservability(otelProvider, otelProvider).
    WithServiceInfo("my-service", "v1.0.0").
    Build()
```

## 📚 API Reference

### Core Interfaces

#### MetricsProvider
```go
type MetricsProvider interface {
    Counter(name string) Counter
    Histogram(name string) Histogram
    Gauge(name string) Gauge
}
```

#### TracingProvider
```go
type TracingProvider interface {
    StartSpan(ctx context.Context, name string) (context.Context, Span)
}
```

#### Metric Instruments
```go
type Counter interface {
    Add(ctx context.Context, value int64, labels ...Label)
}

type Histogram interface {
    Record(ctx context.Context, value float64, labels ...Label)
}

type Gauge interface {
    Set(ctx context.Context, value float64, labels ...Label)
}
```

#### Tracing
```go
type Span interface {
    SetAttributes(labels ...Label)
    SetStatus(code SpanStatusCode, description string)
    End()
}

type SpanStatusCode int
const (
    SpanStatusUnset SpanStatusCode = iota
    SpanStatusOK
    SpanStatusError
)
```

### Standalone Metrics

#### Configuration
```go
type StandaloneMetricsConfig struct {
    Interval     time.Duration // How often to publish metrics (default: 30s)
    MetricsTopic string        // Topic to publish metrics to (default: "$metrics")
    ServiceName  string        // Service name to include in metrics
}
```

#### Metrics Snapshot Format
```json
{
  "timestamp": "2025-08-28T23:02:30.773505-04:00",
  "service_name": "my-service",
  "counters": {
    "eventbus_messages_published_total": 150,
    "eventbus_messages_published_sync_total": 25,
    "eventbus_subscriptions_total": 12,
    "eventbus_errors_total": 0
  },
  "histograms": {
    "eventbus_publish_duration_seconds": [0.001, 0.002, 0.001]
  },
  "gauges": {
    "eventbus_active_subscribers": 8
  }
}
```

## 🔧 Usage Patterns

### Custom Metrics Provider

```go
type MyMetricsProvider struct {
    // your implementation
}

func (m *MyMetricsProvider) Counter(name string) o11y.Counter {
    // return your counter implementation
}

func (m *MyMetricsProvider) Histogram(name string) o11y.Histogram {
    // return your histogram implementation
}

func (m *MyMetricsProvider) Gauge(name string) o11y.Gauge {
    // return your gauge implementation
}

// Use with EventBus
eventBus, _ := bus.NewEventBus().
    WithMetrics(&MyMetricsProvider{}).
    Build()
```

### Metrics Subscription

```go
// Subscribe to metrics events
type MetricsCollector struct{}

func (m *MetricsCollector) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
    if topic == "$metrics" {
        if snapshot, ok := message.(o11y.MetricsSnapshot); ok {
            // Process metrics snapshot
            fmt.Printf("Service: %s, Active Subscribers: %f\n", 
                snapshot.ServiceName, 
                snapshot.Gauges["eventbus_active_subscribers"])
        }
    }
    return nil
}

eventBus.Subscribe(ctx, &MetricsCollector{}, "$metrics")
```

## 🏗️ Architecture

### Design Principles
- **Interface segregation** - Small, focused interfaces
- **Dependency inversion** - Core doesn't depend on implementations
- **Zero-cost abstractions** - No overhead when disabled
- **Pluggable backends** - Easy to swap providers

### Performance
- **Lazy initialization** - Metrics created on first use
- **Atomic operations** - Thread-safe counters
- **Minimal allocations** - Optimized hot paths
- **Optional overhead** - Only pay for what you use

## 🔗 Integrations

### Available Providers
- **Standalone** (`o11y` package) - Zero dependency metrics via EventBus
- **OpenTelemetry** (`otel` package) - Full OTEL integration
- **Custom** - Implement the interfaces for your preferred backend

### Compatible Backends
- **Prometheus** - Via OpenTelemetry
- **Jaeger** - Via OpenTelemetry  
- **DataDog** - Via OpenTelemetry
- **Custom dashboards** - Via standalone metrics JSON events
- **Logging systems** - Via metrics subscription

## 📖 Examples

### Metrics Dashboard
```go
// Create a simple metrics dashboard
type Dashboard struct{}

func (d *Dashboard) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
    if snapshot, ok := message.(o11y.MetricsSnapshot); ok {
        fmt.Printf("=== %s Metrics ===\n", snapshot.ServiceName)
        fmt.Printf("Published: %d\n", snapshot.Counters["eventbus_messages_published_total"])
        fmt.Printf("Errors: %d\n", snapshot.Counters["eventbus_errors_total"])
        fmt.Printf("Active Subscribers: %.0f\n", snapshot.Gauges["eventbus_active_subscribers"])
    }
    return nil
}
```

### Health Monitoring
```go
type HealthMonitor struct {
    errorThreshold int64
}

func (h *HealthMonitor) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
    if snapshot, ok := message.(o11y.MetricsSnapshot); ok {
        errorCount := snapshot.Counters["eventbus_errors_total"]
        if errorCount > h.errorThreshold {
            // Alert or take corrective action
            log.Warn("High error count detected", zap.Int64("errors", errorCount))
        }
    }
    return nil
}
```

## 🎯 Use Cases

- **Performance monitoring** - Track message rates, latencies, error rates
- **Capacity planning** - Monitor subscriber counts, queue sizes
- **Health checks** - Detect anomalies and system issues
- **Debugging** - Trace request flows through the system
- **SLA monitoring** - Measure and report on service level objectives
- **Custom dashboards** - Build application-specific monitoring views
