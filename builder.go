package bus

import (
	"context"
	"fmt"

	"github.com/tsarna/vinculum-bus/o11y"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// EventBusBuilder provides a fluent interface for creating EventBus instances
type EventBusBuilder struct {
	logger          *zap.Logger
	bufferSize      int
	busName         string
	metricsProvider o11y.MetricsProvider
	tracerProvider  trace.TracerProvider
	serviceName     string
	serviceVersion  string
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

// WithMetrics sets the metrics provider for the EventBus
func (b *EventBusBuilder) WithMetrics(provider o11y.MetricsProvider) *EventBusBuilder {
	b.metricsProvider = provider
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
	}

	if b.metricsProvider != nil {
		eb.setupObservability(&o11y.ObservabilityConfig{
			MetricsProvider: b.metricsProvider,
			ServiceName:     b.serviceName,
			ServiceVersion:  b.serviceVersion,
		})
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
