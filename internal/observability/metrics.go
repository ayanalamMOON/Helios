package observability

import (
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// HTTP metrics
	RequestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "helios_requests_total",
			Help: "Total number of HTTP requests",
		},
		[]string{"method", "endpoint", "status"},
	)

	RequestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "helios_request_duration_seconds",
			Help:    "HTTP request duration in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method", "endpoint"},
	)

	// ATLAS metrics
	AOFBytesTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "atlas_aof_bytes_total",
			Help: "Total bytes written to AOF",
		},
	)

	SnapshotDuration = prometheus.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "atlas_snapshot_duration_seconds",
			Help:    "Snapshot operation duration",
			Buckets: prometheus.DefBuckets,
		},
	)

	StoreKeys = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "atlas_store_keys",
			Help: "Number of keys in the store",
		},
	)

	// Worker queue metrics
	JobsEnqueued = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "worker_jobs_enqueued_total",
			Help: "Total jobs enqueued",
		},
	)

	JobsDequeued = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "worker_jobs_dequeued_total",
			Help: "Total jobs dequeued",
		},
	)

	JobsCompleted = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "worker_jobs_completed_total",
			Help: "Total jobs completed",
		},
	)

	JobsFailed = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "worker_jobs_failed_total",
			Help: "Total jobs failed",
		},
	)

	JobQueueDepth = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "worker_job_queue_depth",
			Help: "Current job queue depth",
		},
	)

	// Rate limiter metrics
	RateLimitDenied = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "rate_limiter_denied_total",
			Help: "Total rate limit denials",
		},
	)

	// Proxy metrics
	ProxyBackendLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "proxy_backend_latency_seconds",
			Help:    "Backend latency in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"backend_id"},
	)

	ProxyBackendErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "proxy_backend_errors_total",
			Help: "Total backend errors",
		},
		[]string{"backend_id"},
	)

	ProxyBackendHealthy = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "proxy_backend_healthy",
			Help: "Backend health status (1=healthy, 0=unhealthy)",
		},
		[]string{"backend_id"},
	)
)

func init() {
	// Register all metrics
	prometheus.MustRegister(RequestsTotal)
	prometheus.MustRegister(RequestDuration)
	prometheus.MustRegister(AOFBytesTotal)
	prometheus.MustRegister(SnapshotDuration)
	prometheus.MustRegister(StoreKeys)
	prometheus.MustRegister(JobsEnqueued)
	prometheus.MustRegister(JobsDequeued)
	prometheus.MustRegister(JobsCompleted)
	prometheus.MustRegister(JobsFailed)
	prometheus.MustRegister(JobQueueDepth)
	prometheus.MustRegister(RateLimitDenied)
	prometheus.MustRegister(ProxyBackendLatency)
	prometheus.MustRegister(ProxyBackendErrors)
	prometheus.MustRegister(ProxyBackendHealthy)
}

// MetricsHandler returns an HTTP handler for Prometheus metrics
func MetricsHandler() http.Handler {
	return promhttp.Handler()
}

// HTTPMiddleware wraps an HTTP handler to collect metrics
func HTTPMiddleware(endpoint string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap response writer to capture status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next(rw, r)

		duration := time.Since(start).Seconds()

		RequestsTotal.WithLabelValues(r.Method, endpoint, fmt.Sprintf("%d", rw.statusCode)).Inc()
		RequestDuration.WithLabelValues(r.Method, endpoint).Observe(duration)
	}
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
