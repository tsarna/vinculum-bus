# Vinculum

A high-performance, feature-rich in-process EventBus for Go with MQTT-style topic patterns, and optional observability.

"The [vinculum is the] processing device at the core of every Borg vessel.
It interconnects the minds of all the drones."
   -- Seven of Nine (In Voyager episode "Infinite Regress")

Note that this README was written almost entirely by Claude and it makes bold claims, but it did actually test the performance and got the claimed numbers on a 2021 M1 Max MacBook.

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
- **Buffered channels** for high throughput
- **Atomic operations** for counters
- **Lazy metric creation** for efficiency
- **Context propagation** for distributed tracing
- **Minimal allocations** in hot paths

## 🚀 Quick Start

```go
package main

import (
    "context"
    "github.com/tsarna/vinculum/pkg/vinculum"
    "go.uber.org/zap"
)

func main() {
    logger, _ := zap.NewProduction()
    ctx := context.Background()
    
    // Create and start EventBus
    eventBus := vinculum.NewEventBus(logger)
    eventBus.Start()
    defer eventBus.Stop()
    
    // Create subscriber
    subscriber := vinculum.NewNamedLoggingSubscriber(logger, zap.InfoLevel, "MyService")
    
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
    
    Subscribe(ctx context.Context, subscriber Subscriber, topic string) error
    Unsubscribe(ctx context.Context, subscriber Subscriber, topic string) error
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

```go
// Basic EventBus
eventBus := vinculum.NewEventBus(logger)

// With observability
eventBus := vinculum.NewEventBusWithObservability(logger, &vinculum.ObservabilityConfig{
    MetricsProvider: provider,
    TracingProvider: provider,
    ServiceName:     "my-service",
    ServiceVersion:  "v1.0.0",
})
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

## 📊 Observability Options

### 1. OpenTelemetry Integration

```go
import "github.com/tsarna/vinculum/pkg/vinculum/otel"

provider := otel.NewProvider("my-service", "v1.0.0")
eventBus := vinculum.NewEventBusWithObservability(logger, &vinculum.ObservabilityConfig{
    MetricsProvider: provider,
    TracingProvider: provider,
})
```

### 2. Standalone Metrics (Zero Dependencies)

```go
// Self-contained metrics via EventBus
metricsProvider := vinculum.NewStandaloneMetricsProvider(eventBus, &vinculum.StandaloneMetricsConfig{
    Interval:     30 * time.Second,  // Publish every 30s
    MetricsTopic: "$metrics",        // Topic for metrics
    ServiceName:  "my-service",
})

metricsProvider.Start()
defer metricsProvider.Stop()

// Subscribe to metrics
eventBus.Subscribe(ctx, metricsCollector, "$metrics")
```

#### Metrics JSON Format
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

## 🧪 Testing

### Built-in Test Utilities

```go
// Mock subscriber for testing
mockSub := &vinculum.MockSubscriber{}
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
// Logs all events with structured data
debugSub := vinculum.NewNamedLoggingSubscriber(logger, zap.DebugLevel, "Debug")
eventBus.Subscribe(ctx, debugSub, "#") // Subscribe to everything
```

## 🏗️ Architecture

### Design Principles
- **Interface-based** - Easy to mock and extend
- **Dependency inversion** - Core doesn't depend on observability
- **Zero-cost abstractions** - No overhead when features not used
- **Thread-safe** - Safe for concurrent use
- **Graceful degradation** - Continues working on errors

### Performance Characteristics
- **~110ns** per publish operation (no observability, 0 allocations)
- **~529ns** per publish operation (with OpenTelemetry observability, 12 allocations)  
- **~180ns** per publish operation (with standalone metrics, 1 allocation)
- **~706ns** per PublishSync operation (waits for completion, 3 allocations)
- **4.6+ million messages/second** throughput capability
- **Zero allocations** in async publish hot path (no observability)
- **Optimized hot path** with minimal overhead
- **Configurable buffering** for backpressure handling

## 🔗 Dependencies

### Core (Required)
- `go.uber.org/zap` - Structured logging
- `github.com/tsarna/mqttpattern` - Topic pattern matching

### Optional (Only when used)
- `go.opentelemetry.io/otel` - OpenTelemetry integration

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

## 📄 License

MIT License - see [LICENSE](LICENSE) file for details.

---

**Vinculum** (Latin: "bond" or "link") - connecting your application components with reliable, observable messaging.
