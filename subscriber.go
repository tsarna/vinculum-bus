package bus

import (
	"context"
	"errors"
	"strings"

	"github.com/tsarna/vinculum-bus/topicmatch"
)

// Note: Unless a subscriber is suubscribed to multiple busses and/or async queueing wrappers, it will
// only be called from one thread, so it doesn't need to worry about concurrent calls.
type Subscriber interface {
	OnSubscribe(ctx context.Context, topic string) error
	OnUnsubscribe(ctx context.Context, topic string) error
	OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error

	// PassThrough is used to pass the message to the next subscriber in the chain, eg to handle
	// cases like request responses.
	PassThrough(msg EventBusMessage) error
}

type BaseSubscriber struct {
}

func (b *BaseSubscriber) OnSubscribe(ctx context.Context, topic string) error {
	return nil
}

func (b *BaseSubscriber) OnUnsubscribe(ctx context.Context, topic string) error {
	return nil
}

func (b *BaseSubscriber) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	return nil
}

func (b *BaseSubscriber) PassThrough(msg EventBusMessage) error {
	return nil
}

// ReportedError is an error a subscriber returns to say it has already reported
// the failure itself, in a form the bus cannot produce — a quoted source line,
// say, from a subscriber that knows where the message was being handled.
//
// The bus skips only its own log line for such an error. It is still returned
// to the caller, still recorded on the delivery span, and still counted, so
// nothing that reads the outcome — a dead-letter path, an ack decision — sees
// any difference. This is a property of one error rather than of a subscriber:
// the same subscriber typically reports what it can render and leaves the rest
// to the bus, and marking it per error is what keeps those two cases from
// silencing each other.
//
// Wrap an error to mark it, delegating Error and Unwrap so the text and the
// underlying type both survive:
//
//	type reported struct{ error }
//
//	func (reported) AlreadyReported()  {}
//	func (e reported) Unwrap() error   { return e.error }
type ReportedError interface {
	error
	AlreadyReported()
}

// alreadyReported reports whether err, or anything it wraps, is a ReportedError.
func alreadyReported(err error) bool {
	var reported ReportedError
	return errors.As(err, &reported)
}

// EventReceiverWrapper is wrapper for converting a function to a Subscriber.

type EventReceiver func(ctx context.Context, topic string, message any, fields map[string]string) error

func NewEventReceiver(receiver EventReceiver) Subscriber {
	return &eventReceiverWrapper{
		BaseSubscriber: BaseSubscriber{},
		receiver:       receiver,
	}
}

type eventReceiverWrapper struct {
	BaseSubscriber
	receiver EventReceiver
}

func (w *eventReceiverWrapper) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	return w.receiver(ctx, topic, message, fields)
}

// Matcher is a function that matches a topic and returns true and a map of fields if the topic matches,
// otherwise false and nil.

type matcher func(topic string) (bool, map[string]string)

func makeMatcher(subscribeMsg EventBusMessage) matcher {
	topicPattern := subscribeMsg.Topic

	switch subscribeMsg.MsgType {
	case MessageTypeSubscribe:
		pattern := subscribeMsg.Topic

		hashIndex := strings.Index(subscribeMsg.Topic, "#")
		plusIndex := strings.Index(subscribeMsg.Topic, "+")

		if hashIndex == -1 && plusIndex == -1 {
			// exact match
			return func(topic string) (bool, map[string]string) {
				return topic == pattern, nil
			}
		} else {
			return func(topic string) (bool, map[string]string) {
				return topicmatch.Matches(pattern, topic), nil
			}
		}
	case MessageTypeSubscribeWithExtraction:
		return func(topic string) (bool, map[string]string) {
			if topicmatch.Matches(topicPattern, topic) {
				return true, topicmatch.Extract(topicPattern, topic)
			}

			return false, nil
		}
	}

	panic("unsupported message type")
}
