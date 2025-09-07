package vinculum

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

type Client interface {
	Subscriber

	Connect(ctx context.Context) error
	Disconnect() error

	Subscribe(ctx context.Context, topicPattern string) error
	Unsubscribe(ctx context.Context, topicPattern string) error
	UnsubscribeAll(ctx context.Context) error

	Publish(ctx context.Context, topic string, payload any) error
	PublishSync(ctx context.Context, topic string, payload any) error
}

type ClientMonitor interface {
	OnConnect(ctx context.Context, client Client)
	OnDisconnect(ctx context.Context, client Client, err error)
	OnSubscribe(ctx context.Context, client Client, topic string)
	OnUnsubscribe(ctx context.Context, client Client, topic string)
	OnUnsubscribeAll(ctx context.Context, client Client)
}

// SubscriptionTracker is a sample ClientMonitor implementation that tracks
// client connection state and active subscriptions.
//
// Example usage:
//
//	tracker := &vinculum.SubscriptionTracker{}
//	client, err := client.NewClient().
//		WithURL("ws://localhost:8080/ws").
//		WithSubscriber(subscriber).
//		WithMonitor(tracker).
//		Build()
//
//	// Later, check connection and subscription status
//	if tracker.IsConnected() {
//		subscriptions := tracker.GetSubscriptions()
//		fmt.Printf("Connected with %d active subscriptions: %v\n",
//			len(subscriptions), subscriptions)
//	}
type SubscriptionTracker struct {
	mu             sync.RWMutex
	connected      bool
	connectedAt    time.Time
	disconnectedAt time.Time
	lastError      error
	subscriptions  map[string]time.Time // topic -> subscription time
}

// NewSubscriptionTracker creates a new subscription tracker.
func NewSubscriptionTracker() *SubscriptionTracker {
	return &SubscriptionTracker{
		subscriptions: make(map[string]time.Time),
	}
}

// OnConnect implements ClientMonitor.OnConnect
func (s *SubscriptionTracker) OnConnect(ctx context.Context, client Client) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.connected = true
	s.connectedAt = time.Now()
	s.lastError = nil
}

// OnDisconnect implements ClientMonitor.OnDisconnect
func (s *SubscriptionTracker) OnDisconnect(ctx context.Context, client Client, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.connected = false
	s.disconnectedAt = time.Now()
	s.lastError = err
	// Keep subscriptions - they may be restored on reconnect
}

// OnSubscribe implements ClientMonitor.OnSubscribe
func (s *SubscriptionTracker) OnSubscribe(ctx context.Context, client Client, topicPattern string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.subscriptions[topicPattern] = time.Now()
}

// OnUnsubscribe implements ClientMonitor.OnUnsubscribe
func (s *SubscriptionTracker) OnUnsubscribe(ctx context.Context, client Client, topicPattern string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.subscriptions, topicPattern)
}

// OnUnsubscribeAll implements ClientMonitor.OnUnsubscribeAll
func (s *SubscriptionTracker) OnUnsubscribeAll(ctx context.Context, client Client) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Clear all subscriptions
	s.subscriptions = make(map[string]time.Time)
}

// IsConnected returns true if the client is currently connected.
func (s *SubscriptionTracker) IsConnected() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connected
}

// GetConnectionTime returns when the client last connected.
// Returns zero time if never connected.
func (s *SubscriptionTracker) GetConnectionTime() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connectedAt
}

// GetDisconnectionTime returns when the client last disconnected.
// Returns zero time if never disconnected.
func (s *SubscriptionTracker) GetDisconnectionTime() time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.disconnectedAt
}

// GetLastError returns the error from the last disconnect, if any.
// Returns nil for graceful disconnects.
func (s *SubscriptionTracker) GetLastError() error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastError
}

// GetSubscriptions returns a copy of all active subscriptions with their subscription times.
func (s *SubscriptionTracker) GetSubscriptions() map[string]time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return a copy to prevent external modification
	result := make(map[string]time.Time, len(s.subscriptions))
	for topic, subTime := range s.subscriptions {
		result[topic] = subTime
	}
	return result
}

// GetSubscriptionTopics returns a slice of all active subscription topics.
func (s *SubscriptionTracker) GetSubscriptionTopics() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	topics := make([]string, 0, len(s.subscriptions))
	for topic := range s.subscriptions {
		topics = append(topics, topic)
	}
	return topics
}

// GetSubscriptionCount returns the number of active subscriptions.
func (s *SubscriptionTracker) GetSubscriptionCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.subscriptions)
}

// IsSubscribedTo returns true if the client is subscribed to the given topic pattern.
func (s *SubscriptionTracker) IsSubscribedTo(topicPattern string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, exists := s.subscriptions[topicPattern]
	return exists
}

// GetSubscriptionTime returns when the client subscribed to the given topic.
// Returns zero time if not subscribed to the topic.
func (s *SubscriptionTracker) GetSubscriptionTime(topicPattern string) time.Time {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.subscriptions[topicPattern] // returns zero time if not found
}

// AutoReconnector is a ClientMonitor that automatically reconnects on error disconnects
// with exponential backoff and resubscribes to previous topics.
//
// Features:
//   - Automatic reconnection on error disconnects (but not clean disconnects)
//   - Exponential backoff with configurable parameters
//   - Automatic resubscription to previous topic patterns after reconnect
//   - Configurable maximum retry attempts
//   - Thread-safe operation
//
// Example usage:
//
//	// Default configuration (with no-op logger)
//	reconnector := vinculum.NewAutoReconnector().Build()
//
//	// Custom configuration with logger
//	logger, _ := zap.NewProduction()
//	reconnector := vinculum.NewAutoReconnector().
//		WithInitialDelay(500 * time.Millisecond).
//		WithMaxDelay(10 * time.Second).
//		WithBackoffFactor(1.5).
//		WithMaxRetries(5).
//		WithEnabled(true).
//		WithLogger(logger).
//		Build()
//
//	client, err := client.NewClient().
//		WithURL("ws://localhost:8080/ws").
//		WithSubscriber(subscriber).
//		WithMonitor(reconnector).
//		Build()
//
//	// The client will now automatically reconnect on connection failures
//	// and restore all previous subscriptions
type AutoReconnector struct {
	*SubscriptionTracker

	// Backoff configuration
	initialDelay  time.Duration
	maxDelay      time.Duration
	backoffFactor float64
	maxRetries    int

	// Dependencies
	logger *zap.Logger

	// State
	mu              sync.RWMutex
	enabled         bool
	reconnectCount  int
	lastReconnectAt time.Time
}

// AutoReconnectorBuilder provides a fluent interface for configuring AutoReconnector.
type AutoReconnectorBuilder struct {
	initialDelay  time.Duration
	maxDelay      time.Duration
	backoffFactor float64
	maxRetries    int
	enabled       bool
	logger        *zap.Logger
}

// NewAutoReconnector creates a new AutoReconnectorBuilder with default configuration.
func NewAutoReconnector() *AutoReconnectorBuilder {
	return &AutoReconnectorBuilder{
		initialDelay:  1 * time.Second,  // Default: 1 second
		maxDelay:      30 * time.Second, // Default: 30 seconds
		backoffFactor: 2.0,              // Default: 2.0 (doubles each time)
		maxRetries:    -1,               // Default: -1 (unlimited)
		enabled:       true,             // Default: enabled
		logger:        zap.NewNop(),     // Default: no-op logger
	}
}

// WithInitialDelay sets the initial delay before the first reconnection attempt.
func (b *AutoReconnectorBuilder) WithInitialDelay(delay time.Duration) *AutoReconnectorBuilder {
	if delay > 0 {
		b.initialDelay = delay
	}
	return b
}

// WithMaxDelay sets the maximum delay between reconnection attempts.
func (b *AutoReconnectorBuilder) WithMaxDelay(delay time.Duration) *AutoReconnectorBuilder {
	if delay > 0 {
		b.maxDelay = delay
	}
	return b
}

// WithBackoffFactor sets the multiplier for exponential backoff.
func (b *AutoReconnectorBuilder) WithBackoffFactor(factor float64) *AutoReconnectorBuilder {
	if factor > 1.0 {
		b.backoffFactor = factor
	}
	return b
}

// WithMaxRetries sets the maximum number of reconnection attempts.
// Set to -1 for unlimited retries.
func (b *AutoReconnectorBuilder) WithMaxRetries(retries int) *AutoReconnectorBuilder {
	if retries >= -1 {
		b.maxRetries = retries
	}
	return b
}

// WithEnabled sets whether automatic reconnection is initially enabled.
func (b *AutoReconnectorBuilder) WithEnabled(enabled bool) *AutoReconnectorBuilder {
	b.enabled = enabled
	return b
}

// WithLogger sets the logger for reconnection events.
func (b *AutoReconnectorBuilder) WithLogger(logger *zap.Logger) *AutoReconnectorBuilder {
	if logger != nil {
		b.logger = logger
	}
	return b
}

// Build creates the configured AutoReconnector.
func (b *AutoReconnectorBuilder) Build() *AutoReconnector {
	return &AutoReconnector{
		SubscriptionTracker: NewSubscriptionTracker(),
		enabled:             b.enabled,
		initialDelay:        b.initialDelay,
		maxDelay:            b.maxDelay,
		backoffFactor:       b.backoffFactor,
		maxRetries:          b.maxRetries,
		logger:              b.logger,
	}
}

// SetEnabled enables or disables automatic reconnection.
func (a *AutoReconnector) SetEnabled(enabled bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.enabled = enabled
}

// IsEnabled returns whether automatic reconnection is currently enabled.
func (a *AutoReconnector) IsEnabled() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.enabled
}

// GetReconnectCount returns the number of reconnection attempts made.
func (a *AutoReconnector) GetReconnectCount() int {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.reconnectCount
}

// GetLastReconnectTime returns when the last reconnection attempt was made.
func (a *AutoReconnector) GetLastReconnectTime() time.Time {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.lastReconnectAt
}

// OnConnect implements ClientMonitor.OnConnect and delegates to SubscriptionTracker.
func (a *AutoReconnector) OnConnect(ctx context.Context, client Client) {
	a.SubscriptionTracker.OnConnect(ctx, client)
}

// OnDisconnect implements ClientMonitor.OnDisconnect and triggers reconnection on error disconnects.
func (a *AutoReconnector) OnDisconnect(ctx context.Context, client Client, err error) {
	// Call parent implementation to update state
	a.SubscriptionTracker.OnDisconnect(ctx, client, err)

	// Only attempt reconnection if:
	// 1. Reconnection is enabled
	// 2. This was an error disconnect (not clean)
	a.mu.RLock()
	shouldReconnect := a.enabled && err != nil
	a.mu.RUnlock()

	if shouldReconnect {
		go a.attemptReconnection(ctx, client)
	}
}

// OnSubscribe implements ClientMonitor.OnSubscribe and delegates to SubscriptionTracker.
func (a *AutoReconnector) OnSubscribe(ctx context.Context, client Client, topicPattern string) {
	a.SubscriptionTracker.OnSubscribe(ctx, client, topicPattern)
}

// OnUnsubscribe implements ClientMonitor.OnUnsubscribe and delegates to SubscriptionTracker.
func (a *AutoReconnector) OnUnsubscribe(ctx context.Context, client Client, topicPattern string) {
	a.SubscriptionTracker.OnUnsubscribe(ctx, client, topicPattern)
}

// OnUnsubscribeAll implements ClientMonitor.OnUnsubscribeAll and delegates to SubscriptionTracker.
func (a *AutoReconnector) OnUnsubscribeAll(ctx context.Context, client Client) {
	a.SubscriptionTracker.OnUnsubscribeAll(ctx, client)
}

// attemptReconnection handles the reconnection logic with exponential backoff.
func (a *AutoReconnector) attemptReconnection(ctx context.Context, client Client) {
	a.mu.Lock()
	if !a.enabled {
		a.mu.Unlock()
		return
	}

	// Get previous subscriptions before reconnection
	previousSubscriptions := a.GetSubscriptionTopics()

	delay := a.initialDelay
	attempts := 0
	a.mu.Unlock()

	for {
		// Check if we should continue
		a.mu.RLock()
		shouldContinue := a.enabled
		maxRetriesReached := a.maxRetries > 0 && attempts >= a.maxRetries
		a.mu.RUnlock()

		if !shouldContinue || maxRetriesReached || ctx.Err() != nil {
			break
		}

		// Wait before attempting reconnection
		if attempts > 0 {
			select {
			case <-time.After(delay):
				// Continue with reconnection attempt
			case <-ctx.Done():
				return // Context cancelled
			}
		}

		attempts++
		a.mu.Lock()
		a.reconnectCount++
		a.lastReconnectAt = time.Now()
		a.mu.Unlock()

		a.logger.Info("Attempting reconnection", zap.Int("attempt", attempts))

		// Attempt to reconnect
		err := client.Connect(ctx)
		if err == nil {
			a.logger.Info("Reconnection successful", zap.Int("attempts", attempts))

			// Resubscribe to previous subscriptions
			a.resubscribeToPreviousTopics(ctx, client, previousSubscriptions)
			return // Success!
		}

		a.logger.Warn("Reconnection attempt failed", zap.Int("attempt", attempts), zap.Error(err))

		// Calculate next delay with exponential backoff
		delay = time.Duration(float64(delay) * a.backoffFactor)
		if delay > a.maxDelay {
			delay = a.maxDelay
		}
	}

	if attempts > 0 {
		a.logger.Error("Giving up reconnection attempts", zap.Int("attempts", attempts))
	}
}

// resubscribeToPreviousTopics attempts to resubscribe to all previously subscribed topics.
func (a *AutoReconnector) resubscribeToPreviousTopics(ctx context.Context, client Client, topics []string) {
	if len(topics) == 0 {
		return
	}

	a.logger.Info("Resubscribing to previous topics", zap.Int("count", len(topics)))

	for _, topic := range topics {
		err := client.Subscribe(ctx, topic)
		if err != nil {
			a.logger.Warn("Failed to resubscribe to topic", zap.String("topic", topic), zap.Error(err))
		} else {
			a.logger.Debug("Successfully resubscribed to topic", zap.String("topic", topic))
		}
	}
}
