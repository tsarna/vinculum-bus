package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/tsarna/vinculum/pkg/vinculum"
	"github.com/tsarna/vinculum/pkg/vinculum/websockets"
	"go.uber.org/zap"
)

// Client implements the vinculum.Client interface over a WebSocket connection.
// It connects to a vinculum WebSocket server and provides pub/sub functionality.
type Client struct {
	// Configuration
	url              string
	logger           *zap.Logger
	dialTimeout      time.Duration
	subscriber       vinculum.Subscriber
	writeChannelSize int
	authProvider     AuthorizationProvider // Authorization provider
	headers          map[string][]string   // Custom HTTP headers for WebSocket handshake

	// Connection state
	conn     *websocket.Conn
	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.RWMutex
	started  int32
	stopping int32

	// Message handling
	messageID   int64
	pendingReqs map[int64]chan response
	pendingMu   sync.Mutex

	// Internal channels
	writeChannel chan []byte
	done         chan struct{}
}

// response represents a response to a request
type response struct {
	Success bool
	Error   string
}

// Connect establishes the WebSocket connection and starts message processing.
func (c *Client) Connect(ctx context.Context) error {
	if !atomic.CompareAndSwapInt32(&c.started, 0, 1) {
		return fmt.Errorf("client is already started")
	}

	c.ctx, c.cancel = context.WithCancel(ctx)
	c.done = make(chan struct{})
	c.writeChannel = make(chan []byte, c.writeChannelSize)

	c.pendingMu.Lock()
	c.pendingReqs = make(map[int64]chan response)
	c.pendingMu.Unlock()

	// Parse URL
	_, err := url.Parse(c.url)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Create context with timeout for dialing
	dialCtx, dialCancel := context.WithTimeout(c.ctx, c.dialTimeout)
	defer dialCancel()

	// Prepare dial options with custom headers and authorization
	dialOptions := &websocket.DialOptions{}

	// Start with custom headers if configured
	if c.headers != nil {
		dialOptions.HTTPHeader = make(map[string][]string)
		for key, values := range c.headers {
			dialOptions.HTTPHeader[key] = values
		}
	}

	// Set authorization header if configured (this may override a custom Authorization header)
	if c.authProvider != nil {
		authValue, err := c.authProvider(dialCtx)
		if err != nil {
			return fmt.Errorf("failed to get authorization: %w", err)
		}
		if authValue != "" {
			if dialOptions.HTTPHeader == nil {
				dialOptions.HTTPHeader = make(map[string][]string)
			}
			dialOptions.HTTPHeader["Authorization"] = []string{authValue}
		}
	}

	// Connect to WebSocket
	conn, _, err := websocket.Dial(dialCtx, c.url, dialOptions)
	if err != nil {
		return fmt.Errorf("failed to connect to WebSocket: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	c.logger.Info("WebSocket client connected", zap.String("url", c.url))

	// Start message processing goroutines
	go c.readLoop()
	go c.writeLoop()

	return nil
}

// Disconnect closes the WebSocket connection and stops message processing.
func (c *Client) Disconnect() error {
	if !atomic.CompareAndSwapInt32(&c.stopping, 0, 1) {
		return nil // Already stopping
	}

	c.logger.Info("Disconnecting WebSocket client")

	// Cancel context to signal shutdown
	if c.cancel != nil {
		c.cancel()
	}

	// Close connection
	c.mu.Lock()
	if c.conn != nil {
		c.conn.Close(websocket.StatusNormalClosure, "client disconnect")
		c.conn = nil
	}
	c.mu.Unlock()

	// Wait for goroutines to finish
	if c.done != nil {
		<-c.done
	}

	atomic.StoreInt32(&c.started, 0)
	atomic.StoreInt32(&c.stopping, 0)

	c.logger.Info("WebSocket client disconnected")
	return nil
}

// Client interface implementation

// Subscribe implements Client.Subscribe
func (c *Client) Subscribe(ctx context.Context, topic string) error {
	if atomic.LoadInt32(&c.started) == 0 {
		return fmt.Errorf("client is not connected")
	}

	// Send subscription to server
	msg := websockets.WireMessage{
		Kind:  websockets.MessageKindSubscribe,
		Topic: topic,
		Id:    c.nextMessageID(),
	}

	if err := c.sendMessage(ctx, msg); err != nil {
		return err
	}

	// Notify subscriber
	return c.subscriber.OnSubscribe(ctx, topic)
}

// Unsubscribe implements Client.Unsubscribe
func (c *Client) Unsubscribe(ctx context.Context, topic string) error {
	if atomic.LoadInt32(&c.started) == 0 {
		return fmt.Errorf("client is not connected")
	}

	// Send unsubscription to server
	msg := websockets.WireMessage{
		Kind:  websockets.MessageKindUnsubscribe,
		Topic: topic,
		Id:    c.nextMessageID(),
	}

	if err := c.sendMessage(ctx, msg); err != nil {
		return err
	}

	// Notify subscriber
	return c.subscriber.OnUnsubscribe(ctx, topic)
}

// UnsubscribeAll implements Client.UnsubscribeAll
func (c *Client) UnsubscribeAll(ctx context.Context) error {
	if atomic.LoadInt32(&c.started) == 0 {
		return fmt.Errorf("client is not connected")
	}

	// Send unsubscribe all to server
	msg := websockets.WireMessage{
		Kind: websockets.MessageKindUnsubscribeAll,
		Id:   c.nextMessageID(),
	}

	return c.sendMessage(ctx, msg)
}

// Publish implements Client.Publish
func (c *Client) Publish(ctx context.Context, topic string, payload any) error {
	if atomic.LoadInt32(&c.started) == 0 {
		return fmt.Errorf("client is not connected")
	}

	msg := websockets.WireMessage{
		Kind:  websockets.MessageKindEvent,
		Topic: topic,
		Data:  payload,
	}

	return c.sendMessageNoResponse(msg)
}

// PublishSync implements Client.PublishSync - same as Publish for WebSocket client
func (c *Client) PublishSync(ctx context.Context, topic string, payload any) error {
	return c.Publish(ctx, topic, payload)
}

// Subscriber interface implementation - delegate to the configured subscriber

// OnSubscribe implements Subscriber.OnSubscribe
func (c *Client) OnSubscribe(ctx context.Context, topic string) error {
	return c.subscriber.OnSubscribe(ctx, topic)
}

// OnUnsubscribe implements Subscriber.OnUnsubscribe
func (c *Client) OnUnsubscribe(ctx context.Context, topic string) error {
	return c.subscriber.OnUnsubscribe(ctx, topic)
}

// OnEvent implements Subscriber.OnEvent
func (c *Client) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	return c.subscriber.OnEvent(ctx, topic, message, fields)
}

// PassThrough implements Subscriber.PassThrough
func (c *Client) PassThrough(msg vinculum.EventBusMessage) error {
	return c.subscriber.PassThrough(msg)
}

// sendMessage sends a message and waits for ACK/NACK response
func (c *Client) sendMessage(ctx context.Context, msg websockets.WireMessage) error {
	msgID := msg.Id.(int64)

	// Create response channel
	respChan := make(chan response, 1)
	c.pendingMu.Lock()
	c.pendingReqs[msgID] = respChan
	c.pendingMu.Unlock()

	// Send message
	data, err := json.Marshal(msg)
	if err != nil {
		c.cleanupPendingRequest(msgID)
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	select {
	case c.writeChannel <- data:
	case <-ctx.Done():
		c.cleanupPendingRequest(msgID)
		return ctx.Err()
	case <-c.ctx.Done():
		c.cleanupPendingRequest(msgID)
		return c.ctx.Err()
	}

	// Wait for response
	select {
	case resp := <-respChan:
		if !resp.Success {
			return fmt.Errorf("server error: %s", resp.Error)
		}
		return nil
	case <-ctx.Done():
		c.cleanupPendingRequest(msgID)
		return ctx.Err()
	case <-c.ctx.Done():
		c.cleanupPendingRequest(msgID)
		return c.ctx.Err()
	}
}

// sendMessageNoResponse sends a message without waiting for a response
func (c *Client) sendMessageNoResponse(msg websockets.WireMessage) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	select {
	case c.writeChannel <- data:
		return nil
	case <-c.ctx.Done():
		return c.ctx.Err()
	default:
		return fmt.Errorf("write channel is full")
	}
}

// nextMessageID generates the next message ID
func (c *Client) nextMessageID() int64 {
	return atomic.AddInt64(&c.messageID, 1)
}

// cleanupPendingRequest removes a pending request and returns the channel if it existed
func (c *Client) cleanupPendingRequest(msgID int64) (chan response, bool) {
	c.pendingMu.Lock()
	defer c.pendingMu.Unlock()

	respChan, exists := c.pendingReqs[msgID]
	if exists {
		delete(c.pendingReqs, msgID)
	}
	return respChan, exists
}

// readLoop processes incoming messages from the WebSocket
func (c *Client) readLoop() {
	defer close(c.done)

	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return
	}

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
		}

		_, data, err := conn.Read(c.ctx)
		if err != nil {
			if c.ctx.Err() == nil {
				c.logger.Error("Failed to read from WebSocket", zap.Error(err))
			}
			return
		}

		c.handleMessage(data)
	}
}

// writeLoop processes outgoing messages to the WebSocket
func (c *Client) writeLoop() {
	c.mu.RLock()
	conn := c.conn
	c.mu.RUnlock()

	if conn == nil {
		return
	}

	for {
		select {
		case <-c.ctx.Done():
			return
		case data := <-c.writeChannel:
			if err := conn.Write(c.ctx, websocket.MessageText, data); err != nil {
				if c.ctx.Err() == nil {
					c.logger.Error("Failed to write to WebSocket", zap.Error(err))
				}
				return
			}
		}
	}
}

// handleMessage processes incoming messages
func (c *Client) handleMessage(data []byte) {
	var msg websockets.WireMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		c.logger.Warn("Failed to unmarshal WebSocket message", zap.Error(err))
		return
	}

	switch msg.Kind {
	case websockets.MessageKindAck, websockets.MessageKindNack:
		c.handleResponse(msg)
	case websockets.MessageKindEvent: // Event messages have empty kind
		c.handleEvent(msg)
	default:
		c.logger.Warn("Unknown message kind", zap.String("kind", msg.Kind))
	}
}

// handleResponse processes ACK/NACK responses
func (c *Client) handleResponse(msg websockets.WireMessage) {
	if msg.Id == nil {
		return
	}

	msgID, ok := msg.Id.(float64) // JSON numbers are unmarshaled as float64
	if !ok {
		return
	}

	id := int64(msgID)

	respChan, exists := c.cleanupPendingRequest(id)
	if exists {
		resp := response{
			Success: msg.Kind == websockets.MessageKindAck,
			Error:   msg.Error,
		}
		select {
		case respChan <- resp:
		default:
		}
	}
}

// handleEvent processes incoming event messages
func (c *Client) handleEvent(msg websockets.WireMessage) {
	if msg.Topic == "" {
		return
	}

	// Deliver to our subscriber - the server only sends us events for topics we're subscribed to
	if err := c.subscriber.OnEvent(c.ctx, msg.Topic, msg.Data, nil); err != nil {
		c.logger.Warn("Subscriber error",
			zap.String("topic", msg.Topic),
			zap.Error(err))
	}
}
