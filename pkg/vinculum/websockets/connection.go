package websockets

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"github.com/coder/websocket"
	"github.com/tsarna/vinculum/pkg/vinculum"
	"github.com/tsarna/vinculum/pkg/vinculum/subutils"
	"go.uber.org/zap"
)

// ErrMessageDropped is returned by translateMessage when the transform pipeline
// drops a message. This is not a real error but a signal that the message
// should not be sent.
var ErrMessageDropped = errors.New("message dropped by transform pipeline")

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
	ctx                    context.Context
	conn                   *websocket.Conn
	eventBus               vinculum.EventBus
	logger                 *zap.Logger
	config                 *ListenerConfig
	subscriptionController SubscriptionController
	eventMsg               map[string]any

	// Async subscriber wrapper for handling outbound messages and periodic tasks
	asyncSubscriber *subutils.AsyncQueueingSubscriber

	// Synchronization for cleanup
	cleanupOnce sync.Once
}

// newConnection creates a new WebSocket connection handler that integrates with the EventBus.
//
// Parameters:
//   - ctx: Context for the connection lifecycle
//   - conn: The WebSocket connection to manage
//   - config: The ListenerConfig containing EventBus and Logger
//   - subscriptionController: The SubscriptionController for managing subscriptions
//
// Returns a new Connection instance that implements the Subscriber interface.
func newConnection(ctx context.Context, conn *websocket.Conn, config *ListenerConfig, subscriptionController SubscriptionController) *Connection {
	// Create the base connection
	baseConnection := &Connection{
		ctx:                    ctx,
		conn:                   conn,
		eventBus:               config.eventBus,
		logger:                 config.logger,
		config:                 config,
		subscriptionController: subscriptionController,
		eventMsg:               make(map[string]any), // precreated and reused to avoid allocations
	}

	// Create the subscriber wrapper chain:
	// AsyncQueueingSubscriber (async processing + periodic ticks)
	// Note: WebSocket transforms are still handled in Connection.sendMessage() for now
	asyncSubscriber := subutils.NewAsyncQueueingSubscriber(baseConnection, config.queueSize)

	// Add ticker for ping/pong if configured and start processing
	if config.pingInterval > 0 {
		asyncSubscriber = asyncSubscriber.WithTicker(config.pingInterval).Start()
	} else {
		asyncSubscriber = asyncSubscriber.Start()
	}

	// Store the async subscriber for cleanup
	baseConnection.asyncSubscriber = asyncSubscriber

	return baseConnection
}

// Start begins handling the WebSocket connection.
// This method automatically subscribes to any configured initial subscriptions
// and handles both incoming and outbound messages for the WebSocket client.
//
// This method blocks until the connection is closed, running the message reader
// directly in the calling goroutine for efficiency.
func (c *Connection) Start() {
	c.logger.Debug("Starting WebSocket connection handler")

	// Perform initial subscriptions using the async subscriber wrapper
	// (bypass subscription controller since these are server-initiated)
	for _, topic := range c.config.initialSubscriptions {
		if err := c.eventBus.Subscribe(c.ctx, c.asyncSubscriber, topic); err != nil {
			c.logger.Warn("Failed to create initial subscription",
				zap.String("topic", topic),
				zap.Error(err),
			)
		} else {
			c.logger.Debug("Created initial subscription",
				zap.String("topic", topic),
			)
		}
	}

	// No need for messageSender goroutine - handled by AsyncQueueingSubscriber
	c.logger.Debug("Async subscriber wrapper configured with transforms and ticker")

	// Run the message reader directly in this goroutine (blocks until connection closes)
	c.messageReader()

	c.logger.Debug("WebSocket connection handler stopping")
	c.cleanup()
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

	// Create EventBusMessage for error response and send via PassThrough
	responseMsg := vinculum.EventBusMessage{
		Ctx:     c.ctx,
		MsgType: vinculum.MessageTypePassThrough,
		Topic:   "",       // Error responses don't have topics
		Payload: response, // WireMessage will be JSON marshaled directly
	}

	// Send error response through PassThrough method
	sendErr := c.PassThrough(responseMsg)
	if sendErr != nil {
		c.logger.Warn("Failed to send error response",
			zap.Any("request_id", id),
			zap.String("error_message", errorMsg),
			zap.Error(sendErr),
		)
	}
}

// sendMessage sends a message over the WebSocket connection.
func (c *Connection) sendMessage(msg WebSocketMessage) error {
	var err error
	var data []byte

	rawMessage := msg.RawMessage
	if rawMessage == nil {
		rawMessage, err = c.translateMessage(msg)
		if err != nil {
			// Check if this is a "message dropped" error (not a real error)
			if errors.Is(err, ErrMessageDropped) {
				return nil // Treat as successful no-op
			}
			return err // Real translation error
		}
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

		// Unsubscribe from EventBus using the async subscriber wrapper
		err := c.eventBus.UnsubscribeAll(c.ctx, c.asyncSubscriber)
		if err != nil {
			c.logger.Warn("Failed to unsubscribe from EventBus during cleanup", zap.Error(err))
		}

		// Close the async subscriber (this will stop the background goroutine and process remaining messages)
		if c.asyncSubscriber != nil {
			err = c.asyncSubscriber.Close()
			if err != nil {
				c.logger.Warn("Failed to close async subscriber during cleanup", zap.Error(err))
			}
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

// PassThrough handles messages that should be processed directly without going through
// the normal event processing pipeline. This includes:
// - MessageTypeTick: Periodic ping messages for connection health monitoring
// - Other message types: Direct WebSocket message sending (e.g., responses)
func (c *Connection) PassThrough(msg vinculum.EventBusMessage) error {
	switch msg.MsgType {
	case vinculum.MessageTypeTick:
		return c.handleTick(msg.Ctx)
	default:
		return c.sendRawMessage(msg)
	}
}

// handleTick processes a tick message by sending a WebSocket ping for connection health monitoring
func (c *Connection) handleTick(ctx context.Context) error {
	if ctx == nil {
		ctx = c.ctx
	}

	c.logger.Debug("Sending ping to client")

	// Create ping context with write timeout
	pingCtx, cancel := context.WithTimeout(ctx, c.config.writeTimeout)
	defer cancel()

	err := c.conn.Ping(pingCtx)
	if err != nil {
		c.logger.Error("Failed to send ping", zap.Error(err))
		return err
	}

	c.logger.Debug("Ping sent successfully")
	return nil
}

// sendRawMessage sends a message directly over the WebSocket connection.
// The message payload should be a structure that can be JSON marshaled (e.g., WireMessage).
// This bypasses the event transformation pipeline and is used for responses and control messages.
func (c *Connection) sendRawMessage(msg vinculum.EventBusMessage) error {
	if msg.Payload == nil {
		c.logger.Warn("Attempted to send WebSocket message with nil payload")
		return nil // Treat as no-op
	}

	// Marshal the payload directly to JSON
	data, err := json.Marshal(msg.Payload)
	if err != nil {
		c.logger.Error("Failed to marshal WebSocket message payload",
			zap.Error(err),
			zap.Any("payload", msg.Payload),
		)
		return err
	}

	// Create write context with timeout
	writeCtx, cancel := context.WithTimeout(msg.Ctx, c.config.writeTimeout)
	defer cancel()

	// Send over WebSocket with write deadline
	err = c.conn.Write(writeCtx, websocket.MessageText, data)
	if err != nil {
		c.logger.Error("Failed to send WebSocket message",
			zap.Error(err),
			zap.String("topic", msg.Topic),
		)
		return err
	}

	c.logger.Debug("WebSocket message sent successfully",
		zap.String("topic", msg.Topic),
		zap.Any("payload", msg.Payload),
	)
	return nil
}

// OnEvent is called when an event is published to a topic this connection is subscribed to.
// This method forwards the event to the WebSocket client.
// Note: This method is called directly by the AsyncQueueingSubscriber wrapper,
// so it sends messages directly without additional queuing.
func (c *Connection) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	c.logger.Debug("Forwarding event to WebSocket client",
		zap.String("topic", topic),
		zap.Any("message", message),
		zap.Any("fields", fields),
	)

	// Create event message for WebSocket client
	msg := WebSocketMessage{
		Ctx:     ctx,
		Topic:   topic,
		Message: message,
		Fields:  fields,
	}

	// Send message directly (we're already in the async subscriber's goroutine)
	err := c.sendMessage(msg)
	if err != nil {
		c.logger.Error("Failed to send WebSocket event message",
			zap.Error(err),
			zap.String("topic", topic),
		)
		return err
	}

	return nil
}

// applyMessageTransforms applies the configured message transformation pipeline
// to an outbound WebSocket message. Transform functions are called in order
// until one returns false for continue or the message becomes nil.
func (c *Connection) applyMessageTransforms(msg *WebSocketMessage) *WebSocketMessage {
	if len(c.config.messageTransforms) == 0 {
		return msg // No transforms configured
	}

	current := msg
	for i, transform := range c.config.messageTransforms {
		if current == nil {
			// Previous transform dropped the message, stop processing
			c.logger.Debug("Message transform pipeline stopped due to nil message",
				zap.Int("transform_index", i),
			)
			break
		}

		transformed, continueProcessing := transform(current)
		current = transformed

		if current == nil {
			// Transform dropped the message
			c.logger.Debug("Message dropped by transform function",
				zap.Int("transform_index", i),
			)
			break
		}

		if !continueProcessing {
			// Transform requested to stop processing
			c.logger.Debug("Message transform pipeline stopped by transform function",
				zap.Int("transform_index", i),
			)
			break
		}
	}

	return current
}

func (c *Connection) translateMessage(msg WebSocketMessage) (any, error) {
	// Apply message transformation pipeline
	transformedMsg := c.applyMessageTransforms(&msg)
	if transformedMsg == nil {
		// Message was dropped by transform pipeline
		c.logger.Debug("Message dropped by transform pipeline",
			zap.String("topic", msg.Topic),
		)
		return nil, ErrMessageDropped
	}

	// Use the transformed message for translation
	c.eventMsg["t"] = transformedMsg.Topic
	c.eventMsg["d"] = transformedMsg.Message

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

	// Create EventBusMessage for response and send via PassThrough
	responseMsg := vinculum.EventBusMessage{
		Ctx:     ctx,
		MsgType: vinculum.MessageTypePassThrough,
		Topic:   "",       // Responses don't have topics
		Payload: response, // WireMessage will be JSON marshaled directly
	}

	// Send response through PassThrough method
	sendErr := c.PassThrough(responseMsg)
	if sendErr != nil {
		c.logger.Warn("Failed to send response message",
			zap.String("response_kind", response.Kind),
			zap.Any("request_id", request.Id),
			zap.Error(sendErr),
		)
	}
}

func handleRequest(c *Connection, ctx context.Context, request WireMessage) {
	var err error

	switch request.Kind {
	case MessageKindEvent:
		if request.Topic == "" {
			err = fmt.Errorf("topic is required")
		} else if request.Data == nil {
			err = fmt.Errorf("data is required")
		} else {
			// Apply event authorization
			modifiedMsg, authErr := c.config.eventAuth(ctx, &request)

			if authErr != nil {
				// Event denied by authorization function
				err = authErr
			} else if modifiedMsg == nil {
				// Event silently dropped by authorization function (nil return with no error)
				// Don't publish anything, but still send ACK response (err remains nil)
			} else {
				// Event authorized, use modified message if provided
				msgToPublish := modifiedMsg

				// Publish to EventBus
				err = c.eventBus.Publish(ctx, msgToPublish.Topic, msgToPublish.Data)
			}
		}

		if request.Id == nil {
			return
		}
	case MessageKindAck:
		err = nil
	case MessageKindSubscribe:
		// Use subscription controller to validate/modify the subscription
		err = c.subscriptionController.Subscribe(ctx, c, request.Topic)
		if err == nil {
			// Controller approved, perform the actual subscription
			err = c.eventBus.Subscribe(ctx, c, request.Topic)
		}
	case MessageKindUnsubscribe:
		// Use subscription controller to validate/modify the unsubscription
		err = c.subscriptionController.Unsubscribe(ctx, c, request.Topic)
		if err == nil {
			// Controller approved, perform the actual unsubscription
			err = c.eventBus.Unsubscribe(ctx, c, request.Topic)
		}
	default:
		err = fmt.Errorf("unsupported request type: %s", request.Kind)
	}

	c.respondToRequest(ctx, request, err)
}
