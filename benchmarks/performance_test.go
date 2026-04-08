package benchmarks

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel/metric/noop"
	"go.uber.org/zap"

	bus "github.com/tsarna/vinculum-bus"
	"github.com/tsarna/vinculum-bus/o11y"
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

	// Create standalone meter provider that publishes to the bus
	mp, _ := o11y.NewStandaloneMeterProvider(eventBus, &o11y.StandaloneMetricsConfig{
		Interval:     time.Minute, // Long interval for benchmarking
		MetricsTopic: "$metrics",
		ServiceName:  "benchmark",
	})
	defer mp.Shutdown(context.Background()) //nolint:errcheck

	// Create observable EventBus with standalone metrics
	observableEventBus, err := bus.NewEventBus().
		WithLogger(logger).
		WithMeterProvider(mp).
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

func BenchmarkPublishWithNoopMeterProvider(b *testing.B) {
	logger := zap.NewNop()

	// Create observable EventBus with noop meter provider
	observableEventBus, err := bus.NewEventBus().
		WithLogger(logger).
		WithMeterProvider(noop.NewMeterProvider()).
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
