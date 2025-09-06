package client

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tsarna/vinculum/pkg/vinculum"
	"go.uber.org/zap"
)

func TestClientBuilder(t *testing.T) {
	logger := zap.NewNop()
	subscriber := &mockSubscriber{}

	t.Run("successful build with all parameters", func(t *testing.T) {
		client, err := NewClient().
			WithURL("ws://localhost:8080/ws").
			WithLogger(logger).
			WithDialTimeout(10 * time.Second).
			WithSubscriber(subscriber).
			Build()

		require.NoError(t, err)
		assert.NotNil(t, client)
		assert.Equal(t, "ws://localhost:8080/ws", client.url)
		assert.Equal(t, logger, client.logger)
		assert.Equal(t, 10*time.Second, client.dialTimeout)
		assert.Equal(t, subscriber, client.subscriber)
	})

	t.Run("fluent interface returns same builder", func(t *testing.T) {
		builder := NewClient()
		assert.Same(t, builder, builder.WithURL("ws://localhost:8080/ws"))
		assert.Same(t, builder, builder.WithLogger(logger))
		assert.Same(t, builder, builder.WithDialTimeout(5*time.Second))
		assert.Same(t, builder, builder.WithSubscriber(subscriber))
		assert.Same(t, builder, builder.WithWriteChannelSize(200))
		assert.Same(t, builder, builder.WithAuthorization("Bearer token123"))
		assert.Same(t, builder, builder.WithAuthorizationProvider(func(ctx context.Context) (string, error) {
			return "Bearer dynamic-token", nil
		}))
		assert.Same(t, builder, builder.WithHeaders(map[string][]string{"X-API-Key": {"key123"}}))
		assert.Same(t, builder, builder.WithHeader("User-Agent", "MyApp/1.0"))
	})

	t.Run("build fails with missing URL", func(t *testing.T) {
		_, err := NewClient().
			WithLogger(logger).
			WithDialTimeout(10 * time.Second).
			WithSubscriber(subscriber).
			Build()

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "URL is required")
	})

	t.Run("build fails with missing subscriber", func(t *testing.T) {
		_, err := NewClient().
			WithURL("ws://localhost:8080/ws").
			WithLogger(logger).
			WithDialTimeout(10 * time.Second).
			Build()

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "subscriber is required")
	})

	t.Run("build succeeds with default logger", func(t *testing.T) {
		client, err := NewClient().
			WithURL("ws://localhost:8080/ws").
			WithDialTimeout(10 * time.Second).
			WithSubscriber(subscriber).
			Build()

		assert.NoError(t, err)
		assert.NotNil(t, client)
		assert.NotNil(t, client.logger) // Should have default nop logger
	})

	t.Run("build succeeds with default timeout when zero provided", func(t *testing.T) {
		client, err := NewClient().
			WithURL("ws://localhost:8080/ws").
			WithLogger(logger).
			WithDialTimeout(0).
			WithSubscriber(subscriber).
			Build()

		assert.NoError(t, err)
		assert.NotNil(t, client)
		assert.Equal(t, 30*time.Second, client.dialTimeout) // Should keep default
	})

	t.Run("default values", func(t *testing.T) {
		builder := NewClient()
		assert.Equal(t, 30*time.Second, builder.dialTimeout)
		assert.NotNil(t, builder.logger)               // Should have nop logger
		assert.Equal(t, 100, builder.writeChannelSize) // Default buffer size
	})

	t.Run("nil logger is ignored", func(t *testing.T) {
		builder := NewClient().WithLogger(nil)
		assert.NotNil(t, builder.logger) // Should keep the default nop logger
	})

	t.Run("zero timeout is ignored", func(t *testing.T) {
		builder := NewClient().WithDialTimeout(0)
		assert.Equal(t, 30*time.Second, builder.dialTimeout) // Should keep default
	})

	t.Run("negative timeout is ignored", func(t *testing.T) {
		builder := NewClient().WithDialTimeout(-5 * time.Second)
		assert.Equal(t, 30*time.Second, builder.dialTimeout) // Should keep default
	})

	t.Run("write channel size configuration", func(t *testing.T) {
		client, err := NewClient().
			WithURL("ws://localhost:8080/ws").
			WithSubscriber(subscriber).
			WithWriteChannelSize(500).
			Build()

		require.NoError(t, err)
		assert.Equal(t, 500, client.writeChannelSize)
	})

	t.Run("zero write channel size is ignored", func(t *testing.T) {
		builder := NewClient().WithWriteChannelSize(0)
		assert.Equal(t, 100, builder.writeChannelSize) // Should keep default
	})

	t.Run("negative write channel size is ignored", func(t *testing.T) {
		builder := NewClient().WithWriteChannelSize(-10)
		assert.Equal(t, 100, builder.writeChannelSize) // Should keep default
	})

	t.Run("static authorization configuration", func(t *testing.T) {
		client, err := NewClient().
			WithURL("ws://localhost:8080/ws").
			WithSubscriber(subscriber).
			WithAuthorization("Bearer static-token-123").
			Build()

		require.NoError(t, err)
		assert.NotNil(t, client.authProvider)

		// Test that the provider returns the static value
		auth, err := client.authProvider(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, "Bearer static-token-123", auth)
	})

	t.Run("dynamic authorization provider configuration", func(t *testing.T) {
		provider := func(ctx context.Context) (string, error) {
			return "Bearer dynamic-token-456", nil
		}

		client, err := NewClient().
			WithURL("ws://localhost:8080/ws").
			WithSubscriber(subscriber).
			WithAuthorizationProvider(provider).
			Build()

		require.NoError(t, err)
		assert.NotNil(t, client.authProvider)

		// Test that the provider works
		auth, err := client.authProvider(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, "Bearer dynamic-token-456", auth)
	})

	t.Run("static authorization overrides provider", func(t *testing.T) {
		provider := func(ctx context.Context) (string, error) {
			return "Bearer provider-token", nil
		}

		client, err := NewClient().
			WithURL("ws://localhost:8080/ws").
			WithSubscriber(subscriber).
			WithAuthorizationProvider(provider).
			WithAuthorization("Bearer static-token"). // This should override
			Build()

		require.NoError(t, err)
		assert.NotNil(t, client.authProvider)

		// Test that the provider returns the static value (last one wins)
		auth, err := client.authProvider(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, "Bearer static-token", auth)
	})

	t.Run("provider overrides static authorization", func(t *testing.T) {
		provider := func(ctx context.Context) (string, error) {
			return "Bearer provider-token", nil
		}

		client, err := NewClient().
			WithURL("ws://localhost:8080/ws").
			WithSubscriber(subscriber).
			WithAuthorization("Bearer static-token").
			WithAuthorizationProvider(provider). // This should override
			Build()

		require.NoError(t, err)
		assert.NotNil(t, client.authProvider)

		// Test that the provider returns the dynamic value (last one wins)
		auth, err := client.authProvider(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, "Bearer provider-token", auth)
	})

	t.Run("custom headers configuration", func(t *testing.T) {
		headers := map[string][]string{
			"X-API-Key":   {"key123"},
			"User-Agent":  {"MyApp/1.0"},
			"X-Client-ID": {"client456"},
		}

		client, err := NewClient().
			WithURL("ws://localhost:8080/ws").
			WithSubscriber(subscriber).
			WithHeaders(headers).
			Build()

		require.NoError(t, err)
		assert.Equal(t, headers, client.headers)
	})

	t.Run("single header configuration", func(t *testing.T) {
		client, err := NewClient().
			WithURL("ws://localhost:8080/ws").
			WithSubscriber(subscriber).
			WithHeader("X-API-Key", "key123").
			WithHeader("User-Agent", "MyApp/1.0").
			Build()

		require.NoError(t, err)
		expected := map[string][]string{
			"X-API-Key":  {"key123"},
			"User-Agent": {"MyApp/1.0"},
		}
		assert.Equal(t, expected, client.headers)
	})

	t.Run("headers and authorization combined", func(t *testing.T) {
		headers := map[string][]string{
			"X-API-Key":  {"key123"},
			"User-Agent": {"MyApp/1.0"},
		}

		client, err := NewClient().
			WithURL("ws://localhost:8080/ws").
			WithSubscriber(subscriber).
			WithHeaders(headers).
			WithAuthorization("Bearer token456").
			Build()

		require.NoError(t, err)
		assert.Equal(t, headers, client.headers)
		assert.NotNil(t, client.authProvider)

		// Test that auth provider works
		auth, err := client.authProvider(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, "Bearer token456", auth)
	})

	t.Run("multiple WithHeaders calls merge headers", func(t *testing.T) {
		client, err := NewClient().
			WithURL("ws://localhost:8080/ws").
			WithSubscriber(subscriber).
			WithHeaders(map[string][]string{"X-API-Key": {"key123"}}).
			WithHeaders(map[string][]string{"User-Agent": {"MyApp/1.0"}}).
			Build()

		require.NoError(t, err)
		expected := map[string][]string{
			"X-API-Key":  {"key123"},
			"User-Agent": {"MyApp/1.0"},
		}
		assert.Equal(t, expected, client.headers)
	})
}

func TestClientLifecycle(t *testing.T) {
	logger := zap.NewNop()
	subscriber := &mockSubscriber{}

	t.Run("client starts disconnected", func(t *testing.T) {
		client, err := NewClient().
			WithURL("ws://localhost:8080/ws").
			WithLogger(logger).
			WithSubscriber(subscriber).
			Build()

		require.NoError(t, err)
		assert.Equal(t, int32(0), client.started)
		assert.Equal(t, int32(0), client.stopping)
	})

	t.Run("operations fail when not connected", func(t *testing.T) {
		client, err := NewClient().
			WithURL("ws://localhost:8080/ws").
			WithLogger(logger).
			WithSubscriber(subscriber).
			Build()

		require.NoError(t, err)

		ctx := context.Background()

		// These should all fail because client is not connected
		err = client.Subscribe(ctx, "test/topic")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "client is not connected")

		err = client.Unsubscribe(ctx, "test/topic")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "client is not connected")

		err = client.Publish(ctx, "test/topic", "test message")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "client is not connected")
	})

	t.Run("connect fails with invalid URL", func(t *testing.T) {
		client, err := NewClient().
			WithURL("invalid-url").
			WithLogger(logger).
			WithSubscriber(subscriber).
			Build()

		require.NoError(t, err)

		err = client.Connect(context.Background())
		assert.Error(t, err)
		// The error could be either "invalid URL" or "failed to connect to WebSocket"
		assert.True(t,
			strings.Contains(err.Error(), "invalid URL") ||
				strings.Contains(err.Error(), "failed to connect to WebSocket"),
			"Expected error about invalid URL or connection failure, got: %s", err.Error())
	})

	t.Run("disconnect on unconnected client is safe", func(t *testing.T) {
		client, err := NewClient().
			WithURL("ws://localhost:8080/ws").
			WithLogger(logger).
			WithSubscriber(subscriber).
			Build()

		require.NoError(t, err)

		err = client.Disconnect()
		assert.NoError(t, err)
	})

	t.Run("multiple disconnect calls are safe", func(t *testing.T) {
		client, err := NewClient().
			WithURL("ws://localhost:8080/ws").
			WithLogger(logger).
			WithSubscriber(subscriber).
			Build()

		require.NoError(t, err)

		err = client.Disconnect()
		assert.NoError(t, err)

		err = client.Disconnect()
		assert.NoError(t, err)
	})
}

// mockSubscriber is a test helper that implements the Subscriber interface
type mockSubscriber struct {
	subscriptions   []string
	unsubscriptions []string
	events          []eventData
	passThrough     []vinculum.EventBusMessage
}

type eventData struct {
	topic   string
	message interface{}
	fields  map[string]string
}

func (m *mockSubscriber) OnSubscribe(ctx context.Context, topic string) error {
	m.subscriptions = append(m.subscriptions, topic)
	return nil
}

func (m *mockSubscriber) OnUnsubscribe(ctx context.Context, topic string) error {
	m.unsubscriptions = append(m.unsubscriptions, topic)
	return nil
}

func (m *mockSubscriber) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	m.events = append(m.events, eventData{
		topic:   topic,
		message: message,
		fields:  fields,
	})
	return nil
}

func (m *mockSubscriber) PassThrough(msg vinculum.EventBusMessage) error {
	m.passThrough = append(m.passThrough, msg)
	return nil
}
