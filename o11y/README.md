# Vinculum Observability (o11y)

Standalone metrics exporter for Vinculum EventBus - publishes OTel metric snapshots to bus topics for monitoring without external infrastructure.

## Features

- **OTel SDK Exporter** - Implements `sdkmetric.Exporter` for the OTel SDK pipeline
- **Bus-based publishing** - Metrics snapshots published as EventBus messages
- **Zero external deps** - No Prometheus, OTLP, or external collectors required
- **Configurable intervals** - Control metrics publishing frequency

## Quick Start

```go
import (
    "github.com/tsarna/vinculum-bus"
    "github.com/tsarna/vinculum-bus/o11y"
)

// Create EventBus
eventBus, _ := bus.NewEventBus().Build()
eventBus.Start()

// Create standalone meter provider that publishes to the bus
mp, _ := o11y.NewStandaloneMeterProvider(eventBus, &o11y.StandaloneMetricsConfig{
    Interval:     30 * time.Second,
    MetricsTopic: "$metrics",
    ServiceName:  "my-service",
})
defer mp.Shutdown(ctx)

// Create EventBus with metrics
observableEventBus, _ := bus.NewEventBus().
    WithMeterProvider(mp).
    Build()
```

## API Reference

### StandaloneMetricsConfig

```go
type StandaloneMetricsConfig struct {
    Interval     time.Duration // How often to publish metrics (default: 30s)
    MetricsTopic string        // Topic to publish metrics to (default: "$metrics")
    ServiceName  string        // Service name to include in metrics
}
```

### NewStandaloneMeterProvider

```go
func NewStandaloneMeterProvider(publisher MetricsPublisher, config *StandaloneMetricsConfig) (*sdkmetric.MeterProvider, *StandaloneExporter)
```

Creates an `sdkmetric.MeterProvider` backed by a `PeriodicReader` that exports
metrics snapshots to a bus topic. The returned `StandaloneExporter` can be used
to call `SetPublisher()` if the publisher is not yet available at creation time.

### MetricsSnapshot

Published to the configured topic as an `o11y.MetricsSnapshot`:

```json
{
  "timestamp": "2025-08-28T23:02:30.773505-04:00",
  "service_name": "my-service",
  "counters": {
    "messaging.client.sent.messages": 175,
    "eventbus.subscriptions": 12
  },
  "histograms": {
    "messaging.client.operation.duration": {
      "count": 25,
      "sum": 0.045,
      "bounds": [0.005, 0.01, 0.025, 0.05],
      "bucket_counts": [10, 8, 5, 2, 0]
    }
  },
  "gauges": {
    "eventbus.active_subscribers": 8
  }
}
```

### Subscribing to Metrics

```go
type MetricsCollector struct {
    bus.BaseSubscriber
}

func (m *MetricsCollector) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
    if snapshot, ok := message.(o11y.MetricsSnapshot); ok {
        fmt.Printf("Service: %s, Subscribers: %.0f\n",
            snapshot.ServiceName,
            snapshot.Gauges["eventbus.active_subscribers"])
    }
    return nil
}

eventBus.Subscribe(ctx, "$metrics", &MetricsCollector{})
```

## Architecture

The `StandaloneExporter` implements `sdkmetric.Exporter`. When wired into an
`sdkmetric.MeterProvider` via `PeriodicReader`, the OTel SDK handles:

- Instrument registration and aggregation
- Periodic collection on the configured interval
- Thread-safe metric recording

The exporter receives pre-aggregated `metricdata.ResourceMetrics` and converts
them into a `MetricsSnapshot` for publishing to the bus.
