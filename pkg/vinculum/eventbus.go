package vinculum

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tsarna/mqttpattern"
	"go.uber.org/zap"
)

type EventBus interface {
	Start() error
	Stop() error

	Subscribe(ctx context.Context, subscriber Subscriber, topic string) error
	Unsubscribe(ctx context.Context, subscriber Subscriber, topic string) error
	UnsubscribeAll(ctx context.Context, subscriber Subscriber) error

	Publish(ctx context.Context, topic string, payload any) error
	PublishSync(ctx context.Context, topic string, payload any) error
}

type messageType int

const (
	messageTypeEvent messageType = iota
	messageTypeSubscribe
	messageTypeSubscribeWithExtraction
	messageTypeUnsubscribe
	messageTypeEventSync
)

type eventBusMessage struct {
	ctx     context.Context
	msgType messageType
	topic   string
	payload any
}

// subscriptionRequest holds subscriber and response channel for subscribe/unsubscribe operations
type subscriptionRequest struct {
	subscriber Subscriber
	responseCh chan error
}

// syncPublishRequest holds payload and response channel for synchronous publish operations
type syncPublishRequest struct {
	payload    any
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

	// Observability (nil if not configured)
	metricsProvider MetricsProvider
	tracingProvider TracingProvider

	// Pre-created metrics (lazy loaded)
	publishCounter     Counter
	publishSyncCounter Counter
	subscribeCounter   Counter
	errorCounter       Counter
	latencyHistogram   Histogram
	subscriberGauge    Gauge
}

func NewEventBus(logger *zap.Logger) EventBus {
	return NewEventBusWithObservability(logger, nil)
}

func NewEventBusWithObservability(logger *zap.Logger, obs *ObservabilityConfig) EventBus {
	ctx, cancel := context.WithCancel(context.Background())
	eb := &basicEventBus{
		ch:            make(chan eventBusMessage, 100), // Buffered channel to prevent blocking
		ctx:           ctx,
		cancel:        cancel,
		subscriptions: make(map[Subscriber]map[string]matcher),
		logger:        logger,
	}

	if obs != nil {
		eb.setupObservability(obs)
	}

	return eb
}

func (b *basicEventBus) setupObservability(config *ObservabilityConfig) {
	b.metricsProvider = config.MetricsProvider
	b.tracingProvider = config.TracingProvider

	if b.metricsProvider != nil {
		// Create metrics instruments
		b.publishCounter = b.metricsProvider.Counter("eventbus_messages_published_total")
		b.publishSyncCounter = b.metricsProvider.Counter("eventbus_messages_published_sync_total")
		b.subscribeCounter = b.metricsProvider.Counter("eventbus_subscriptions_total")
		b.errorCounter = b.metricsProvider.Counter("eventbus_errors_total")
		b.latencyHistogram = b.metricsProvider.Histogram("eventbus_publish_duration_seconds")
		b.subscriberGauge = b.metricsProvider.Gauge("eventbus_active_subscribers")
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
					// Extract context from message (guaranteed non-nil by public API)
					ctx := msg.ctx

					for subscriber := range b.subscriptions {
						for _, matcher := range b.subscriptions[subscriber] {
							if ok, fields := matcher(msg.topic); ok {
								err := subscriber.OnEvent(ctx, msg.topic, msg.payload, fields)
								if err != nil {
									b.logger.Error("Error in OnEvent", zap.Error(err))
									if b.errorCounter != nil {
										b.errorCounter.Add(ctx, 1,
											Label{Key: "operation", Value: "on_event"},
											Label{Key: "topic", Value: msg.topic},
										)
									}
								}
								break
							}
						}
					}

				case messageTypeEventSync:
					if err := b.doPublishSync(msg); err != nil {
						b.logger.Error("Error in doPublishSync", zap.Error(err))
						if b.errorCounter != nil {
							// Use context from message if available
							ctx := msg.ctx
							if ctx == nil {
								ctx = context.Background()
							}
							b.errorCounter.Add(ctx, 1,
								Label{Key: "operation", Value: "publish_sync"},
								Label{Key: "topic", Value: msg.topic},
							)
						}
					}

				case messageTypeSubscribe, messageTypeSubscribeWithExtraction:
					if err := b.doSubscribe(msg); err != nil {
						b.logger.Error("Error in doSubscribe", zap.Error(err))
						if b.errorCounter != nil {
							// Use context from message if available
							ctx := msg.ctx
							if ctx == nil {
								ctx = context.Background()
							}
							b.errorCounter.Add(ctx, 1,
								Label{Key: "operation", Value: "subscribe"},
								Label{Key: "topic", Value: msg.topic},
							)
						}
					}
				case messageTypeUnsubscribe:
					if err := b.doUnsubscribe(msg); err != nil {
						b.logger.Error("Error in doUnsubscribe", zap.Error(err))
						if b.errorCounter != nil {
							// Use context from message if available
							ctx := msg.ctx
							if ctx == nil {
								ctx = context.Background()
							}
							b.errorCounter.Add(ctx, 1,
								Label{Key: "operation", Value: "unsubscribe"},
								Label{Key: "topic", Value: msg.topic},
							)
						}
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

func (b *basicEventBus) Publish(ctx context.Context, topic string, payload any) error {
	// Use the provided context instead of creating a new one
	if ctx == nil {
		ctx = context.Background()
	}

	// Start tracing span if available
	if b.tracingProvider != nil {
		var span Span
		ctx, span = b.tracingProvider.StartSpan(ctx, "eventbus.publish")
		defer span.End()

		span.SetAttributes(
			Label{Key: "topic", Value: topic},
			Label{Key: "operation", Value: "publish"},
		)
	}

	// Record metrics if available
	if b.publishCounter != nil {
		b.publishCounter.Add(ctx, 1, Label{Key: "topic", Value: topic})
	}

	b.accept(eventBusMessage{
		msgType: messageTypeEvent,
		topic:   topic,
		payload: payload,
		ctx:     ctx,
	})
	return nil
}

func (b *basicEventBus) PublishSync(ctx context.Context, topic string, payload any) error {
	// Use the provided context instead of creating a new one
	if ctx == nil {
		ctx = context.Background()
	}
	start := time.Now()

	// Start tracing span if available
	var span Span
	if b.tracingProvider != nil {
		ctx, span = b.tracingProvider.StartSpan(ctx, "eventbus.publish_sync")
		defer span.End()

		span.SetAttributes(
			Label{Key: "topic", Value: topic},
			Label{Key: "operation", Value: "publish_sync"},
		)
	}

	responseCh := make(chan error, 1)
	b.acceptWithResponse(eventBusMessage{
		msgType: messageTypeEventSync,
		topic:   topic,
		payload: syncPublishRequest{
			payload:    payload,
			responseCh: responseCh,
		},
		ctx: ctx,
	}, responseCh)

	err := <-responseCh

	// Record metrics and span status
	if b.publishSyncCounter != nil {
		labels := []Label{
			{Key: "topic", Value: topic},
		}
		if err != nil {
			labels = append(labels, Label{Key: "status", Value: "error"})
		} else {
			labels = append(labels, Label{Key: "status", Value: "success"})
		}
		b.publishSyncCounter.Add(ctx, 1, labels...)
	}

	if b.latencyHistogram != nil {
		duration := time.Since(start).Seconds()
		b.latencyHistogram.Record(ctx, duration, Label{Key: "topic", Value: topic})
	}

	if span != nil {
		if err != nil {
			span.SetStatus(SpanStatusError, err.Error())
		} else {
			span.SetStatus(SpanStatusOK, "")
		}
	}

	return err
}

func (b *basicEventBus) Subscribe(ctx context.Context, subscriber Subscriber, topic string) error {
	// Use the provided context instead of creating a new one
	if ctx == nil {
		ctx = context.Background()
	}

	// Start tracing span if available
	var span Span
	if b.tracingProvider != nil {
		ctx, span = b.tracingProvider.StartSpan(ctx, "eventbus.subscribe")
		defer span.End()

		span.SetAttributes(
			Label{Key: "topic", Value: topic},
			Label{Key: "operation", Value: "subscribe"},
		)
	}

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
		ctx: ctx,
	}, responseCh)

	err := <-responseCh

	// Record metrics and span status
	if b.subscribeCounter != nil {
		labels := []Label{
			{Key: "topic", Value: topic},
		}
		if err != nil {
			labels = append(labels, Label{Key: "status", Value: "error"})
		} else {
			labels = append(labels, Label{Key: "status", Value: "success"})
		}
		b.subscribeCounter.Add(ctx, 1, labels...)
	}

	if span != nil {
		if err != nil {
			span.SetStatus(SpanStatusError, err.Error())
		} else {
			span.SetStatus(SpanStatusOK, "")
		}
	}

	return err
}

func (b *basicEventBus) Unsubscribe(ctx context.Context, subscriber Subscriber, topic string) error {
	if ctx == nil {
		ctx = context.Background()
	}

	responseCh := make(chan error, 1)
	b.acceptWithResponse(eventBusMessage{
		msgType: messageTypeUnsubscribe,
		topic:   topic,
		payload: subscriptionRequest{
			subscriber: subscriber,
			responseCh: responseCh,
		},
		ctx: ctx,
	}, responseCh)

	return <-responseCh
}

func (b *basicEventBus) doSubscribe(msg eventBusMessage) error {
	req := msg.payload.(subscriptionRequest)
	subscriber := req.subscriber

	// Extract context from the message (guaranteed non-nil by public API)
	ctx := msg.ctx

	var currentSubscriptions map[string]matcher
	var ok bool

	if currentSubscriptions, ok = b.subscriptions[subscriber]; !ok {
		currentSubscriptions = make(map[string]matcher)
		b.subscriptions[subscriber] = currentSubscriptions
	}

	currentSubscriptions[msg.topic] = makeMatcher(msg)

	// Update subscriber gauge
	if b.subscriberGauge != nil {
		subscriberCount := float64(len(b.subscriptions))
		b.subscriberGauge.Set(ctx, subscriberCount)
	}

	err := subscriber.OnSubscribe(ctx, msg.topic)
	req.responseCh <- err
	return err
}

func (b *basicEventBus) doUnsubscribe(msg eventBusMessage) error {
	req := msg.payload.(subscriptionRequest)
	subscriber := req.subscriber

	// Extract context from the message (guaranteed non-nil by public API)
	ctx := msg.ctx

	currentSubscriptions, ok := b.subscriptions[subscriber]
	if !ok {
		req.responseCh <- nil // not subscribed - not an error
		return nil
	}

	delete(currentSubscriptions, msg.topic)

	if len(currentSubscriptions) == 0 {
		delete(b.subscriptions, subscriber)
	}

	// Update subscriber gauge
	if b.subscriberGauge != nil {
		subscriberCount := float64(len(b.subscriptions))
		b.subscriberGauge.Set(ctx, subscriberCount)
	}

	err := subscriber.OnUnsubscribe(ctx, msg.topic)
	req.responseCh <- err
	return err
}

func (b *basicEventBus) doPublishSync(msg eventBusMessage) error {
	req := msg.payload.(syncPublishRequest)

	// Extract context from the message (guaranteed non-nil by public API)
	ctx := msg.ctx

	// Process the message just like a regular event, but track any errors
	var publishError error
	eventCount := 0

	for subscriber := range b.subscriptions {
		for _, matcher := range b.subscriptions[subscriber] {
			if ok, fields := matcher(msg.topic); ok {
				eventCount++
				if err := subscriber.OnEvent(ctx, msg.topic, req.payload, fields); err != nil {
					if publishError == nil {
						publishError = err // Store first error
					} else {
						// log any additional errors
						b.logger.Error("Error in OnEvent during sync publish", zap.Error(err))
					}
				}
				break
			}
		}
	}

	// Send response back to caller
	req.responseCh <- publishError
	return publishError
}

func (b *basicEventBus) UnsubscribeAll(ctx context.Context, subscriber Subscriber) error {
	if ctx == nil {
		ctx = context.Background()
	}

	delete(b.subscriptions, subscriber)

	return subscriber.OnUnsubscribe(ctx, "")
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
