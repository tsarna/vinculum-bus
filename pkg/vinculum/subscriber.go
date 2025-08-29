package vinculum

import (
	"strings"

	"github.com/tsarna/mqttpattern"
)

type Subscriber interface {
	OnSubscribe(topic string)
	OnUnsubscribe(topic string)
	OnEvent(topic string, message any, fields map[string]string)
}

type BaseSubscriber struct {
}

func (b *BaseSubscriber) OnSubscribe(topic string) {
}

func (b *BaseSubscriber) OnUnsubscribe(topic string) {
}

func (b *BaseSubscriber) OnEvent(topic string, message any, fields map[string]string) {
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
