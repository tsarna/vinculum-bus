package vinculum

import (
	"fmt"
	"strings"

	"github.com/tsarna/mqttpattern"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Subscriber interface {
	OnSubscribe(topic string) error
	OnUnsubscribe(topic string) error
	OnEvent(topic string, message any, fields map[string]string) error
}

type BaseSubscriber struct {
}

func (b *BaseSubscriber) OnSubscribe(topic string) error {
	return nil
}

func (b *BaseSubscriber) OnUnsubscribe(topic string) error {
	return nil
}

func (b *BaseSubscriber) OnEvent(topic string, message any, fields map[string]string) error {
	return nil
}

type matcher func(topic string) (bool, map[string]string)

func makeMatcher(subscribeMsg eventBusMessage) matcher {
	topicPattern := subscribeMsg.topic

	switch subscribeMsg.msgType {
	case messageTypeSubscribe:
		pattern := subscribeMsg.topic

		hashIndex := strings.Index(subscribeMsg.topic, "#")
		plusIndex := strings.Index(subscribeMsg.topic, "+")

		if hashIndex == -1 && plusIndex == -1 {
			// exact match
			return func(topic string) (bool, map[string]string) {
				return topic == pattern, nil
			}
		} else {
			return func(topic string) (bool, map[string]string) {
				return mqttpattern.Matches(pattern, topic), nil
			}
		}
	case messageTypeSubscribeWithExtraction:
		return func(topic string) (bool, map[string]string) {
			if mqttpattern.Matches(topicPattern, topic) {
				return true, mqttpattern.Extract(topicPattern, topic)
			}

			return false, nil
		}
	}

	panic("unsupported message type")
}

// LoggingSubscriber is a BaseSubscriber that logs all method calls for debugging and demonstration
type LoggingSubscriber struct {
	BaseSubscriber
	logger   *zap.Logger
	logLevel zapcore.Level
	name     string // Optional name for identification in logs
}

// NewLoggingSubscriber creates a new LoggingSubscriber with the specified logger and log level
func NewLoggingSubscriber(logger *zap.Logger, logLevel zapcore.Level) *LoggingSubscriber {
	return &LoggingSubscriber{
		logger:   logger,
		logLevel: logLevel,
		name:     "LoggingSubscriber",
	}
}

// NewNamedLoggingSubscriber creates a new LoggingSubscriber with a custom name for identification
func NewNamedLoggingSubscriber(logger *zap.Logger, logLevel zapcore.Level, name string) *LoggingSubscriber {
	return &LoggingSubscriber{
		logger:   logger,
		logLevel: logLevel,
		name:     name,
	}
}

// OnSubscribe logs subscription events
func (l *LoggingSubscriber) OnSubscribe(topic string) error {
	l.logger.Log(l.logLevel, "OnSubscribe called",
		zap.String("subscriber", l.name),
		zap.String("topic", topic),
	)

	// Call parent implementation (which is empty, but maintains the pattern)
	return l.BaseSubscriber.OnSubscribe(topic)
}

// OnUnsubscribe logs unsubscription events
func (l *LoggingSubscriber) OnUnsubscribe(topic string) error {
	l.logger.Log(l.logLevel, "OnUnsubscribe called",
		zap.String("subscriber", l.name),
		zap.String("topic", topic),
	)

	// Call parent implementation (which is empty, but maintains the pattern)
	return l.BaseSubscriber.OnUnsubscribe(topic)
}

// OnEvent logs event reception with full details
func (l *LoggingSubscriber) OnEvent(topic string, message any, fields map[string]string) error {
	// Convert message to string for logging (handle various types safely)
	var messageStr string
	switch v := message.(type) {
	case string:
		messageStr = v
	case []byte:
		messageStr = string(v)
	case nil:
		messageStr = "<nil>"
	default:
		messageStr = fmt.Sprintf("%v", v)
	}

	// The fields are already structured as a map, so we can log them directly via zap.Any

	l.logger.Log(l.logLevel, "OnEvent called",
		zap.String("subscriber", l.name),
		zap.String("topic", topic),
		zap.String("message", messageStr),
		zap.Any("extractedFields", fields),
		zap.Int("fieldCount", len(fields)),
	)

	// Call parent implementation (which is empty, but maintains the pattern)
	return l.BaseSubscriber.OnEvent(topic, message, fields)
}
