package plugin

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestBuiltinKeyGuardEnhancedPolicies(t *testing.T) {
	m := NewManager(ManagerConfig{DefaultFailOpen: false})
	if err := m.Register(NewKeyGuardPlugin(), RuntimeConfig{
		Enabled:  true,
		FailOpen: false,
		Settings: map[string]interface{}{
			"allowed_commands": []string{"SET", "GET"},
			"denied_suffixes":  []string{":tmp"},
			"max_ttl":          60,
		},
	}); err != nil {
		t.Fatalf("register keyguard: %v", err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer m.Stop(context.Background())

	if _, err := m.ExecuteBeforeCommand(context.Background(), &CommandContext{Type: "DEL", Key: "x"}); err == nil {
		t.Fatal("expected DEL to be blocked by allowed_commands policy")
	}

	if _, err := m.ExecuteBeforeCommand(context.Background(), &CommandContext{Type: "SET", Key: "session:tmp", Value: []byte("ok")}); err == nil {
		t.Fatal("expected denied suffix policy to reject key")
	}

	if _, err := m.ExecuteBeforeCommand(context.Background(), &CommandContext{Type: "SET", Key: "session:good", Value: []byte("ok"), TTL: 120}); err == nil {
		t.Fatal("expected max_ttl policy to reject ttl")
	}

	if _, err := m.ExecuteBeforeCommand(context.Background(), &CommandContext{Type: "SET", Key: "session:good", Value: []byte("ok"), TTL: 10}); err != nil {
		t.Fatalf("expected valid command to pass, got %v", err)
	}
}

func TestBuiltinHTTPAccessRequestIDAndCustomEvent(t *testing.T) {
	collector := &eventCollectorPlugin{events: make(chan Event, 10)}

	m := NewManager(ManagerConfig{})
	if err := m.Register(collector, RuntimeConfig{Enabled: true}); err != nil {
		t.Fatalf("register event collector: %v", err)
	}
	if err := m.Register(NewHTTPAccessPlugin(), RuntimeConfig{
		Enabled: true,
		Settings: map[string]interface{}{
			"emit_request_id":   true,
			"request_id_header": "X-Request-ID",
			"event_name":        "custom.http.completed",
			"methods":           []string{"GET"},
			"publish_events":    true,
		},
	}); err != nil {
		t.Fatalf("register http_access plugin: %v", err)
	}

	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer m.Stop(context.Background())

	handler := m.WrapHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health?raw=true", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := strings.TrimSpace(rec.Header().Get("X-Request-ID")); got == "" {
		t.Fatal("expected X-Request-ID to be emitted")
	}

	select {
	case event := <-collector.events:
		if event.Type != "custom.http.completed" {
			t.Fatalf("expected custom event type, got %s", event.Type)
		}
		if _, ok := event.Data["request_id"]; !ok {
			t.Fatalf("expected request_id in event payload, got %#v", event.Data)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for HTTP access event")
	}
}

func TestBuiltinAuditCommandFilterAndExport(t *testing.T) {
	audit := NewAuditPlugin()
	m := NewManager(ManagerConfig{})
	if err := m.Register(audit, RuntimeConfig{
		Enabled: true,
		Settings: map[string]interface{}{
			"command_types":           []string{"SET"},
			"include_values":          true,
			"max_value_preview_bytes": 4,
			"capture_events":          true,
		},
	}); err != nil {
		t.Fatalf("register audit plugin: %v", err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer m.Stop(context.Background())

	setReq := &CommandContext{Type: "SET", Key: "audit:k", Value: []byte("abcdefgh")}
	if _, err := m.ExecuteBeforeCommand(context.Background(), setReq); err != nil {
		t.Fatalf("set before command failed: %v", err)
	}
	m.ExecuteAfterCommand(context.Background(), &CommandResult{Request: setReq, OK: true, Duration: 8 * time.Millisecond})

	// GET should be ignored by command_types filter.
	getReq := &CommandContext{Type: "GET", Key: "audit:k"}
	if _, err := m.ExecuteBeforeCommand(context.Background(), getReq); err != nil {
		t.Fatalf("get before command failed: %v", err)
	}
	m.ExecuteAfterCommand(context.Background(), &CommandResult{Request: getReq, OK: true, Duration: 2 * time.Millisecond})

	m.Publish(NewEvent("audit.event", "test", map[string]interface{}{"ok": true}))

	deadline := time.Now().Add(1 * time.Second)
	for time.Now().Before(deadline) {
		if len(audit.Records()) >= 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	records := audit.Records()
	if len(records) < 2 {
		t.Fatalf("expected at least 2 records (SET + event), got %d", len(records))
	}

	var commandRecord *AuditRecord
	for i := range records {
		if records[i].Kind == "command" {
			commandRecord = &records[i]
			break
		}
	}
	if commandRecord == nil {
		t.Fatalf("expected command record in %#v", records)
	}
	if commandRecord.CommandType != "SET" {
		t.Fatalf("expected command type SET, got %s", commandRecord.CommandType)
	}

	decoded, err := base64.StdEncoding.DecodeString(commandRecord.ValuePreview)
	if err != nil {
		t.Fatalf("failed to decode value preview: %v", err)
	}
	if string(decoded) != "abcd" {
		t.Fatalf("expected preview to be truncated to 'abcd', got %q", string(decoded))
	}

	ndjson := audit.ExportNDJSON()
	if strings.TrimSpace(ndjson) == "" {
		t.Fatal("expected non-empty NDJSON export")
	}

	audit.Reset()
	if len(audit.Records()) != 0 {
		t.Fatal("expected records to be empty after reset")
	}
}
