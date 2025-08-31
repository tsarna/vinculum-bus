package websockets

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tsarna/vinculum/pkg/vinculum"
	"go.uber.org/zap"
)

func TestListenerConfig_BuilderPattern(t *testing.T) {
	logger := zap.NewNop()
	eventBus := vinculum.NewEventBus(logger)

	t.Run("successful build with all parameters", func(t *testing.T) {
		listener, err := NewListenerConfig().
			WithEventBus(eventBus).
			WithLogger(logger).
			Build()

		require.NoError(t, err)
		assert.NotNil(t, listener)
		assert.Equal(t, eventBus, listener.eventBus)
		assert.Equal(t, logger, listener.logger)
	})

	t.Run("fluent interface returns same config", func(t *testing.T) {
		config := NewListenerConfig()
		result1 := config.WithEventBus(eventBus)
		result2 := result1.WithLogger(logger)

		assert.Same(t, config, result1)
		assert.Same(t, config, result2)
	})

	t.Run("isValid returns error when missing parameters", func(t *testing.T) {
		// Empty config
		config := NewListenerConfig()
		err := config.IsValid()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "EventBus")
		assert.Contains(t, err.Error(), "Logger")

		// Only EventBus
		config.WithEventBus(eventBus)
		err = config.IsValid()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "Logger")
		assert.NotContains(t, err.Error(), "EventBus")

		// Only Logger
		config = NewListenerConfig().WithLogger(logger)
		err = config.IsValid()
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "EventBus")
		assert.NotContains(t, err.Error(), "Logger")

		// Both parameters
		config.WithEventBus(eventBus)
		err = config.IsValid()
		assert.NoError(t, err)
	})

	t.Run("build fails with missing EventBus", func(t *testing.T) {
		listener, err := NewListenerConfig().
			WithLogger(logger).
			Build()

		assert.Error(t, err)
		assert.Nil(t, listener)
		assert.Contains(t, err.Error(), "EventBus")
	})

	t.Run("build fails with missing Logger", func(t *testing.T) {
		listener, err := NewListenerConfig().
			WithEventBus(eventBus).
			Build()

		assert.Error(t, err)
		assert.Nil(t, listener)
		assert.Contains(t, err.Error(), "Logger")
	})

	t.Run("build fails with missing both parameters", func(t *testing.T) {
		listener, err := NewListenerConfig().Build()

		assert.Error(t, err)
		assert.Nil(t, listener)
		assert.Contains(t, err.Error(), "EventBus")
		assert.Contains(t, err.Error(), "Logger")
	})

	t.Run("queue size configuration", func(t *testing.T) {
		// Default queue size
		config := NewListenerConfig()
		assert.Equal(t, DefaultQueueSize, config.queueSize)

		// Custom queue size
		config.WithQueueSize(512)
		assert.Equal(t, 512, config.queueSize)

		// Invalid queue size (should be ignored)
		config.WithQueueSize(-10)
		assert.Equal(t, 512, config.queueSize) // Should remain unchanged

		config.WithQueueSize(0)
		assert.Equal(t, 512, config.queueSize) // Should remain unchanged

		// Valid small queue size
		config.WithQueueSize(1)
		assert.Equal(t, 1, config.queueSize)
	})

	t.Run("fluent interface with queue size", func(t *testing.T) {
		listener, err := NewListenerConfig().
			WithEventBus(eventBus).
			WithLogger(logger).
			WithQueueSize(1024).
			Build()

		require.NoError(t, err)
		assert.NotNil(t, listener)
		assert.Equal(t, 1024, listener.config.queueSize)
	})

	t.Run("ping interval configuration", func(t *testing.T) {
		// Default ping interval
		config := NewListenerConfig()
		assert.Equal(t, DefaultPingInterval, config.pingInterval)

		// Custom ping interval
		config.WithPingInterval(45 * time.Second)
		assert.Equal(t, 45*time.Second, config.pingInterval)

		// Disable ping/pong (0 duration)
		config.WithPingInterval(0)
		assert.Equal(t, time.Duration(0), config.pingInterval)

		// Invalid ping interval (negative - should be ignored)
		config.WithPingInterval(10 * time.Second)
		config.WithPingInterval(-5 * time.Second)
		assert.Equal(t, 10*time.Second, config.pingInterval) // Should remain unchanged
	})

	t.Run("fluent interface with ping interval", func(t *testing.T) {
		listener, err := NewListenerConfig().
			WithEventBus(eventBus).
			WithLogger(logger).
			WithPingInterval(60 * time.Second).
			Build()

		require.NoError(t, err)
		assert.NotNil(t, listener)
		assert.Equal(t, 60*time.Second, listener.config.pingInterval)
	})

	t.Run("complete fluent interface", func(t *testing.T) {
		listener, err := NewListenerConfig().
			WithEventBus(eventBus).
			WithLogger(logger).
			WithQueueSize(512).
			WithPingInterval(45 * time.Second).
			Build()

		require.NoError(t, err)
		assert.NotNil(t, listener)
		assert.Equal(t, 512, listener.config.queueSize)
		assert.Equal(t, 45*time.Second, listener.config.pingInterval)
	})
}
