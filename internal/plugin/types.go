package plugin

import (
	"context"
	"net/http"
	"time"
)

// Metadata describes a plugin and its capabilities.
type Metadata struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	Description  string   `json:"description,omitempty"`
	Author       string   `json:"author,omitempty"`
	Tags         []string `json:"tags,omitempty"`
	Dependencies []string `json:"dependencies,omitempty"`
}

// RuntimeConfig controls plugin execution behavior.
type RuntimeConfig struct {
	Enabled     bool                   `json:"enabled"`
	Priority    int                    `json:"priority"`
	Timeout     time.Duration          `json:"timeout"`
	FailOpen    bool                   `json:"fail_open"`
	MaxFailures int                    `json:"max_failures"`
	Cooldown    time.Duration          `json:"cooldown"`
	Settings    map[string]interface{} `json:"settings,omitempty"`
}

// CommandContext contains a normalized command request.
type CommandContext struct {
	Type      string                 `json:"type"`
	Key       string                 `json:"key,omitempty"`
	Value     []byte                 `json:"value,omitempty"`
	TTL       int64                  `json:"ttl,omitempty"`
	SessionID string                 `json:"session_id,omitempty"`
	ReadOnly  bool                   `json:"read_only"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// Clone returns a deep copy of the command context.
func (c *CommandContext) Clone() *CommandContext {
	if c == nil {
		return nil
	}

	out := &CommandContext{
		Type:      c.Type,
		Key:       c.Key,
		TTL:       c.TTL,
		SessionID: c.SessionID,
		ReadOnly:  c.ReadOnly,
	}

	if len(c.Value) > 0 {
		out.Value = make([]byte, len(c.Value))
		copy(out.Value, c.Value)
	}

	if c.Metadata != nil {
		out.Metadata = make(map[string]interface{}, len(c.Metadata))
		for k, v := range c.Metadata {
			out.Metadata[k] = v
		}
	}

	return out
}

// CommandResult captures command execution outcome.
type CommandResult struct {
	Request  *CommandContext        `json:"request"`
	OK       bool                   `json:"ok"`
	Error    error                  `json:"-"`
	Duration time.Duration          `json:"duration"`
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// Event represents a pub/sub event emitted by core systems and plugins.
type Event struct {
	Type      string                 `json:"type"`
	Source    string                 `json:"source"`
	Timestamp time.Time              `json:"timestamp"`
	Data      map[string]interface{} `json:"data,omitempty"`
}

// NewEvent creates a timestamped event.
func NewEvent(eventType, source string, data map[string]interface{}) Event {
	return Event{
		Type:      eventType,
		Source:    source,
		Timestamp: time.Now().UTC(),
		Data:      data,
	}
}

// Host is the host environment exposed to plugins.
type Host interface {
	Publish(event Event)
	Logger() Logger
}

// Plugin is the base plugin lifecycle interface.
type Plugin interface {
	Metadata() Metadata
	Init(ctx context.Context, host Host, cfg RuntimeConfig) error
	Start(ctx context.Context) error
	Stop(ctx context.Context) error
}

// ConfigReloader allows runtime configuration updates without unloading a plugin.
type ConfigReloader interface {
	Reload(ctx context.Context, cfg RuntimeConfig) error
}

// HealthStatus describes plugin health state.
type HealthStatus string

const (
	HealthUnknown   HealthStatus = "unknown"
	HealthHealthy   HealthStatus = "healthy"
	HealthDegraded  HealthStatus = "degraded"
	HealthUnhealthy HealthStatus = "unhealthy"
)

// PluginHealth is a point-in-time health snapshot for one plugin.
type PluginHealth struct {
	Status    HealthStatus           `json:"status"`
	Message   string                 `json:"message,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
	Details   map[string]interface{} `json:"details,omitempty"`
}

// HealthChecker lets plugins provide health diagnostics.
type HealthChecker interface {
	Health(ctx context.Context) (PluginHealth, error)
}

// CommandHook can inspect/mutate command requests and observe outcomes.
type CommandHook interface {
	BeforeCommand(ctx context.Context, req *CommandContext) (*CommandContext, error)
	AfterCommand(ctx context.Context, result *CommandResult) error
}

// HTTPMiddleware wraps HTTP traffic.
type HTTPMiddleware interface {
	WrapHTTP(next http.Handler) http.Handler
}

// EventSubscriber receives published events.
type EventSubscriber interface {
	EventTypes() []string
	OnEvent(ctx context.Context, event Event) error
}

// Logger is intentionally tiny to avoid hard coupling with host logging packages.
type Logger interface {
	Debug(message string, fields map[string]interface{})
	Info(message string, fields map[string]interface{})
	Warn(message string, fields map[string]interface{})
	Error(message string, fields map[string]interface{})
}

// NoopLogger is used when no logger is provided.
type NoopLogger struct{}

func (NoopLogger) Debug(string, map[string]interface{}) {}
func (NoopLogger) Info(string, map[string]interface{})  {}
func (NoopLogger) Warn(string, map[string]interface{})  {}
func (NoopLogger) Error(string, map[string]interface{}) {}

// ManagerConfig controls manager-wide behavior.
type ManagerConfig struct {
	DefaultTimeout       time.Duration
	DefaultFailOpen      bool
	EventBuffer          int
	EventDispatchTimeout time.Duration
	Logger               Logger
	Registry             *Registry
}

// PluginStats is runtime health and performance information for a plugin.
type PluginStats struct {
	Name             string        `json:"name"`
	Enabled          bool          `json:"enabled"`
	Started          bool          `json:"started"`
	Priority         int           `json:"priority"`
	Health           PluginHealth  `json:"health"`
	ConsecutiveFails int           `json:"consecutive_fails"`
	CircuitOpen      bool          `json:"circuit_open"`
	CircuitOpenUntil time.Time     `json:"circuit_open_until,omitempty"`
	TotalCalls       uint64        `json:"total_calls"`
	TotalErrors      uint64        `json:"total_errors"`
	TotalPanics      uint64        `json:"total_panics"`
	TotalDuration    time.Duration `json:"total_duration"`
	LastError        string        `json:"last_error,omitempty"`
}

// ManagerStats represents aggregate plugin manager metrics.
type ManagerStats struct {
	PublishedEvents uint64        `json:"published_events"`
	DroppedEvents   uint64        `json:"dropped_events"`
	PluginCount     int           `json:"plugin_count"`
	Running         bool          `json:"running"`
	Plugins         []PluginStats `json:"plugins"`
}
