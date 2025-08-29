package vinculum

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// StandaloneMetricsConfig configures the standalone metrics provider
type StandaloneMetricsConfig struct {
	Interval     time.Duration // How often to publish metrics (default: 30s)
	MetricsTopic string        // Topic to publish metrics to (default: "$metrics")
	ServiceName  string        // Service name to include in metrics
}

// MetricsSnapshot represents the metrics data published to the EventBus
type MetricsSnapshot struct {
	Timestamp   time.Time            `json:"timestamp"`
	ServiceName string               `json:"service_name"`
	Counters    map[string]int64     `json:"counters"`
	Histograms  map[string][]float64 `json:"histograms"`
	Gauges      map[string]float64   `json:"gauges"`
}

// StandaloneMetricsProvider collects metrics and publishes them to the EventBus periodically
type StandaloneMetricsProvider struct {
	config   StandaloneMetricsConfig
	eventBus EventBus // Reference to the EventBus to publish metrics to

	// Thread-safe metric storage
	counters   sync.Map // map[string]*standaloneCounter
	histograms sync.Map // map[string]*standaloneHistogram
	gauges     sync.Map // map[string]*standaloneGauge

	// Control
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	started int32 // atomic boolean
}

// NewStandaloneMetricsProvider creates a new standalone metrics provider
func NewStandaloneMetricsProvider(eventBus EventBus, config *StandaloneMetricsConfig) *StandaloneMetricsProvider {
	if config == nil {
		config = &StandaloneMetricsConfig{}
	}

	// Set defaults
	if config.Interval == 0 {
		config.Interval = 30 * time.Second
	}
	if config.MetricsTopic == "" {
		config.MetricsTopic = "$metrics"
	}
	if config.ServiceName == "" {
		config.ServiceName = "unknown"
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &StandaloneMetricsProvider{
		config:   *config,
		eventBus: eventBus,
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Start begins the periodic metrics publishing
func (s *StandaloneMetricsProvider) Start() error {
	if !atomic.CompareAndSwapInt32(&s.started, 0, 1) {
		return nil // Already started
	}

	s.wg.Add(1)
	go s.publishLoop()

	return nil
}

// Stop gracefully stops the metrics publishing
func (s *StandaloneMetricsProvider) Stop() error {
	if !atomic.CompareAndSwapInt32(&s.started, 1, 0) {
		return nil // Already stopped
	}

	s.cancel()
	s.wg.Wait()

	return nil
}

// publishLoop runs the periodic metrics publishing
func (s *StandaloneMetricsProvider) publishLoop() {
	defer s.wg.Done()

	ticker := time.NewTicker(s.config.Interval)
	defer ticker.Stop()

	// Publish initial metrics immediately
	s.publishMetrics()

	for {
		select {
		case <-ticker.C:
			s.publishMetrics()
		case <-s.ctx.Done():
			// Publish final metrics before stopping
			s.publishMetrics()
			return
		}
	}
}

// publishMetrics collects current metrics and publishes them
func (s *StandaloneMetricsProvider) publishMetrics() {
	snapshot := MetricsSnapshot{
		Timestamp:   time.Now(),
		ServiceName: s.config.ServiceName,
		Counters:    make(map[string]int64),
		Histograms:  make(map[string][]float64),
		Gauges:      make(map[string]float64),
	}

	// Collect counters
	s.counters.Range(func(key, value interface{}) bool {
		name := key.(string)
		counter := value.(*standaloneCounter)
		snapshot.Counters[name] = atomic.LoadInt64(&counter.value)
		return true
	})

	// Collect histograms
	s.histograms.Range(func(key, value interface{}) bool {
		name := key.(string)
		histogram := value.(*standaloneHistogram)
		histogram.mu.RLock()
		snapshot.Histograms[name] = make([]float64, len(histogram.values))
		copy(snapshot.Histograms[name], histogram.values)
		histogram.mu.RUnlock()
		return true
	})

	// Collect gauges
	s.gauges.Range(func(key, value interface{}) bool {
		name := key.(string)
		gauge := value.(*standaloneGauge)
		snapshot.Gauges[name] = gauge.getValue()
		return true
	})

	// Publish to EventBus with background context
	s.eventBus.Publish(context.Background(), s.config.MetricsTopic, snapshot)
}

// MetricsProvider interface implementation

func (s *StandaloneMetricsProvider) Counter(name string) Counter {
	if existing, ok := s.counters.Load(name); ok {
		return existing.(*standaloneCounter)
	}

	counter := &standaloneCounter{}
	actual, _ := s.counters.LoadOrStore(name, counter)
	return actual.(*standaloneCounter)
}

func (s *StandaloneMetricsProvider) Histogram(name string) Histogram {
	if existing, ok := s.histograms.Load(name); ok {
		return existing.(*standaloneHistogram)
	}

	histogram := &standaloneHistogram{}
	actual, _ := s.histograms.LoadOrStore(name, histogram)
	return actual.(*standaloneHistogram)
}

func (s *StandaloneMetricsProvider) Gauge(name string) Gauge {
	if existing, ok := s.gauges.Load(name); ok {
		return existing.(*standaloneGauge)
	}

	gauge := &standaloneGauge{}
	actual, _ := s.gauges.LoadOrStore(name, gauge)
	return actual.(*standaloneGauge)
}

// Metric implementations

type standaloneCounter struct {
	value int64
}

func (c *standaloneCounter) Add(ctx context.Context, value int64, labels ...Label) {
	atomic.AddInt64(&c.value, value)
}

type standaloneHistogram struct {
	mu     sync.RWMutex
	values []float64
}

func (h *standaloneHistogram) Record(ctx context.Context, value float64, labels ...Label) {
	h.mu.Lock()
	h.values = append(h.values, value)
	h.mu.Unlock()
}

type standaloneGauge struct {
	mu    sync.RWMutex
	value float64
}

func (g *standaloneGauge) Set(ctx context.Context, value float64, labels ...Label) {
	g.mu.Lock()
	g.value = value
	g.mu.Unlock()
}

func (g *standaloneGauge) getValue() float64 {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.value
}
