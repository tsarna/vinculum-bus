package benchmarks

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"

	bus "github.com/tsarna/vinculum-bus"
	"github.com/tsarna/vinculum-bus/o11y"
	"github.com/tsarna/vinculum-bus/otel"
)

// NoOpSubscriber for benchmarking - minimal overhead
type NoOpSubscriber struct {
	bus.BaseSubscriber
}

func (n *NoOpSubscriber) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	return nil
}

func BenchmarkPublishNoObservability(b *testing.B) {
	logger := zap.NewNop()
	eventBus, err := bus.NewEventBus().WithLogger(logger).Build()
	if err != nil {
		b.Fatalf("Build() returned error: %v", err)
	}
	eventBus.Start()
	defer eventBus.Stop()

	// Add a subscriber to avoid messages being dropped
	subscriber := &NoOpSubscriber{}
	eventBus.Subscribe(context.Background(), "test/topic", subscriber)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		eventBus.Publish(context.Background(), "test/topic", "benchmark message")
	}
}

func BenchmarkPublishWithStandaloneMetrics(b *testing.B) {
	logger := zap.NewNop()
	eventBus, err := bus.NewEventBus().WithLogger(logger).Build()
	if err != nil {
		b.Fatalf("Build() returned error: %v", err)
	}
	eventBus.Start()
	defer eventBus.Stop()

	// Create standalone metrics provider
	metricsProvider := o11y.NewStandaloneMetricsProvider(eventBus, &o11y.StandaloneMetricsConfig{
		Interval:     time.Minute, // Long interval for benchmarking
		MetricsTopic: "$metrics",
		ServiceName:  "benchmark",
	})
	metricsProvider.Start()
	defer metricsProvider.Stop()

	// Create observable EventBus with standalone metrics
	observableEventBus, err := bus.NewEventBus().
		WithLogger(logger).
		WithMetrics(metricsProvider).
		WithServiceInfo("benchmark", "v1.0.0").
		Build()
	if err != nil {
		b.Fatalf("Build() returned error: %v", err)
	}

	observableEventBus.Start()
	defer observableEventBus.Stop()

	// Add a subscriber to avoid messages being dropped
	subscriber := &NoOpSubscriber{}
	observableEventBus.Subscribe(context.Background(), "test/topic", subscriber)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		observableEventBus.Publish(context.Background(), "test/topic", "benchmark message")
	}
}

func BenchmarkPublishWithOpenTelemetry(b *testing.B) {
	logger := zap.NewNop()

	// Create OpenTelemetry provider
	otelProvider := otel.NewProvider("benchmark", "v1.0.0")

	// Create observable EventBus with OpenTelemetry
	observableEventBus, err := bus.NewEventBus().
		WithLogger(logger).
		WithObservability(otelProvider, otelProvider).
		WithServiceInfo("benchmark", "v1.0.0").
		Build()
	if err != nil {
		b.Fatalf("Build() returned error: %v", err)
	}

	observableEventBus.Start()
	defer observableEventBus.Stop()

	// Add a subscriber to avoid messages being dropped
	subscriber := &NoOpSubscriber{}
	observableEventBus.Subscribe(context.Background(), "test/topic", subscriber)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		observableEventBus.Publish(context.Background(), "test/topic", "benchmark message")
	}
}

func BenchmarkPublishSyncNoObservability(b *testing.B) {
	logger := zap.NewNop()
	eventBus, err := bus.NewEventBus().WithLogger(logger).Build()
	if err != nil {
		b.Fatalf("Build() returned error: %v", err)
	}
	eventBus.Start()
	defer eventBus.Stop()

	// Add a subscriber to avoid messages being dropped
	subscriber := &NoOpSubscriber{}
	eventBus.Subscribe(context.Background(), "test/topic", subscriber)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		eventBus.PublishSync(context.Background(), "test/topic", "benchmark message")
	}
}

func BenchmarkThroughput(b *testing.B) {
	logger := zap.NewNop()
	eventBus, err := bus.NewEventBus().WithLogger(logger).Build()
	if err != nil {
		b.Fatalf("Build() returned error: %v", err)
	}
	eventBus.Start()
	defer eventBus.Stop()

	// Add a subscriber
	subscriber := &NoOpSubscriber{}
	eventBus.Subscribe(context.Background(), "test/topic", subscriber)

	// Measure messages per second
	start := time.Now()
	const numMessages = 1000000 // 1 million messages

	for i := 0; i < numMessages; i++ {
		eventBus.Publish(context.Background(), "test/topic", "throughput test")
	}

	// Wait a bit for processing to complete
	time.Sleep(100 * time.Millisecond)
	duration := time.Since(start)

	messagesPerSecond := float64(numMessages) / duration.Seconds()
	b.Logf("Throughput: %.0f messages/second (%.2f million/sec)", messagesPerSecond, messagesPerSecond/1000000)
}
