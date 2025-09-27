# WebSocket Client

This package provides a WebSocket client that implements the `vinculum.Client` interface, allowing you to connect to a vinculum WebSocket server and participate in pub/sub messaging with a single subscriber.

## Features

- **Client Interface**: Implements the `vinculum.Client` interface
- **Single Subscriber**: Simplified design with one subscriber per client
- **Fluent Builder**: Easy configuration using a fluent builder pattern
- **Graceful Connection**: Proper connection lifecycle management
- **Topic Management**: Clean subscription management with automatic server sync
- **Protocol Compliance**: Follows the vinculum WebSocket protocol specification
- **Comprehensive Testing**: Well-tested with unit tests and examples

## Quick Start

```go
package main

import (
    "context"
    "log"
    "time"

    "github.com/tsarna/vinculum/pkg/vinculum/vws/client"
    "go.uber.org/zap"
)

func main() {
    logger, _ := zap.NewDevelopment()

    // Create subscriber
    subscriber := &MySubscriber{}

    // Create client using fluent builder
    client, err := client.NewClient().
        WithURL("ws://localhost:8080/ws").
        WithLogger(logger).
        WithDialTimeout(10 * time.Second).
        WithSubscriber(subscriber).
        WithWriteChannelSize(200).  // Optional: configure buffer size
        WithAuthorization("Bearer your-token-here").  // Optional: add auth
        WithMonitor(&MyMonitor{}).  // Optional: add lifecycle monitoring
        Build()
    if err != nil {
        log.Fatal(err)
    }

    // Connect to server
    ctx := context.Background()
    if err := client.Connect(ctx); err != nil {
        log.Fatal(err)
    }
    defer client.Disconnect()

    // Use as Client
    client.Subscribe(ctx, "sensor/+/temperature")
    client.Publish(ctx, "sensor/room1/temperature", 23.5)
}
```

## Builder Options

### Required
- `WithURL(url string)`: WebSocket server URL (e.g., "ws://localhost:8080/ws")
- `WithSubscriber(subscriber vinculum.Subscriber)`: The subscriber that will receive events

### Optional  
- `WithLogger(logger *zap.Logger)`: Custom logger (defaults to nop logger)
- `WithDialTimeout(timeout time.Duration)`: Connection timeout (defaults to 30s)
- `WithWriteChannelSize(size int)`: Write channel buffer size (defaults to 100)
- `WithAuthorization(authHeader string)`: Static Authorization header value (convenience method)
- `WithAuthorizationProvider(provider AuthorizationProvider)`: Authorization provider function
- `WithHeaders(headers map[string][]string)`: Custom HTTP headers for WebSocket handshake
- `WithHeader(key, value string)`: Single HTTP header for WebSocket handshake (convenience method)
- `WithMonitor(monitor vinculum.ClientMonitor)`: Optional monitor for client lifecycle events

## Client Interface

The client implements all `vinculum.Client` methods:

```go
// Connection lifecycle
client.Connect(ctx)
client.Disconnect()

// Subscription management (simplified - no subscriber parameter needed)
client.Subscribe(ctx, topic)
client.Unsubscribe(ctx, topic) 
client.UnsubscribeAll(ctx)  // Sends UnsubscribeAll message to server

// Publishing
client.Publish(ctx, topic, payload)
client.PublishSync(ctx, topic, payload)  // Same as Publish for WebSocket

// Subscriber interface (delegates to configured subscriber)
client.OnSubscribe(ctx, topic)
client.OnUnsubscribe(ctx, topic)
client.OnEvent(ctx, topic, message, fields)
client.PassThrough(msg)
```

## Subscription Management

The client provides simple subscription management:
- **Single Subscriber**: One subscriber per client simplifies the design
- **No Local Tracking**: Client doesn't track subscriptions locally - relies on server
- **Direct Server Communication**: All subscribe/unsubscribe calls go directly to server

## Protocol Support

Supports the full vinculum WebSocket protocol:
- **Subscribe/Unsubscribe**: Topic pattern subscriptions with ACK/NACK responses
- **UnsubscribeAll**: Dedicated message kind "U" for unsubscribing from all topics
- **Event Publishing**: Publish events to topics
- **Event Receiving**: Receive events from subscribed topics
- **Error Handling**: Proper error responses and connection management

## Performance Tuning

### Write Channel Buffer Size

The `WithWriteChannelSize()` option controls the internal buffer for outgoing messages:

- **Default (100)**: Good for most applications with moderate message rates
- **Larger Buffer (500-1000)**: Better for high-throughput applications that send many messages rapidly
- **Smaller Buffer (10-50)**: Lower memory usage for applications with infrequent messaging

```go
// High-throughput configuration
client := client.NewClient().
    WithURL("ws://localhost:8080/ws").
    WithSubscriber(subscriber).
    WithWriteChannelSize(1000).  // Larger buffer for high message rates
    Build()
```

**Trade-offs:**
- **Larger buffers**: Better performance under load, but use more memory
- **Smaller buffers**: Lower memory usage, but may block on high message rates

## Authorization

The client supports authorization for WebSocket connections using a provider function pattern.

### Static Authorization (Convenience Method)

Use `WithAuthorization()` for fixed authorization headers:

```go
client := client.NewClient().
    WithURL("ws://localhost:8080/ws").
    WithSubscriber(subscriber).
    WithAuthorization("Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9...").
    Build()
```

This is a convenience method that internally creates a simple provider function.

### Dynamic Authorization (Provider Function)

Use `WithAuthorizationProvider()` for tokens that need to be refreshed or computed:

```go
// Example: JWT token refresh
tokenProvider := func(ctx context.Context) (string, error) {
    // Refresh token logic here
    token, err := refreshJWTToken(ctx)
    if err != nil {
        return "", err
    }
    return "Bearer " + token, nil
}

client := client.NewClient().
    WithURL("ws://localhost:8080/ws").
    WithSubscriber(subscriber).
    WithAuthorizationProvider(tokenProvider).
    Build()
```

### Authorization Behavior

- **Error Handling**: Provider errors prevent connection establishment
- **Context**: Provider receives the dial context (respects timeouts)
- **Header Format**: Authorization value is sent as-is in the `Authorization` header

**Example Use Cases:**
- **Static**: API keys, long-lived tokens (use `WithAuthorization()`)
- **Dynamic**: JWT tokens, OAuth2 access tokens, rotating credentials (use `WithAuthorizationProvider()`)

## Client Monitoring

The client supports optional lifecycle monitoring through the `vinculum.ClientMonitor` interface:

### Monitor Interface

```go
type ClientMonitor interface {
    OnConnect(ctx context.Context)
    OnDisconnect(ctx context.Context, err error)
    OnSubscribe(ctx context.Context, topic string)
    OnUnsubscribe(ctx context.Context, topic string)
    OnUnsubscribeAll(ctx context.Context)
}
```

### Examples

```go
// Implement a custom monitor
type MyMonitor struct {
    logger *zap.Logger
}

func (m *MyMonitor) OnConnect(ctx context.Context) {
    m.logger.Info("Client connected")
}

func (m *MyMonitor) OnDisconnect(ctx context.Context, err error) {
    if err != nil {
        m.logger.Error("Client disconnected with error", zap.Error(err))
    } else {
        m.logger.Info("Client disconnected gracefully")
    }
}

func (m *MyMonitor) OnSubscribe(ctx context.Context, topic string) {
    m.logger.Info("Subscribed to topic", zap.String("topic", topic))
}

func (m *MyMonitor) OnUnsubscribe(ctx context.Context, topic string) {
    m.logger.Info("Unsubscribed from topic", zap.String("topic", topic))
}

func (m *MyMonitor) OnUnsubscribeAll(ctx context.Context) {
    m.logger.Info("Unsubscribed from all topics")
}

// Use the monitor
client, err := client.NewClient().
    WithURL("ws://localhost:8080/ws").
    WithSubscriber(subscriber).
    WithMonitor(&MyMonitor{logger: logger}).
    Build()
```

### Event Details

- **OnConnect**: Called after successful WebSocket connection establishment
- **OnDisconnect**: Called when connection is closed
  - `err == nil`: Graceful disconnect (via `Disconnect()` method)
  - `err != nil`: Error-based disconnect (network issues, server errors, etc.)
- **OnSubscribe**: Called after successful subscription to a topic
- **OnUnsubscribe**: Called after successful unsubscription from a topic  
- **OnUnsubscribeAll**: Called after successful unsubscription from all topics

### Use Cases

- **Logging**: Track connection lifecycle and subscription changes
- **Metrics**: Collect connection and subscription statistics
- **Alerting**: Monitor for connection failures or unexpected disconnects
- **Debugging**: Trace client behavior and troubleshoot issues
- **Health Checks**: Monitor client connectivity status

## Custom Headers

The client supports setting custom HTTP headers for the WebSocket handshake request.

### Multiple Headers

Use `WithHeaders()` to set multiple headers at once:

```go
client := client.NewClient().
    WithURL("ws://localhost:8080/ws").
    WithSubscriber(subscriber).
    WithHeaders(map[string][]string{
        "X-API-Key":    {"your-api-key"},
        "User-Agent":   {"MyApp/1.0"},
        "X-Client-ID":  {"client-123"},
        "Accept":       {"application/json"},
    }).
    Build()
```

### Single Headers

Use `WithHeader()` for individual headers (convenience method):

```go
client := client.NewClient().
    WithURL("ws://localhost:8080/ws").
    WithSubscriber(subscriber).
    WithHeader("X-API-Key", "your-api-key").
    WithHeader("User-Agent", "MyApp/1.0").
    WithHeader("X-Client-ID", "client-123").
    Build()
```

### Headers with Authorization

Custom headers work alongside authorization:

```go
client := client.NewClient().
    WithURL("ws://localhost:8080/ws").
    WithSubscriber(subscriber).
    WithHeaders(map[string][]string{
        "X-API-Key":  {"your-api-key"},
        "User-Agent": {"MyApp/1.0"},
    }).
    WithAuthorization("Bearer your-token").  // Authorization takes precedence
    Build()
```

### Header Behavior

- **Merging**: Multiple `WithHeaders()` calls merge headers together
- **Overriding**: `WithHeader()` overwrites any existing header with the same name
- **Authorization Priority**: `WithAuthorization()` and `WithAuthorizationProvider()` override any custom "Authorization" header
- **Case Sensitivity**: Header names are case-sensitive (follow HTTP standards)

**Example Use Cases:**
- **API Keys**: Custom authentication schemes
- **Client Identification**: User-Agent, X-Client-ID headers
- **Content Negotiation**: Accept, Accept-Language headers
- **Tracing**: X-Trace-ID, X-Request-ID headers

## Error Handling

The client handles various error conditions gracefully:
- **Connection Failures**: Returns meaningful errors for connection issues
- **Protocol Errors**: Handles server NACK responses appropriately
- **Network Issues**: Detects and reports network problems
- **Graceful Shutdown**: Safely closes connections and cleans up resources

## Thread Safety

The client is designed to be thread-safe:
- **Concurrent Operations**: Safe to call from multiple goroutines
- **Subscription Safety**: Subscription management is properly synchronized
- **Connection Safety**: Connection state is protected with proper locking

## Testing

The package includes comprehensive tests:
- **Builder Tests**: Validate configuration and defaults
- **Lifecycle Tests**: Test connection management
- **Protocol Tests**: Verify message handling (would require integration tests)
- **Error Tests**: Validate error conditions

Run tests with:
```bash
go test ./pkg/vinculum/vws/client -v
```

## Examples

See `example_test.go` for complete usage examples showing:
- Basic client usage
- Using client as a vinculum.Client
- Subscriber implementation patterns
