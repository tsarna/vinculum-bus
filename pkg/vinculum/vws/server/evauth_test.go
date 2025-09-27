package server

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tsarna/vinculum/pkg/vinculum/vws"
)

func TestEventAuthFunctions(t *testing.T) {
	ctx := context.Background()
	testMsg := &vws.WireMessage{Topic: "test/topic", Data: "test message"}

	t.Run("AllowAllEvents", func(t *testing.T) {
		modifiedMsg, err := AllowAllEvents(ctx, testMsg)
		assert.NoError(t, err)
		assert.Equal(t, testMsg, modifiedMsg) // Returns original message unchanged
	})

	t.Run("DenyAllEvents", func(t *testing.T) {
		modifiedMsg, err := DenyAllEvents(ctx, testMsg)
		assert.Error(t, err)
		assert.Nil(t, modifiedMsg)
		assert.Contains(t, err.Error(), "not allowed")
	})

	t.Run("DropAllEvents", func(t *testing.T) {
		modifiedMsg, err := DropAllEvents(ctx, testMsg)
		assert.NoError(t, err)
		assert.Nil(t, modifiedMsg) // nil means silently drop
	})

	t.Run("AllowTopicPrefix", func(t *testing.T) {
		authFunc := AllowTopicPrefix("client/")

		// Should allow topics with prefix
		clientMsg := &vws.WireMessage{Topic: "client/events", Data: "test message"}
		modifiedMsg, err := authFunc(ctx, clientMsg)
		assert.NoError(t, err)
		assert.Equal(t, clientMsg, modifiedMsg) // Returns original message unchanged

		// Should deny topics without prefix
		serverMsg := &vws.WireMessage{Topic: "server/events", Data: "test message"}
		modifiedMsg, err = authFunc(ctx, serverMsg)
		assert.Error(t, err)
		assert.Nil(t, modifiedMsg)
		assert.Contains(t, err.Error(), "client/")

		// Test edge cases
		// Exact prefix match
		exactMsg := &vws.WireMessage{Topic: "client/", Data: "test"}
		modifiedMsg, err = authFunc(ctx, exactMsg)
		assert.NoError(t, err)
		assert.Equal(t, exactMsg, modifiedMsg)

		// Prefix is longer than topic
		shortMsg := &vws.WireMessage{Topic: "cli", Data: "test"}
		modifiedMsg, err = authFunc(ctx, shortMsg)
		assert.Error(t, err)
		assert.Nil(t, modifiedMsg)

		// Empty topic
		emptyMsg := &vws.WireMessage{Topic: "", Data: "test"}
		modifiedMsg, err = authFunc(ctx, emptyMsg)
		assert.Error(t, err)
		assert.Nil(t, modifiedMsg)
	})

	t.Run("AllowTopicPattern", func(t *testing.T) {
		ctx := context.Background()

		t.Run("single-level wildcard", func(t *testing.T) {
			authFunc := AllowTopicPattern("sensor/+/data")

			// Should allow matching topics
			tempMsg := &vws.WireMessage{Topic: "sensor/temperature/data", Data: "23.5"}
			modifiedMsg, err := authFunc(ctx, tempMsg)
			assert.NoError(t, err)
			assert.Equal(t, tempMsg, modifiedMsg)

			humidityMsg := &vws.WireMessage{Topic: "sensor/humidity/data", Data: "65%"}
			modifiedMsg, err = authFunc(ctx, humidityMsg)
			assert.NoError(t, err)
			assert.Equal(t, humidityMsg, modifiedMsg)

			// Should deny non-matching topics
			wrongMsg := &vws.WireMessage{Topic: "sensor/temperature/reading", Data: "wrong"}
			modifiedMsg, err = authFunc(ctx, wrongMsg)
			assert.Error(t, err)
			assert.Nil(t, modifiedMsg)

			nonSensorMsg := &vws.WireMessage{Topic: "weather/temperature/data", Data: "wrong"}
			modifiedMsg, err = authFunc(ctx, nonSensorMsg)
			assert.Error(t, err)
			assert.Nil(t, modifiedMsg)
		})

		t.Run("multi-level wildcard", func(t *testing.T) {
			authFunc := AllowTopicPattern("events/+category/#")

			// Should allow various matching topics
			userMsg := &vws.WireMessage{Topic: "events/user/login", Data: "user event"}
			modifiedMsg, err := authFunc(ctx, userMsg)
			assert.NoError(t, err)
			assert.Equal(t, userMsg, modifiedMsg)

			systemMsg := &vws.WireMessage{Topic: "events/system/alerts/critical/memory", Data: "system event"}
			modifiedMsg, err = authFunc(ctx, systemMsg)
			assert.NoError(t, err)
			assert.Equal(t, systemMsg, modifiedMsg)

			// Should deny non-matching topics
			wrongMsg := &vws.WireMessage{Topic: "notifications/user/login", Data: "wrong"}
			modifiedMsg, err = authFunc(ctx, wrongMsg)
			assert.Error(t, err)
			assert.Nil(t, modifiedMsg)
		})

		t.Run("multiple wildcards", func(t *testing.T) {
			authFunc := AllowTopicPattern("client/+userId/actions/+action")

			// Should allow matching topics
			loginMsg := &vws.WireMessage{Topic: "client/john123/actions/login", Data: "login event"}
			modifiedMsg, err := authFunc(ctx, loginMsg)
			assert.NoError(t, err)
			assert.Equal(t, loginMsg, modifiedMsg)

			logoutMsg := &vws.WireMessage{Topic: "client/jane456/actions/logout", Data: "logout event"}
			modifiedMsg, err = authFunc(ctx, logoutMsg)
			assert.NoError(t, err)
			assert.Equal(t, logoutMsg, modifiedMsg)

			// Should deny non-matching topics
			wrongMsg := &vws.WireMessage{Topic: "client/john123/settings/update", Data: "wrong"}
			modifiedMsg, err = authFunc(ctx, wrongMsg)
			assert.Error(t, err)
			assert.Nil(t, modifiedMsg)
		})

		t.Run("exact pattern", func(t *testing.T) {
			authFunc := AllowTopicPattern("exact/topic/path")

			// Should allow exact match
			exactMsg := &vws.WireMessage{Topic: "exact/topic/path", Data: "exact"}
			modifiedMsg, err := authFunc(ctx, exactMsg)
			assert.NoError(t, err)
			assert.Equal(t, exactMsg, modifiedMsg)

			// Should deny non-exact matches
			wrongMsg := &vws.WireMessage{Topic: "exact/topic/path/extra", Data: "wrong"}
			modifiedMsg, err = authFunc(ctx, wrongMsg)
			assert.Error(t, err)
			assert.Nil(t, modifiedMsg)

			prefixMsg := &vws.WireMessage{Topic: "exact/topic", Data: "wrong"}
			modifiedMsg, err = authFunc(ctx, prefixMsg)
			assert.Error(t, err)
			assert.Nil(t, modifiedMsg)
		})
	})

	t.Run("ChainEventAuth", func(t *testing.T) {
		ctx := context.Background()

		t.Run("all functions allow", func(t *testing.T) {
			// Chain: Allow client prefix, then allow sensor pattern
			chainedAuth := ChainEventAuth(
				AllowTopicPrefix("client/"),
				AllowTopicPattern("client/+/sensor/#"),
			)

			// Should allow when both conditions are met
			validMsg := &vws.WireMessage{Topic: "client/user123/sensor/temperature", Data: "valid"}
			modifiedMsg, err := chainedAuth(ctx, validMsg)
			assert.NoError(t, err)
			assert.Equal(t, validMsg, modifiedMsg)
		})

		t.Run("first function denies", func(t *testing.T) {
			chainedAuth := ChainEventAuth(
				AllowTopicPrefix("client/"),
				AllowTopicPattern("+/+/sensor/#"),
			)

			// Should be denied by first function (wrong prefix)
			invalidMsg := &vws.WireMessage{Topic: "server/user123/sensor/temperature", Data: "invalid"}
			modifiedMsg, err := chainedAuth(ctx, invalidMsg)
			assert.Error(t, err)
			assert.Nil(t, modifiedMsg)
			assert.Contains(t, err.Error(), "client/")
		})

		t.Run("second function denies", func(t *testing.T) {
			chainedAuth := ChainEventAuth(
				AllowTopicPrefix("client/"),
				AllowTopicPattern("client/+/sensor/#"),
			)

			// Should pass first function but fail second (wrong pattern)
			invalidMsg := &vws.WireMessage{Topic: "client/user123/actions/login", Data: "invalid"}
			modifiedMsg, err := chainedAuth(ctx, invalidMsg)
			assert.Error(t, err)
			assert.Nil(t, modifiedMsg)
		})

		t.Run("function silently drops", func(t *testing.T) {
			dropTestClients := func(ctx context.Context, msg *vws.WireMessage) (*vws.WireMessage, error) {
				if strings.Contains(msg.Topic, "/test/") {
					return nil, nil // Silently drop test events
				}
				return msg, nil
			}

			chainedAuth := ChainEventAuth(
				AllowTopicPrefix("client/"),
				dropTestClients,
			)

			// Should be silently dropped by second function
			testMsg := &vws.WireMessage{Topic: "client/test/sensor/data", Data: "test"}
			modifiedMsg, err := chainedAuth(ctx, testMsg)
			assert.NoError(t, err)
			assert.Nil(t, modifiedMsg) // Silently dropped

			// Should pass through for non-test clients
			prodMsg := &vws.WireMessage{Topic: "client/prod/sensor/data", Data: "prod"}
			modifiedMsg, err = chainedAuth(ctx, prodMsg)
			assert.NoError(t, err)
			assert.Equal(t, prodMsg, modifiedMsg)
		})

		t.Run("function modifies message", func(t *testing.T) {
			addPrefix := func(ctx context.Context, msg *vws.WireMessage) (*vws.WireMessage, error) {
				modified := &vws.WireMessage{
					Kind:  msg.Kind,
					Topic: "processed/" + msg.Topic,
					Data:  msg.Data,
					Id:    msg.Id,
				}
				return modified, nil
			}

			chainedAuth := ChainEventAuth(
				AllowTopicPrefix("client/"),
				addPrefix,
			)

			// Should be modified by second function
			originalMsg := &vws.WireMessage{Topic: "client/user123/data", Data: "original"}
			modifiedMsg, err := chainedAuth(ctx, originalMsg)
			assert.NoError(t, err)
			assert.NotEqual(t, originalMsg, modifiedMsg)
			assert.Equal(t, "processed/client/user123/data", modifiedMsg.Topic)
			assert.Equal(t, "original", modifiedMsg.Data)
		})

		t.Run("empty chain", func(t *testing.T) {
			chainedAuth := ChainEventAuth()

			// Empty chain should allow everything unchanged
			msg := &vws.WireMessage{Topic: "any/topic", Data: "any data"}
			modifiedMsg, err := chainedAuth(ctx, msg)
			assert.NoError(t, err)
			assert.Equal(t, msg, modifiedMsg)
		})

		t.Run("single function in chain", func(t *testing.T) {
			chainedAuth := ChainEventAuth(AllowTopicPrefix("allowed/"))

			// Should behave like the single function
			allowedMsg := &vws.WireMessage{Topic: "allowed/topic", Data: "allowed"}
			modifiedMsg, err := chainedAuth(ctx, allowedMsg)
			assert.NoError(t, err)
			assert.Equal(t, allowedMsg, modifiedMsg)

			deniedMsg := &vws.WireMessage{Topic: "denied/topic", Data: "denied"}
			modifiedMsg, err = chainedAuth(ctx, deniedMsg)
			assert.Error(t, err)
			assert.Nil(t, modifiedMsg)
		})
	})
}
