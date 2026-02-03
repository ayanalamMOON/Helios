# Configuration Reloading

## Overview

Helios supports hot-reloading of configuration without service restart. This allows you to dynamically adjust operational parameters like log levels, rate limits, and timeouts while the system is running.

## Configuration Structure

Configuration is divided into two categories:

### Immutable Fields (Cannot Be Changed After Startup)
- `node_id`: Unique node identifier
- `data_dir`: Data directory path
- `listen_addr`: Service listen address
- `raft_addr`: Raft cluster address
- `raft_enabled`: Whether Raft is enabled

### Hot-Reloadable Fields

#### Observability
- `log_level`: Logging level (DEBUG, INFO, WARN, ERROR, FATAL)
- `metrics_enabled`: Enable/disable Prometheus metrics
- `metrics_port`: Metrics endpoint port
- `tracing_enabled`: Enable/disable distributed tracing
- `tracing_endpoint`: OpenTelemetry collector endpoint

#### Rate Limiting
- `enabled`: Enable/disable rate limiting
- `requests_per_second`: Maximum requests per second
- `burst_size`: Burst capacity
- `cleanup_interval`: Interval for cleaning up expired entries

#### Timeouts
- `read_timeout`: Read operation timeout
- `write_timeout`: Write operation timeout
- `idle_timeout`: Connection idle timeout
- `shutdown_timeout`: Graceful shutdown timeout

#### Performance
- `max_connections`: Maximum concurrent connections
- `worker_pool_size`: Worker goroutine pool size
- `queue_size`: Internal queue size
- `snapshot_interval`: Snapshot creation interval
- `aof_sync_mode`: AOF sync mode (always, every, no)

## Configuration File Example

```yaml
# config.yaml

# Immutable configuration (set once at startup)
immutable:
  node_id: "node-1"
  data_dir: "./data/node1"
  listen_addr: ":6379"
  raft_addr: "127.0.0.1:7000"
  raft_enabled: true

# Hot-reloadable configuration
observability:
  log_level: "INFO"
  metrics_enabled: true
  metrics_port: ":9090"
  tracing_enabled: false
  tracing_endpoint: ""

rate_limiting:
  enabled: true
  requests_per_second: 1000
  burst_size: 100
  cleanup_interval: 5m

timeouts:
  read_timeout: 30s
  write_timeout: 30s
  idle_timeout: 120s
  shutdown_timeout: 30s

performance:
  max_connections: 10000
  worker_pool_size: 100
  queue_size: 10000
  snapshot_interval: 5m
  aof_sync_mode: "every"
```

## Using Configuration Reloading

### Via HTTP API

**Get current configuration:**
```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config
```

**Get configuration in YAML format:**
```bash
curl -H "Authorization: Bearer $TOKEN" \
  -H "Accept: application/x-yaml" \
  http://localhost:8443/admin/config
```

Or using query parameter:
```bash
curl -H "Authorization: Bearer $TOKEN" \
  "http://localhost:8443/admin/config?format=yaml"
```

**Reload configuration:**
```bash
curl -X POST \
  -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config/reload
```

Example response:
```json
{
  "success": true,
  "message": "Configuration reloaded successfully",
  "reload_count": 5,
  "last_reload": "2026-02-04T15:30:45Z"
}
```

**Preview configuration changes (diff):**
```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config/diff
```

Example response:
```json
{
  "has_changes": true,
  "changes": {
    "observability": [
      {
        "field": "log_level",
        "old_value": "INFO",
        "new_value": "DEBUG",
        "category": "observability"
      },
      {
        "field": "metrics_enabled",
        "old_value": true,
        "new_value": false,
        "category": "observability"
      }
    ],
    "rate_limiting": [
      {
        "field": "requests_per_second",
        "old_value": 1000,
        "new_value": 2000,
        "category": "rate_limiting"
      }
    ]
  },
  "summary": "5 field(s) will be updated"
}
```

If there are immutable field changes or validation errors:
```json
{
  "has_changes": true,
  "immutable_changes": [
    {
      "field": "node_id",
      "old_value": "node-1",
      "new_value": "node-2",
      "category": "immutable"
    }
  ],
  "validation_errors": [
    "invalid log level: INVALID"
  ],
  "summary": "3 field(s) will be updated, 1 immutable field(s) cannot be changed, 1 validation error(s)"
}
```

### Automatic File Watching

Helios can automatically watch the configuration file for changes and reload when modifications are detected:

```go
package main

import (
	"github.com/helios/helios/internal/config"
	"log"
)

func main() {
	// Create configuration manager
	mgr, err := config.NewManager("config.yaml")
	if err != nil {
		log.Fatal(err)
	}

	// Start automatic file watching
	if err := mgr.StartWatching(); err != nil {
		log.Fatal(err)
	}
	defer mgr.StopWatching()

	// Register listeners to apply changes
	mgr.AddReloadListener(func(old, new *config.Config) error {
		log.Printf("Config changed: log level %s -> %s",
			old.Observability.LogLevel, new.Observability.LogLevel)
		return nil
	})

	// File changes are now automatically detected and applied
	// Your application continues running...
}
```

**Features:**
- **Automatic Reload**: Changes to config file trigger automatic reload
- **Debouncing**: Rapid successive changes are batched (100ms delay)
- **Editor Support**: Handles editor patterns (delete/recreate, temp files)
- **Safe**: Failed reloads don't crash the application
- **Controllable**: Can be started/stopped as needed

**Checking Status:**
```go
if mgr.IsWatching() {
	log.Println("File watching is active")
}
```

### Programmatic Usage

```go
package main

import (
	"github.com/helios/helios/internal/config"
	"log"
)

func main() {
	// Create configuration manager
	mgr, err := config.NewManager("config.yaml")
	if err != nil {
		log.Fatal(err)
	}

	// Get current configuration
	cfg := mgr.GetConfig()
	log.Printf("Current log level: %s", cfg.Observability.LogLevel)

	// Register reload listener to apply changes
	mgr.AddReloadListener(func(old, new *config.Config) error {
		// Update log level when configuration changes
		if old.Observability.LogLevel != new.Observability.LogLevel {
			log.Printf("Updating log level from %s to %s",
				old.Observability.LogLevel, new.Observability.LogLevel)
			// Apply the change to your logger
			// logger.SetLevel(new.Observability.LogLevel)
		}

		// Update rate limiter settings
		if old.RateLimiting.RequestsPerSecond != new.RateLimiting.RequestsPerSecond {
			log.Printf("Updating rate limit from %d to %d RPS",
				old.RateLimiting.RequestsPerSecond, new.RateLimiting.RequestsPerSecond)
			// Apply the change to your rate limiter
			// rateLimiter.UpdateLimit(new.RateLimiting.RequestsPerSecond)
		}

		return nil
	})

	// Reload configuration (usually triggered by signal or API call)
	if err := mgr.Reload(); err != nil {
		log.Printf("Failed to reload config: %v", err)
	}
}
```

## Reload Process

When configuration is reloaded:

1. **Load**: New configuration is loaded from the file
2. **Validate Immutable**: System ensures immutable fields haven't changed
3. **Validate**: New configuration is validated for correctness
4. **Notify Listeners**: All registered listeners are called with old and new configs
5. **Apply**: If all listeners approve, new configuration is applied atomically

Listeners can veto the reload by returning an error, preventing invalid changes from being applied.

## Configuration Diff (Preview Changes)

Before reloading configuration, you can preview what changes will be applied using the diff endpoint.

### Via HTTP API

**Preview changes from file:**
```bash
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config/diff
```

The response shows:
- **has_changes**: Whether any changes were detected
- **changes**: Grouped by category (observability, rate_limiting, timeouts, performance)
- **immutable_changes**: Immutable fields that would prevent reload (if any)
- **validation_errors**: Configuration validation errors (if any)
- **summary**: Human-readable summary of changes

### Programmatic Usage

```go
// Preview reload without applying changes
diff, err := configManager.PreviewReload()
if err != nil {
    log.Fatal(err)
}

// Check if there are changes
if !diff.HasChanges {
    log.Println("No configuration changes detected")
    return
}

// Check for immutable changes (would block reload)
if len(diff.ImmutableChanges) > 0 {
    log.Println("Warning: Immutable fields cannot be changed:")
    for _, change := range diff.ImmutableChanges {
        log.Printf("  - %s: %v → %v", change.Field, change.OldValue, change.NewValue)
    }
}

// Check for validation errors
if len(diff.ValidationErrors) > 0 {
    log.Println("Warning: Configuration has validation errors:")
    for _, err := range diff.ValidationErrors {
        log.Printf("  - %s", err)
    }
}

// Review changes by category
for category, changes := range diff.Changes {
    log.Printf("Changes in %s:", category)
    for _, change := range changes {
        log.Printf("  - %s: %v → %v", change.Field, change.OldValue, change.NewValue)
    }
}

// Summary
log.Println(diff.Summary)
```

### Workflow with Diff

**Recommended workflow for production changes:**

1. **Edit** configuration file
2. **Preview** changes with diff endpoint
3. **Review** changes and verify they're correct
4. **Reload** if changes look good

```bash
# 1. Edit config.yaml (increase rate limit)
vim config.yaml

# 2. Preview changes
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config/diff | jq .

# Output shows:
# {
#   "has_changes": true,
#   "changes": {
#     "rate_limiting": [
#       {
#         "field": "requests_per_second",
#         "old_value": 1000,
#         "new_value": 2000
#       }
#     ]
#   },
#   "summary": "1 field(s) will be updated"
# }

# 3. Looks good! Apply the changes
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config/reload
```

### Diff Response Structure

```typescript
{
  has_changes: boolean,
  changes?: {
    [category: string]: Array<{
      field: string,
      old_value: any,
      new_value: any,
      category: string
    }>
  },
  immutable_changes?: Array<{
    field: string,
    old_value: any,
    new_value: any,
    category: "immutable"
  }>,
  validation_errors?: string[],
  summary: string
}
```

## Workflow Example

### 1. Initial Startup

```bash
# Start Helios with config file
./helios-atlasd --config config.yaml
```

### 2. Update Configuration

Edit `config.yaml` to change hot-reloadable settings:

```yaml
observability:
  log_level: "DEBUG"  # Changed from INFO
  metrics_enabled: true
  metrics_port: ":9090"

rate_limiting:
  enabled: true
  requests_per_second: 2000  # Increased from 1000
  burst_size: 200  # Increased from 100
```

### 3. Trigger Reload

**Option A: Preview changes first (recommended)**
```bash
# Preview what will change
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config/diff | jq .

# If changes look good, apply them
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config/reload
```

**Option B: Automatic (with file watching enabled)**
```bash
# Simply save the config file - reload happens automatically
# No manual action needed!
```

**Option C: Manual via API**
```bash
curl -X POST \
  -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config/reload
```

**Option D: Manual via SIGHUP signal**
```bash
kill -HUP $(pidof helios-atlasd)
```

### 4. Verify Changes

```bash
# Check current configuration
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config | jq '.observability.log_level'
# Should return "DEBUG"
```

## Best Practices

### 1. Enable File Watching for Production

For production deployments, enable automatic file watching:

```go
if err := configManager.StartWatching(); err != nil {
	log.Fatal(err)
}
defer configManager.StopWatching()
```

**Benefits:**
- No manual reload needed - just update the file
- Changes apply within 100-200ms automatically
- Works with configuration management tools (Ansible, Chef, Puppet)
- Handles editor patterns (vim, nano, VS Code)

**When to disable:**
- Development environments where you want explicit control
- Environments where config files shouldn't change (immutable infrastructure)
- When using external configuration services (etcd, Consul)

### 2. Preview Changes Before Applying

Always preview configuration changes before reloading:

```bash
# Backup current config
cp config.yaml config.yaml.backup

# Make changes to config.yaml

# Preview the changes
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config/diff | jq .

# Review the output:
# - Check has_changes is true
# - Verify no immutable_changes (would block reload)
# - Verify no validation_errors
# - Review each change to ensure it's expected

# If everything looks good, apply the changes
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config/reload

# If reload fails, restore backup
if [ $? -ne 0 ]; then
  mv config.yaml.backup config.yaml
fi
```

**Benefits of using diff:**
- Catch immutable field changes before reload fails
- Identify validation errors before applying
- Verify expected changes match reality
- Document what changed for auditing

### 3. Monitor Reload Events

Track reload statistics:

```go
count, lastReload := mgr.GetReloadStats()
log.Printf("Configuration reloaded %d times, last reload: %v", count, lastReload)
```

### 4. Gradual Changes

For critical settings like rate limits, make gradual changes:

```yaml
# Instead of 1000 → 10000 immediately
# Do: 1000 → 2000 → 5000 → 10000 with monitoring between each step
```

### 4. Use Reload Listeners

Implement listeners to apply configuration changes to running components:

```go
mgr.AddReloadListener(func(old, new *config.Config) error {
	// Update logger
	if old.Observability.LogLevel != new.Observability.LogLevel {
		logger.SetLevel(new.Observability.LogLevel)
	}

	// Update rate limiter
	if old.RateLimiting != new.RateLimiting {
		rateLimiter.UpdateConfig(&new.RateLimiting)
	}

	// Update timeouts
	if old.Timeouts != new.Timeouts {
		server.UpdateTimeouts(&new.Timeouts)
	}

	return nil
})
```

### 5. Handle Reload Failures Gracefully

```go
if err := mgr.Reload(); err != nil {
	// Log error but don't crash
	log.Printf("Config reload failed: %v", err)
	// Alert monitoring system
	// metrics.RecordConfigReloadError()
}
```

## Error Handling

### Immutable Field Change

```bash
# Attempt to change node_id
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config/reload

# Response:
{
  "success": false,
  "error": "immutable field change detected: node_id cannot be changed (old: node-1, new: node-2)"
}
```

### Invalid Configuration

```bash
# Configuration with invalid log level
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config/reload

# Response:
{
  "success": false,
  "error": "invalid configuration: invalid log level: INVALID"
}
```

### Listener Veto

```go
// Listener that prevents rate limit from being set too low
mgr.AddReloadListener(func(old, new *config.Config) error {
	if new.RateLimiting.RequestsPerSecond < 100 {
		return fmt.Errorf("rate limit too low: %d (minimum: 100)",
			new.RateLimiting.RequestsPerSecond)
	}
	return nil
})
```

```bash
# Reload with RPS = 50
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config/reload

# Response:
{
  "success": false,
  "error": "reload listener rejected change: rate limit too low: 50 (minimum: 100)"
}
```

## Security Considerations

1. **Authentication Required**: Configuration endpoints require admin authentication
2. **File Permissions**: Ensure config file has appropriate permissions (e.g., 0600)
3. **Audit Logging**: Log all configuration reload attempts and changes
4. **Validation**: Always validate before applying changes
5. **Rollback Plan**: Keep backups and be prepared to rollback quickly

## Integration with Cluster

In a multi-node cluster, configuration changes should be coordinated:

```bash
# Option 1: Update all nodes sequentially
for host in node1 node2 node3; do
  echo "Updating $host..."
  scp config.yaml $host:/etc/helios/config.yaml
  curl -X POST -H "Authorization: Bearer $TOKEN" \
    http://$host:8443/admin/config/reload
done

# Option 2: Use configuration management tool (Ansible, Chef, etc.)
ansible-playbook update-config.yml

# Option 3: Central configuration service
# Store config in etcd/consul and have nodes watch for changes
```

## Monitoring

Track configuration reload metrics:

```prometheus
# Number of configuration reloads
helios_config_reload_total

# Failed reload attempts
helios_config_reload_errors_total

# Time since last successful reload
helios_config_last_reload_timestamp_seconds
```

## Troubleshooting

### Reload Not Taking Effect

1. **Check file permissions**: Ensure the process can read the config file
2. **Verify file path**: Confirm the correct config file is being read
3. **Check for syntax errors**: Validate YAML/JSON syntax
4. **Review logs**: Check application logs for error messages
5. **Verify listeners**: Ensure reload listeners are properly applying changes

### Performance Impact

Configuration reloading has minimal performance impact:

- **Memory**: Creates a new config object (~1-2 KB)
- **CPU**: Brief spike during validation and application
- **Latency**: <10ms for reload operation
- **Downtime**: Zero - configuration is applied atomically

### Rollback Procedure

If a configuration change causes issues:

```bash
# 1. Restore previous configuration
cp config.yaml.backup config.yaml

# 2. Reload
curl -X POST -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config/reload

# 3. Verify restoration
curl -H "Authorization: Bearer $TOKEN" \
  http://localhost:8443/admin/config
```

## Next Steps

- [x] Add configuration file watching for automatic reloads ✅
- [x] Implement SIGHUP signal handling ✅
- [x] Add configuration diff endpoint to preview changes ✅
- [x] Create configuration migration tools ✅ (See [CONFIG_MIGRATION.md](CONFIG_MIGRATION.md))
- [x] Add configuration versioning and history ✅ (See [CONFIG_HISTORY.md](CONFIG_HISTORY.md))

## File Watching Details

### How It Works

The file watching system uses `fsnotify` to monitor the configuration file:

1. **Detection**: Monitors Write, Create, Remove, and Rename events
2. **Debouncing**: Waits 100ms after last change before reloading
3. **Editor Handling**: Re-establishes watch after file recreation (vim pattern)
4. **Validation**: All normal validation rules apply before reload
5. **Error Handling**: Failed reloads log errors but don't crash

### Editor Compatibility

Tested with:
- **vim/vi**: Delete and recreate pattern ✅
- **nano**: In-place editing ✅
- **VS Code**: Multiple rapid saves ✅
- **Emacs**: Backup file pattern ✅
- **sed/awk**: Stream editing ✅

### Performance Impact

- **CPU**: Minimal (<0.1% during idle, brief spike on reload)
- **Memory**: ~4KB for watcher goroutine
- **Latency**: 100-200ms from file save to reload
- **I/O**: Triggered only on actual file changes

### Troubleshooting File Watching

**Problem**: Changes not detected
```bash
# Check if watching is enabled
if mgr.IsWatching() { ... }

# Check file permissions
ls -la config.yaml

# Check for inotify limits (Linux)
cat /proc/sys/fs/inotify/max_user_watches
```

**Problem**: Too many reloads
```bash
# Increase debounce delay (requires code change)
# Default: 100ms, can be increased to 500ms or 1s

# Or disable file watching and use manual reloads
mgr.StopWatching()
```

**Problem**: File watching stops working
```bash
# This can happen if file is moved/deleted permanently
# Stop and restart watching:
mgr.StopWatching()
mgr.StartWatching()
```
