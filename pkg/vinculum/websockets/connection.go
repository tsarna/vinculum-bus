package websockets

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/tsarna/vinculum/pkg/vinculum"
	"go.uber.org/zap"
)

// WebSocketMessage represents a message to be sent over the WebSocket connection.
type WebSocketMessage struct {
	Ctx        context.Context
	Topic      string
	RawMessage any
	Message    any
	Fields     map[string]string
}

// Connection represents an individual WebSocket connection that integrates with the EventBus.
// It implements the Subscriber interface to receive events from the EventBus and forwards
// them to the WebSocket client. It also handles incoming messages from the WebSocket client
// and can publish them to the EventBus.
type Connection struct {
	ctx      context.Context
	conn     *websocket.Conn
	eventBus vinculum.EventBus
	logger   *zap.Logger
	config   *ListenerConfig
	eventMsg map[string]any

	// Channel for outbound messages to avoid blocking EventBus
	outbound chan WebSocketMessage
	done     chan struct{}

	// Synchronization for cleanup
	cleanupOnce sync.Once
}

// newConnection creates a new WebSocket connection handler that integrates with the EventBus.
//
// Parameters:
//   - ctx: Context for the connection lifecycle
//   - conn: The WebSocket connection to manage
//   - config: The ListenerConfig containing EventBus and Logger
//
// Returns a new Connection instance that implements the Subscriber interface.
func newConnection(ctx context.Context, conn *websocket.Conn, config *ListenerConfig) *Connection {
	return &Connection{
		ctx:      ctx,
		conn:     conn,
		eventBus: config.eventBus,
		logger:   config.logger,
		config:   config,
		eventMsg: make(map[string]any),                          // precreated and reused to avoid allocations
		outbound: make(chan WebSocketMessage, config.queueSize), // Buffered channel to prevent blocking
		done:     make(chan struct{}),
	}
}

// Start begins handling the WebSocket connection.
// This method automatically subscribes to all topics ("#") and handles
// both incoming and outbound messages for the WebSocket client.
//
// This method blocks until the connection is closed, running the message reader
// directly in the calling goroutine for efficiency.
func (c *Connection) Start() {
	c.logger.Debug("Starting WebSocket connection handler")

	// Start the message sender goroutine for outbound messages and ping handling
	go c.messageSender()

	// Run the message reader directly in this goroutine (blocks until connection closes)
	c.messageReader()

	c.logger.Debug("WebSocket connection handler stopping")
	c.cleanup()
}

// messageSender runs in a goroutine and handles sending messages to the WebSocket client
// and periodic ping frames for connection health monitoring.
// This prevents blocking the EventBus when sending messages and ensures all WebSocket
// writes are serialized through a single goroutine.
func (c *Connection) messageSender() {
	defer c.logger.Debug("Message sender goroutine stopped")

	// Set up ping ticker if ping/pong is enabled
	var pingTicker *time.Ticker
	var pingChan <-chan time.Time

	if c.config.pingInterval > 0 {
		pingTicker = time.NewTicker(c.config.pingInterval)
		pingChan = pingTicker.C
		c.logger.Debug("Ping/pong health monitoring enabled",
			zap.Duration("interval", c.config.pingInterval))
		defer pingTicker.Stop()
	} else {
		c.logger.Debug("Ping/pong health monitoring disabled")
	}

	for {
		select {
		case msg, ok := <-c.outbound:
			if !ok {
				// Channel closed, exit gracefully
				return
			}

			err := c.sendMessage(msg)
			if err != nil {
				c.logger.Error("Failed to send WebSocket message",
					zap.Error(err),
					zap.String("topic", msg.Topic),
				)

				// Check if it's a connection error that requires cleanup
				if websocket.CloseStatus(err) != -1 {
					c.logger.Debug("WebSocket connection closed, stopping sender")
					return
				}
				// For other errors, continue trying to send other messages
			}

		case <-pingChan:
			// Send ping for connection health monitoring
			c.logger.Debug("Sending ping to client")

			// Create ping context with write timeout
			pingCtx, cancel := context.WithTimeout(c.ctx, c.config.writeTimeout)
			err := c.conn.Ping(pingCtx)
			cancel()

			if err != nil {
				c.logger.Error("Failed to send ping", zap.Error(err))
				return
			}
			c.logger.Debug("Ping sent successfully")

		case <-c.done:
			// Cleanup signal received
			return

		case <-c.ctx.Done():
			// Context cancelled
			return
		}
	}
}

// messageReader handles reading messages from the WebSocket client.
// It parses incoming JSON messages and delegates to handleRequest for processing.
// This method blocks until the connection is closed or an error occurs.
func (c *Connection) messageReader() {
	defer c.logger.Debug("Message reader stopped")

	// Set read limit to prevent large message attacks
	c.conn.SetReadLimit(32768) // 32KB max message size

	for {
		// Check context before each read
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		// Set read deadline before each read operation
		readCtx, cancel := context.WithTimeout(c.ctx, c.config.readTimeout)

		// Read message from WebSocket with timeout context
		_, data, err := c.conn.Read(readCtx)
		cancel() // Always cancel to free resources
		if err != nil {
			closeStatus := websocket.CloseStatus(err)
			if closeStatus != -1 {
				// WebSocket closed with a close frame
				c.logger.Debug("WebSocket connection closed by client",
					zap.Int("close_status", int(closeStatus)),
				)
			} else {
				// Read error (network issue, etc.)
				c.logger.Error("Failed to read WebSocket message", zap.Error(err))
			}
			return
		}

		// Validate message size
		if len(data) == 0 {
			c.logger.Debug("Received empty WebSocket message, ignoring")
			continue
		}

		// Parse JSON message
		var request WireMessage
		if err := json.Unmarshal(data, &request); err != nil {
			c.logger.Warn("Failed to parse incoming WebSocket message",
				zap.Error(err),
				zap.String("raw_data", string(data)),
				zap.Int("data_length", len(data)),
			)
			// Send error response if message has an ID
			c.sendErrorResponse("", "Invalid JSON format")
			continue
		}

		c.logger.Debug("Received WebSocket message",
			zap.String("kind", request.Kind),
			zap.String("topic", request.Topic),
			zap.Any("id", request.Id),
		)

		handleRequest(c, c.ctx, request)
	}
}

// sendErrorResponse sends a NACK response for malformed requests
func (c *Connection) sendErrorResponse(id any, errorMsg string) {
	response := WireMessage{
		Kind:  MessageKindNack,
		Id:    id,
		Error: errorMsg,
	}

	msg := WebSocketMessage{
		Ctx:        c.ctx,
		RawMessage: response,
	}

	select {
	case c.outbound <- msg:
		// Message queued successfully
	default:
		// Channel full, log warning
		c.logger.Warn("Outbound channel full, dropping error response")
	}
}

// sendMessage sends a message over the WebSocket connection.
func (c *Connection) sendMessage(msg WebSocketMessage) error {
	var err error
	var data []byte

	rawMessage := msg.RawMessage
	if rawMessage == nil {
		rawMessage, err = c.translateMessage(msg)
	}

	if err == nil {
		data, err = json.Marshal(rawMessage)
	}

	if err == nil {
		// Create write context with timeout
		writeCtx, cancel := context.WithTimeout(msg.Ctx, c.config.writeTimeout)
		defer cancel()

		// Send over WebSocket with write deadline
		return c.conn.Write(writeCtx, websocket.MessageText, data)
	} else {
		return err
	}
}

// cleanup handles connection cleanup when the connection is closing.
// Uses sync.Once to ensure cleanup only happens once, even if called multiple times.
func (c *Connection) cleanup() {
	c.cleanupOnce.Do(func() {
		c.logger.Debug("Cleaning up WebSocket connection")

		// Signal message sender to stop (safe to close multiple times)
		select {
		case <-c.done:
			// Already closed
		default:
			close(c.done)
		}

		// Unsubscribe from EventBus
		err := c.eventBus.UnsubscribeAll(c.ctx, c)
		if err != nil {
			c.logger.Warn("Failed to unsubscribe from EventBus during cleanup", zap.Error(err))
		}

		// Close outbound channel (safe to close multiple times with select)
		select {
		case <-c.outbound:
			// Channel already closed or empty
		default:
			close(c.outbound)
		}

		// Close WebSocket connection gracefully (if not already closed)
		err = c.conn.Close(websocket.StatusNormalClosure, "Connection closed")
		if err != nil {
			// This is expected if the connection was already closed (e.g., by shutdownClose)
			c.logger.Debug("WebSocket close error (may be expected)", zap.Error(err))
		}

		c.logger.Debug("WebSocket connection cleanup completed")
	})
}

// shutdownClose closes the WebSocket connection with a specific close code and reason.
// This is used during graceful shutdown to properly inform clients of the shutdown reason.
func (c *Connection) shutdownClose(code websocket.StatusCode, reason string) {
	c.logger.Debug("Closing connection for shutdown",
		zap.Int("close_code", int(code)),
		zap.String("reason", reason),
	)

	// Close the WebSocket with the specified code and reason
	// This will cause the messageReader to exit with an error, which will
	// trigger cleanup through the normal Start() -> messageReader() -> cleanup() flow
	err := c.conn.Close(code, reason)
	if err != nil {
		c.logger.Debug("Error closing WebSocket during shutdown", zap.Error(err))
	}
}

func (c *Connection) OnSubscribe(ctx context.Context, topic string) error {
	return nil
}

func (c *Connection) OnUnsubscribe(ctx context.Context, topic string) error {
	return nil
}

// OnEvent is called when an event is published to a topic this connection is subscribed to.
// This method forwards the event to the WebSocket client.
func (c *Connection) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	c.logger.Debug("Forwarding event to WebSocket client",
		zap.String("topic", topic),
		zap.Any("message", message),
		zap.Any("fields", fields),
	)

	// Create event message for WebSocket client (non-blocking)
	msg := WebSocketMessage{
		Ctx:     ctx,
		Topic:   topic,
		Message: message,
		Fields:  fields,
	}

	select {
	case c.outbound <- msg:
		// Message queued successfully
	default:
		// Channel full, log warning but don't block EventBus
		c.logger.Warn("Outbound channel full, dropping event message",
			zap.String("topic", topic),
		)
	}

	return nil
}

func (c *Connection) translateMessage(msg WebSocketMessage) (any, error) {
	c.eventMsg["t"] = msg.Topic
	c.eventMsg["d"] = msg.Message

	return c.eventMsg, nil
}

func (c *Connection) respondToRequest(ctx context.Context, request WireMessage, err error) {
	response := WireMessage{
		Kind: MessageKindAck,
		Id:   request.Id,
	}
	if err != nil {
		response.Kind = MessageKindNack
		response.Error = err.Error()
	}

	msg := WebSocketMessage{
		Ctx:        ctx,
		RawMessage: response,
	}

	c.sendMessage(msg)
}

func handleRequest(c *Connection, ctx context.Context, request WireMessage) {
	var err error

	switch request.Kind {
	case MessageKindEvent:
		if request.Topic == "" {
			err = fmt.Errorf("topic is required")
		} else if request.Data == nil {
			err = fmt.Errorf("data is required")
		}

		if err == nil {
			err = c.eventBus.Publish(ctx, request.Topic, request.Data)
		}

		if request.Id == nil {
			return
		}
	case MessageKindAck:
		err = nil
	case MessageKindSubscribe:
		err = c.eventBus.Subscribe(ctx, c, request.Topic)
	case MessageKindUnsubscribe:
		err = c.eventBus.Unsubscribe(ctx, c, request.Topic)
	default:
		err = fmt.Errorf("unsupported request type: %s", request.Kind)
	}

	c.respondToRequest(ctx, request, err)
}
