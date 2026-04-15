package plugin

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"
)

// KeyGuardPlugin enforces key/value constraints before command execution.
type KeyGuardPlugin struct {
	mu sync.RWMutex

	host Host
	cfg  RuntimeConfig

	maxKeyLength       int
	maxValueBytes      int
	deniedPrefixes     []string
	deniedSuffixes     []string
	allowedPrefixes    []string
	allowedCommands    map[string]struct{}
	deniedCommands     map[string]struct{}
	allowEmptyValue    bool
	enforcePositiveTTL bool
	minTTL             int64
	maxTTL             int64
}

// NewKeyGuardPlugin creates a key validation/security plugin.
func NewKeyGuardPlugin() *KeyGuardPlugin {
	return &KeyGuardPlugin{}
}

func (p *KeyGuardPlugin) Metadata() Metadata {
	return Metadata{
		Name:        "keyguard",
		Version:     "1.0.0",
		Description: "Enforces key/value policy constraints before command execution",
		Author:      "Helios",
		Tags:        []string{"security", "validation", "policy"},
	}
}

func (p *KeyGuardPlugin) Init(_ context.Context, host Host, cfg RuntimeConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.host = host
	p.cfg = cfg
	p.applyConfigLocked(cfg)
	return nil
}

func (p *KeyGuardPlugin) applyConfigLocked(cfg RuntimeConfig) {
	p.maxKeyLength = settingInt(cfg.Settings, "max_key_length", 256)
	if p.maxKeyLength <= 0 {
		p.maxKeyLength = 256
	}

	p.maxValueBytes = settingInt(cfg.Settings, "max_value_bytes", 1024*1024)
	if p.maxValueBytes <= 0 {
		p.maxValueBytes = 1024 * 1024
	}

	p.deniedPrefixes = settingStringSlice(cfg.Settings, "denied_prefixes", nil)
	p.deniedSuffixes = settingStringSlice(cfg.Settings, "denied_suffixes", nil)
	p.allowedPrefixes = settingStringSlice(cfg.Settings, "allowed_prefixes", nil)
	p.allowedCommands = toUpperSet(settingStringSlice(cfg.Settings, "allowed_commands", nil))
	p.deniedCommands = toUpperSet(settingStringSlice(cfg.Settings, "denied_commands", nil))
	p.allowEmptyValue = settingBool(cfg.Settings, "allow_empty_value", true)
	p.enforcePositiveTTL = settingBool(cfg.Settings, "enforce_positive_ttl", true)

	p.minTTL = int64(settingInt(cfg.Settings, "min_ttl", 0))
	p.maxTTL = int64(settingInt(cfg.Settings, "max_ttl", 0))
}

func (p *KeyGuardPlugin) Start(_ context.Context) error {
	p.mu.RLock()
	maxKeyLength := p.maxKeyLength
	maxValueBytes := p.maxValueBytes
	deniedPrefixes := append([]string(nil), p.deniedPrefixes...)
	deniedSuffixes := append([]string(nil), p.deniedSuffixes...)
	allowEmptyValue := p.allowEmptyValue
	enforcePositiveTTL := p.enforcePositiveTTL
	minTTL := p.minTTL
	maxTTL := p.maxTTL
	p.mu.RUnlock()

	if p.host != nil {
		p.host.Logger().Info("KeyGuard plugin started", map[string]interface{}{
			"max_key_length":       maxKeyLength,
			"max_value_bytes":      maxValueBytes,
			"denied_prefixes":      deniedPrefixes,
			"denied_suffixes":      deniedSuffixes,
			"allow_empty_value":    allowEmptyValue,
			"enforce_positive_ttl": enforcePositiveTTL,
			"min_ttl":              minTTL,
			"max_ttl":              maxTTL,
		})
	}
	return nil
}

func (p *KeyGuardPlugin) Stop(_ context.Context) error {
	if p.host != nil {
		p.host.Logger().Info("KeyGuard plugin stopped", nil)
	}
	return nil
}

func (p *KeyGuardPlugin) BeforeCommand(_ context.Context, req *CommandContext) (*CommandContext, error) {
	if req == nil {
		return nil, fmt.Errorf("nil command context")
	}

	p.mu.RLock()
	maxKeyLength := p.maxKeyLength
	maxValueBytes := p.maxValueBytes
	deniedPrefixes := append([]string(nil), p.deniedPrefixes...)
	deniedSuffixes := append([]string(nil), p.deniedSuffixes...)
	allowedPrefixes := append([]string(nil), p.allowedPrefixes...)
	allowedCommands := cloneSet(p.allowedCommands)
	deniedCommands := cloneSet(p.deniedCommands)
	allowEmptyValue := p.allowEmptyValue
	enforcePositiveTTL := p.enforcePositiveTTL
	minTTL := p.minTTL
	maxTTL := p.maxTTL
	p.mu.RUnlock()

	commandType := strings.ToUpper(strings.TrimSpace(req.Type))
	if commandType == "" {
		return nil, fmt.Errorf("command type is required")
	}

	if commandDenied(deniedCommands, commandType) {
		return nil, fmt.Errorf("command %s is blocked by policy", commandType)
	}
	if !commandAllowed(allowedCommands, commandType) {
		return nil, fmt.Errorf("command %s is not allowed by policy", commandType)
	}

	if requiresKey(commandType) {
		if strings.TrimSpace(req.Key) == "" {
			return nil, fmt.Errorf("key is required for %s", commandType)
		}

		if len(req.Key) > maxKeyLength {
			return nil, fmt.Errorf("key length %d exceeds max_key_length %d", len(req.Key), maxKeyLength)
		}

		for _, denied := range deniedPrefixes {
			if denied != "" && strings.HasPrefix(req.Key, denied) {
				return nil, fmt.Errorf("key prefix %q is blocked by policy", denied)
			}
		}

		for _, denied := range deniedSuffixes {
			if denied != "" && strings.HasSuffix(req.Key, denied) {
				return nil, fmt.Errorf("key suffix %q is blocked by policy", denied)
			}
		}

		if len(allowedPrefixes) > 0 {
			allowed := false
			for _, prefix := range allowedPrefixes {
				if prefix != "" && strings.HasPrefix(req.Key, prefix) {
					allowed = true
					break
				}
			}
			if !allowed {
				return nil, fmt.Errorf("key %q does not match allowed prefixes", req.Key)
			}
		}
	}

	if commandType == "SET" {
		if !allowEmptyValue && len(req.Value) == 0 {
			return nil, fmt.Errorf("empty values are not allowed")
		}
		if len(req.Value) > maxValueBytes {
			return nil, fmt.Errorf("value size %d exceeds max_value_bytes %d", len(req.Value), maxValueBytes)
		}
	}

	if enforcePositiveTTL && (commandType == "SET" || commandType == "EXPIRE") {
		if req.TTL < 0 {
			return nil, fmt.Errorf("ttl cannot be negative")
		}
		if minTTL > 0 && req.TTL > 0 && req.TTL < minTTL {
			return nil, fmt.Errorf("ttl %d is below min_ttl %d", req.TTL, minTTL)
		}
		if maxTTL > 0 && req.TTL > maxTTL {
			return nil, fmt.Errorf("ttl %d exceeds max_ttl %d", req.TTL, maxTTL)
		}
	}

	return req, nil
}

func (p *KeyGuardPlugin) AfterCommand(_ context.Context, _ *CommandResult) error {
	return nil
}

// Reload updates keyguard policy settings at runtime.
func (p *KeyGuardPlugin) Reload(_ context.Context, cfg RuntimeConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cfg = cfg
	p.applyConfigLocked(cfg)
	return nil
}

// Health reports a healthy status for static policy enforcement.
func (p *KeyGuardPlugin) Health(_ context.Context) (PluginHealth, error) {
	p.mu.RLock()
	maxKeyLength := p.maxKeyLength
	maxValueBytes := p.maxValueBytes
	minTTL := p.minTTL
	maxTTL := p.maxTTL
	p.mu.RUnlock()

	return PluginHealth{
		Status:    HealthHealthy,
		Message:   "policy rules loaded",
		Timestamp: time.Now().UTC(),
		Details: map[string]interface{}{
			"max_key_length":  maxKeyLength,
			"max_value_bytes": maxValueBytes,
			"min_ttl":         minTTL,
			"max_ttl":         maxTTL,
		},
	}, nil
}

func cloneSet(in map[string]struct{}) map[string]struct{} {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(in))
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}

func commandDenied(set map[string]struct{}, commandType string) bool {
	if len(set) == 0 {
		return false
	}
	if _, ok := set["*"]; ok {
		return true
	}
	_, ok := set[commandType]
	return ok
}

func commandAllowed(set map[string]struct{}, commandType string) bool {
	if len(set) == 0 {
		return true
	}
	if _, ok := set["*"]; ok {
		return true
	}
	_, ok := set[commandType]
	return ok
}

func requiresKey(commandType string) bool {
	switch commandType {
	case "SET", "GET", "DEL", "EXPIRE", "TTL":
		return true
	default:
		return false
	}
}

func init() {
	MustRegisterFactory("keyguard", func() Plugin { return NewKeyGuardPlugin() })
}
