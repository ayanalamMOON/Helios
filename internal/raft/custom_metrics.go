package raft

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// CustomMetricType represents the type of custom metric
type CustomMetricType string

const (
	MetricTypeCounter   CustomMetricType = "counter"
	MetricTypeGauge     CustomMetricType = "gauge"
	MetricTypeHistogram CustomMetricType = "histogram"
)

// CustomMetric represents a user-defined metric
type CustomMetric struct {
	Name         string            `json:"name"`
	Type         CustomMetricType  `json:"type"`
	Help         string            `json:"help"`
	Labels       map[string]string `json:"labels,omitempty"`
	Value        float64           `json:"value"`
	Buckets      []float64         `json:"buckets,omitempty"`      // For histograms
	Observations []float64         `json:"observations,omitempty"` // For histograms
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// CustomMetricsTracker manages user-defined metrics
type CustomMetricsTracker struct {
	mu        sync.RWMutex
	metrics   map[string]*CustomMetric
	dataDir   string
	saveTimer *time.Timer
	nodeID    string
}

// CustomMetricStats provides statistics about custom metrics
type CustomMetricStats struct {
	TotalMetrics     int                      `json:"total_metrics"`
	CounterMetrics   int                      `json:"counter_metrics"`
	GaugeMetrics     int                      `json:"gauge_metrics"`
	HistogramMetrics int                      `json:"histogram_metrics"`
	Metrics          map[string]*CustomMetric `json:"metrics"`
}

// NewCustomMetricsTracker creates a new custom metrics tracker
func NewCustomMetricsTracker(nodeID, dataDir string) *CustomMetricsTracker {
	tracker := &CustomMetricsTracker{
		metrics: make(map[string]*CustomMetric),
		dataDir: dataDir,
		nodeID:  nodeID,
	}

	// Load persisted metrics
	tracker.load()

	return tracker
}

// RegisterMetric registers a new custom metric
func (t *CustomMetricsTracker) RegisterMetric(name string, metricType CustomMetricType, help string, labels map[string]string, buckets []float64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Validate metric name
	if name == "" {
		return fmt.Errorf("metric name cannot be empty")
	}

	// Check if metric already exists
	if _, exists := t.metrics[name]; exists {
		return fmt.Errorf("metric %s already exists", name)
	}

	// Validate buckets for histograms
	if metricType == MetricTypeHistogram {
		if len(buckets) == 0 {
			// Default buckets
			buckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}
		}
	}

	metric := &CustomMetric{
		Name:         name,
		Type:         metricType,
		Help:         help,
		Labels:       labels,
		Value:        0,
		Buckets:      buckets,
		Observations: make([]float64, 0),
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	t.metrics[name] = metric
	t.scheduleSave()

	return nil
}

// SetMetric sets the value of a gauge metric
func (t *CustomMetricsTracker) SetMetric(name string, value float64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	metric, exists := t.metrics[name]
	if !exists {
		return fmt.Errorf("metric %s not found", name)
	}

	if metric.Type != MetricTypeGauge {
		return fmt.Errorf("metric %s is not a gauge", name)
	}

	metric.Value = value
	metric.UpdatedAt = time.Now()
	t.scheduleSave()

	return nil
}

// IncrementCounter increments a counter metric
func (t *CustomMetricsTracker) IncrementCounter(name string, delta float64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	metric, exists := t.metrics[name]
	if !exists {
		return fmt.Errorf("metric %s not found", name)
	}

	if metric.Type != MetricTypeCounter {
		return fmt.Errorf("metric %s is not a counter", name)
	}

	if delta < 0 {
		return fmt.Errorf("counter increment must be non-negative")
	}

	metric.Value += delta
	metric.UpdatedAt = time.Now()
	t.scheduleSave()

	return nil
}

// ObserveHistogram records an observation for a histogram metric
func (t *CustomMetricsTracker) ObserveHistogram(name string, value float64) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	metric, exists := t.metrics[name]
	if !exists {
		return fmt.Errorf("metric %s not found", name)
	}

	if metric.Type != MetricTypeHistogram {
		return fmt.Errorf("metric %s is not a histogram", name)
	}

	// Keep last 1000 observations for statistics
	metric.Observations = append(metric.Observations, value)
	if len(metric.Observations) > 1000 {
		metric.Observations = metric.Observations[len(metric.Observations)-1000:]
	}

	metric.Value++ // Count of observations
	metric.UpdatedAt = time.Now()
	t.scheduleSave()

	return nil
}

// GetMetric retrieves a metric by name
func (t *CustomMetricsTracker) GetMetric(name string) (*CustomMetric, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	metric, exists := t.metrics[name]
	if !exists {
		return nil, fmt.Errorf("metric %s not found", name)
	}

	// Return a copy
	metricCopy := *metric
	if metric.Labels != nil {
		metricCopy.Labels = make(map[string]string)
		for k, v := range metric.Labels {
			metricCopy.Labels[k] = v
		}
	}
	if metric.Buckets != nil {
		metricCopy.Buckets = make([]float64, len(metric.Buckets))
		copy(metricCopy.Buckets, metric.Buckets)
	}
	if metric.Observations != nil {
		metricCopy.Observations = make([]float64, len(metric.Observations))
		copy(metricCopy.Observations, metric.Observations)
	}

	return &metricCopy, nil
}

// ListMetrics returns all registered custom metrics
func (t *CustomMetricsTracker) ListMetrics() map[string]*CustomMetric {
	t.mu.RLock()
	defer t.mu.RUnlock()

	result := make(map[string]*CustomMetric)
	for name, metric := range t.metrics {
		metricCopy := *metric
		if metric.Labels != nil {
			metricCopy.Labels = make(map[string]string)
			for k, v := range metric.Labels {
				metricCopy.Labels[k] = v
			}
		}
		if metric.Buckets != nil {
			metricCopy.Buckets = make([]float64, len(metric.Buckets))
			copy(metricCopy.Buckets, metric.Buckets)
		}
		if metric.Observations != nil {
			metricCopy.Observations = make([]float64, len(metric.Observations))
			copy(metricCopy.Observations, metric.Observations)
		}
		result[name] = &metricCopy
	}

	return result
}

// DeleteMetric removes a custom metric
func (t *CustomMetricsTracker) DeleteMetric(name string) error {
	t.mu.Lock()
	defer t.mu.Unlock()

	if _, exists := t.metrics[name]; !exists {
		return fmt.Errorf("metric %s not found", name)
	}

	delete(t.metrics, name)
	t.scheduleSave()

	return nil
}

// GetStats returns statistics about custom metrics
func (t *CustomMetricsTracker) GetStats() *CustomMetricStats {
	t.mu.RLock()
	defer t.mu.RUnlock()

	stats := &CustomMetricStats{
		TotalMetrics:     len(t.metrics),
		CounterMetrics:   0,
		GaugeMetrics:     0,
		HistogramMetrics: 0,
		Metrics:          make(map[string]*CustomMetric),
	}

	for name, metric := range t.metrics {
		switch metric.Type {
		case MetricTypeCounter:
			stats.CounterMetrics++
		case MetricTypeGauge:
			stats.GaugeMetrics++
		case MetricTypeHistogram:
			stats.HistogramMetrics++
		}

		// Add metric copy to stats
		metricCopy := *metric
		if metric.Labels != nil {
			metricCopy.Labels = make(map[string]string)
			for k, v := range metric.Labels {
				metricCopy.Labels[k] = v
			}
		}
		// Don't include observations in stats to save space
		metricCopy.Observations = nil

		stats.Metrics[name] = &metricCopy
	}

	return stats
}

// Reset clears all custom metrics
func (t *CustomMetricsTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.metrics = make(map[string]*CustomMetric)
	t.scheduleSave()
}

// scheduleSave schedules a save operation
func (t *CustomMetricsTracker) scheduleSave() {
	if t.saveTimer != nil {
		t.saveTimer.Stop()
	}

	t.saveTimer = time.AfterFunc(5*time.Second, func() {
		t.save()
	})
}

// save persists custom metrics to disk
func (t *CustomMetricsTracker) save() {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.dataDir == "" {
		return
	}

	filePath := filepath.Join(t.dataDir, "custom-metrics.json")

	data, err := json.MarshalIndent(t.metrics, "", "  ")
	if err != nil {
		return
	}

	// Write to temp file first
	tempPath := filePath + ".tmp"
	if err := os.WriteFile(tempPath, data, 0644); err != nil {
		return
	}

	// Atomic rename
	os.Rename(tempPath, filePath)
}

// load loads persisted custom metrics from disk
func (t *CustomMetricsTracker) load() {
	if t.dataDir == "" {
		return
	}

	filePath := filepath.Join(t.dataDir, "custom-metrics.json")

	data, err := os.ReadFile(filePath)
	if err != nil {
		return
	}

	var metrics map[string]*CustomMetric
	if err := json.Unmarshal(data, &metrics); err != nil {
		return
	}

	t.metrics = metrics
}

// Shutdown gracefully shuts down the tracker
func (t *CustomMetricsTracker) Shutdown() {
	if t.saveTimer != nil {
		t.saveTimer.Stop()
	}
	t.save()
}
