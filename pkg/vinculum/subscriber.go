package vinculum

import (
	"context"
	"strings"

	"github.com/amir-yaghoubi/mqttpattern"
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
				return mqttpattern.Matches(pattern, topic), nil
			}
		}
	case MessageTypeSubscribeWithExtraction:
		return func(topic string) (bool, map[string]string) {
			if mqttpattern.Matches(topicPattern, topic) {
				return true, mqttpattern.Extract(topicPattern, topic)
			}

			return false, nil
		}
	}

	panic("unsupported message type")
}
