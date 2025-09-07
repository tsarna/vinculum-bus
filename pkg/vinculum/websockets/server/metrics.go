package server

import (
	"context"
	"time"

	"github.com/tsarna/vinculum/pkg/vinculum"
)

// WebSocketMetrics defines the standard metrics collected by the WebSocket server.
// This struct holds references to all the metric instruments used for monitoring
// WebSocket server performance and behavior.
type WebSocketMetrics struct {
	// Connection metrics
	activeConnections  vinculum.Gauge     // Current number of active WebSocket connections
	totalConnections   vinculum.Counter   // Total number of connections established
	connectionDuration vinculum.Histogram // Duration of WebSocket connections
	connectionErrors   vinculum.Counter   // Number of connection errors (upgrade failures, etc.)

	// Message metrics
	messagesReceived vinculum.Counter   // Total messages received from clients
	messagesSent     vinculum.Counter   // Total messages sent to clients
	messageErrors    vinculum.Counter   // Number of message processing errors
	messageSize      vinculum.Histogram // Size distribution of messages (bytes)

	// Request metrics (by message kind)
	requestsTotal   vinculum.Counter   // Total requests by kind (subscribe, unsubscribe, event, etc.)
	requestDuration vinculum.Histogram // Request processing duration
	requestErrors   vinculum.Counter   // Request errors by kind

	// Health metrics
	pingsSent     vinculum.Counter // Number of ping frames sent
	pongTimeouts  vinculum.Counter // Number of pong timeouts (dead connections)
	writeTimeouts vinculum.Counter // Number of write timeouts
}

// NewWebSocketMetrics creates a new WebSocketMetrics instance using the provided MetricsProvider.
// If the provider is nil, returns nil (no metrics will be collected).
func NewWebSocketMetrics(provider vinculum.MetricsProvider) *WebSocketMetrics {
	if provider == nil {
		return nil
	}

	return &WebSocketMetrics{
		// Connection metrics
		activeConnections:  provider.Gauge("websocket_active_connections"),
		totalConnections:   provider.Counter("websocket_connections_total"),
		connectionDuration: provider.Histogram("websocket_connection_duration_seconds"),
		connectionErrors:   provider.Counter("websocket_connection_errors_total"),

		// Message metrics
		messagesReceived: provider.Counter("websocket_messages_received_total"),
		messagesSent:     provider.Counter("websocket_messages_sent_total"),
		messageErrors:    provider.Counter("websocket_message_errors_total"),
		messageSize:      provider.Histogram("websocket_message_size_bytes"),

		// Request metrics
		requestsTotal:   provider.Counter("websocket_requests_total"),
		requestDuration: provider.Histogram("websocket_request_duration_seconds"),
		requestErrors:   provider.Counter("websocket_request_errors_total"),

		// Health metrics
		pingsSent:     provider.Counter("websocket_pings_sent_total"),
		pongTimeouts:  provider.Counter("websocket_pong_timeouts_total"),
		writeTimeouts: provider.Counter("websocket_write_timeouts_total"),
	}
}

// Connection lifecycle metrics

// RecordConnectionStart records when a new WebSocket connection is established.
func (m *WebSocketMetrics) RecordConnectionStart(ctx context.Context) {
	if m == nil {
		return
	}
	m.totalConnections.Add(ctx, 1)
}

// RecordConnectionActive updates the active connection count.
func (m *WebSocketMetrics) RecordConnectionActive(ctx context.Context, count int) {
	if m == nil {
		return
	}
	m.activeConnections.Set(ctx, float64(count))
}

// RecordConnectionEnd records when a WebSocket connection ends and its duration.
func (m *WebSocketMetrics) RecordConnectionEnd(ctx context.Context, duration time.Duration) {
	if m == nil {
		return
	}
	m.connectionDuration.Record(ctx, duration.Seconds())
}

// RecordConnectionError records connection-level errors (upgrade failures, etc.).
func (m *WebSocketMetrics) RecordConnectionError(ctx context.Context, errorType string) {
	if m == nil {
		return
	}
	m.connectionErrors.Add(ctx, 1, vinculum.Label{Key: "error_type", Value: errorType})
}

// Message metrics

// RecordMessageReceived records when a message is received from a client.
func (m *WebSocketMetrics) RecordMessageReceived(ctx context.Context, sizeBytes int, messageKind string) {
	if m == nil {
		return
	}
	m.messagesReceived.Add(ctx, 1, vinculum.Label{Key: "kind", Value: messageKind})
	m.messageSize.Record(ctx, float64(sizeBytes), vinculum.Label{Key: "direction", Value: "received"})
}

// RecordMessageSent records when a message is sent to a client.
func (m *WebSocketMetrics) RecordMessageSent(ctx context.Context, sizeBytes int, messageType string) {
	if m == nil {
		return
	}
	m.messagesSent.Add(ctx, 1, vinculum.Label{Key: "type", Value: messageType})
	m.messageSize.Record(ctx, float64(sizeBytes), vinculum.Label{Key: "direction", Value: "sent"})
}

// RecordMessageError records message processing errors.
func (m *WebSocketMetrics) RecordMessageError(ctx context.Context, errorType string, messageKind string) {
	if m == nil {
		return
	}
	labels := []vinculum.Label{
		{Key: "error_type", Value: errorType},
		{Key: "kind", Value: messageKind},
	}
	m.messageErrors.Add(ctx, 1, labels...)
}

// Request metrics

// RecordRequest records the start of a request and returns a function to record completion.
// Usage:
//
//	recordCompletion := metrics.RecordRequest(ctx, "subscribe")
//	defer recordCompletion(err)
func (m *WebSocketMetrics) RecordRequest(ctx context.Context, requestKind string) func(error) {
	if m == nil {
		return func(error) {} // No-op function
	}

	startTime := time.Now()
	m.requestsTotal.Add(ctx, 1, vinculum.Label{Key: "kind", Value: requestKind})

	return func(err error) {
		duration := time.Since(startTime)
		m.requestDuration.Record(ctx, duration.Seconds(), vinculum.Label{Key: "kind", Value: requestKind})

		if err != nil {
			labels := []vinculum.Label{
				{Key: "kind", Value: requestKind},
				{Key: "error", Value: err.Error()},
			}
			m.requestErrors.Add(ctx, 1, labels...)
		}
	}
}

// Health metrics

// RecordPingSent records when a ping frame is sent to a client.
func (m *WebSocketMetrics) RecordPingSent(ctx context.Context) {
	if m == nil {
		return
	}
	m.pingsSent.Add(ctx, 1)
}

// RecordPongTimeout records when a client fails to respond to a ping (dead connection).
func (m *WebSocketMetrics) RecordPongTimeout(ctx context.Context) {
	if m == nil {
		return
	}
	m.pongTimeouts.Add(ctx, 1)
}

// RecordWriteTimeout records when a write operation times out.
func (m *WebSocketMetrics) RecordWriteTimeout(ctx context.Context) {
	if m == nil {
		return
	}
	m.writeTimeouts.Add(ctx, 1)
}
