# Vinculum Bus

"The [vinculum is the] processing device at the core of every Borg vessel.
It interconnects the minds of all the drones."
   -- Seven of Nine (In Voyager episode "Infinite Regress")

vinculum-bus is the core event bus functionality of Vinculum.  It's a
high-performance, feature-rich in-process EventBus for Go with
MQTT-style topic patterns and optional observability. It does not
depend on any other Vinculum projects.

## ✨ Features

### 🚀 **Core EventBus**
- **MQTT-style topics** with wildcards (`+` single-level, `#` multi-level)
- **Parameter extraction** from topic patterns (`users/+userId/events`)
- **Async & sync publishing** with error handling
- **Thread-safe** with minimal locking design
- **Graceful shutdown** and lifecycle management

### 📊 **Observability** 
- **Optional OpenTelemetry integration** (metrics + tracing)
- **Standalone metrics provider** (publishes to `$metrics` topic)
- **Minimal overhead** when observability disabled
- **Comprehensive instrumentation** (counters, histograms, gauges)
- **Interface-based** design for custom providers

### 🔧 **Developer Experience**
- **Simple API** with intuitive interfaces
- **Structured logging** with zap integration
- **Comprehensive test suite** with 83%+ coverage
- **Type-safe** subscriber patterns
- **Mock-friendly** for testing

### ⚡ **Performance**
- **Configurable buffering** (1000 messages default) for burst handling
- **Atomic operations** for counters
- **Lazy metric creation** for efficiency
- **Context propagation** for distributed tracing
- **Minimal allocations** in hot paths

## 🚀 Quick Start

```go
package main

import (
    "context"
    "github.com/tsarna/vinculum-bus"
    "github.com/tsarna/vinculum-bus/subutils"
    "go.uber.org/zap"
)

func main() {
    logger, _ := zap.NewProduction()
    ctx := context.Background()
    
    // Create and start EventBus using builder pattern
    eventBus, err := bus.NewEventBus().
        WithLogger(logger).
        Build()
    if err != nil {
        log.Fatal(err)
    }
    eventBus.Start()
    defer eventBus.Stop()
    
    // Create subscriber (nil = standalone logging subscriber)
    subscriber := subutils.NewNamedLoggingSubscriber(nil, logger, zap.InfoLevel, "MyService")
    
    // Subscribe to topic pattern (context propagation enabled)
    eventBus.Subscribe(ctx, subscriber, "users/+userId/events")
    
    // Publish messages with context
    eventBus.Publish(ctx, "users/123/events", "User logged in")
    eventBus.PublishSync(ctx, "users/456/events", "User created account")
}
```

## 📚 API Reference

### EventBus Interface

```go
type EventBus interface {
    Start() error
    Stop() error
    
    Subscribe(ctx context.Context, topic string, subscriber Subscriber) error
    Unsubscribe(ctx context.Context, topic string, subscriber Subscriber) error
    UnsubscribeAll(ctx context.Context, subscriber Subscriber) error
    
    Publish(ctx context.Context, topic string, payload any) error      // Async, fire-and-forget
    PublishSync(ctx context.Context, topic string, payload any) error  // Sync, waits for completion
}
```

### Subscriber Interface

```go
type Subscriber interface {
    OnSubscribe(ctx context.Context, topic string) error
    OnUnsubscribe(ctx context.Context, topic string) error
    OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error
}
```

### Creating EventBus

#### Builder Pattern

```go
// Basic EventBus
eventBus, err := bus.NewEventBus().
    WithLogger(logger).
    Build()
if err != nil {
    return err
}

// With custom buffer size
eventBus, err := bus.NewEventBus().
    WithLogger(logger).
    WithBufferSize(2000).
    Build()
if err != nil {
    return err
}

// With observability
eventBus, err := bus.NewEventBus().
    WithLogger(logger).
    WithMeterProvider(meterProvider).
    WithTracerProvider(tracerProvider).
    WithServiceInfo("my-service", "v1.0.0").
    WithBufferSize(1500).
    Build()
if err != nil {
    return err
}

// Without logger (uses nop logger)
eventBus, err := bus.NewEventBus().
    WithBufferSize(1000).
    Build()
if err != nil {
    return err
}
```

## 🎯 Topic Patterns

### Wildcards
- **`+`** - Single-level wildcard (`sensors/+/temperature`)
- **`#`** - Multi-level wildcard (`logs/#`)

### Parameter Extraction
```go
// Subscribe to pattern
eventBus.Subscribe(ctx, subscriber, "users/+userId/orders/+orderId")

// Publish message
eventBus.Publish(ctx, "users/123/orders/456", orderData)

// Subscriber receives:
// topic = "users/123/orders/456"
// fields = {"userId": "123", "orderId": "456"}
```

## 📊 Observability

### OpenTelemetry Metrics

The EventBus accepts a standard OTel `metric.MeterProvider`. Metrics follow
OTel semantic conventions (`messaging.client.*` where applicable).

```go
eventBus, err := bus.NewEventBus().
    WithLogger(logger).
    WithMeterProvider(meterProvider).
    WithServiceInfo("my-service", "v1.0.0").
    Build()
```

### Standalone Metrics (publish to bus)

```go
import "github.com/tsarna/vinculum-bus/o11y"

// Creates an sdkmetric.MeterProvider that periodically exports metrics to a bus topic
mp, exporter := o11y.NewStandaloneMeterProvider(eventBus, &o11y.StandaloneMetricsConfig{
    Interval:     30 * time.Second,  // Publish every 30s
    MetricsTopic: "$metrics",        // Topic for metrics
    ServiceName:  "my-service",
})
defer mp.Shutdown(ctx)

// Use the MeterProvider with the EventBus
observableEventBus, _ := bus.NewEventBus().
    WithMeterProvider(mp).
    Build()

// Subscribe to metrics snapshots
eventBus.Subscribe(ctx, metricsCollector, "$metrics")
```

#### Metrics Snapshot Format
```json
{
  "timestamp": "2025-08-28T23:02:30.773505-04:00",
  "service_name": "my-service",
  "counters": {
    "messaging.client.sent.messages": 175,
    "eventbus.subscriptions": 12,
    "messaging.client.errors": 0
  },
  "histograms": {
    "messaging.client.operation.duration": {
      "count": 25,
      "sum": 0.045,
      "bounds": [0.005, 0.01, 0.025, 0.05, 0.075, 0.1, 0.25, 0.5, 0.75, 1, 2.5, 5, 7.5, 10],
      "bucket_counts": [10, 8, 5, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0]
    }
  },
  "gauges": {
    "eventbus.active_subscribers": 8
  }
}
```

## 🧪 Testing

### Built-in Test Utilities

```go
// Mock subscriber for testing
mockSub := &bus.MockSubscriber{}
eventBus.Subscribe(ctx, mockSub, "test/+param")

eventBus.Publish(ctx, "test/value", "message")

// Verify events
events := mockSub.GetEvents()
assert.Equal(t, 1, len(events))
assert.Equal(t, "test/value", events[0].Topic)
assert.Equal(t, "value", events[0].Fields["param"])
```

### Logging Subscriber for Debugging

```go
// Logs all events with structured data (standalone mode)
debugSub := subutils.NewNamedLoggingSubscriber(nil, logger, zap.DebugLevel, "Debug")
eventBus.Subscribe(ctx, debugSub, "#") // Subscribe to everything

// Or wrap another subscriber to add logging
// wrappedSub := subutils.NewNamedLoggingSubscriber(mySubscriber, logger, zap.DebugLevel, "Debug")
```

## 🏗️ Architecture

### Design Principles
- **Interface-based** - Easy to mock and extend
- **Dependency inversion** - Core doesn't depend on observability
- **Zero-cost abstractions** - No overhead when features not used
- **Thread-safe** - Safe for concurrent use
- **Graceful degradation** - Continues working on errors

### Performance Characteristics

Note that this README was written almost entirely by Claude and it makes bold claims, but it did actually test the performance and got the claimed numbers on a 2021 M1 Max MacBook.

- **~110ns** per publish operation (no observability, 0 allocations)
- **~529ns** per publish operation (with OpenTelemetry observability, 12 allocations)  
- **~180ns** per publish operation (with standalone metrics, 1 allocation)
- **~706ns** per PublishSync operation (waits for completion, 3 allocations)
- **4.6+ million messages/second** throughput capability
- **Zero allocations** in async publish hot path (no observability)
- **Optimized hot path** with minimal overhead
- **Configurable buffering** (default: 1000 messages) with message dropping for backpressure

## ⚙️ Configuration

### Buffer Size Configuration

Control the internal channel buffer size to handle burst traffic:

```go
// Custom buffer size for high-throughput scenarios
eventBus, err := bus.NewEventBus().
    WithLogger(logger).
    WithBufferSize(5000). // Default is 1000
    Build()
if err != nil {
    return err
}
```

**Buffer Size Guidelines:**
- **Default (1000)**: Good for most applications (~0.2ms burst capacity)
- **Small (100-500)**: Memory-constrained environments
- **Large (2000+)**: High-throughput, burst-heavy workloads
- **Very Large (10000+)**: Extreme burst scenarios (consider batching instead)

## 📖 Examples

### Basic Pub/Sub
```go
subscriber := &MySubscriber{}
eventBus.Subscribe(ctx, subscriber, "notifications/+type")
eventBus.Publish(ctx, "notifications/email", emailData)
```

### Error Handling
```go
err := eventBus.PublishSync(ctx, "critical/operation", data)
if err != nil {
    log.Error("Critical operation failed", zap.Error(err))
}
```

### Graceful Shutdown
```go
// Start EventBus
eventBus.Start()

// Handle shutdown signal
c := make(chan os.Signal, 1)
signal.Notify(c, os.Interrupt, syscall.SIGTERM)
<-c

// Graceful shutdown
eventBus.Stop() // Waits for in-flight messages
```

## 🎯 Use Cases

- **Microservice communication** within a process
- **Event-driven architectures** 
- **Decoupled component communication**
- **Real-time data processing pipelines**
- **Plugin systems** with event coordination
- **Application telemetry** and monitoring

## 🔗 Dependencies

### Core (Required)
- `go.uber.org/zap` - Structured logging
- `github.com/amir-yaghoubi/mqttpattern` - Topic pattern matching

### Observability (Optional)
- `github.com/tsarna/vinculum-bus/o11y` - Observability interfaces and standalone metrics
- `github.com/tsarna/vinculum-bus/otel` - OpenTelemetry integration
- `go.opentelemetry.io/otel` - OpenTelemetry SDK (when using otel package)
