package plugin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// AuditRecord is an immutable record emitted by AuditPlugin.
type AuditRecord struct {
	Timestamp    time.Time              `json:"timestamp"`
	Kind         string                 `json:"kind"`
	Plugin       string                 `json:"plugin"`
	CommandType  string                 `json:"command_type,omitempty"`
	Key          string                 `json:"key,omitempty"`
	ValuePreview string                 `json:"value_preview,omitempty"`
	ReadOnly     bool                   `json:"read_only,omitempty"`
	Success      bool                   `json:"success,omitempty"`
	DurationMs   float64                `json:"duration_ms,omitempty"`
	Error        string                 `json:"error,omitempty"`
	EventType    string                 `json:"event_type,omitempty"`
	Source       string                 `json:"source,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// AuditPlugin captures command outcomes and runtime events.
type AuditPlugin struct {
	mu sync.RWMutex

	host Host
	cfg  RuntimeConfig

	maxEntries    int
	includeValues bool
	captureEvents bool
	eventTypes    []string
	commandTypes  map[string]struct{}
	previewBytes  int
	records       []AuditRecord
}

// NewAuditPlugin creates an audit plugin with default behavior.
func NewAuditPlugin() *AuditPlugin {
	return &AuditPlugin{}
}

func (p *AuditPlugin) Metadata() Metadata {
	return Metadata{
		Name:        "audit",
		Version:     "1.0.0",
		Description: "Captures command outcomes and plugin events for observability and forensics",
		Author:      "Helios",
		Tags:        []string{"security", "observability", "compliance"},
	}
}

func (p *AuditPlugin) Init(_ context.Context, host Host, cfg RuntimeConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.host = host
	p.cfg = cfg
	p.applyConfigLocked(cfg)
	return nil
}

func (p *AuditPlugin) applyConfigLocked(cfg RuntimeConfig) {
	p.maxEntries = settingInt(cfg.Settings, "max_entries", 500)
	if p.maxEntries <= 0 {
		p.maxEntries = 500
	}
	p.includeValues = settingBool(cfg.Settings, "include_values", false)
	p.captureEvents = settingBool(cfg.Settings, "capture_events", true)
	p.previewBytes = settingInt(cfg.Settings, "max_value_preview_bytes", 96)
	if p.previewBytes <= 0 {
		p.previewBytes = 96
	}
	p.eventTypes = settingStringSlice(cfg.Settings, "event_types", []string{"*"})
	if len(p.eventTypes) == 0 {
		p.eventTypes = []string{"*"}
	}
	p.commandTypes = toUpperSet(settingStringSlice(cfg.Settings, "command_types", []string{"*"}))
	if len(p.commandTypes) == 0 {
		p.commandTypes = map[string]struct{}{"*": {}}
	}

	if p.records == nil {
		p.records = make([]AuditRecord, 0, p.maxEntries)
		return
	}

	if len(p.records) > p.maxEntries {
		p.records = append([]AuditRecord(nil), p.records[len(p.records)-p.maxEntries:]...)
	}
}

func (p *AuditPlugin) Start(_ context.Context) error {
	p.mu.RLock()
	maxEntries := p.maxEntries
	includeValues := p.includeValues
	captureEvents := p.captureEvents
	p.mu.RUnlock()

	if p.host != nil {
		p.host.Logger().Info("Audit plugin started", map[string]interface{}{
			"max_entries":    maxEntries,
			"include_values": includeValues,
			"capture_events": captureEvents,
		})
	}
	return nil
}

func (p *AuditPlugin) Stop(_ context.Context) error {
	if p.host != nil {
		p.host.Logger().Info("Audit plugin stopped", nil)
	}
	return nil
}

func (p *AuditPlugin) BeforeCommand(_ context.Context, req *CommandContext) (*CommandContext, error) {
	if req == nil {
		return nil, fmt.Errorf("nil command context")
	}
	return req, nil
}

func (p *AuditPlugin) AfterCommand(_ context.Context, result *CommandResult) error {
	if result == nil || result.Request == nil {
		return nil
	}
	if !p.shouldCaptureCommand(result.Request.Type) {
		return nil
	}

	p.mu.RLock()
	includeValues := p.includeValues
	previewBytes := p.previewBytes
	p.mu.RUnlock()

	record := AuditRecord{
		Timestamp:   time.Now().UTC(),
		Kind:        "command",
		Plugin:      "audit",
		CommandType: result.Request.Type,
		Key:         result.Request.Key,
		ReadOnly:    result.Request.ReadOnly,
		Success:     result.OK,
		DurationMs:  float64(result.Duration.Microseconds()) / 1000.0,
	}

	if includeValues && len(result.Request.Value) > 0 {
		preview := result.Request.Value
		truncated := false
		if previewBytes > 0 && len(preview) > previewBytes {
			preview = preview[:previewBytes]
			truncated = true
		}
		record.ValuePreview = base64.StdEncoding.EncodeToString(preview)
		if truncated {
			if record.Metadata == nil {
				record.Metadata = make(map[string]interface{})
			}
			record.Metadata["value_truncated"] = true
			record.Metadata["original_value_bytes"] = len(result.Request.Value)
		}
	}

	if result.Error != nil {
		record.Error = result.Error.Error()
	}

	if len(result.Metadata) > 0 {
		record.Metadata = make(map[string]interface{}, len(result.Metadata))
		for k, v := range result.Metadata {
			record.Metadata[k] = v
		}
	}

	p.addRecord(record)
	return nil
}

func (p *AuditPlugin) EventTypes() []string {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if !p.captureEvents {
		return []string{}
	}
	out := make([]string, len(p.eventTypes))
	copy(out, p.eventTypes)
	return out
}

func (p *AuditPlugin) OnEvent(_ context.Context, event Event) error {
	p.mu.RLock()
	capture := p.captureEvents
	p.mu.RUnlock()
	if !capture {
		return nil
	}

	record := AuditRecord{
		Timestamp: event.Timestamp,
		Kind:      "event",
		Plugin:    "audit",
		EventType: event.Type,
		Source:    event.Source,
	}

	if len(event.Data) > 0 {
		record.Metadata = make(map[string]interface{}, len(event.Data))
		for k, v := range event.Data {
			record.Metadata[k] = v
		}
	}

	p.addRecord(record)
	return nil
}

func (p *AuditPlugin) shouldCaptureCommand(commandType string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()

	if len(p.commandTypes) == 0 {
		return true
	}
	if _, ok := p.commandTypes["*"]; ok {
		return true
	}
	_, ok := p.commandTypes[strings.ToUpper(strings.TrimSpace(commandType))]
	return ok
}

func (p *AuditPlugin) addRecord(record AuditRecord) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.records) >= p.maxEntries {
		copy(p.records, p.records[1:])
		p.records[len(p.records)-1] = record
		return
	}

	p.records = append(p.records, record)
}

// Records returns a copy of the in-memory audit ring buffer.
func (p *AuditPlugin) Records() []AuditRecord {
	p.mu.RLock()
	defer p.mu.RUnlock()

	out := make([]AuditRecord, len(p.records))
	copy(out, p.records)
	return out
}

// Reset clears in-memory audit records.
func (p *AuditPlugin) Reset() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.records = p.records[:0]
}

// ExportNDJSON serializes audit records as newline-delimited JSON.
func (p *AuditPlugin) ExportNDJSON() string {
	records := p.Records()
	if len(records) == 0 {
		return ""
	}

	var b strings.Builder
	for i := range records {
		payload, err := json.Marshal(records[i])
		if err != nil {
			continue
		}
		b.Write(payload)
		if i != len(records)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// Reload updates audit plugin settings at runtime.
func (p *AuditPlugin) Reload(_ context.Context, cfg RuntimeConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cfg = cfg
	p.applyConfigLocked(cfg)
	return nil
}

// Health reports audit buffer pressure and status.
func (p *AuditPlugin) Health(_ context.Context) (PluginHealth, error) {
	p.mu.RLock()
	recordCount := len(p.records)
	maxEntries := p.maxEntries
	p.mu.RUnlock()

	status := HealthHealthy
	message := "audit buffer healthy"
	if maxEntries > 0 {
		usage := float64(recordCount) / float64(maxEntries)
		switch {
		case usage >= 1:
			status = HealthDegraded
			message = "audit buffer at capacity (ring overwrite active)"
		case usage >= 0.9:
			status = HealthDegraded
			message = "audit buffer nearing capacity"
		}
	}

	return PluginHealth{
		Status:    status,
		Message:   message,
		Timestamp: time.Now().UTC(),
		Details: map[string]interface{}{
			"record_count": recordCount,
			"max_entries":  maxEntries,
		},
	}, nil
}

func toUpperSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		normalized := strings.ToUpper(strings.TrimSpace(value))
		if normalized == "" {
			continue
		}
		result[normalized] = struct{}{}
	}
	return result
}

func init() {
	MustRegisterFactory("audit", func() Plugin { return NewAuditPlugin() })
}
