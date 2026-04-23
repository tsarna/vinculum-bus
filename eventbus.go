package bus

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tsarna/vinculum-bus/topicmatch"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

type EventBus interface {
	Subscriber // An EventBus can be subscribed to other event buses

	Start() error
	Stop() error

	Subscribe(ctx context.Context, topic string, subscriber Subscriber) error
	SubscribeFunc(ctx context.Context, topic string, receiver EventReceiver) (Subscriber, error)
	Unsubscribe(ctx context.Context, topic string, subscriber Subscriber) error
	UnsubscribeAll(ctx context.Context, subscriber Subscriber) error

	Publish(ctx context.Context, topic string, payload any) error
	PublishSync(ctx context.Context, topic string, payload any) error
}

// MessageType represents the type of message in the event bus
type MessageType int

const (
	MessageTypeEvent MessageType = iota
	MessageTypeSubscribe
	MessageTypeSubscribeWithExtraction
	MessageTypeUnsubscribe
	MessageTypeUnsubscribeAll
	MessageTypeEventSync

	MessageTypeOnSubscribe
	MessageTypeOnUnsubscribe
	MessageTypePassThrough
	MessageTypeTick
)

// EventBusMessage represents a message in the event bus with its context and metadata
type EventBusMessage struct {
	Ctx     context.Context
	MsgType MessageType
	Topic   string
	Payload any
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
	ch            chan EventBusMessage
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	started       int32 // Atomic boolean (0 = false, 1 = true)
	subscriptions map[Subscriber]map[string]matcher
	logger        *zap.Logger
	busName       string

	// Observability (nil instruments are safe to call via OTel noop)
	tracer     trace.Tracer
	hasMetrics bool // fast-path to skip attribute allocation

	// OTel metric instruments
	publishCounter     metric.Int64Counter
	publishSyncCounter metric.Int64Counter
	subscribeCounter   metric.Int64Counter
	unsubscribeCounter metric.Int64Counter
	errorCounter       metric.Int64Counter
	latencyHistogram   metric.Float64Histogram
	subscriberGauge    metric.Float64Gauge
}

// Common attribute keys for eventbus metrics
var (
	attrKeyOperation = attribute.Key("operation")
	attrKeyTopic     = attribute.Key("messaging.destination.name")
	attrKeyStatus    = attribute.Key("status")
)

func (b *basicEventBus) setupMetrics(mp metric.MeterProvider) {
	meter := mp.Meter("github.com/tsarna/vinculum-bus")

	b.publishCounter, _ = meter.Int64Counter("messaging.client.sent.messages",
		metric.WithUnit("{message}"),
		metric.WithDescription("Messages published asynchronously to the event bus"),
	)
	b.publishSyncCounter, _ = meter.Int64Counter("messaging.client.sent.messages",
		metric.WithUnit("{message}"),
		metric.WithDescription("Messages published synchronously to the event bus"),
	)
	b.subscribeCounter, _ = meter.Int64Counter("eventbus.subscriptions",
		metric.WithUnit("{subscription}"),
		metric.WithDescription("Topic subscriptions created"),
	)
	b.unsubscribeCounter, _ = meter.Int64Counter("eventbus.unsubscriptions",
		metric.WithUnit("{subscription}"),
		metric.WithDescription("Topic unsubscriptions performed"),
	)
	b.errorCounter, _ = meter.Int64Counter("messaging.client.errors",
		metric.WithUnit("{error}"),
		metric.WithDescription("Errors encountered during event bus operations"),
	)
	b.latencyHistogram, _ = meter.Float64Histogram("messaging.client.operation.duration",
		metric.WithUnit("s"),
		metric.WithDescription("Duration of synchronous publish operations"),
	)
	b.subscriberGauge, _ = meter.Float64Gauge("eventbus.active_subscribers",
		metric.WithUnit("{subscriber}"),
		metric.WithDescription("Current number of active subscribers"),
	)

	b.hasMetrics = true
}

// messagingAttrs returns the common OTel messaging semantic convention attributes
// for a span on this bus.
func (b *basicEventBus) messagingAttrs(topic string) []attribute.KeyValue {
	attrs := []attribute.KeyValue{
		semconv.MessagingDestinationNameKey.String(topic),
		semconv.MessagingSystemKey.String("vinculum"),
	}
	if b.busName != "" {
		attrs = append(attrs, attribute.String("vinculum.bus.name", b.busName))
	}
	return attrs
}

// metricAttrs returns common metric attributes for a topic.
func (b *basicEventBus) metricAttrs(topic string) metric.MeasurementOption {
	attrs := []attribute.KeyValue{
		semconv.MessagingSystemKey.String("eventbus"),
		attrKeyTopic.String(topic),
	}
	if b.busName != "" {
		attrs = append(attrs, attribute.String("vinculum.bus.name", b.busName))
	}
	return metric.WithAttributes(attrs...)
}

// Start begins the event bus's message processing goroutine
func (b *basicEventBus) Start() error {
	// Use atomic compare-and-swap to prevent double-start
	if !atomic.CompareAndSwapInt32(&b.started, 0, 1) {
		return fmt.Errorf("event bus %s already started", b.busName)
	}

	b.wg.Add(1)

	go func() {
		defer b.wg.Done()
		b.logger.Info("EventBus started, listening for messages", zap.String("bus", b.busName))

		for {
			select {
			case msg := <-b.ch: // Thread-safe channel operation
				switch msg.MsgType {
				case MessageTypeEvent:
					// Extract context from message (guaranteed non-nil by public API).
					// ctx carries the publish span context; extract it to link delivery spans.
					ctx := msg.Ctx

					for subscriber := range b.subscriptions {
						for _, matcher := range b.subscriptions[subscriber] {
							if ok, fields := matcher(msg.Topic); ok {
								b.deliverAsync(ctx, msg.Topic, msg.Payload, fields, subscriber)
								break
							}
						}
					}

				case MessageTypeEventSync:
					if err := b.doPublishSync(msg); err != nil {
						b.logger.Error("Error in doPublishSync", zap.Error(err))
						if b.hasMetrics {
							b.errorCounter.Add(msg.Ctx, 1,
								b.metricAttrs(msg.Topic),
								metric.WithAttributes(attrKeyOperation.String("publish_sync")),
							)
						}
					}

				case MessageTypeSubscribe, MessageTypeSubscribeWithExtraction:
					if err := b.doSubscribe(msg); err != nil {
						b.logger.Error("Error in doSubscribe", zap.Error(err))
						if b.hasMetrics {
							b.errorCounter.Add(msg.Ctx, 1,
								b.metricAttrs(msg.Topic),
								metric.WithAttributes(attrKeyOperation.String("subscribe")),
							)
						}
					}
				case MessageTypeUnsubscribe:
					if err := b.doUnsubscribe(msg); err != nil {
						b.logger.Error("Error in doUnsubscribe", zap.Error(err))
						if b.hasMetrics {
							b.errorCounter.Add(msg.Ctx, 1,
								b.metricAttrs(msg.Topic),
								metric.WithAttributes(attrKeyOperation.String("unsubscribe")),
							)
						}
					}
				case MessageTypeUnsubscribeAll:
					if err := b.doUnsubscribeAll(msg); err != nil {
						b.logger.Error("Error in doUnsubscribeAll", zap.Error(err))
						if b.hasMetrics {
							b.errorCounter.Add(msg.Ctx, 1,
								b.metricAttrs(msg.Topic),
								metric.WithAttributes(attrKeyOperation.String("unsubscribe_all")),
							)
						}
					}
				default:
					b.logger.Debug("EventBus received unknown message type", zap.Int("msgType", int(msg.MsgType)))
				}
			case <-b.ctx.Done():
				b.logger.Info("EventBus stopping")
				return
			}
		}
	}()

	return nil
}

// deliverAsync delivers one async event to a single subscriber, wrapped in a new-root consumer
// span linked to the publish span (per OTel messaging semantic conventions for async pub/sub).
func (b *basicEventBus) deliverAsync(ctx context.Context, topic string, payload any, fields map[string]string, subscriber Subscriber) {
	if b.tracer != nil {
		publishSpanCtx := trace.SpanFromContext(ctx).SpanContext()
		spanOpts := []trace.SpanStartOption{
			trace.WithNewRoot(),
			trace.WithSpanKind(trace.SpanKindConsumer),
			trace.WithAttributes(append(b.messagingAttrs(topic),
				semconv.MessagingOperationTypeDeliver,
				semconv.MessagingOperationNameKey.String("process"),
			)...),
		}
		if publishSpanCtx.IsValid() {
			spanOpts = append(spanOpts, trace.WithLinks(trace.Link{SpanContext: publishSpanCtx}))
		}
		var deliverySpan trace.Span
		ctx, deliverySpan = b.tracer.Start(context.WithoutCancel(ctx), "process "+topic, spanOpts...)
		defer deliverySpan.End()

		if err := subscriber.OnEvent(ctx, topic, payload, fields); err != nil {
			b.logger.Error("Error in OnEvent", zap.Error(err))
			deliverySpan.RecordError(err)
			deliverySpan.SetStatus(codes.Error, err.Error())
			if b.hasMetrics {
				b.errorCounter.Add(ctx, 1,
					b.metricAttrs(topic),
					metric.WithAttributes(attrKeyOperation.String("on_event")),
				)
			}
		}
		return
	}

	// Detach from the producer's context cancellation for async delivery —
	// the producer may have already returned (e.g. an HTTP handler whose
	// request context is canceled after the response is sent). WithoutCancel
	// preserves context values (including trace spans) while preventing
	// cancellation propagation.
	ctx = context.WithoutCancel(ctx)

	if err := subscriber.OnEvent(ctx, topic, payload, fields); err != nil {
		b.logger.Error("Error in OnEvent", zap.Error(err))
		if b.hasMetrics {
			b.errorCounter.Add(ctx, 1,
				b.metricAttrs(topic),
				metric.WithAttributes(attrKeyOperation.String("on_event")),
			)
		}
	}
}

func (b *basicEventBus) Publish(ctx context.Context, topic string, payload any) error {
	if ctx == nil {
		ctx = context.Background()
	}

	// Producer span: ends when the message is enqueued. The span context is
	// stored in ctx and carried in the message so async delivery spans can link to it.
	if b.tracer != nil {
		var span trace.Span
		ctx, span = b.tracer.Start(ctx, "publish "+topic,
			trace.WithSpanKind(trace.SpanKindProducer),
			trace.WithAttributes(append(b.messagingAttrs(topic),
				semconv.MessagingOperationTypePublish,
				semconv.MessagingOperationNameKey.String("publish"),
			)...),
		)
		defer span.End()
	}

	// Record metrics if available
	if b.hasMetrics {
		b.publishCounter.Add(ctx, 1,
			b.metricAttrs(topic),
			metric.WithAttributes(attribute.String("messaging.operation.name", "send")),
		)
	}

	b.accept(EventBusMessage{
		MsgType: MessageTypeEvent,
		Topic:   topic,
		Payload: payload,
		Ctx:     ctx,
	})
	return nil
}

func (b *basicEventBus) PublishSync(ctx context.Context, topic string, payload any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	start := time.Now()

	// Producer span wrapping the entire synchronous call (enqueue + all subscriber delivery).
	var span trace.Span
	if b.tracer != nil {
		ctx, span = b.tracer.Start(ctx, "publish "+topic,
			trace.WithSpanKind(trace.SpanKindProducer),
			trace.WithAttributes(append(b.messagingAttrs(topic),
				semconv.MessagingOperationTypePublish,
				semconv.MessagingOperationNameKey.String("publish"),
			)...),
		)
		defer span.End()
	}

	responseCh := make(chan error, 1)
	b.acceptWithResponse(EventBusMessage{
		MsgType: MessageTypeEventSync,
		Topic:   topic,
		Payload: syncPublishRequest{
			payload:    payload,
			responseCh: responseCh,
		},
		Ctx: ctx,
	}, responseCh)

	err := <-responseCh

	// Record metrics
	if b.hasMetrics {
		attrs := b.metricAttrs(topic)
		statusAttr := metric.WithAttributes(attribute.String("messaging.operation.name", "send_sync"))
		b.publishSyncCounter.Add(ctx, 1, attrs, statusAttr)
		b.latencyHistogram.Record(ctx, time.Since(start).Seconds(), attrs)
	}

	if span != nil {
		if err != nil {
			span.RecordError(err)
			span.SetStatus(codes.Error, err.Error())
		} else {
			span.SetStatus(codes.Ok, "")
		}
	}

	return err
}

func (b *basicEventBus) Subscribe(ctx context.Context, topic string, subscriber Subscriber) error {
	if ctx == nil {
		ctx = context.Background()
	}

	msgType := MessageTypeSubscribe
	if topicmatch.HasExtractions(topic) {
		msgType = MessageTypeSubscribeWithExtraction
	}

	responseCh := make(chan error, 1)
	b.acceptWithResponse(EventBusMessage{
		MsgType: msgType,
		Topic:   topic,
		Payload: subscriptionRequest{
			subscriber: subscriber,
			responseCh: responseCh,
		},
		Ctx: ctx,
	}, responseCh)

	err := <-responseCh

	if b.hasMetrics {
		b.subscribeCounter.Add(ctx, 1, b.metricAttrs(topic))
	}

	return err
}

// SubscribeFunc subscribes a function to a topic. Returns a Subscriber that can be passed to Unsubscribe.
func (b *basicEventBus) SubscribeFunc(ctx context.Context, topic string, receiver EventReceiver) (Subscriber, error) {
	subscriber := NewEventReceiver(receiver)
	err := b.Subscribe(ctx, topic, subscriber)
	if err != nil {
		return nil, err
	}

	return subscriber, nil
}

func (b *basicEventBus) Unsubscribe(ctx context.Context, topic string, subscriber Subscriber) error {
	if ctx == nil {
		ctx = context.Background()
	}

	responseCh := make(chan error, 1)
	b.acceptWithResponse(EventBusMessage{
		MsgType: MessageTypeUnsubscribe,
		Topic:   topic,
		Payload: subscriptionRequest{
			subscriber: subscriber,
			responseCh: responseCh,
		},
		Ctx: ctx,
	}, responseCh)

	err := <-responseCh

	if b.hasMetrics {
		b.unsubscribeCounter.Add(ctx, 1, b.metricAttrs(topic))
	}

	return err
}

func (b *basicEventBus) UnsubscribeAll(ctx context.Context, subscriber Subscriber) error {
	if ctx == nil {
		ctx = context.Background()
	}

	responseCh := make(chan error, 1)
	b.acceptWithResponse(EventBusMessage{
		MsgType: MessageTypeUnsubscribeAll,
		Topic:   "*", // Use "*" to indicate all topics
		Payload: subscriptionRequest{
			subscriber: subscriber,
			responseCh: responseCh,
		},
		Ctx: ctx,
	}, responseCh)

	err := <-responseCh

	if b.hasMetrics {
		b.unsubscribeCounter.Add(ctx, 1, b.metricAttrs("*"))
	}

	return err
}

func (b *basicEventBus) doSubscribe(msg EventBusMessage) error {
	req := msg.Payload.(subscriptionRequest)
	subscriber := req.subscriber

	// Extract context from the message (guaranteed non-nil by public API)
	ctx := msg.Ctx

	var currentSubscriptions map[string]matcher
	var ok bool

	if currentSubscriptions, ok = b.subscriptions[subscriber]; !ok {
		currentSubscriptions = make(map[string]matcher)
		b.subscriptions[subscriber] = currentSubscriptions
	}

	currentSubscriptions[msg.Topic] = makeMatcher(msg)

	// Update subscriber gauge
	if b.hasMetrics {
		subscriberCount := float64(len(b.subscriptions))
		b.subscriberGauge.Record(ctx, subscriberCount)
	}

	err := subscriber.OnSubscribe(ctx, msg.Topic)
	req.responseCh <- err
	return err
}

func (b *basicEventBus) doUnsubscribe(msg EventBusMessage) error {
	req := msg.Payload.(subscriptionRequest)
	subscriber := req.subscriber

	// Extract context from the message (guaranteed non-nil by public API)
	ctx := msg.Ctx

	currentSubscriptions, ok := b.subscriptions[subscriber]
	if !ok {
		req.responseCh <- nil // not subscribed - not an error
		return nil
	}

	delete(currentSubscriptions, msg.Topic)

	if len(currentSubscriptions) == 0 {
		delete(b.subscriptions, subscriber)
	}

	// Update subscriber gauge
	if b.hasMetrics {
		subscriberCount := float64(len(b.subscriptions))
		b.subscriberGauge.Record(ctx, subscriberCount)
	}

	err := subscriber.OnUnsubscribe(ctx, msg.Topic)
	req.responseCh <- err
	return err
}

func (b *basicEventBus) doUnsubscribeAll(msg EventBusMessage) error {
	req := msg.Payload.(subscriptionRequest)
	subscriber := req.subscriber

	// Extract context from the message (guaranteed non-nil by public API)
	ctx := msg.Ctx

	delete(b.subscriptions, subscriber)

	// Update subscriber gauge
	if b.hasMetrics {
		subscriberCount := float64(len(b.subscriptions))
		b.subscriberGauge.Record(ctx, subscriberCount)
	}

	err := subscriber.OnUnsubscribe(ctx, "")
	req.responseCh <- err
	return err
}

func (b *basicEventBus) doPublishSync(msg EventBusMessage) error {
	req := msg.Payload.(syncPublishRequest)

	// Extract context from the message (guaranteed non-nil by public API)
	ctx := msg.Ctx

	// Process each matching subscriber, wrapping each delivery in a child consumer span.
	var publishError error

	for subscriber := range b.subscriptions {
		for _, matcher := range b.subscriptions[subscriber] {
			if ok, fields := matcher(msg.Topic); ok {
				err := b.deliverSync(ctx, msg.Topic, req.payload, fields, subscriber)
				if err != nil {
					if publishError == nil {
						publishError = err // store first error
					} else {
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

// deliverSync delivers one sync event to a single subscriber, wrapped in a child consumer span.
func (b *basicEventBus) deliverSync(ctx context.Context, topic string, payload any, fields map[string]string, subscriber Subscriber) error {
	if b.tracer != nil {
		var deliverySpan trace.Span
		ctx, deliverySpan = b.tracer.Start(ctx, "process "+topic,
			trace.WithSpanKind(trace.SpanKindConsumer),
			trace.WithAttributes(append(b.messagingAttrs(topic),
				semconv.MessagingOperationTypeDeliver,
				semconv.MessagingOperationNameKey.String("process"),
			)...),
		)
		defer deliverySpan.End()

		err := subscriber.OnEvent(ctx, topic, payload, fields)
		if err != nil {
			deliverySpan.RecordError(err)
			deliverySpan.SetStatus(codes.Error, err.Error())
		}
		return err
	}

	return subscriber.OnEvent(ctx, topic, payload, fields)
}

// accept sends a message to the event bus's channel (for publish operations - no response needed)
func (b *basicEventBus) accept(msg EventBusMessage) {
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
func (b *basicEventBus) acceptWithResponse(msg EventBusMessage, responseCh chan error) {
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

// EventBusses implement the Subscriber interface and can be subscribed to other event busses
// Events received from other event busses are published to this event bus
func (b *basicEventBus) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	b.Publish(ctx, topic, message)

	return nil
}

func (b *basicEventBus) OnSubscribe(ctx context.Context, topic string) error {
	return nil
}

func (b *basicEventBus) OnUnsubscribe(ctx context.Context, topic string) error {
	return nil
}

func (b *basicEventBus) PassThrough(msg EventBusMessage) error {
	return nil
}
