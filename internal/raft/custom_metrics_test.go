package raft

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCustomMetricsTracker_RegisterMetric(t *testing.T) {
	tempDir := t.TempDir()
	tracker := NewCustomMetricsTracker("test-node", tempDir)
	defer tracker.Shutdown()

	tests := []struct {
		name       string
		metricName string
		metricType CustomMetricType
		help       string
		labels     map[string]string
		buckets    []float64
		wantErr    bool
	}{
		{
			name:       "register counter",
			metricName: "test_counter",
			metricType: MetricTypeCounter,
			help:       "A test counter",
			labels:     map[string]string{"env": "test"},
			wantErr:    false,
		},
		{
			name:       "register gauge",
			metricName: "test_gauge",
			metricType: MetricTypeGauge,
			help:       "A test gauge",
			labels:     nil,
			wantErr:    false,
		},
		{
			name:       "register histogram with custom buckets",
			metricName: "test_histogram",
			metricType: MetricTypeHistogram,
			help:       "A test histogram",
			labels:     map[string]string{"service": "api"},
			buckets:    []float64{0.1, 0.5, 1, 5, 10},
			wantErr:    false,
		},
		{
			name:       "register histogram with default buckets",
			metricName: "test_histogram_default",
			metricType: MetricTypeHistogram,
			help:       "A test histogram with defaults",
			labels:     nil,
			buckets:    nil,
			wantErr:    false,
		},
		{
			name:       "empty metric name",
			metricName: "",
			metricType: MetricTypeCounter,
			help:       "Test",
			wantErr:    true,
		},
		{
			name:       "duplicate metric name",
			metricName: "test_counter",
			metricType: MetricTypeGauge,
			help:       "Duplicate",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tracker.RegisterMetric(tt.metricName, tt.metricType, tt.help, tt.labels, tt.buckets)
			if (err != nil) != tt.wantErr {
				t.Errorf("RegisterMetric() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestCustomMetricsTracker_SetMetric(t *testing.T) {
	tempDir := t.TempDir()
	tracker := NewCustomMetricsTracker("test-node", tempDir)
	defer tracker.Shutdown()

	// Register a gauge
	err := tracker.RegisterMetric("test_gauge", MetricTypeGauge, "Test gauge", nil, nil)
	if err != nil {
		t.Fatalf("Failed to register gauge: %v", err)
	}

	// Set value
	err = tracker.SetMetric("test_gauge", 42.5)
	if err != nil {
		t.Errorf("SetMetric() error = %v", err)
	}

	// Verify value
	metric, err := tracker.GetMetric("test_gauge")
	if err != nil {
		t.Fatalf("GetMetric() error = %v", err)
	}
	if metric.Value != 42.5 {
		t.Errorf("Expected value 42.5, got %f", metric.Value)
	}

	// Try to set non-gauge metric
	tracker.RegisterMetric("test_counter", MetricTypeCounter, "Test counter", nil, nil)
	err = tracker.SetMetric("test_counter", 10)
	if err == nil {
		t.Error("Expected error when setting counter as gauge")
	}

	// Try to set non-existent metric
	err = tracker.SetMetric("non_existent", 5)
	if err == nil {
		t.Error("Expected error for non-existent metric")
	}
}

func TestCustomMetricsTracker_IncrementCounter(t *testing.T) {
	tempDir := t.TempDir()
	tracker := NewCustomMetricsTracker("test-node", tempDir)
	defer tracker.Shutdown()

	// Register a counter
	err := tracker.RegisterMetric("test_counter", MetricTypeCounter, "Test counter", nil, nil)
	if err != nil {
		t.Fatalf("Failed to register counter: %v", err)
	}

	// Increment multiple times
	for i := 0; i < 5; i++ {
		err = tracker.IncrementCounter("test_counter", 1)
		if err != nil {
			t.Errorf("IncrementCounter() error = %v", err)
		}
	}

	// Verify value
	metric, err := tracker.GetMetric("test_counter")
	if err != nil {
		t.Fatalf("GetMetric() error = %v", err)
	}
	if metric.Value != 5 {
		t.Errorf("Expected value 5, got %f", metric.Value)
	}

	// Increment by custom delta
	err = tracker.IncrementCounter("test_counter", 10.5)
	if err != nil {
		t.Errorf("IncrementCounter() error = %v", err)
	}

	metric, _ = tracker.GetMetric("test_counter")
	if metric.Value != 15.5 {
		t.Errorf("Expected value 15.5, got %f", metric.Value)
	}

	// Try negative increment
	err = tracker.IncrementCounter("test_counter", -1)
	if err == nil {
		t.Error("Expected error for negative increment")
	}

	// Try to increment non-counter metric
	tracker.RegisterMetric("test_gauge", MetricTypeGauge, "Test gauge", nil, nil)
	err = tracker.IncrementCounter("test_gauge", 1)
	if err == nil {
		t.Error("Expected error when incrementing gauge as counter")
	}
}

func TestCustomMetricsTracker_ObserveHistogram(t *testing.T) {
	tempDir := t.TempDir()
	tracker := NewCustomMetricsTracker("test-node", tempDir)
	defer tracker.Shutdown()

	// Register a histogram
	err := tracker.RegisterMetric("test_histogram", MetricTypeHistogram, "Test histogram", nil, []float64{0.1, 0.5, 1, 5, 10})
	if err != nil {
		t.Fatalf("Failed to register histogram: %v", err)
	}

	// Record observations
	observations := []float64{0.05, 0.2, 0.7, 1.5, 8.0, 12.0}
	for _, obs := range observations {
		err = tracker.ObserveHistogram("test_histogram", obs)
		if err != nil {
			t.Errorf("ObserveHistogram() error = %v", err)
		}
	}

	// Verify count
	metric, err := tracker.GetMetric("test_histogram")
	if err != nil {
		t.Fatalf("GetMetric() error = %v", err)
	}
	if metric.Value != float64(len(observations)) {
		t.Errorf("Expected %d observations, got %f", len(observations), metric.Value)
	}

	// Verify observations are stored
	if len(metric.Observations) != len(observations) {
		t.Errorf("Expected %d stored observations, got %d", len(observations), len(metric.Observations))
	}

	// Try to observe non-histogram metric
	tracker.RegisterMetric("test_counter", MetricTypeCounter, "Test counter", nil, nil)
	err = tracker.ObserveHistogram("test_counter", 1.0)
	if err == nil {
		t.Error("Expected error when observing counter as histogram")
	}
}

func TestCustomMetricsTracker_GetStats(t *testing.T) {
	tempDir := t.TempDir()
	tracker := NewCustomMetricsTracker("test-node", tempDir)
	defer tracker.Shutdown()

	// Register various metrics
	tracker.RegisterMetric("counter1", MetricTypeCounter, "Counter 1", nil, nil)
	tracker.RegisterMetric("counter2", MetricTypeCounter, "Counter 2", nil, nil)
	tracker.RegisterMetric("gauge1", MetricTypeGauge, "Gauge 1", nil, nil)
	tracker.RegisterMetric("histogram1", MetricTypeHistogram, "Histogram 1", nil, nil)

	stats := tracker.GetStats()

	if stats.TotalMetrics != 4 {
		t.Errorf("Expected 4 total metrics, got %d", stats.TotalMetrics)
	}
	if stats.CounterMetrics != 2 {
		t.Errorf("Expected 2 counters, got %d", stats.CounterMetrics)
	}
	if stats.GaugeMetrics != 1 {
		t.Errorf("Expected 1 gauge, got %d", stats.GaugeMetrics)
	}
	if stats.HistogramMetrics != 1 {
		t.Errorf("Expected 1 histogram, got %d", stats.HistogramMetrics)
	}
	if len(stats.Metrics) != 4 {
		t.Errorf("Expected 4 metrics in stats, got %d", len(stats.Metrics))
	}
}

func TestCustomMetricsTracker_DeleteMetric(t *testing.T) {
	tempDir := t.TempDir()
	tracker := NewCustomMetricsTracker("test-node", tempDir)
	defer tracker.Shutdown()

	// Register a metric
	tracker.RegisterMetric("test_metric", MetricTypeCounter, "Test", nil, nil)

	// Delete it
	err := tracker.DeleteMetric("test_metric")
	if err != nil {
		t.Errorf("DeleteMetric() error = %v", err)
	}

	// Verify it's gone
	_, err = tracker.GetMetric("test_metric")
	if err == nil {
		t.Error("Expected error for deleted metric")
	}

	// Try to delete non-existent metric
	err = tracker.DeleteMetric("non_existent")
	if err == nil {
		t.Error("Expected error for non-existent metric")
	}
}

func TestCustomMetricsTracker_ListMetrics(t *testing.T) {
	tempDir := t.TempDir()
	tracker := NewCustomMetricsTracker("test-node", tempDir)
	defer tracker.Shutdown()

	// Register metrics
	tracker.RegisterMetric("metric1", MetricTypeCounter, "Metric 1", nil, nil)
	tracker.RegisterMetric("metric2", MetricTypeGauge, "Metric 2", map[string]string{"env": "prod"}, nil)

	metrics := tracker.ListMetrics()

	if len(metrics) != 2 {
		t.Errorf("Expected 2 metrics, got %d", len(metrics))
	}

	if _, exists := metrics["metric1"]; !exists {
		t.Error("Expected metric1 to exist")
	}

	if _, exists := metrics["metric2"]; !exists {
		t.Error("Expected metric2 to exist")
	}

	// Verify labels are copied
	if metrics["metric2"].Labels == nil {
		t.Error("Expected labels to be copied")
	}
	if metrics["metric2"].Labels["env"] != "prod" {
		t.Error("Expected env label to be 'prod'")
	}
}

func TestCustomMetricsTracker_Reset(t *testing.T) {
	tempDir := t.TempDir()
	tracker := NewCustomMetricsTracker("test-node", tempDir)
	defer tracker.Shutdown()

	// Register metrics
	tracker.RegisterMetric("metric1", MetricTypeCounter, "Metric 1", nil, nil)
	tracker.RegisterMetric("metric2", MetricTypeGauge, "Metric 2", nil, nil)

	// Reset
	tracker.Reset()

	stats := tracker.GetStats()
	if stats.TotalMetrics != 0 {
		t.Errorf("Expected 0 metrics after reset, got %d", stats.TotalMetrics)
	}
}

func TestCustomMetricsTracker_Persistence(t *testing.T) {
	tempDir := t.TempDir()

	// Create tracker and register metrics
	tracker1 := NewCustomMetricsTracker("test-node", tempDir)
	tracker1.RegisterMetric("persistent_counter", MetricTypeCounter, "Persistent counter", nil, nil)
	tracker1.IncrementCounter("persistent_counter", 10)
	tracker1.RegisterMetric("persistent_gauge", MetricTypeGauge, "Persistent gauge", map[string]string{"label": "value"}, nil)
	tracker1.SetMetric("persistent_gauge", 42.5)

	// Force save
	tracker1.save()
	tracker1.Shutdown()

	// Create new tracker and verify metrics are loaded
	tracker2 := NewCustomMetricsTracker("test-node", tempDir)
	defer tracker2.Shutdown()

	metric1, err := tracker2.GetMetric("persistent_counter")
	if err != nil {
		t.Fatalf("Failed to get persistent counter: %v", err)
	}
	if metric1.Value != 10 {
		t.Errorf("Expected counter value 10, got %f", metric1.Value)
	}

	metric2, err := tracker2.GetMetric("persistent_gauge")
	if err != nil {
		t.Fatalf("Failed to get persistent gauge: %v", err)
	}
	if metric2.Value != 42.5 {
		t.Errorf("Expected gauge value 42.5, got %f", metric2.Value)
	}
	if metric2.Labels["label"] != "value" {
		t.Error("Expected label to be persisted")
	}
}

func TestCustomMetricsTracker_HistogramObservationLimit(t *testing.T) {
	tempDir := t.TempDir()
	tracker := NewCustomMetricsTracker("test-node", tempDir)
	defer tracker.Shutdown()

	// Register a histogram
	tracker.RegisterMetric("test_histogram", MetricTypeHistogram, "Test", nil, nil)

	// Record more than 1000 observations
	for i := 0; i < 1500; i++ {
		tracker.ObserveHistogram("test_histogram", float64(i))
	}

	// Verify only last 1000 are kept
	metric, _ := tracker.GetMetric("test_histogram")
	if len(metric.Observations) != 1000 {
		t.Errorf("Expected 1000 observations, got %d", len(metric.Observations))
	}

	// Verify it's the last 1000 (500-1499)
	if metric.Observations[0] != 500 {
		t.Errorf("Expected first observation to be 500, got %f", metric.Observations[0])
	}
	if metric.Observations[999] != 1499 {
		t.Errorf("Expected last observation to be 1499, got %f", metric.Observations[999])
	}
}

func TestCustomMetricsTracker_ConcurrentAccess(t *testing.T) {
	tempDir := t.TempDir()
	tracker := NewCustomMetricsTracker("test-node", tempDir)
	defer tracker.Shutdown()

	// Register metrics
	tracker.RegisterMetric("concurrent_counter", MetricTypeCounter, "Test", nil, nil)
	tracker.RegisterMetric("concurrent_gauge", MetricTypeGauge, "Test", nil, nil)

	// Concurrent operations
	done := make(chan bool)

	// Goroutine 1: Increment counter
	go func() {
		for i := 0; i < 100; i++ {
			tracker.IncrementCounter("concurrent_counter", 1)
		}
		done <- true
	}()

	// Goroutine 2: Set gauge
	go func() {
		for i := 0; i < 100; i++ {
			tracker.SetMetric("concurrent_gauge", float64(i))
		}
		done <- true
	}()

	// Goroutine 3: Read metrics
	go func() {
		for i := 0; i < 100; i++ {
			tracker.GetStats()
		}
		done <- true
	}()

	// Wait for all goroutines
	for i := 0; i < 3; i++ {
		<-done
	}

	// Verify counter value
	metric, _ := tracker.GetMetric("concurrent_counter")
	if metric.Value != 100 {
		t.Errorf("Expected counter value 100, got %f", metric.Value)
	}
}

func TestCustomMetricsTracker_FileOperations(t *testing.T) {
	tempDir := t.TempDir()
	tracker := NewCustomMetricsTracker("test-node", tempDir)

	// Register and save
	tracker.RegisterMetric("test", MetricTypeCounter, "Test", nil, nil)
	tracker.save()

	// Verify file exists
	filePath := filepath.Join(tempDir, "custom-metrics.json")
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		t.Error("Expected custom-metrics.json to exist")
	}

	tracker.Shutdown()
}
