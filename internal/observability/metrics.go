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

	// Raft peer latency metrics
	RaftPeerLatency = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "raft_peer_latency_seconds",
			Help:    "Latency of Raft RPC calls to peers in seconds",
			Buckets: []float64{0.0005, 0.001, 0.002, 0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1.0},
		},
		[]string{"peer_id", "rpc_type"},
	)

	RaftPeerRPCTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "raft_peer_rpc_total",
			Help: "Total number of Raft RPC calls to peers",
		},
		[]string{"peer_id", "rpc_type", "result"},
	)

	RaftPeerHealthy = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "raft_peer_healthy",
			Help: "Peer health status (1=healthy, 0=unhealthy)",
		},
		[]string{"peer_id"},
	)

	RaftPeerLastContact = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "raft_peer_last_contact_seconds",
			Help: "Seconds since last successful contact with peer",
		},
		[]string{"peer_id"},
	)

	RaftPeerConsecutiveErrors = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "raft_peer_consecutive_errors",
			Help: "Number of consecutive errors for a peer",
		},
		[]string{"peer_id"},
	)

	RaftClusterHealthyPeers = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "raft_cluster_healthy_peers",
			Help: "Number of healthy peers in the cluster",
		},
	)

	RaftClusterUnhealthyPeers = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name: "raft_cluster_unhealthy_peers",
			Help: "Number of unhealthy peers in the cluster",
		},
	)

	// Raft uptime metrics
	RaftNodeUptimeSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "raft_node_uptime_seconds",
			Help: "Current uptime of the Raft node in seconds",
		},
		[]string{"node_id"},
	)

	RaftNodeTotalUptimeSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "raft_node_total_uptime_seconds",
			Help: "Total historical uptime of the Raft node in seconds",
		},
		[]string{"node_id"},
	)

	RaftNodeTotalDowntimeSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "raft_node_total_downtime_seconds",
			Help: "Total historical downtime of the Raft node in seconds",
		},
		[]string{"node_id"},
	)

	RaftNodeUptimePercentage = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "raft_node_uptime_percentage",
			Help: "Uptime percentage of the Raft node (0-100)",
		},
		[]string{"node_id", "period"}, // period: 24h, 7d, 30d, all
	)

	RaftNodeSessionsTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "raft_node_sessions_total",
			Help: "Total number of uptime sessions (restarts)",
		},
		[]string{"node_id"},
	)

	RaftNodeRestartsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "raft_node_restarts_total",
			Help: "Total number of node restarts",
		},
		[]string{"node_id"},
	)

	RaftNodeCrashesTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "raft_node_crashes_total",
			Help: "Total number of node crashes (unclean shutdowns)",
		},
		[]string{"node_id"},
	)

	RaftNodeLeaderTimeSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "raft_node_leader_time_seconds",
			Help: "Total time spent as leader in seconds",
		},
		[]string{"node_id"},
	)

	RaftNodeFollowerTimeSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "raft_node_follower_time_seconds",
			Help: "Total time spent as follower in seconds",
		},
		[]string{"node_id"},
	)

	RaftNodeCandidateTimeSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "raft_node_candidate_time_seconds",
			Help: "Total time spent as candidate in seconds",
		},
		[]string{"node_id"},
	)

	RaftNodeElectionsTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "raft_node_elections_total",
			Help: "Total number of leader elections participated in",
		},
		[]string{"node_id"},
	)

	RaftNodeTermsAsLeaderTotal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "raft_node_terms_as_leader_total",
			Help: "Total number of terms served as leader",
		},
		[]string{"node_id"},
	)

	RaftNodeMTBFSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "raft_node_mtbf_seconds",
			Help: "Mean Time Between Failures in seconds",
		},
		[]string{"node_id"},
	)

	RaftNodeLeadershipRatio = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "raft_node_leadership_ratio",
			Help: "Ratio of time spent as leader (0-1)",
		},
		[]string{"node_id"},
	)

	// Raft snapshot metrics
	RaftSnapshotTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "raft_snapshot_total",
			Help: "Total number of snapshots taken",
		},
		[]string{"node_id", "type"}, // type: created, received, restored
	)

	RaftSnapshotSize = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "raft_snapshot_size_bytes",
			Help: "Size of the latest snapshot in bytes",
		},
		[]string{"node_id"},
	)

	RaftSnapshotDurationSeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "raft_snapshot_duration_seconds",
			Help:    "Duration of snapshot operations in seconds",
			Buckets: []float64{0.1, 0.25, 0.5, 1.0, 2.5, 5.0, 10.0, 30.0, 60.0},
		},
		[]string{"node_id", "operation"}, // operation: create, restore
	)

	RaftSnapshotIndex = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "raft_snapshot_index",
			Help: "Log index of the latest snapshot",
		},
		[]string{"node_id"},
	)

	RaftSnapshotTerm = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "raft_snapshot_term",
			Help: "Term of the latest snapshot",
		},
		[]string{"node_id"},
	)

	RaftSnapshotAgeSeconds = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "raft_snapshot_age_seconds",
			Help: "Age of the latest snapshot in seconds",
		},
		[]string{"node_id"},
	)

	RaftSnapshotErrors = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "raft_snapshot_errors_total",
			Help: "Total number of snapshot errors",
		},
		[]string{"node_id", "operation"}, // operation: create, restore, receive
	)

	RaftSnapshotBytesWritten = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "raft_snapshot_bytes_written_total",
			Help: "Total bytes written by snapshots",
		},
		[]string{"node_id"},
	)

	RaftSnapshotEntriesCompacted = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "raft_snapshot_entries_compacted_total",
			Help: "Total log entries compacted by snapshots",
		},
		[]string{"node_id"},
	)

	RaftSnapshotOnDisk = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "raft_snapshots_on_disk",
			Help: "Number of snapshots stored on disk",
		},
		[]string{"node_id"},
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
	prometheus.MustRegister(RaftPeerLatency)
	prometheus.MustRegister(RaftPeerRPCTotal)
	prometheus.MustRegister(RaftPeerHealthy)
	prometheus.MustRegister(RaftPeerLastContact)
	prometheus.MustRegister(RaftPeerConsecutiveErrors)
	prometheus.MustRegister(RaftClusterHealthyPeers)
	prometheus.MustRegister(RaftClusterUnhealthyPeers)
	// Uptime metrics
	prometheus.MustRegister(RaftNodeUptimeSeconds)
	prometheus.MustRegister(RaftNodeTotalUptimeSeconds)
	prometheus.MustRegister(RaftNodeTotalDowntimeSeconds)
	prometheus.MustRegister(RaftNodeUptimePercentage)
	prometheus.MustRegister(RaftNodeSessionsTotal)
	prometheus.MustRegister(RaftNodeRestartsTotal)
	prometheus.MustRegister(RaftNodeCrashesTotal)
	prometheus.MustRegister(RaftNodeLeaderTimeSeconds)
	prometheus.MustRegister(RaftNodeFollowerTimeSeconds)
	prometheus.MustRegister(RaftNodeCandidateTimeSeconds)
	prometheus.MustRegister(RaftNodeElectionsTotal)
	prometheus.MustRegister(RaftNodeTermsAsLeaderTotal)
	prometheus.MustRegister(RaftNodeMTBFSeconds)
	prometheus.MustRegister(RaftNodeLeadershipRatio)
	// Snapshot metrics
	prometheus.MustRegister(RaftSnapshotTotal)
	prometheus.MustRegister(RaftSnapshotSize)
	prometheus.MustRegister(RaftSnapshotDurationSeconds)
	prometheus.MustRegister(RaftSnapshotIndex)
	prometheus.MustRegister(RaftSnapshotTerm)
	prometheus.MustRegister(RaftSnapshotAgeSeconds)
	prometheus.MustRegister(RaftSnapshotErrors)
	prometheus.MustRegister(RaftSnapshotBytesWritten)
	prometheus.MustRegister(RaftSnapshotEntriesCompacted)
	prometheus.MustRegister(RaftSnapshotOnDisk)

	// Note: Custom metrics are registered dynamically via RegisterCustomMetric()
}

// RegisterCustomMetric dynamically registers a custom user-defined metric with Prometheus
func RegisterCustomMetric(name string, metricType string, help string, labels map[string]string) (interface{}, error) {
	// Build label names slice
	labelNames := make([]string, 0, len(labels))
	for k := range labels {
		labelNames = append(labelNames, k)
	}

	switch metricType {
	case "counter":
		counter := prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Name: name,
				Help: help,
			},
			labelNames,
		)
		if err := prometheus.Register(counter); err != nil {
			return nil, fmt.Errorf("failed to register counter: %w", err)
		}
		return counter, nil

	case "gauge":
		gauge := prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Name: name,
				Help: help,
			},
			labelNames,
		)
		if err := prometheus.Register(gauge); err != nil {
			return nil, fmt.Errorf("failed to register gauge: %w", err)
		}
		return gauge, nil

	case "histogram":
		histogram := prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    name,
				Help:    help,
				Buckets: prometheus.DefBuckets,
			},
			labelNames,
		)
		if err := prometheus.Register(histogram); err != nil {
			return nil, fmt.Errorf("failed to register histogram: %w", err)
		}
		return histogram, nil

	default:
		return nil, fmt.Errorf("unsupported metric type: %s", metricType)
	}
}

// UnregisterCustomMetric removes a custom metric from Prometheus
func UnregisterCustomMetric(collector prometheus.Collector) bool {
	return prometheus.Unregister(collector)
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
