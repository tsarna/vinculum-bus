package vinculum

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
)

// NoOpSubscriber for benchmarking - minimal overhead
type NoOpSubscriber struct {
	BaseSubscriber
}

func (n *NoOpSubscriber) OnEvent(ctx context.Context, topic string, message any, fields map[string]string) error {
	return nil
}

func BenchmarkPublishNoObservability(b *testing.B) {
	logger := zap.NewNop()
	eventBus := NewEventBus(logger)
	eventBus.Start()
	defer eventBus.Stop()

	// Add a subscriber to avoid messages being dropped
	subscriber := &NoOpSubscriber{}
	eventBus.Subscribe(context.Background(), subscriber, "test/topic")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		eventBus.Publish(context.Background(), "test/topic", "benchmark message")
	}
}

// Note: OpenTelemetry benchmark removed to avoid import cycle issues

func BenchmarkPublishWithStandaloneMetrics(b *testing.B) {
	logger := zap.NewNop()
	eventBus := NewEventBus(logger)
	eventBus.Start()
	defer eventBus.Stop()

	// Create standalone metrics provider
	metricsProvider := NewStandaloneMetricsProvider(eventBus, &StandaloneMetricsConfig{
		Interval:     time.Minute, // Long interval for benchmarking
		MetricsTopic: "$metrics",
		ServiceName:  "benchmark",
	})
	metricsProvider.Start()
	defer metricsProvider.Stop()

	// Create observable EventBus with standalone metrics
	observableEventBus := NewEventBusWithObservability(logger, &ObservabilityConfig{
		MetricsProvider: metricsProvider,
		ServiceName:     "benchmark",
		ServiceVersion:  "v1.0.0",
	})

	observableEventBus.Start()
	defer observableEventBus.Stop()

	// Add a subscriber to avoid messages being dropped
	subscriber := &NoOpSubscriber{}
	observableEventBus.Subscribe(context.Background(), subscriber, "test/topic")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		observableEventBus.Publish(context.Background(), "test/topic", "benchmark message")
	}
}

func BenchmarkPublishSyncNoObservability(b *testing.B) {
	logger := zap.NewNop()
	eventBus := NewEventBus(logger)
	eventBus.Start()
	defer eventBus.Stop()

	// Add a subscriber to avoid messages being dropped
	subscriber := &NoOpSubscriber{}
	eventBus.Subscribe(context.Background(), subscriber, "test/topic")

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		eventBus.PublishSync(context.Background(), "test/topic", "benchmark message")
	}
}

func BenchmarkThroughput(b *testing.B) {
	logger := zap.NewNop()
	eventBus := NewEventBus(logger)
	eventBus.Start()
	defer eventBus.Stop()

	// Add a subscriber
	subscriber := &NoOpSubscriber{}
	eventBus.Subscribe(context.Background(), subscriber, "test/topic")

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
