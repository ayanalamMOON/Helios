package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type testLifecyclePlugin struct {
	name       string
	startOrder *[]string
	stopOrder  *[]string
	mu         sync.Mutex
}

func (p *testLifecyclePlugin) Metadata() Metadata {
	return Metadata{Name: p.name, Version: "1.0.0"}
}

func (p *testLifecyclePlugin) Init(context.Context, Host, RuntimeConfig) error { return nil }

func (p *testLifecyclePlugin) Start(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	*p.startOrder = append(*p.startOrder, p.name)
	return nil
}

func (p *testLifecyclePlugin) Stop(context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	*p.stopOrder = append(*p.stopOrder, p.name)
	return nil
}

type failingCommandPlugin struct {
	name      string
	callCount int
	mu        sync.Mutex
}

func (p *failingCommandPlugin) Metadata() Metadata {
	return Metadata{Name: p.name, Version: "1.0.0"}
}

func (p *failingCommandPlugin) Init(context.Context, Host, RuntimeConfig) error { return nil }
func (p *failingCommandPlugin) Start(context.Context) error                     { return nil }
func (p *failingCommandPlugin) Stop(context.Context) error                      { return nil }

func (p *failingCommandPlugin) BeforeCommand(context.Context, *CommandContext) (*CommandContext, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.callCount++
	return nil, context.DeadlineExceeded
}

func (p *failingCommandPlugin) AfterCommand(context.Context, *CommandResult) error { return nil }

func (p *failingCommandPlugin) Calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callCount
}

type headerMiddlewarePlugin struct{}

func (p *headerMiddlewarePlugin) Metadata() Metadata {
	return Metadata{Name: "header_middleware", Version: "1.0.0"}
}

func (p *headerMiddlewarePlugin) Init(context.Context, Host, RuntimeConfig) error { return nil }
func (p *headerMiddlewarePlugin) Start(context.Context) error                     { return nil }
func (p *headerMiddlewarePlugin) Stop(context.Context) error                      { return nil }

func (p *headerMiddlewarePlugin) WrapHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Plugin", "enabled")
		next.ServeHTTP(w, r)
	})
}

type eventCollectorPlugin struct {
	events chan Event
}

func (p *eventCollectorPlugin) Metadata() Metadata {
	return Metadata{Name: "event_collector", Version: "1.0.0"}
}

type filteredEventCollectorPlugin struct {
	events   chan Event
	patterns []string
}

func (p *filteredEventCollectorPlugin) Metadata() Metadata {
	return Metadata{Name: "filtered_event_collector", Version: "1.0.0"}
}

func (p *filteredEventCollectorPlugin) Init(context.Context, Host, RuntimeConfig) error { return nil }
func (p *filteredEventCollectorPlugin) Start(context.Context) error                     { return nil }
func (p *filteredEventCollectorPlugin) Stop(context.Context) error                      { return nil }
func (p *filteredEventCollectorPlugin) EventTypes() []string                            { return p.patterns }
func (p *filteredEventCollectorPlugin) OnEvent(_ context.Context, event Event) error {
	select {
	case p.events <- event:
	default:
	}
	return nil
}

type reloadablePlugin struct {
	name    string
	reloads int
	cfg     RuntimeConfig
}

func (p *reloadablePlugin) Metadata() Metadata {
	return Metadata{Name: p.name, Version: "1.0.0"}
}

func (p *reloadablePlugin) Init(context.Context, Host, RuntimeConfig) error { return nil }
func (p *reloadablePlugin) Start(context.Context) error                     { return nil }
func (p *reloadablePlugin) Stop(context.Context) error                      { return nil }
func (p *reloadablePlugin) Reload(_ context.Context, cfg RuntimeConfig) error {
	p.reloads++
	p.cfg = cfg
	return nil
}
func (p *reloadablePlugin) Health(context.Context) (PluginHealth, error) {
	return PluginHealth{Status: HealthHealthy, Message: "ok", Timestamp: time.Now().UTC()}, nil
}

func (p *eventCollectorPlugin) Init(context.Context, Host, RuntimeConfig) error { return nil }
func (p *eventCollectorPlugin) Start(context.Context) error                     { return nil }
func (p *eventCollectorPlugin) Stop(context.Context) error                      { return nil }
func (p *eventCollectorPlugin) EventTypes() []string                            { return []string{"*"} }
func (p *eventCollectorPlugin) OnEvent(_ context.Context, event Event) error {
	select {
	case p.events <- event:
	default:
	}
	return nil
}

func TestManagerLifecyclePriorityOrder(t *testing.T) {
	startOrder := []string{}
	stopOrder := []string{}

	m := NewManager(ManagerConfig{})
	if err := m.Register(&testLifecyclePlugin{name: "low", startOrder: &startOrder, stopOrder: &stopOrder}, RuntimeConfig{Enabled: true, Priority: 10}); err != nil {
		t.Fatalf("register low plugin: %v", err)
	}
	if err := m.Register(&testLifecyclePlugin{name: "high", startOrder: &startOrder, stopOrder: &stopOrder}, RuntimeConfig{Enabled: true, Priority: 100}); err != nil {
		t.Fatalf("register high plugin: %v", err)
	}

	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	if err := m.Stop(context.Background()); err != nil {
		t.Fatalf("stop manager: %v", err)
	}

	if len(startOrder) != 2 || startOrder[0] != "high" || startOrder[1] != "low" {
		t.Fatalf("unexpected start order: %v", startOrder)
	}
	if len(stopOrder) != 2 || stopOrder[0] != "low" || stopOrder[1] != "high" {
		t.Fatalf("unexpected stop order: %v", stopOrder)
	}
}

func TestManagerFailOpenVsFailClosed(t *testing.T) {
	failing := &failingCommandPlugin{name: "failing"}

	failClosed := NewManager(ManagerConfig{DefaultFailOpen: false})
	if err := failClosed.Register(failing, RuntimeConfig{Enabled: true, FailOpen: false}); err != nil {
		t.Fatalf("register fail-closed plugin: %v", err)
	}
	if err := failClosed.Start(context.Background()); err != nil {
		t.Fatalf("start fail-closed manager: %v", err)
	}

	_, err := failClosed.ExecuteBeforeCommand(context.Background(), &CommandContext{Type: "SET", Key: "x"})
	if err == nil {
		t.Fatal("expected fail-closed manager to return an error")
	}
	_ = failClosed.Stop(context.Background())

	failingOpen := &failingCommandPlugin{name: "failing_open"}
	failOpen := NewManager(ManagerConfig{DefaultFailOpen: true})
	if err := failOpen.Register(failingOpen, RuntimeConfig{Enabled: true, FailOpen: true}); err != nil {
		t.Fatalf("register fail-open plugin: %v", err)
	}
	if err := failOpen.Start(context.Background()); err != nil {
		t.Fatalf("start fail-open manager: %v", err)
	}

	_, err = failOpen.ExecuteBeforeCommand(context.Background(), &CommandContext{Type: "SET", Key: "x"})
	if err != nil {
		t.Fatalf("expected fail-open manager not to block command, got: %v", err)
	}
	_ = failOpen.Stop(context.Background())
}

func TestManagerCircuitBreaker(t *testing.T) {
	failing := &failingCommandPlugin{name: "circuit"}

	m := NewManager(ManagerConfig{DefaultFailOpen: true})
	if err := m.Register(failing, RuntimeConfig{
		Enabled:     true,
		FailOpen:    true,
		MaxFailures: 2,
		Cooldown:    2 * time.Minute,
	}); err != nil {
		t.Fatalf("register plugin: %v", err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer m.Stop(context.Background())

	for i := 0; i < 3; i++ {
		_, err := m.ExecuteBeforeCommand(context.Background(), &CommandContext{Type: "SET", Key: "k"})
		if err != nil {
			t.Fatalf("expected fail-open behavior, got error: %v", err)
		}
	}

	calls := failing.Calls()
	if calls != 2 {
		t.Fatalf("expected plugin to be short-circuited after 2 failures, calls=%d", calls)
	}
}

func TestManagerHTTPMiddleware(t *testing.T) {
	m := NewManager(ManagerConfig{})
	if err := m.Register(&headerMiddlewarePlugin{}, RuntimeConfig{Enabled: true}); err != nil {
		t.Fatalf("register middleware plugin: %v", err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer m.Stop(context.Background())

	handler := m.WrapHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if got := rec.Header().Get("X-Plugin"); got != "enabled" {
		t.Fatalf("expected X-Plugin header to be set, got %q", got)
	}
}

func TestManagerSetEnabledAtRuntime(t *testing.T) {
	startOrder := []string{}
	stopOrder := []string{}

	toggle := &testLifecyclePlugin{name: "toggle", startOrder: &startOrder, stopOrder: &stopOrder}
	m := NewManager(ManagerConfig{})
	if err := m.Register(toggle, RuntimeConfig{Enabled: false, Timeout: 100 * time.Millisecond}); err != nil {
		t.Fatalf("register toggle plugin: %v", err)
	}

	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer m.Stop(context.Background())

	if len(startOrder) != 0 {
		t.Fatalf("expected disabled plugin not to start, startOrder=%v", startOrder)
	}

	if err := m.SetEnabled(context.Background(), "toggle", true); err != nil {
		t.Fatalf("enable plugin: %v", err)
	}
	if len(startOrder) != 1 || startOrder[0] != "toggle" {
		t.Fatalf("expected toggle plugin to start once, got %v", startOrder)
	}

	if err := m.SetEnabled(context.Background(), "toggle", false); err != nil {
		t.Fatalf("disable plugin: %v", err)
	}
	if len(stopOrder) != 1 || stopOrder[0] != "toggle" {
		t.Fatalf("expected toggle plugin to stop once, got %v", stopOrder)
	}
}

func TestManagerReloadRuntimeConfig(t *testing.T) {
	reloadable := &reloadablePlugin{name: "reloadable"}

	m := NewManager(ManagerConfig{})
	if err := m.Register(reloadable, RuntimeConfig{Enabled: true}); err != nil {
		t.Fatalf("register plugin: %v", err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer m.Stop(context.Background())

	err := m.Reload(context.Background(), "reloadable", RuntimeConfig{
		Enabled: true,
		Timeout: 200 * time.Millisecond,
		Settings: map[string]interface{}{
			"mode": "strict",
		},
	})
	if err != nil {
		t.Fatalf("reload plugin config: %v", err)
	}

	if reloadable.reloads != 1 {
		t.Fatalf("expected reload to be called once, got %d", reloadable.reloads)
	}

	cfg, ok := m.RuntimeConfig("reloadable")
	if !ok {
		t.Fatal("expected runtime config for reloadable plugin")
	}
	if cfg.Settings["mode"] != "strict" {
		t.Fatalf("expected updated runtime setting, got %#v", cfg.Settings)
	}
}

func TestManagerWildcardEventMatching(t *testing.T) {
	collector := &filteredEventCollectorPlugin{
		events:   make(chan Event, 10),
		patterns: []string{"queue.*"},
	}

	m := NewManager(ManagerConfig{})
	if err := m.Register(collector, RuntimeConfig{Enabled: true}); err != nil {
		t.Fatalf("register collector: %v", err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer m.Stop(context.Background())

	m.Publish(NewEvent("queue.job.enqueued", "test", nil))
	m.Publish(NewEvent("atlas.command.executed", "test", nil))

	time.Sleep(50 * time.Millisecond)

	if got := len(collector.events); got != 1 {
		t.Fatalf("expected exactly one matching event, got %d", got)
	}
	event := <-collector.events
	if !strings.HasPrefix(event.Type, "queue.") {
		t.Fatalf("expected queue event, got %s", event.Type)
	}
}

func TestManagerHealthCheck(t *testing.T) {
	healthy := &reloadablePlugin{name: "healthy-plugin"}

	m := NewManager(ManagerConfig{})
	if err := m.Register(healthy, RuntimeConfig{Enabled: true}); err != nil {
		t.Fatalf("register health plugin: %v", err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer m.Stop(context.Background())

	health := m.HealthCheck(context.Background())
	pluginHealth, ok := health["healthy-plugin"]
	if !ok {
		t.Fatalf("expected health entry for plugin")
	}
	if pluginHealth.Status != HealthHealthy {
		t.Fatalf("expected healthy status, got %s", pluginHealth.Status)
	}
}

func TestBuiltinTrafficGuardPlugin(t *testing.T) {
	m := NewManager(ManagerConfig{DefaultFailOpen: false})
	if err := m.Register(NewTrafficGuardPlugin(), RuntimeConfig{
		Enabled:  true,
		FailOpen: false,
		Settings: map[string]interface{}{
			"window":       "30s",
			"max_commands": 2,
			"scope":        "global",
			"applies_to":   []string{"SET"},
		},
	}); err != nil {
		t.Fatalf("register traffic_guard: %v", err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer m.Stop(context.Background())

	for i := 0; i < 2; i++ {
		_, err := m.ExecuteBeforeCommand(context.Background(), &CommandContext{Type: "SET", Key: "k"})
		if err != nil {
			t.Fatalf("expected command within limit to pass, iteration=%d err=%v", i, err)
		}
	}

	_, err := m.ExecuteBeforeCommand(context.Background(), &CommandContext{Type: "SET", Key: "k"})
	if err == nil {
		t.Fatal("expected traffic guard to block request after limit")
	}
	if !strings.Contains(err.Error(), "traffic_guard limit exceeded") {
		t.Fatalf("unexpected traffic guard error: %v", err)
	}
}

func TestManagerEventDispatch(t *testing.T) {
	collector := &eventCollectorPlugin{events: make(chan Event, 10)}

	m := NewManager(ManagerConfig{})
	if err := m.Register(collector, RuntimeConfig{Enabled: true}); err != nil {
		t.Fatalf("register event collector: %v", err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer m.Stop(context.Background())

	m.Publish(NewEvent("test.event", "unit-test", map[string]interface{}{"ok": true}))

	select {
	case event := <-collector.events:
		if event.Type != "test.event" {
			t.Fatalf("unexpected event type: %s", event.Type)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

func TestBuiltinAuditPlugin(t *testing.T) {
	audit := NewAuditPlugin()

	m := NewManager(ManagerConfig{})
	if err := m.Register(audit, RuntimeConfig{Enabled: true, Settings: map[string]interface{}{"include_values": true}}); err != nil {
		t.Fatalf("register audit plugin: %v", err)
	}
	if err := m.Start(context.Background()); err != nil {
		t.Fatalf("start manager: %v", err)
	}
	defer m.Stop(context.Background())

	req := &CommandContext{Type: "SET", Key: "audit:key", Value: []byte("hello")}
	_, err := m.ExecuteBeforeCommand(context.Background(), req)
	if err != nil {
		t.Fatalf("before command failed: %v", err)
	}
	m.ExecuteAfterCommand(context.Background(), &CommandResult{Request: req, OK: true, Duration: 12 * time.Millisecond})
	m.Publish(NewEvent("audit.event", "test", nil))

	deadline := time.Now().Add(1 * time.Second)
	for len(audit.Records()) < 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	records := audit.Records()
	if len(records) < 2 {
		t.Fatalf("expected at least 2 audit records, got %d", len(records))
	}
}

func TestLoadFromEnv(t *testing.T) {
	const envVar = "HELIOS_PLUGIN_TEST_LIST"
	t.Setenv(envVar, "audit,keyguard")

	m := NewManager(ManagerConfig{})
	loaded, err := LoadFromEnv(m, envVar, RuntimeConfig{Enabled: true})
	if err != nil {
		t.Fatalf("load from env failed: %v", err)
	}

	if len(loaded) != 2 {
		t.Fatalf("expected 2 loaded plugin names, got %d", len(loaded))
	}

	names := m.PluginNames()
	if len(names) != 2 {
		t.Fatalf("expected 2 registered plugins, got %d", len(names))
	}
}

func TestParsePluginList(t *testing.T) {
	got := ParsePluginList(" audit , keyguard, audit, ,HTTP_ACCESS ")
	if len(got) != 3 {
		t.Fatalf("expected 3 unique plugin names, got %v", got)
	}
	if got[0] != "audit" || got[1] != "keyguard" || got[2] != "http_access" {
		t.Fatalf("unexpected parse result: %v", got)
	}
}

func TestLoadFromEnvRequiresEnvName(t *testing.T) {
	m := NewManager(ManagerConfig{})
	_, err := LoadFromEnv(m, "", RuntimeConfig{Enabled: true})
	if err == nil {
		t.Fatal("expected error for empty env var name")
	}
}

func TestMain(m *testing.M) {
	// Ensure default registry builtins are initialized before tests inspect known factories.
	_ = KnownFactories()
	os.Exit(m.Run())
}
