package bus

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// EventBusBuilder provides a fluent interface for creating EventBus instances
type EventBusBuilder struct {
	logger         *zap.Logger
	bufferSize     int
	busName        string
	undeliverable  bool
	meterProvider  metric.MeterProvider
	tracerProvider trace.TracerProvider
	serviceName    string
	serviceVersion string
}

// NewEventBus creates a new EventBusBuilder
func NewEventBus() *EventBusBuilder {
	return &EventBusBuilder{
		bufferSize: 1000, // default buffer size
	}
}

// WithLogger sets the logger for the EventBus
func (b *EventBusBuilder) WithLogger(logger *zap.Logger) *EventBusBuilder {
	b.logger = logger
	return b
}

// WithName sets the name for the EventBus
func (b *EventBusBuilder) WithName(name string) *EventBusBuilder {
	b.busName = name
	return b
}

// WithBufferSize sets the channel buffer size for the EventBus
func (b *EventBusBuilder) WithBufferSize(size int) *EventBusBuilder {
	b.bufferSize = size
	return b
}

// WithUndeliverable controls whether a message that matched no subscriber is
// republished under UndeliverableTopic, carrying its original context, payload,
// and — reachable through UndeliverableTopicFromContext — the topic that failed
// to route.
//
// Off by default. Publishing to a topic nobody wants is normal in pub/sub and
// must stay free, and a bus that republished every unmatched message would
// double its own load precisely when a configuration is already misrouting
// traffic. The undelivered counter is always kept and is the diagnostic; this
// is the remedy, for a bus where an unmatched message means something is wrong.
func (b *EventBusBuilder) WithUndeliverable(undeliverable bool) *EventBusBuilder {
	b.undeliverable = undeliverable
	return b
}

// WithMeterProvider sets the OTel MeterProvider for the EventBus
func (b *EventBusBuilder) WithMeterProvider(provider metric.MeterProvider) *EventBusBuilder {
	b.meterProvider = provider
	return b
}

// WithTracerProvider sets the OTel TracerProvider for the EventBus
func (b *EventBusBuilder) WithTracerProvider(provider trace.TracerProvider) *EventBusBuilder {
	b.tracerProvider = provider
	return b
}

// WithServiceInfo sets service name and version for observability
func (b *EventBusBuilder) WithServiceInfo(name, version string) *EventBusBuilder {
	b.serviceName = name
	b.serviceVersion = version
	return b
}

// IsValid validates the builder configuration and returns an error if invalid
func (b *EventBusBuilder) IsValid() error {
	if b.bufferSize <= 0 {
		return fmt.Errorf("buffer size must be positive, got %d", b.bufferSize)
	}

	// If service info is partially set, both name and version should be provided
	if (b.serviceName != "" && b.serviceVersion == "") || (b.serviceName == "" && b.serviceVersion != "") {
		return fmt.Errorf("both service name and version must be provided together, got name='%s' version='%s'", b.serviceName, b.serviceVersion)
	}

	return nil
}

// Build creates and returns the EventBus instance, returning an error if configuration is invalid
func (b *EventBusBuilder) Build() (EventBus, error) {
	if err := b.IsValid(); err != nil {
		return nil, err
	}

	// Use nop logger if none provided
	logger := b.logger
	if logger == nil {
		logger = zap.NewNop()
	}

	busName := b.busName

	ctx, cancel := context.WithCancel(context.Background())
	eb := &basicEventBus{
		ch:            make(chan EventBusMessage, b.bufferSize),
		ctx:           ctx,
		cancel:        cancel,
		subscriptions: make(map[Subscriber]map[string]matcher),
		logger:        logger,
		busName:       busName,
		undeliverable: b.undeliverable,
	}

	if b.meterProvider != nil {
		eb.setupMetrics(b.meterProvider)
	}

	if b.tracerProvider != nil {
		scope := "vinculum-bus"
		if busName != "" {
			scope = "vinculum-bus/" + busName
		}
		eb.tracer = b.tracerProvider.Tracer(scope)
	}

	return eb, nil
}
