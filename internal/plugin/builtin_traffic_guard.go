package plugin

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

type trafficWindow struct {
	windowStart time.Time
	lastSeen    time.Time
	count       int
}

// TrafficGuardPlugin enforces per-scope command throughput limits.
type TrafficGuardPlugin struct {
	mu sync.RWMutex

	host Host
	cfg  RuntimeConfig

	window       time.Duration
	maxCommands  int
	maxBuckets   int
	scope        string
	includeReads bool
	appliesTo    map[string]struct{}
	buckets      map[string]trafficWindow
}

// NewTrafficGuardPlugin creates a throughput guard plugin.
func NewTrafficGuardPlugin() *TrafficGuardPlugin {
	return &TrafficGuardPlugin{}
}

func (p *TrafficGuardPlugin) Metadata() Metadata {
	return Metadata{
		Name:        "traffic_guard",
		Version:     "1.0.0",
		Description: "Applies per-scope throughput limits to protect ATLAS from abusive traffic",
		Author:      "Helios",
		Tags:        []string{"protection", "throttling", "security"},
	}
}

func (p *TrafficGuardPlugin) Init(_ context.Context, host Host, cfg RuntimeConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.host = host
	p.cfg = cfg
	p.applyConfigLocked(cfg)
	if p.buckets == nil {
		p.buckets = make(map[string]trafficWindow)
	}
	return nil
}

func (p *TrafficGuardPlugin) applyConfigLocked(cfg RuntimeConfig) {
	p.window = settingDuration(cfg.Settings, "window", 1*time.Second)
	if p.window <= 0 {
		p.window = 1 * time.Second
	}

	p.maxCommands = settingInt(cfg.Settings, "max_commands", 100)
	if p.maxCommands <= 0 {
		p.maxCommands = 100
	}

	p.maxBuckets = settingInt(cfg.Settings, "max_buckets", 10000)
	if p.maxBuckets <= 0 {
		p.maxBuckets = 10000
	}

	p.scope = strings.ToLower(settingString(cfg.Settings, "scope", "session"))
	if p.scope == "" {
		p.scope = "session"
	}

	p.includeReads = settingBool(cfg.Settings, "include_reads", false)
	defaultCommands := []string{"SET", "DEL", "EXPIRE"}
	if p.includeReads {
		defaultCommands = append(defaultCommands, "GET", "TTL")
	}
	p.appliesTo = toUpperSet(settingStringSlice(cfg.Settings, "applies_to", defaultCommands))
	if len(p.appliesTo) == 0 {
		p.appliesTo = toUpperSet(defaultCommands)
	}

	if p.buckets == nil {
		p.buckets = make(map[string]trafficWindow)
	}
}

func (p *TrafficGuardPlugin) Start(_ context.Context) error {
	p.mu.RLock()
	maxCommands := p.maxCommands
	window := p.window
	scope := p.scope
	p.mu.RUnlock()

	if p.host != nil {
		p.host.Logger().Info("Traffic guard plugin started", map[string]interface{}{
			"max_commands": maxCommands,
			"window":       window.String(),
			"scope":        scope,
		})
	}
	return nil
}

func (p *TrafficGuardPlugin) Stop(_ context.Context) error {
	if p.host != nil {
		p.host.Logger().Info("Traffic guard plugin stopped", nil)
	}
	return nil
}

func (p *TrafficGuardPlugin) BeforeCommand(_ context.Context, req *CommandContext) (*CommandContext, error) {
	if req == nil {
		return nil, fmt.Errorf("nil command context")
	}

	commandType := strings.ToUpper(strings.TrimSpace(req.Type))
	if commandType == "" {
		return nil, fmt.Errorf("command type is required")
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if _, exists := p.appliesTo["*"]; !exists {
		if _, exists := p.appliesTo[commandType]; !exists {
			return req, nil
		}
	}

	now := time.Now().UTC()
	identity := p.scopeIdentity(commandType, req)
	p.pruneLocked(now)

	window := p.buckets[identity]
	if window.windowStart.IsZero() || now.Sub(window.windowStart) >= p.window {
		window.windowStart = now
		window.count = 0
	}

	window.lastSeen = now
	window.count++
	p.buckets[identity] = window

	if window.count > p.maxCommands {
		if p.host != nil {
			p.host.Logger().Warn("Traffic guard rate limit exceeded", map[string]interface{}{
				"scope":         p.scope,
				"identity":      identity,
				"command_type":  commandType,
				"window":        p.window.String(),
				"max_commands":  p.maxCommands,
				"current_count": window.count,
			})
		}
		return nil, fmt.Errorf("traffic_guard limit exceeded for %s (%d/%d in %s)", identity, window.count, p.maxCommands, p.window)
	}

	return req, nil
}

func (p *TrafficGuardPlugin) AfterCommand(_ context.Context, _ *CommandResult) error {
	return nil
}

func (p *TrafficGuardPlugin) scopeIdentity(commandType string, req *CommandContext) string {
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = "anonymous"
	}

	key := strings.TrimSpace(req.Key)
	if key == "" {
		key = "_"
	}

	switch p.scope {
	case "global":
		return "global"
	case "key":
		return "key:" + key
	case "session_key":
		return "session_key:" + sessionID + "|" + key
	case "command":
		return "command:" + commandType
	default:
		return "session:" + sessionID
	}
}

func (p *TrafficGuardPlugin) pruneLocked(now time.Time) {
	if len(p.buckets) == 0 {
		return
	}

	// Remove stale buckets first.
	maxAge := 2 * p.window
	for identity, bucket := range p.buckets {
		if now.Sub(bucket.lastSeen) > maxAge {
			delete(p.buckets, identity)
		}
	}

	if len(p.buckets) <= p.maxBuckets {
		return
	}

	type bucketEntry struct {
		identity string
		lastSeen time.Time
	}
	entries := make([]bucketEntry, 0, len(p.buckets))
	for identity, bucket := range p.buckets {
		entries = append(entries, bucketEntry{identity: identity, lastSeen: bucket.lastSeen})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].lastSeen.Before(entries[j].lastSeen)
	})

	removeCount := len(p.buckets) - p.maxBuckets
	for i := 0; i < removeCount && i < len(entries); i++ {
		delete(p.buckets, entries[i].identity)
	}
}

// Reload updates guard limits and scope at runtime.
func (p *TrafficGuardPlugin) Reload(_ context.Context, cfg RuntimeConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cfg = cfg
	p.applyConfigLocked(cfg)
	p.pruneLocked(time.Now().UTC())
	return nil
}

// Health reports plugin load/pressure indicators.
func (p *TrafficGuardPlugin) Health(_ context.Context) (PluginHealth, error) {
	p.mu.RLock()
	bucketCount := len(p.buckets)
	maxBuckets := p.maxBuckets
	window := p.window
	maxCommands := p.maxCommands
	scope := p.scope
	p.mu.RUnlock()

	status := HealthHealthy
	message := "traffic limits active"
	if maxBuckets > 0 {
		usage := float64(bucketCount) / float64(maxBuckets)
		if usage >= 0.9 {
			status = HealthDegraded
			message = "bucket table near capacity"
		}
	}

	return PluginHealth{
		Status:    status,
		Message:   message,
		Timestamp: time.Now().UTC(),
		Details: map[string]interface{}{
			"bucket_count": bucketCount,
			"max_buckets":  maxBuckets,
			"window":       window.String(),
			"max_commands": maxCommands,
			"scope":        scope,
		},
	}, nil
}

func init() {
	MustRegisterFactory("traffic_guard", func() Plugin { return NewTrafficGuardPlugin() })
}
