package websockets

import (
	"fmt"
	"time"

	"github.com/tsarna/vinculum/pkg/vinculum"
	"go.uber.org/zap"
)

// ListenerConfig holds the configuration for creating a WebSocket Listener.
// Use NewListenerConfig() to create a new configuration and chain methods
// to set the required parameters before calling Build().
type ListenerConfig struct {
	eventBus     vinculum.EventBus
	logger       *zap.Logger
	queueSize    int
	pingInterval time.Duration
	readTimeout  time.Duration
	writeTimeout time.Duration
}

const (
	// DefaultQueueSize is the default size for the WebSocket message queue.
	// This provides a good balance between memory usage and burst handling capacity.
	DefaultQueueSize = 256

	// DefaultPingInterval is the default interval for sending WebSocket ping frames.
	// This helps detect dead connections and maintain connection health.
	DefaultPingInterval = 30 * time.Second

	// DefaultReadTimeout is the default timeout for reading messages from clients.
	// Should be longer than ping interval to allow for pong responses.
	DefaultReadTimeout = 60 * time.Second

	// DefaultWriteTimeout is the default timeout for writing messages to clients.
	// Should be short enough to detect slow/dead clients quickly.
	DefaultWriteTimeout = 10 * time.Second
)

// NewListenerConfig creates a new ListenerConfig for building a WebSocket Listener.
// Use the fluent methods to set the required EventBus and Logger, then call Build().
//
// Example:
//
//	listener, err := websockets.NewListenerConfig().
//	    WithEventBus(eventBus).
//	    WithLogger(logger).
//	    WithQueueSize(512).
//	    WithPingInterval(45 * time.Second).
//	    Build()
func NewListenerConfig() *ListenerConfig {
	return &ListenerConfig{
		queueSize:    DefaultQueueSize,
		pingInterval: DefaultPingInterval,
		readTimeout:  DefaultReadTimeout,
		writeTimeout: DefaultWriteTimeout,
	}
}

// WithEventBus sets the EventBus for the WebSocket Listener.
// The EventBus is required for integrating WebSocket connections with the pub/sub system.
func (c *ListenerConfig) WithEventBus(eventBus vinculum.EventBus) *ListenerConfig {
	c.eventBus = eventBus
	return c
}

// WithLogger sets the Logger for the WebSocket Listener.
// The Logger is required for connection events, errors, and debugging.
func (c *ListenerConfig) WithLogger(logger *zap.Logger) *ListenerConfig {
	c.logger = logger
	return c
}

// WithQueueSize sets the message queue size for WebSocket connections.
// This controls how many messages can be buffered per connection before
// messages start getting dropped. Larger values handle bursts better but
// use more memory. Must be positive.
//
// Default: 256 messages per connection
func (c *ListenerConfig) WithQueueSize(size int) *ListenerConfig {
	if size > 0 {
		c.queueSize = size
	}
	return c
}

// WithPingInterval sets the interval for sending WebSocket ping frames.
// This helps detect dead connections and maintain connection health.
// Must be positive. Set to 0 to disable ping/pong health monitoring.
//
// Default: 30 seconds
func (c *ListenerConfig) WithPingInterval(interval time.Duration) *ListenerConfig {
	if interval >= 0 {
		c.pingInterval = interval
	}
	return c
}

// WithReadTimeout sets the timeout for reading messages from WebSocket clients.
// This prevents connections from hanging indefinitely when clients stop responding.
// Should be longer than ping interval to allow for pong responses.
//
// Default: 60 seconds
func (c *ListenerConfig) WithReadTimeout(timeout time.Duration) *ListenerConfig {
	if timeout > 0 {
		c.readTimeout = timeout
	}
	return c
}

// WithWriteTimeout sets the timeout for writing messages to WebSocket clients.
// This prevents the server from hanging when clients are slow to receive data.
// Should be short enough to detect slow/dead clients quickly.
//
// Default: 10 seconds
func (c *ListenerConfig) WithWriteTimeout(timeout time.Duration) *ListenerConfig {
	if timeout > 0 {
		c.writeTimeout = timeout
	}
	return c
}

// IsValid checks if the configuration has all required parameters set.
// Returns nil if the configuration is valid, or an error describing what's missing.
func (c *ListenerConfig) IsValid() error {
	var missing []string
	if c.eventBus == nil {
		missing = append(missing, "EventBus")
	}
	if c.logger == nil {
		missing = append(missing, "Logger")
	}

	if len(missing) > 0 {
		return fmt.Errorf("invalid listener configuration, missing: %v", missing)
	}

	return nil
}

// Build creates a new WebSocket Listener from the configuration.
// Returns an error if the configuration is invalid (missing EventBus or Logger).
//
// The returned Listener is ready to accept WebSocket connections and integrate
// them with the EventBus for real-time message streaming.
func (c *ListenerConfig) Build() (*Listener, error) {
	if err := c.IsValid(); err != nil {
		return nil, err
	}

	return newListener(c), nil
}
