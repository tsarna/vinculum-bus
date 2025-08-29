package vinculum

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/tsarna/mqttpattern"
	"go.uber.org/zap"
)

type EventBus interface {
	Start() error
	Stop() error

	Subscribe(subscriber Subscriber, topic string) error
	Unsubscribe(subscriber Subscriber, topic string) error
	UnsubscribeAll(subscriber Subscriber) error

	Publish(topic string, payload any) error

	accept(eventBusMessage)
}

type messageType int

const (
	messageTypeEvent messageType = iota
	messageTypeSubscribe
	messageTypeSubscribeWithExtraction
	messageTypeUnsubscribe
)

type eventBusMessage struct {
	msgType messageType
	topic   string
	payload any
}

// subscriptionRequest holds subscriber and response channel for subscribe/unsubscribe operations
type subscriptionRequest struct {
	subscriber Subscriber
	responseCh chan error
}

// basicEventBus implements the EventBus interface using minimal locking.
// Uses atomic operations for the started flag and relies on Go's
// inherently thread-safe channels for message passing.
type basicEventBus struct {
	ch            chan eventBusMessage
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	started       int32 // Atomic boolean (0 = false, 1 = true)
	subscriptions map[Subscriber]map[string]matcher
	logger        *zap.Logger
}

func NewEventBus(logger *zap.Logger) EventBus {
	ctx, cancel := context.WithCancel(context.Background())
	return &basicEventBus{
		ch:            make(chan eventBusMessage, 100), // Buffered channel to prevent blocking
		ctx:           ctx,
		cancel:        cancel,
		subscriptions: make(map[Subscriber]map[string]matcher),
		logger:        logger,
	}
}

// Start begins the event bus's message processing goroutine
func (b *basicEventBus) Start() error {
	// Use atomic compare-and-swap to prevent double-start
	if !atomic.CompareAndSwapInt32(&b.started, 0, 1) {
		return fmt.Errorf("event bus already started")
	}

	b.wg.Add(1)

	go func() {
		defer b.wg.Done()
		b.logger.Info("EventBus started, listening for messages")

		for {
			select {
			case msg := <-b.ch: // Thread-safe channel operation
				switch msg.msgType {
				case messageTypeEvent:
					for subscriber := range b.subscriptions {
						for _, matcher := range b.subscriptions[subscriber] {
							if ok, fields := matcher(msg.topic); ok {
								err := subscriber.OnEvent(msg.topic, msg.payload, fields) // TODO: Handle error
								if err != nil {
									b.logger.Error("Error in OnEvent", zap.Error(err))
								}
								break
							}
						}
					}

				case messageTypeSubscribe, messageTypeSubscribeWithExtraction:
					if err := b.doSubscribe(msg); err != nil {
						b.logger.Error("Error in doSubscribe", zap.Error(err))
					}
				case messageTypeUnsubscribe:
					if err := b.doUnsubscribe(msg); err != nil {
						b.logger.Error("Error in doUnsubscribe", zap.Error(err))
					}
				default:
					b.logger.Debug("EventBus received unknown message type", zap.Int("msgType", int(msg.msgType)))
				}
			case <-b.ctx.Done():
				b.logger.Info("EventBus stopping")
				return
			}
		}
	}()

	return nil
}

func (b *basicEventBus) Publish(topic string, payload any) error {
	b.accept(eventBusMessage{
		msgType: messageTypeEvent,
		topic:   topic,
		payload: payload,
	})
	return nil
}

func (b *basicEventBus) Subscribe(subscriber Subscriber, topic string) error {
	msgType := messageTypeSubscribe
	if mqttpattern.HasExtractions(topic) {
		msgType = messageTypeSubscribeWithExtraction
	}

	responseCh := make(chan error, 1)
	b.acceptWithResponse(eventBusMessage{
		msgType: msgType,
		topic:   topic,
		payload: subscriptionRequest{
			subscriber: subscriber,
			responseCh: responseCh,
		},
	}, responseCh)

	return <-responseCh
}

func (b *basicEventBus) Unsubscribe(subscriber Subscriber, topic string) error {
	responseCh := make(chan error, 1)
	b.acceptWithResponse(eventBusMessage{
		msgType: messageTypeUnsubscribe,
		topic:   topic,
		payload: subscriptionRequest{
			subscriber: subscriber,
			responseCh: responseCh,
		},
	}, responseCh)

	return <-responseCh
}

func (b *basicEventBus) doSubscribe(msg eventBusMessage) error {
	req := msg.payload.(subscriptionRequest)
	subscriber := req.subscriber

	var currentSubscriptions map[string]matcher
	var ok bool

	if currentSubscriptions, ok = b.subscriptions[subscriber]; !ok {
		currentSubscriptions = make(map[string]matcher)
		b.subscriptions[subscriber] = currentSubscriptions
	}

	currentSubscriptions[msg.topic] = makeMatcher(msg)

	err := subscriber.OnSubscribe(msg.topic)
	req.responseCh <- err
	return err
}

func (b *basicEventBus) doUnsubscribe(msg eventBusMessage) error {
	req := msg.payload.(subscriptionRequest)
	subscriber := req.subscriber

	currentSubscriptions, ok := b.subscriptions[subscriber]
	if !ok {
		req.responseCh <- nil // not subscribed - not an error
		return nil
	}

	delete(currentSubscriptions, msg.topic)

	if len(currentSubscriptions) == 0 {
		delete(b.subscriptions, subscriber)
	}

	err := subscriber.OnUnsubscribe(msg.topic)
	req.responseCh <- err
	return err
}

func (b *basicEventBus) UnsubscribeAll(subscriber Subscriber) error {
	delete(b.subscriptions, subscriber)

	return subscriber.OnUnsubscribe("")
}

// accept sends a message to the event bus's channel (for publish operations - no response needed)
func (b *basicEventBus) accept(msg eventBusMessage) {
	// Quick atomic check - no mutex needed
	if atomic.LoadInt32(&b.started) == 0 {
		b.logger.Warn("Event bus not started, message ignored")
		return
	}

	select {
	case b.ch <- msg: // Thread-safe channel operation
		// Message sent successfully
	case <-b.ctx.Done():
		b.logger.Debug("EventBus stopped, message ignored")
	default:
		b.logger.Warn("Event bus channel full, message dropped")
	}
}

// acceptWithResponse sends a subscription message and handles error responses
func (b *basicEventBus) acceptWithResponse(msg eventBusMessage, responseCh chan error) {
	sendErrorResponse := func(err error) {
		responseCh <- err
	}

	// Quick atomic check - no mutex needed
	if atomic.LoadInt32(&b.started) == 0 {
		b.logger.Warn("Event bus not started, message ignored")
		sendErrorResponse(fmt.Errorf("event bus not started"))
		return
	}

	select {
	case b.ch <- msg: // Thread-safe channel operation
		// Message sent successfully
	case <-b.ctx.Done():
		b.logger.Debug("EventBus stopped, message ignored")
		sendErrorResponse(fmt.Errorf("event bus stopped"))
	default:
		b.logger.Warn("Event bus channel full, message dropped")
		sendErrorResponse(fmt.Errorf("event bus channel full"))
	}
}

// Stop gracefully shuts down the event bus
func (b *basicEventBus) Stop() error {
	// Use atomic compare-and-swap to prevent double-stop
	if !atomic.CompareAndSwapInt32(&b.started, 1, 0) {
		return fmt.Errorf("event bus not started")
	}

	b.cancel()
	b.wg.Wait()
	close(b.ch) // Thread-safe channel operation

	b.logger.Info("EventBus stopped")
	return nil
}
