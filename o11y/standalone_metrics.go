package o11y

import (
	"context"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// StandaloneMetricsConfig configures the standalone metrics exporter.
type StandaloneMetricsConfig struct {
	Interval     time.Duration // How often to publish metrics (default: 30s)
	MetricsTopic string        // Topic to publish metrics to (default: "$metrics")
	ServiceName  string        // Service name to include in metrics
}

// MetricsSnapshot represents the metrics data published via MetricsPublisher.
type MetricsSnapshot struct {
	Timestamp   time.Time                    `json:"timestamp"`
	ServiceName string                       `json:"service_name"`
	Counters    map[string]float64           `json:"counters"`
	Histograms  map[string]HistogramSnapshot `json:"histograms"`
	Gauges      map[string]float64           `json:"gauges"`
}

// HistogramSnapshot holds pre-aggregated histogram data.
type HistogramSnapshot struct {
	Count        uint64    `json:"count"`
	Sum          float64   `json:"sum"`
	Bounds       []float64 `json:"bounds"`
	BucketCounts []uint64  `json:"bucket_counts"`
}

// StandaloneExporter implements sdkmetric.Exporter by converting OTel metric
// data into MetricsSnapshot and publishing it to a bus topic.
type StandaloneExporter struct {
	publisher   MetricsPublisher
	topic       string
	serviceName string
	shutdown    bool
}

// NewStandaloneExporter creates a new standalone metrics exporter.
func NewStandaloneExporter(publisher MetricsPublisher, config *StandaloneMetricsConfig) *StandaloneExporter {
	if config == nil {
		config = &StandaloneMetricsConfig{}
	}
	topic := config.MetricsTopic
	if topic == "" {
		topic = "$metrics"
	}
	serviceName := config.ServiceName
	if serviceName == "" {
		serviceName = "unknown"
	}
	return &StandaloneExporter{
		publisher:   publisher,
		topic:       topic,
		serviceName: serviceName,
	}
}

// SetPublisher sets the MetricsPublisher reference for publishing metrics.
// This allows creating the exporter before the publisher to avoid circular dependencies.
func (e *StandaloneExporter) SetPublisher(publisher MetricsPublisher) {
	e.publisher = publisher
}

// Export converts OTel metric data into a MetricsSnapshot and publishes it.
func (e *StandaloneExporter) Export(ctx context.Context, rm *metricdata.ResourceMetrics) error {
	if e.shutdown || e.publisher == nil {
		return nil
	}

	snapshot := MetricsSnapshot{
		Timestamp:   time.Now(),
		ServiceName: e.serviceName,
		Counters:    make(map[string]float64),
		Histograms:  make(map[string]HistogramSnapshot),
		Gauges:      make(map[string]float64),
	}

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			switch data := m.Data.(type) {
			case metricdata.Sum[int64]:
				for _, dp := range data.DataPoints {
					snapshot.Counters[m.Name] += float64(dp.Value)
				}
			case metricdata.Sum[float64]:
				for _, dp := range data.DataPoints {
					snapshot.Counters[m.Name] += dp.Value
				}
			case metricdata.Gauge[int64]:
				for _, dp := range data.DataPoints {
					snapshot.Gauges[m.Name] = float64(dp.Value)
				}
			case metricdata.Gauge[float64]:
				for _, dp := range data.DataPoints {
					snapshot.Gauges[m.Name] = dp.Value
				}
			case metricdata.Histogram[int64]:
				for _, dp := range data.DataPoints {
					snapshot.Histograms[m.Name] = HistogramSnapshot{
						Count:        dp.Count,
						Sum:          float64(dp.Sum),
						Bounds:       dp.Bounds,
						BucketCounts: dp.BucketCounts,
					}
				}
			case metricdata.Histogram[float64]:
				for _, dp := range data.DataPoints {
					snapshot.Histograms[m.Name] = HistogramSnapshot{
						Count:        dp.Count,
						Sum:          dp.Sum,
						Bounds:       dp.Bounds,
						BucketCounts: dp.BucketCounts,
					}
				}
			}
		}
	}

	return e.publisher.Publish(ctx, e.topic, snapshot)
}

// Temporality returns cumulative temporality for all instrument kinds.
func (e *StandaloneExporter) Temporality(_ sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}

// Aggregation returns the default aggregation for all instrument kinds.
func (e *StandaloneExporter) Aggregation(_ sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.AggregationDefault{}
}

// ForceFlush is a no-op; flushing is handled by the PeriodicReader.
func (e *StandaloneExporter) ForceFlush(_ context.Context) error {
	return nil
}

// Shutdown marks the exporter as shut down.
func (e *StandaloneExporter) Shutdown(_ context.Context) error {
	e.shutdown = true
	return nil
}

// NewStandaloneMeterProvider creates an sdkmetric.MeterProvider that
// periodically exports metrics to a bus topic via MetricsPublisher.
func NewStandaloneMeterProvider(publisher MetricsPublisher, config *StandaloneMetricsConfig) (*sdkmetric.MeterProvider, *StandaloneExporter) {
	if config == nil {
		config = &StandaloneMetricsConfig{}
	}
	interval := config.Interval
	if interval == 0 {
		interval = 30 * time.Second
	}

	exporter := NewStandaloneExporter(publisher, config)
	reader := sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(interval))
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	return mp, exporter
}
