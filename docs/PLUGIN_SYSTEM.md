# Helios Plugin System

Helios includes a first-class plugin framework focused on **extensibility**, **runtime safety**, and **operational control**.

## Highlights

- **Lifecycle management**: `Init` → `Start` → `Stop`
- **Multiple extension surfaces**:
  - command hooks (`BeforeCommand`, `AfterCommand`)
  - HTTP middleware wrapping
  - async event subscription (`Publish` / `OnEvent`)
- **Runtime control APIs**:
  - `SetEnabled(ctx, name, enabled)`
  - `Reload(ctx, name, cfg)` (for reload-capable plugins)
  - `RuntimeConfig(name)`
  - `HealthCheck(ctx)`
- **Execution safeguards**:
  - per-plugin timeout
  - fail-open / fail-closed policy
  - circuit breaker with cooldown
  - deterministic priority ordering
- **Factory registry** for built-in and custom plugin loading
- **Operational stats** via `Manager.Stats()` and plugin health snapshots

## Built-in plugins

- `audit`
  - Captures command outcomes and subscribed events in an in-memory ring buffer.
  - Supports command filtering (`command_types`), value preview truncation, and NDJSON export utilities.
- `keyguard`
  - Enforces command/key/value policy rules (size limits, prefix/suffix controls, command allow/deny lists, TTL policy).
- `http_access`
  - HTTP middleware for request telemetry, optional request ID propagation, event emission, and configurable logging detail.
- `traffic_guard`
  - Throughput protection plugin for command hooks using per-scope windows (`session`, `key`, `session_key`, `global`, `command`).

## Quick start

### 1) Enable plugins in runtime

Set environment variable:

- `HELIOS_PLUGINS=audit,keyguard,http_access,traffic_guard`

Both `helios-atlasd` and `helios-gateway` bootstrap plugin manager and load plugins from this variable.

### 2) Programmatic manager setup

```go
pm := plugin.NewManager(plugin.ManagerConfig{
    DefaultTimeout: 150 * time.Millisecond,
    DefaultFailOpen: true,
    EventBuffer: 1024,
    Logger: plugin.NewStdLogger("plugins"),
})

_ = pm.Load("keyguard", plugin.RuntimeConfig{
    Enabled: true,
    FailOpen: false,
    Settings: map[string]interface{}{
        "max_key_length": 256,
        "max_value_bytes": 1_048_576,
        "denied_prefixes": []string{"internal:", "system:"},
    },
})

_ = pm.Load("traffic_guard", plugin.RuntimeConfig{
    Enabled: true,
    FailOpen: false,
    Settings: map[string]interface{}{
        "window": "1s",
        "max_commands": 500,
        "scope": "session",
        "applies_to": []string{"SET", "DEL", "EXPIRE"},
    },
})

_ = pm.Start(context.Background())
defer pm.Stop(context.Background())
```

### 3) Wire into components

- `atlas.Config.PluginManager = pm` (command hooks + event publishing)
- `queue.SetPluginManager(pm)` (queue lifecycle events)
- `gateway.SetPluginManager(pm)` (HTTP middleware wrapping)

## Runtime operations

You can operate plugins without restarting the process:

- Enable/disable a plugin dynamically:
  - `pm.SetEnabled(ctx, "traffic_guard", true)`
- Reload runtime settings (when plugin implements `ConfigReloader`):
  - `pm.Reload(ctx, "keyguard", newCfg)`
- Pull live configuration for inspection:
  - `cfg, ok := pm.RuntimeConfig("audit")`
- Run health sampling across all loaded plugins:
  - `health := pm.HealthCheck(ctx)`

## Gateway plugin admin API

The API gateway exposes a live admin control surface for plugin operations (no process restart required).

All endpoints below require:

- valid bearer token
- `admin:*` permission (admin role)

### Endpoints

- `GET /admin/plugins`
  - Lists all loaded plugins.
  - Query options:
    - `refresh_health` (default: `true`)
    - `include_runtime` (default: `true`)
    - `include_stats` (default: `true`)
- `GET /admin/plugins/{name}`
  - Returns details for one plugin (`runtime`, `state`, `health`).
- `GET /admin/plugins/{name}/health`
  - Runs live health check for a specific plugin.
- `GET /admin/plugins/audit`
  - Queries plugin-admin audit trail records captured by the gateway.
  - Supports filtering and pagination with query parameters:
    - `action`, `target`, `user_id`, `path`, `success`
    - `since`, `until` (RFC3339 / RFC3339Nano)
    - `contains` (substring search across core fields + metadata)
    - `source` (`auto`, `memory`, `persistent`; default `auto`)
    - `sort` (`asc` or `desc`, default `desc`)
    - `limit`, `offset`
  - Returns summary counters (`matched`, `returned`, `success_count`, `failure_count`, `action_breakdown`) plus source diagnostics (`source`, optional `source_warning`) and `persistence` metadata.
- `POST /admin/plugins/audit/compact`
  - Performs manual compaction of persistent plugin-admin audit storage.
  - Enforces retention policy (`max records` and `max age`) immediately and rewrites the on-disk audit file.
  - Returns compaction stats (`before_records`, `after_records`, `removed_records`, `rewritten`, `timestamp`).
- `POST /admin/plugins/{name}/enable`
  - Enables plugin immediately.
- `POST /admin/plugins/{name}/disable`
  - Disables plugin immediately.
- `PUT /admin/plugins/{name}/enabled`
  - Sets enable state explicitly with body:
    - `{ "enabled": true|false }`
- `POST /admin/plugins/{name}/reload`
  - Reloads runtime config for reload-capable plugins.
  - Body supports partial updates:
    - `enabled`, `priority`, `timeout`, `fail_open`, `max_failures`, `cooldown`, `settings`, `replace_settings`
- `POST /admin/plugins/bulk`
  - Executes one action across multiple plugins in one request.
  - Supported actions:
    - `enable`
    - `disable`
    - `set_enabled`
    - `reload`
    - `health`
  - Supports robust execution options:
    - `continue_on_error` (default: `true`)
    - `rollback_on_error` (default: `false`, mutating actions only)
    - `dry_run` (default: `false`)
    - `refresh_health` (default: `true`)
    - `include_runtime` (default: `true`)
    - `include_stats` (default: `true`)
  - Returns per-item results and a summary (`requested`, `processed`, `succeeded`, `failed`, `skipped`).
  - In rollback mode, summary includes both:
    - `continue_on_error_requested`
    - effective `continue_on_error` (forced `false` when rollback mode is enabled)
  - Returns `207 Multi-Status` when any item fails.
  - When `rollback_on_error=true`, gateway performs **best-effort reverse rollback** of already successful items and returns a `rollback` object with per-item rollback outcomes.

### Bulk request shape

```json
{
  "action": "reload",
  "continue_on_error": true,
  "rollback_on_error": true,
  "dry_run": false,
  "default_reload": {
    "timeout": "250ms",
    "settings": {
      "mode": "strict"
    }
  },
  "items": [
    {
      "name": "traffic_guard",
      "reload": {
        "settings": {
          "max_commands": 800
        }
      }
    },
    {
      "name": "keyguard"
    }
  ]
}
```

### Transactional rollback mode (bulk)

For mutating actions (`enable`, `disable`, `set_enabled`, `reload`):

- set `rollback_on_error: true` to enable rollback-friendly execution.
- gateway automatically stops on first failure (transaction-style behavior) even if `continue_on_error` was requested as `true`.
- previously successful items are rolled back in reverse order (best-effort).
- response includes:
  - `summary.rollback_performed`
  - top-level `rollback` block with `attempted`, `succeeded`, `failed`, and per-item details.

> For `health` action, rollback mode is invalid because no mutation occurs.

### Reload payload notes

- `timeout` and `cooldown` accept:
  - duration string (recommended), e.g. `"250ms"`, `"5s"`
  - numeric nanoseconds (for compatibility)
- `settings` merges by default.
- set `replace_settings: true` to replace the entire settings map.

### Error behavior

- unknown plugin → `404`
- plugin does not implement `ConfigReloader` → `409`
- invalid query/body values → `400`
- plugin manager not initialized in gateway → `503`
- bulk requests with mixed success/failure → `207`

### Bulk health sweep example

```json
{
  "action": "health",
  "items": [
    {"name": "traffic_guard"},
    {"name": "keyguard"},
    {"name": "http_access"}
  ],
  "continue_on_error": true,
  "include_runtime": false,
  "include_stats": false
}
```

### Audit query example

```http
GET /admin/plugins/audit?action=reload&success=false&since=2026-04-15T10:00:00Z&limit=50&sort=desc
```

### Persistent audit storage and retention

Gateway plugin-admin audit history supports durable JSONL-backed storage with configurable retention.

- Storage is enabled by default and can be controlled through environment variables:
  - `HELIOS_PLUGIN_ADMIN_AUDIT_PERSIST` (bool, default `true`)
  - `HELIOS_PLUGIN_ADMIN_AUDIT_FILE` (full file path override)
  - `HELIOS_PLUGIN_ADMIN_AUDIT_DIR` (directory fallback; file defaults to `plugin-admin-audit.jsonl`)
  - `HELIOS_PLUGIN_ADMIN_AUDIT_MEMORY_LIMIT` (in-memory query window)
  - `HELIOS_PLUGIN_ADMIN_AUDIT_MAX_RECORDS` (max persisted records retained)
  - `HELIOS_PLUGIN_ADMIN_AUDIT_MAX_AGE` (Go duration, e.g. `720h`, `48h`, `30m`; set `0` to disable age pruning)
  - `HELIOS_PLUGIN_ADMIN_AUDIT_COMPACT_EVERY` (periodic compaction interval in appended records)
  - `HELIOS_PLUGIN_ADMIN_AUDIT_REPAIR_MODE` (`strict` or `skip_bad_lines`, default `strict`)
  - `HELIOS_PLUGIN_ADMIN_AUDIT_BACKGROUND_COMPACT_INTERVAL` (Go duration, e.g. `1m`, `5m`; `0` disables background scheduler)
- Startup integrity repair mode:
  - `strict` fails fast on malformed JSONL lines (default, conservative mode).
  - `skip_bad_lines` ignores malformed lines during startup/file reads and keeps valid records.
  - Startup forced compaction rewrites persisted audit data, so skipped malformed lines are cleaned out of the on-disk file in repair mode.
- Background compaction scheduler:
  - When `HELIOS_PLUGIN_ADMIN_AUDIT_BACKGROUND_COMPACT_INTERVAL > 0`, gateway runs automatic periodic compaction in the background.
  - Scheduler health/telemetry is exposed in audit response `persistence` metadata:
    - `background_compaction_running`
    - `background_compaction_interval`
    - `background_compaction_last_run`
    - `background_compaction_last_error`
    - `skipped_corrupt_lines_total`
    - `last_repair_at`
- Query behavior:
  - `source=auto` prefers persistent records and falls back to memory with a warning when storage cannot be read.
  - `source=persistent` fails fast when persistent storage is unavailable or corrupted.
  - `source=memory` bypasses persistent file reads.
- Manual maintenance:
  - call `POST /admin/plugins/audit/compact` to force compaction at any time.

Example persistent query:

```http
GET /admin/plugins/audit?source=persistent&contains=rollback&limit=100&sort=desc
```

## Admin action audit events

Gateway plugin-admin operations emit structured audit signals to both:

- gateway structured logger (`component=gateway`)
- plugin event bus (for `audit` or custom subscribers)

Published event types:

- `admin.plugin.action` (canonical event)
- `admin.plugin.<action>` (action-specific event), for example:
  - `admin.plugin.enable`
  - `admin.plugin.reload`
  - `admin.plugin.bulk_reload`

Event data includes fields such as:

- `action`
- `target`
- `success`
- `user_id`
- `method`
- `path`
- `remote_addr`
- optional `error`
- optional bulk metadata (`requested`, `processed`, `succeeded`, `failed`, `skipped`, `dry_run`)

## Event model

Plugins can subscribe to exact events or wildcard patterns:

- exact: `queue.job.enqueued`
- prefix wildcard: `queue.*`
- global wildcard: `*`

Common event types emitted by core components:

- `atlas.command.executed`
- `queue.job.enqueued`
- `queue.job.dequeued`
- `queue.job.acked`
- `queue.job.nacked`
- `queue.job.lease_expired`
- `http.request.completed`

## Config schema

`configs/default.yaml` includes an expanded plugin configuration section with examples for:

- `audit` (`command_types`, `max_value_preview_bytes`)
- `keyguard` (`allowed_commands`, `denied_suffixes`, TTL policy)
- `traffic_guard` (`window`, `max_commands`, `scope`)
- `http_access` (`emit_request_id`, `event_name`, request metadata toggles)

> Note: current daemon bootstrap path is environment-driven (`HELIOS_PLUGINS`). The config schema is still useful for central config workflows and inspection/diff APIs.

## Authoring custom plugins

Implement base interface:

- `Metadata() Metadata`
- `Init(context.Context, Host, RuntimeConfig) error`
- `Start(context.Context) error`
- `Stop(context.Context) error`

Optional extension interfaces:

- `CommandHook`
- `HTTPMiddleware`
- `EventSubscriber`
- `ConfigReloader`
- `HealthChecker`

Register custom factories:

- `plugin.RegisterFactory(name, factory)`
- or `plugin.MustRegisterFactory(name, factory)`

## Reliability and safety behavior

For plugin invocations, the manager enforces:

- timeout (`RuntimeConfig.Timeout`)
- fail-open/closed policy (`RuntimeConfig.FailOpen`)
- consecutive failure threshold (`RuntimeConfig.MaxFailures`)
- cooldown-based circuit reopening (`RuntimeConfig.Cooldown`)

This keeps plugin failures isolated and helps prevent cascading outages in the host runtime.
