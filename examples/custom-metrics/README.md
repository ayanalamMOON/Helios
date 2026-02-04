# Custom Metrics Example

This example demonstrates how to use the custom metrics feature in Helios.

## Setup

Start your Helios cluster with authentication enabled:

```bash
./helios-server --config config.yaml
```

Get an admin token:

```bash
export TOKEN="your-admin-token-here"
```

## 1. Register Custom Metrics

### Register a Counter

Track total API requests:

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  http://localhost:8443/admin/cluster/metrics \
  -d '{
    "name": "api_requests_total",
    "type": "counter",
    "help": "Total number of API requests processed",
    "labels": {
      "service": "api-gateway",
      "environment": "production"
    }
  }'
```

### Register a Gauge

Track current queue depth:

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  http://localhost:8443/admin/cluster/metrics \
  -d '{
    "name": "queue_depth",
    "type": "gauge",
    "help": "Current number of items in processing queue"
  }'
```

### Register a Histogram

Track request processing time:

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  http://localhost:8443/admin/cluster/metrics \
  -d '{
    "name": "request_duration_seconds",
    "type": "histogram",
    "help": "HTTP request duration in seconds",
    "labels": {
      "method": "POST",
      "endpoint": "/api/v1/data"
    },
    "buckets": [0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5]
  }'
```

## 2. Update Metric Values

### Increment Counter

Increment the API request counter:

```bash
curl -X PUT -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  http://localhost:8443/admin/cluster/metrics \
  -d '{
    "name": "api_requests_total",
    "delta": 1
  }'
```

Increment by a custom amount:

```bash
curl -X PUT -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  http://localhost:8443/admin/cluster/metrics \
  -d '{
    "name": "api_requests_total",
    "delta": 10
  }'
```

### Update Gauge

Set the current queue depth:

```bash
curl -X PUT -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  http://localhost:8443/admin/cluster/metrics \
  -d '{
    "name": "queue_depth",
    "value": 42
  }'
```

### Record Histogram Observation

Record a request duration:

```bash
curl -X PUT -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  http://localhost:8443/admin/cluster/metrics \
  -d '{
    "name": "request_duration_seconds",
    "value": 0.125
  }'
```

## 3. Query Metrics

### Get All Metrics

```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/cluster/metrics
```

**Response:**
```json
{
  "total_metrics": 3,
  "counter_metrics": 1,
  "gauge_metrics": 1,
  "histogram_metrics": 1,
  "metrics": {
    "api_requests_total": {
      "name": "api_requests_total",
      "type": "counter",
      "help": "Total number of API requests processed",
      "value": 1523,
      "labels": {
        "service": "api-gateway",
        "environment": "production"
      },
      "created_at": "2024-01-15T10:00:00Z",
      "updated_at": "2024-01-15T14:30:25Z"
    },
    "queue_depth": {
      "name": "queue_depth",
      "type": "gauge",
      "help": "Current number of items in processing queue",
      "value": 42,
      "labels": {},
      "created_at": "2024-01-15T10:05:00Z",
      "updated_at": "2024-01-15T14:30:20Z"
    },
    "request_duration_seconds": {
      "name": "request_duration_seconds",
      "type": "histogram",
      "help": "HTTP request duration in seconds",
      "value": 1523,
      "labels": {
        "method": "POST",
        "endpoint": "/api/v1/data"
      },
      "buckets": [0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5],
      "created_at": "2024-01-15T10:10:00Z",
      "updated_at": "2024-01-15T14:30:25Z"
    }
  }
}
```

### Get Specific Metric

```bash
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8443/admin/cluster/metrics?name=queue_depth"
```

### View in Cluster Status

Custom metrics are included in the cluster status:

```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/cluster/status
```

The response includes a `custom_metrics` section with all your metrics.

## 4. Prometheus Integration

All custom metrics are automatically exposed at the Prometheus endpoint:

```bash
curl http://localhost:8080/metrics
```

Example output:

```
# HELP api_requests_total Total number of API requests processed
# TYPE api_requests_total counter
api_requests_total{service="api-gateway",environment="production"} 1523

# HELP queue_depth Current number of items in processing queue
# TYPE queue_depth gauge
queue_depth 42

# HELP request_duration_seconds HTTP request duration in seconds
# TYPE request_duration_seconds histogram
request_duration_seconds_bucket{method="POST",endpoint="/api/v1/data",le="0.001"} 120
request_duration_seconds_bucket{method="POST",endpoint="/api/v1/data",le="0.005"} 450
request_duration_seconds_bucket{method="POST",endpoint="/api/v1/data",le="0.01"} 890
request_duration_seconds_bucket{method="POST",endpoint="/api/v1/data",le="0.05"} 1200
request_duration_seconds_bucket{method="POST",endpoint="/api/v1/data",le="0.1"} 1450
request_duration_seconds_bucket{method="POST",endpoint="/api/v1/data",le="0.5"} 1500
request_duration_seconds_bucket{method="POST",endpoint="/api/v1/data",le="1"} 1520
request_duration_seconds_bucket{method="POST",endpoint="/api/v1/data",le="5"} 1523
request_duration_seconds_bucket{method="POST",endpoint="/api/v1/data",le="+Inf"} 1523
request_duration_seconds_sum{method="POST",endpoint="/api/v1/data"} 156.789
request_duration_seconds_count{method="POST",endpoint="/api/v1/data"} 1523
```

## 5. Prometheus Queries

### Query Counter Rate

Requests per second:

```promql
rate(api_requests_total[5m])
```

### Query Gauge Value

Current queue depth:

```promql
queue_depth
```

### Query Histogram Percentiles

P95 request duration:

```promql
histogram_quantile(0.95, rate(request_duration_seconds_bucket[5m]))
```

P99 request duration:

```promql
histogram_quantile(0.99, rate(request_duration_seconds_bucket[5m]))
```

Average request duration:

```promql
rate(request_duration_seconds_sum[5m]) / rate(request_duration_seconds_count[5m])
```

## 6. Delete Metrics

Delete a specific metric:

```bash
curl -X DELETE -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8443/admin/cluster/metrics?name=old_metric"
```

## 7. Reset All Metrics

Clear all custom metrics:

```bash
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/cluster/metrics/reset
```

## Use Case Examples

### Track Business Events

```bash
# Register metric
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  http://localhost:8443/admin/cluster/metrics \
  -d '{
    "name": "orders_completed_total",
    "type": "counter",
    "help": "Total number of completed orders"
  }'

# Increment when order completes
curl -X PUT -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  http://localhost:8443/admin/cluster/metrics \
  -d '{"name": "orders_completed_total", "delta": 1}'
```

### Track Cache Performance

```bash
# Register hit counter
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  http://localhost:8443/admin/cluster/metrics \
  -d '{
    "name": "cache_hits_total",
    "type": "counter",
    "help": "Total cache hits"
  }'

# Register miss counter
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  http://localhost:8443/admin/cluster/metrics \
  -d '{
    "name": "cache_misses_total",
    "type": "counter",
    "help": "Total cache misses"
  }'

# Calculate hit rate in Prometheus
# cache_hits_total / (cache_hits_total + cache_misses_total)
```

### Monitor Worker Pool

```bash
# Register active workers gauge
curl -X POST -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  http://localhost:8443/admin/cluster/metrics \
  -d '{
    "name": "worker_pool_active",
    "type": "gauge",
    "help": "Number of active workers"
  }'

# Update as workers start/stop
curl -X PUT -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  http://localhost:8443/admin/cluster/metrics \
  -d '{"name": "worker_pool_active", "value": 8}'
```

## Integration with Application Code

While the API examples above show manual metric management, in production you would typically integrate custom metrics into your application code. Here's a conceptual example:

```go
// Pseudo-code - integrate with your application
func handleAPIRequest(w http.ResponseWriter, r *http.Request) {
    start := time.Now()

    // Increment request counter
    incrementCustomMetric("api_requests_total", 1)

    // Process request
    result := processRequest(r)

    // Record duration
    duration := time.Since(start).Seconds()
    observeCustomMetric("request_duration_seconds", duration)

    // Update queue depth gauge
    queueDepth := getQueueDepth()
    setCustomMetric("queue_depth", float64(queueDepth))

    writeResponse(w, result)
}
```

## Best Practices

1. **Use meaningful names**: `api_requests_total` instead of `requests`
2. **Include units**: `_seconds`, `_bytes`, `_total`
3. **Keep label cardinality low**: Avoid unbounded labels
4. **Document with help text**: Explain what each metric tracks
5. **Choose the right type**:
   - Counter: Things that only go up (requests, errors)
   - Gauge: Things that go up and down (connections, queue depth)
   - Histogram: Distributions (durations, sizes)

## Troubleshooting

### Metric Not Appearing in Prometheus

1. Check if metric is registered:
   ```bash
   curl -H "Authorization: Bearer $TOKEN" \
     "http://localhost:8443/admin/cluster/metrics?name=your_metric"
   ```

2. Verify Prometheus endpoint:
   ```bash
   curl http://localhost:8080/metrics | grep your_metric
   ```

### Permission Denied

Ensure you're using an admin token:
```bash
# Token must have admin role
export TOKEN="admin-token-with-proper-permissions"
```

### Metric Already Exists

Delete the existing metric first:
```bash
curl -X DELETE -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8443/admin/cluster/metrics?name=existing_metric"
```
