package subutils

import (
	"context"
	"fmt"

	"github.com/tsarna/vinculum/pkg/vinculum"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// LoggingSubscriber wraps another subscriber and logs all method calls for debugging and demonstration.
// If the wrapped subscriber is nil, it acts as a standalone logging subscriber.
type LoggingSubscriber struct {
	wrapped  vinculum.Subscriber // The subscriber to wrap (can be nil)
	logger   *zap.Logger
	logLevel zapcore.Level
	name     string // Optional name for identification in logs
}

// NewLoggingSubscriber creates a new LoggingSubscriber that wraps another subscriber.
// If wrapped is nil, it acts as a standalone logging subscriber.
func NewLoggingSubscriber(wrapped vinculum.Subscriber, logger *zap.Logger, logLevel zapcore.Level) *LoggingSubscriber {
	return &LoggingSubscriber{
		wrapped:  wrapped,
		logger:   logger,
		logLevel: logLevel,
		name:     "LoggingSubscriber",
	}
}

// NewNamedLoggingSubscriber creates a new LoggingSubscriber that wraps another subscriber with a custom name.
// If wrapped is nil, it acts as a standalone logging subscriber.
func NewNamedLoggingSubscriber(wrapped vinculum.Subscriber, logger *zap.Logger, logLevel zapcore.Level, name string) *LoggingSubscriber {
	return &LoggingSubscriber{
		wrapped:  wrapped,
		logger:   logger,
		logLevel: logLevel,
		name:     name,
	}
}

// OnSubscribe logs subscription events and calls the wrapped subscriber if present
func (l *LoggingSubscriber) OnSubscribe(ctx context.Context, topic string) error {
	l.logger.Log(l.logLevel, "OnSubscribe called",
		zap.String("subscriber", l.name),
		zap.String("topic", topic),
		zap.Bool("hasWrapped", l.wrapped != nil),
	)

	// Call wrapped subscriber if present
	if l.wrapped != nil {
		return l.wrapped.OnSubscribe(ctx, topic)
	}

	// No wrapped subscriber, return nil (successful no-op)
	return nil
}

// OnUnsubscribe logs unsubscription events and calls the wrapped subscriber if present
func (l *LoggingSubscriber) OnUnsubscribe(ctx context.Context, topic string) error {
	l.logger.Log(l.logLevel, "OnUnsubscribe called",
		zap.String("subscriber", l.name),
		zap.String("topic", topic),
		zap.Bool("hasWrapped", l.wrapped != nil),
	)

	// Call wrapped subscriber if present
	if l.wrapped != nil {
		return l.wrapped.OnUnsubscribe(ctx, topic)
	}

	// No wrapped subscriber, return nil (successful no-op)
	return nil
}

// OnEvent logs event reception with full details and calls the wrapped subscriber if present
func (l *LoggingSubscriber) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
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

	l.logger.Log(l.logLevel, "OnEvent called",
		zap.String("subscriber", l.name),
		zap.String("topic", topic),
		zap.String("message", messageStr),
		zap.Any("extractedFields", fields),
		zap.Int("fieldCount", len(fields)),
		zap.Bool("hasWrapped", l.wrapped != nil),
	)

	// Call wrapped subscriber if present
	if l.wrapped != nil {
		return l.wrapped.OnEvent(ctx, topic, message, fields)
	}

	// No wrapped subscriber, return nil (successful no-op)
	return nil
}

func (l *LoggingSubscriber) PassThrough(msg vinculum.EventBusMessage) error {
	if l.wrapped != nil {
		return l.wrapped.PassThrough(msg)
	} else {
		l.logger.Log(l.logLevel, "PassThrough called",
			zap.String("subscriber", l.name),
			zap.String("topic", msg.Topic),
			zap.Any("message", msg.Payload),
		)
		return nil
	}
}
