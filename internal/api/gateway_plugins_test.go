package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/helios/helios/internal/plugin"
)

type pluginAdminLifecyclePlugin struct {
	name       string
	startCalls int32
	stopCalls  int32
}

func (p *pluginAdminLifecyclePlugin) Metadata() plugin.Metadata {
	return plugin.Metadata{Name: p.name, Version: "1.0.0"}
}

func (p *pluginAdminLifecyclePlugin) Init(context.Context, plugin.Host, plugin.RuntimeConfig) error {
	return nil
}

func (p *pluginAdminLifecyclePlugin) Start(context.Context) error {
	atomic.AddInt32(&p.startCalls, 1)
	return nil
}

func (p *pluginAdminLifecyclePlugin) Stop(context.Context) error {
	atomic.AddInt32(&p.stopCalls, 1)
	return nil
}

type pluginAdminReloadablePlugin struct {
	name        string
	startCalls  int32
	stopCalls   int32
	reloadCalls int32

	mu      sync.RWMutex
	lastCfg plugin.RuntimeConfig
}

func (p *pluginAdminReloadablePlugin) Metadata() plugin.Metadata {
	return plugin.Metadata{Name: p.name, Version: "1.0.0"}
}

func (p *pluginAdminReloadablePlugin) Init(_ context.Context, _ plugin.Host, cfg plugin.RuntimeConfig) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastCfg = cfg
	return nil
}

func (p *pluginAdminReloadablePlugin) Start(context.Context) error {
	atomic.AddInt32(&p.startCalls, 1)
	return nil
}

func (p *pluginAdminReloadablePlugin) Stop(context.Context) error {
	atomic.AddInt32(&p.stopCalls, 1)
	return nil
}

func (p *pluginAdminReloadablePlugin) Reload(_ context.Context, cfg plugin.RuntimeConfig) error {
	atomic.AddInt32(&p.reloadCalls, 1)

	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastCfg = cfg
	return nil
}

func (p *pluginAdminReloadablePlugin) Health(context.Context) (plugin.PluginHealth, error) {
	return plugin.PluginHealth{
		Status:    plugin.HealthHealthy,
		Message:   "ready",
		Timestamp: time.Now().UTC(),
		Details: map[string]interface{}{
			"component": p.name,
		},
	}, nil
}

func makeGatewayRequest(t *testing.T, gateway *Gateway, method, path, token string, body []byte) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	rec := httptest.NewRecorder()
	mux := http.NewServeMux()
	gateway.RegisterRoutes(mux)
	mux.ServeHTTP(rec, req)

	return rec
}

func decodeResponseMap(t *testing.T, rec *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()

	var out map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&out); err != nil {
		t.Fatalf("failed to decode response JSON: %v body=%s", err, rec.Body.String())
	}
	return out
}

func createTokenForRole(t *testing.T, gateway *Gateway, role string) string {
	t.Helper()

	username := fmt.Sprintf("%s-%d", role, time.Now().UnixNano())
	user, err := gateway.authService.CreateUser(username, "password123")
	if err != nil {
		t.Fatalf("failed to create user: %v", err)
	}

	if err := gateway.rbacService.AssignRole(user.ID, role); err != nil {
		t.Fatalf("failed to assign role %s: %v", role, err)
	}

	token, err := gateway.authService.CreateToken(user.ID, 3600*1000000000)
	if err != nil {
		t.Fatalf("failed to create token: %v", err)
	}

	return token.TokenHash
}

func findPluginEntryByName(entries []interface{}, name string) (map[string]interface{}, bool) {
	for _, entry := range entries {
		pluginMap, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		if pluginMap["name"] == name {
			return pluginMap, true
		}
	}
	return nil, false
}

func findBulkResultByName(entries []interface{}, name string) (map[string]interface{}, bool) {
	for _, entry := range entries {
		resultMap, ok := entry.(map[string]interface{})
		if !ok {
			continue
		}
		if resultMap["name"] == name {
			return resultMap, true
		}
	}
	return nil, false
}

func TestPluginAdminEndpoints_PluginManagerUnavailable(t *testing.T) {
	gateway, _ := setupTestGateway(t)
	adminToken := createTokenForRole(t, gateway, "admin")

	rec := makeGatewayRequest(t, gateway, http.MethodGet, "/admin/plugins", adminToken, nil)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when plugin manager is missing, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResponseMap(t, rec)
	if resp["success"] != false {
		t.Fatalf("expected success=false, got %#v", resp)
	}
	if !strings.Contains(fmt.Sprintf("%v", resp["error"]), "Plugin manager not initialized") {
		t.Fatalf("unexpected error response: %#v", resp)
	}
}

func TestPluginAdminEndpoints_ListAndDetail(t *testing.T) {
	gateway, _ := setupTestGateway(t)

	pm := plugin.NewManager(plugin.ManagerConfig{})
	reloadable := &pluginAdminReloadablePlugin{name: "reloadable_admin"}
	if err := pm.Register(reloadable, plugin.RuntimeConfig{
		Enabled: true,
		Settings: map[string]interface{}{
			"mode":   "safe",
			"region": "eu",
		},
	}); err != nil {
		t.Fatalf("failed to register plugin: %v", err)
	}
	if err := pm.Start(context.Background()); err != nil {
		t.Fatalf("failed to start plugin manager: %v", err)
	}
	defer pm.Stop(context.Background())

	gateway.SetPluginManager(pm)
	adminToken := createTokenForRole(t, gateway, "admin")

	rec := makeGatewayRequest(t, gateway, http.MethodGet, "/admin/plugins?refresh_health=true&include_runtime=true&include_stats=true", adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from list endpoint, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResponseMap(t, rec)
	if resp["success"] != true {
		t.Fatalf("expected success=true for list endpoint, got %#v", resp)
	}

	entries, ok := resp["plugins"].([]interface{})
	if !ok {
		t.Fatalf("expected plugins array, got %#v", resp["plugins"])
	}

	entry, found := findPluginEntryByName(entries, "reloadable_admin")
	if !found {
		t.Fatalf("expected reloadable_admin in plugin list, got %#v", entries)
	}
	if _, ok := entry["runtime"]; !ok {
		t.Fatalf("expected runtime details for plugin, got %#v", entry)
	}
	if _, ok := entry["state"]; !ok {
		t.Fatalf("expected state details for plugin, got %#v", entry)
	}
	if _, ok := entry["health"]; !ok {
		t.Fatalf("expected health details for plugin, got %#v", entry)
	}

	detailRec := makeGatewayRequest(t, gateway, http.MethodGet, "/admin/plugins/reloadable_admin?refresh_health=true", adminToken, nil)
	if detailRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from detail endpoint, got %d: %s", detailRec.Code, detailRec.Body.String())
	}

	detailResp := decodeResponseMap(t, detailRec)
	pluginObj, ok := detailResp["plugin"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected plugin object in detail response, got %#v", detailResp)
	}
	if pluginObj["name"] != "reloadable_admin" {
		t.Fatalf("expected plugin name reloadable_admin, got %#v", pluginObj["name"])
	}
}

func TestPluginAdminEndpoints_EnableDisable(t *testing.T) {
	gateway, _ := setupTestGateway(t)

	pm := plugin.NewManager(plugin.ManagerConfig{})
	toggle := &pluginAdminLifecyclePlugin{name: "toggle_admin"}
	if err := pm.Register(toggle, plugin.RuntimeConfig{Enabled: false}); err != nil {
		t.Fatalf("failed to register plugin: %v", err)
	}
	if err := pm.Start(context.Background()); err != nil {
		t.Fatalf("failed to start plugin manager: %v", err)
	}
	defer pm.Stop(context.Background())

	gateway.SetPluginManager(pm)
	adminToken := createTokenForRole(t, gateway, "admin")

	enableRec := makeGatewayRequest(t, gateway, http.MethodPost, "/admin/plugins/toggle_admin/enable", adminToken, []byte("{}"))
	if enableRec.Code != http.StatusOK {
		t.Fatalf("expected 200 when enabling plugin, got %d: %s", enableRec.Code, enableRec.Body.String())
	}
	if got := atomic.LoadInt32(&toggle.startCalls); got != 1 {
		t.Fatalf("expected startCalls=1 after enable, got %d", got)
	}

	disablePayload, _ := json.Marshal(map[string]interface{}{"enabled": false})
	disableRec := makeGatewayRequest(t, gateway, http.MethodPut, "/admin/plugins/toggle_admin/enabled", adminToken, disablePayload)
	if disableRec.Code != http.StatusOK {
		t.Fatalf("expected 200 when disabling plugin, got %d: %s", disableRec.Code, disableRec.Body.String())
	}
	if got := atomic.LoadInt32(&toggle.stopCalls); got != 1 {
		t.Fatalf("expected stopCalls=1 after disable, got %d", got)
	}

	missingFieldRec := makeGatewayRequest(t, gateway, http.MethodPut, "/admin/plugins/toggle_admin/enabled", adminToken, []byte("{}"))
	if missingFieldRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing enabled field, got %d: %s", missingFieldRec.Code, missingFieldRec.Body.String())
	}
}

func TestPluginAdminEndpoints_ReloadAndValidation(t *testing.T) {
	gateway, _ := setupTestGateway(t)

	pm := plugin.NewManager(plugin.ManagerConfig{})
	reloadable := &pluginAdminReloadablePlugin{name: "reloadable_admin"}
	nonReloadable := &pluginAdminLifecyclePlugin{name: "simple_admin"}

	if err := pm.Register(reloadable, plugin.RuntimeConfig{
		Enabled:  true,
		Timeout:  100 * time.Millisecond,
		Cooldown: 3 * time.Second,
		Settings: map[string]interface{}{
			"mode":   "safe",
			"region": "eu",
		},
	}); err != nil {
		t.Fatalf("failed to register reloadable plugin: %v", err)
	}
	if err := pm.Register(nonReloadable, plugin.RuntimeConfig{Enabled: true}); err != nil {
		t.Fatalf("failed to register non-reloadable plugin: %v", err)
	}
	if err := pm.Start(context.Background()); err != nil {
		t.Fatalf("failed to start plugin manager: %v", err)
	}
	defer pm.Stop(context.Background())

	gateway.SetPluginManager(pm)
	adminToken := createTokenForRole(t, gateway, "admin")

	reloadPayload, _ := json.Marshal(map[string]interface{}{
		"timeout":  "250ms",
		"cooldown": "5s",
		"settings": map[string]interface{}{
			"mode":  "strict",
			"burst": 200,
		},
	})
	reloadRec := makeGatewayRequest(t, gateway, http.MethodPost, "/admin/plugins/reloadable_admin/reload", adminToken, reloadPayload)
	if reloadRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for reload endpoint, got %d: %s", reloadRec.Code, reloadRec.Body.String())
	}

	if got := atomic.LoadInt32(&reloadable.reloadCalls); got != 1 {
		t.Fatalf("expected reloadCalls=1, got %d", got)
	}

	cfg, ok := pm.RuntimeConfig("reloadable_admin")
	if !ok {
		t.Fatal("expected runtime config for reloadable_admin")
	}
	if cfg.Timeout != 250*time.Millisecond {
		t.Fatalf("expected timeout=250ms, got %s", cfg.Timeout)
	}
	if cfg.Cooldown != 5*time.Second {
		t.Fatalf("expected cooldown=5s, got %s", cfg.Cooldown)
	}
	if cfg.Settings["mode"] != "strict" {
		t.Fatalf("expected mode=strict after reload, got %#v", cfg.Settings["mode"])
	}
	if cfg.Settings["region"] != "eu" {
		t.Fatalf("expected existing setting region=eu to be preserved, got %#v", cfg.Settings["region"])
	}
	if cfg.Settings["burst"] == nil {
		t.Fatalf("expected burst setting to be added, got %#v", cfg.Settings)
	}

	invalidDurationPayload, _ := json.Marshal(map[string]interface{}{"timeout": "not-a-duration"})
	invalidDurationRec := makeGatewayRequest(t, gateway, http.MethodPost, "/admin/plugins/reloadable_admin/reload", adminToken, invalidDurationPayload)
	if invalidDurationRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid duration, got %d: %s", invalidDurationRec.Code, invalidDurationRec.Body.String())
	}

	nonReloadableRec := makeGatewayRequest(t, gateway, http.MethodPost, "/admin/plugins/simple_admin/reload", adminToken, []byte("{}"))
	if nonReloadableRec.Code != http.StatusConflict {
		t.Fatalf("expected 409 for non-reloadable plugin, got %d: %s", nonReloadableRec.Code, nonReloadableRec.Body.String())
	}
}

func TestPluginAdminEndpoints_AuthAndInputValidation(t *testing.T) {
	gateway, _ := setupTestGateway(t)

	pm := plugin.NewManager(plugin.ManagerConfig{})
	if err := pm.Register(&pluginAdminLifecyclePlugin{name: "auth_admin_plugin"}, plugin.RuntimeConfig{Enabled: true}); err != nil {
		t.Fatalf("failed to register plugin: %v", err)
	}
	if err := pm.Start(context.Background()); err != nil {
		t.Fatalf("failed to start plugin manager: %v", err)
	}
	defer pm.Stop(context.Background())

	gateway.SetPluginManager(pm)

	noAuthRec := makeGatewayRequest(t, gateway, http.MethodGet, "/admin/plugins", "", nil)
	if noAuthRec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without token, got %d: %s", noAuthRec.Code, noAuthRec.Body.String())
	}

	userToken := createTokenForRole(t, gateway, "user")
	forbiddenRec := makeGatewayRequest(t, gateway, http.MethodGet, "/admin/plugins", userToken, nil)
	if forbiddenRec.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for non-admin user, got %d: %s", forbiddenRec.Code, forbiddenRec.Body.String())
	}

	adminToken := createTokenForRole(t, gateway, "admin")
	invalidQueryRec := makeGatewayRequest(t, gateway, http.MethodGet, "/admin/plugins?refresh_health=maybe", adminToken, nil)
	if invalidQueryRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid refresh_health query value, got %d: %s", invalidQueryRec.Code, invalidQueryRec.Body.String())
	}

	missingPluginRec := makeGatewayRequest(t, gateway, http.MethodPost, "/admin/plugins/does_not_exist/enable", adminToken, []byte("{}"))
	if missingPluginRec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing plugin, got %d: %s", missingPluginRec.Code, missingPluginRec.Body.String())
	}
}

type pluginAdminAuditCollector struct {
	events chan plugin.Event
}

func (p *pluginAdminAuditCollector) Metadata() plugin.Metadata {
	return plugin.Metadata{Name: "plugin_admin_audit_collector", Version: "1.0.0"}
}

func (p *pluginAdminAuditCollector) Init(context.Context, plugin.Host, plugin.RuntimeConfig) error {
	return nil
}

func (p *pluginAdminAuditCollector) Start(context.Context) error { return nil }
func (p *pluginAdminAuditCollector) Stop(context.Context) error  { return nil }
func (p *pluginAdminAuditCollector) EventTypes() []string        { return []string{"admin.plugin.*"} }

func (p *pluginAdminAuditCollector) OnEvent(_ context.Context, event plugin.Event) error {
	select {
	case p.events <- event:
	default:
	}
	return nil
}

func TestPluginAdminBulkEndpoints_EnableDisableAndDryRun(t *testing.T) {
	gateway, _ := setupTestGateway(t)

	pm := plugin.NewManager(plugin.ManagerConfig{})
	alpha := &pluginAdminLifecyclePlugin{name: "bulk_alpha"}
	beta := &pluginAdminLifecyclePlugin{name: "bulk_beta"}

	if err := pm.Register(alpha, plugin.RuntimeConfig{Enabled: false}); err != nil {
		t.Fatalf("failed to register alpha plugin: %v", err)
	}
	if err := pm.Register(beta, plugin.RuntimeConfig{Enabled: false}); err != nil {
		t.Fatalf("failed to register beta plugin: %v", err)
	}
	if err := pm.Start(context.Background()); err != nil {
		t.Fatalf("failed to start plugin manager: %v", err)
	}
	defer pm.Stop(context.Background())

	gateway.SetPluginManager(pm)
	adminToken := createTokenForRole(t, gateway, "admin")

	enablePayload, _ := json.Marshal(map[string]interface{}{
		"action": "enable",
		"items": []map[string]interface{}{
			{"name": "bulk_alpha"},
			{"name": "bulk_beta"},
		},
		"continue_on_error": true,
		"refresh_health":    false,
		"include_runtime":   false,
		"include_stats":     false,
	})
	enableRec := makeGatewayRequest(t, gateway, http.MethodPost, "/admin/plugins/bulk", adminToken, enablePayload)
	if enableRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from bulk enable, got %d: %s", enableRec.Code, enableRec.Body.String())
	}

	enableResp := decodeResponseMap(t, enableRec)
	if enableResp["success"] != true {
		t.Fatalf("expected bulk enable success=true, got %#v", enableResp)
	}

	if got := atomic.LoadInt32(&alpha.startCalls); got != 1 {
		t.Fatalf("expected alpha startCalls=1, got %d", got)
	}
	if got := atomic.LoadInt32(&beta.startCalls); got != 1 {
		t.Fatalf("expected beta startCalls=1, got %d", got)
	}

	disableDryRunPayload, _ := json.Marshal(map[string]interface{}{
		"action": "disable",
		"items": []map[string]interface{}{
			{"name": "bulk_alpha"},
			{"name": "bulk_beta"},
		},
		"dry_run": true,
	})
	disableDryRunRec := makeGatewayRequest(t, gateway, http.MethodPost, "/admin/plugins/bulk", adminToken, disableDryRunPayload)
	if disableDryRunRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from bulk dry-run disable, got %d: %s", disableDryRunRec.Code, disableDryRunRec.Body.String())
	}

	if got := atomic.LoadInt32(&alpha.stopCalls); got != 0 {
		t.Fatalf("expected alpha stopCalls=0 during dry-run, got %d", got)
	}
	if got := atomic.LoadInt32(&beta.stopCalls); got != 0 {
		t.Fatalf("expected beta stopCalls=0 during dry-run, got %d", got)
	}
}

func TestPluginAdminBulkEndpoints_ReloadPartialFailuresAndAuditEvents(t *testing.T) {
	gateway, _ := setupTestGateway(t)

	pm := plugin.NewManager(plugin.ManagerConfig{})
	auditCollector := &pluginAdminAuditCollector{events: make(chan plugin.Event, 256)}
	reloadable := &pluginAdminReloadablePlugin{name: "bulk_reloadable"}
	simple := &pluginAdminLifecyclePlugin{name: "bulk_nonreloadable"}

	if err := pm.Register(auditCollector, plugin.RuntimeConfig{Enabled: true}); err != nil {
		t.Fatalf("failed to register audit collector plugin: %v", err)
	}
	if err := pm.Register(reloadable, plugin.RuntimeConfig{
		Enabled:  true,
		Timeout:  100 * time.Millisecond,
		Cooldown: 3 * time.Second,
		Settings: map[string]interface{}{"mode": "safe"},
	}); err != nil {
		t.Fatalf("failed to register reloadable plugin: %v", err)
	}
	if err := pm.Register(simple, plugin.RuntimeConfig{Enabled: true}); err != nil {
		t.Fatalf("failed to register non-reloadable plugin: %v", err)
	}
	if err := pm.Start(context.Background()); err != nil {
		t.Fatalf("failed to start plugin manager: %v", err)
	}
	defer pm.Stop(context.Background())

	gateway.SetPluginManager(pm)
	adminToken := createTokenForRole(t, gateway, "admin")

	reloadBulkPayload, _ := json.Marshal(map[string]interface{}{
		"action": "reload",
		"default_reload": map[string]interface{}{
			"timeout": "250ms",
			"settings": map[string]interface{}{
				"mode": "strict",
			},
		},
		"continue_on_error": true,
		"items": []map[string]interface{}{
			{
				"name": "bulk_reloadable",
				"reload": map[string]interface{}{
					"settings": map[string]interface{}{
						"burst": 200,
					},
				},
			},
			{"name": "bulk_nonreloadable"},
			{"name": "bulk_missing_plugin"},
		},
	})

	reloadBulkRec := makeGatewayRequest(t, gateway, http.MethodPost, "/admin/plugins/bulk", adminToken, reloadBulkPayload)
	if reloadBulkRec.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207 for partial bulk reload failures, got %d: %s", reloadBulkRec.Code, reloadBulkRec.Body.String())
	}

	reloadBulkResp := decodeResponseMap(t, reloadBulkRec)
	if reloadBulkResp["success"] != false {
		t.Fatalf("expected bulk reload success=false with partial failures, got %#v", reloadBulkResp)
	}

	summary, ok := reloadBulkResp["summary"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected summary object in bulk response, got %#v", reloadBulkResp)
	}
	if summary["succeeded"].(float64) != 1 {
		t.Fatalf("expected exactly 1 success in bulk reload, got %#v", summary)
	}
	if summary["failed"].(float64) != 2 {
		t.Fatalf("expected exactly 2 failures in bulk reload, got %#v", summary)
	}

	cfg, ok := pm.RuntimeConfig("bulk_reloadable")
	if !ok {
		t.Fatal("expected runtime config for bulk_reloadable")
	}
	if cfg.Timeout != 250*time.Millisecond {
		t.Fatalf("expected timeout=250ms after bulk reload, got %s", cfg.Timeout)
	}
	if cfg.Settings["mode"] != "strict" {
		t.Fatalf("expected mode=strict after bulk reload, got %#v", cfg.Settings["mode"])
	}
	if cfg.Settings["burst"] == nil {
		t.Fatalf("expected burst setting merged into bulk_reloadable config, got %#v", cfg.Settings)
	}

	deadline := time.Now().Add(2 * time.Second)
	foundReloadEvent := false
	foundBulkSummaryEvent := false
	for time.Now().Before(deadline) && !(foundReloadEvent && foundBulkSummaryEvent) {
		select {
		case event := <-auditCollector.events:
			if event.Type == "admin.plugin.reload" {
				foundReloadEvent = true
			}
			if event.Type == "admin.plugin.bulk_reload" {
				foundBulkSummaryEvent = true
			}
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	if !foundReloadEvent {
		t.Fatal("expected admin.plugin.reload audit event for per-item bulk action")
	}
	if !foundBulkSummaryEvent {
		t.Fatal("expected admin.plugin.bulk_reload audit event for bulk summary")
	}
}

func TestPluginAdminBulkEndpoints_ContinueOnErrorFalseStopsProcessing(t *testing.T) {
	gateway, _ := setupTestGateway(t)

	pm := plugin.NewManager(plugin.ManagerConfig{})
	nonReloadable := &pluginAdminLifecyclePlugin{name: "cof_nonreloadable"}
	reloadable := &pluginAdminReloadablePlugin{name: "cof_reloadable"}

	if err := pm.Register(nonReloadable, plugin.RuntimeConfig{Enabled: true}); err != nil {
		t.Fatalf("failed to register non-reloadable plugin: %v", err)
	}
	if err := pm.Register(reloadable, plugin.RuntimeConfig{
		Enabled: true,
		Settings: map[string]interface{}{
			"mode": "safe",
		},
	}); err != nil {
		t.Fatalf("failed to register reloadable plugin: %v", err)
	}
	if err := pm.Start(context.Background()); err != nil {
		t.Fatalf("failed to start plugin manager: %v", err)
	}
	defer pm.Stop(context.Background())

	gateway.SetPluginManager(pm)
	adminToken := createTokenForRole(t, gateway, "admin")

	payload, _ := json.Marshal(map[string]interface{}{
		"action":            "reload",
		"continue_on_error": false,
		"default_reload": map[string]interface{}{
			"settings": map[string]interface{}{
				"mode": "strict",
			},
		},
		"items": []map[string]interface{}{
			{"name": "cof_nonreloadable"},
			{"name": "cof_reloadable"},
		},
	})

	rec := makeGatewayRequest(t, gateway, http.MethodPost, "/admin/plugins/bulk", adminToken, payload)
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207 when first item fails and continue_on_error=false, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResponseMap(t, rec)
	if resp["success"] != false {
		t.Fatalf("expected overall success=false, got %#v", resp)
	}

	summary, ok := resp["summary"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected summary object, got %#v", resp["summary"])
	}
	if summary["requested"].(float64) != 2 {
		t.Fatalf("expected requested=2, got %#v", summary)
	}
	if summary["processed"].(float64) != 1 {
		t.Fatalf("expected processed=1, got %#v", summary)
	}
	if summary["failed"].(float64) != 1 {
		t.Fatalf("expected failed=1, got %#v", summary)
	}
	if summary["skipped"].(float64) != 1 {
		t.Fatalf("expected skipped=1, got %#v", summary)
	}
	if summary["succeeded"].(float64) != 0 {
		t.Fatalf("expected succeeded=0, got %#v", summary)
	}
	if summary["continue_on_error"] != false {
		t.Fatalf("expected continue_on_error=false in summary, got %#v", summary)
	}

	results, ok := resp["results"].([]interface{})
	if !ok {
		t.Fatalf("expected results array, got %#v", resp["results"])
	}
	if len(results) != 1 {
		t.Fatalf("expected only first item result when continue_on_error=false, got %d entries", len(results))
	}

	first, ok := results[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected first result object, got %#v", results[0])
	}
	if first["name"] != "cof_nonreloadable" {
		t.Fatalf("expected first result for cof_nonreloadable, got %#v", first)
	}
	if first["success"] != false {
		t.Fatalf("expected first result success=false, got %#v", first)
	}

	if got := atomic.LoadInt32(&reloadable.reloadCalls); got != 0 {
		t.Fatalf("expected second item not to execute reload (reloadCalls=0), got %d", got)
	}
}

func TestPluginAdminBulkEndpoints_MalformedPayloads(t *testing.T) {
	gateway, _ := setupTestGateway(t)

	pm := plugin.NewManager(plugin.ManagerConfig{})
	if err := pm.Register(&pluginAdminLifecyclePlugin{name: "malformed_alpha"}, plugin.RuntimeConfig{Enabled: true}); err != nil {
		t.Fatalf("failed to register plugin: %v", err)
	}
	if err := pm.Start(context.Background()); err != nil {
		t.Fatalf("failed to start plugin manager: %v", err)
	}
	defer pm.Stop(context.Background())

	gateway.SetPluginManager(pm)
	adminToken := createTokenForRole(t, gateway, "admin")

	tests := []struct {
		name         string
		body         string
		containsText string
	}{
		{
			name:         "unknown top-level field",
			body:         `{"action":"enable","items":[{"name":"malformed_alpha"}],"mystery":true}`,
			containsText: "Invalid request body",
		},
		{
			name:         "unknown item field",
			body:         `{"action":"enable","items":[{"name":"malformed_alpha","oops":true}]}`,
			containsText: "Invalid request body",
		},
		{
			name:         "multiple json objects",
			body:         `{"action":"enable","items":[{"name":"malformed_alpha"}]}{"extra":true}`,
			containsText: "single JSON object",
		},
		{
			name:         "missing items",
			body:         `{"action":"enable"}`,
			containsText: "items must contain at least one plugin",
		},
		{
			name:         "duplicate plugin names case-insensitive",
			body:         `{"action":"enable","items":[{"name":"malformed_alpha"},{"name":"MALFORMED_ALPHA"}]}`,
			containsText: "duplicate plugin name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := makeGatewayRequest(t, gateway, http.MethodPost, "/admin/plugins/bulk", adminToken, []byte(tc.body))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("expected 400 for malformed payload, got %d: %s", rec.Code, rec.Body.String())
			}

			resp := decodeResponseMap(t, rec)
			if resp["success"] != false {
				t.Fatalf("expected success=false, got %#v", resp)
			}

			errText := fmt.Sprintf("%v", resp["error"])
			if !strings.Contains(errText, tc.containsText) {
				t.Fatalf("expected error to contain %q, got %q", tc.containsText, errText)
			}
		})
	}
}

func TestPluginAdminBulkEndpoints_DryRunReloadMergeCorners(t *testing.T) {
	gateway, _ := setupTestGateway(t)

	pm := plugin.NewManager(plugin.ManagerConfig{})
	reloadable := &pluginAdminReloadablePlugin{name: "dryrun_reloadable"}

	if err := pm.Register(reloadable, plugin.RuntimeConfig{
		Enabled: true,
		Timeout: 100 * time.Millisecond,
		Settings: map[string]interface{}{
			"mode":   "safe",
			"region": "eu",
		},
	}); err != nil {
		t.Fatalf("failed to register reloadable plugin: %v", err)
	}
	if err := pm.Start(context.Background()); err != nil {
		t.Fatalf("failed to start plugin manager: %v", err)
	}
	defer pm.Stop(context.Background())

	gateway.SetPluginManager(pm)
	adminToken := createTokenForRole(t, gateway, "admin")

	mergedPreviewPayload, _ := json.Marshal(map[string]interface{}{
		"action":  "reload",
		"dry_run": true,
		"default_reload": map[string]interface{}{
			"timeout": "250ms",
			"settings": map[string]interface{}{
				"mode":   "strict",
				"global": "x",
			},
		},
		"items": []map[string]interface{}{
			{
				"name": "dryrun_reloadable",
				"reload": map[string]interface{}{
					"settings": map[string]interface{}{
						"burst": 200,
					},
				},
			},
		},
	})

	mergedRec := makeGatewayRequest(t, gateway, http.MethodPost, "/admin/plugins/bulk", adminToken, mergedPreviewPayload)
	if mergedRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for dry-run merged reload preview, got %d: %s", mergedRec.Code, mergedRec.Body.String())
	}

	mergedResp := decodeResponseMap(t, mergedRec)
	mergedResults, ok := mergedResp["results"].([]interface{})
	if !ok || len(mergedResults) != 1 {
		t.Fatalf("expected one result entry in merged preview response, got %#v", mergedResp["results"])
	}
	mergedItem, ok := mergedResults[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected merged result object, got %#v", mergedResults[0])
	}
	previewRuntime, ok := mergedItem["preview_runtime"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected preview_runtime map, got %#v", mergedItem["preview_runtime"])
	}
	if previewRuntime["timeout"] != "250ms" {
		t.Fatalf("expected preview timeout=250ms, got %#v", previewRuntime["timeout"])
	}
	previewSettings, ok := previewRuntime["settings"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected preview settings map, got %#v", previewRuntime["settings"])
	}
	if previewSettings["mode"] != "strict" {
		t.Fatalf("expected merged preview mode=strict, got %#v", previewSettings["mode"])
	}
	if previewSettings["region"] != "eu" {
		t.Fatalf("expected merged preview to preserve existing region=eu, got %#v", previewSettings["region"])
	}
	if previewSettings["global"] != "x" {
		t.Fatalf("expected merged preview to include default global=x, got %#v", previewSettings["global"])
	}
	if previewSettings["burst"] != float64(200) {
		t.Fatalf("expected merged preview burst=200, got %#v", previewSettings["burst"])
	}

	replacePreviewPayload, _ := json.Marshal(map[string]interface{}{
		"action":  "reload",
		"dry_run": true,
		"default_reload": map[string]interface{}{
			"settings": map[string]interface{}{
				"mode":   "strict",
				"global": "x",
			},
		},
		"items": []map[string]interface{}{
			{
				"name": "dryrun_reloadable",
				"reload": map[string]interface{}{
					"replace_settings": true,
					"settings": map[string]interface{}{
						"burst": 999,
					},
				},
			},
		},
	})

	replaceRec := makeGatewayRequest(t, gateway, http.MethodPost, "/admin/plugins/bulk", adminToken, replacePreviewPayload)
	if replaceRec.Code != http.StatusOK {
		t.Fatalf("expected 200 for dry-run replace_settings preview, got %d: %s", replaceRec.Code, replaceRec.Body.String())
	}

	replaceResp := decodeResponseMap(t, replaceRec)
	replaceResults, ok := replaceResp["results"].([]interface{})
	if !ok || len(replaceResults) != 1 {
		t.Fatalf("expected one result entry in replace preview response, got %#v", replaceResp["results"])
	}
	replaceItem, ok := replaceResults[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected replace result object, got %#v", replaceResults[0])
	}
	replacePreview, ok := replaceItem["preview_runtime"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected replace preview_runtime map, got %#v", replaceItem["preview_runtime"])
	}
	replaceSettings, ok := replacePreview["settings"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected replace preview settings map, got %#v", replacePreview["settings"])
	}
	if len(replaceSettings) != 1 {
		t.Fatalf("expected replace_settings preview to contain exactly one setting, got %#v", replaceSettings)
	}
	if replaceSettings["burst"] != float64(999) {
		t.Fatalf("expected replace preview burst=999, got %#v", replaceSettings["burst"])
	}
	if _, exists := replaceSettings["mode"]; exists {
		t.Fatalf("did not expect mode key after replace_settings, got %#v", replaceSettings)
	}
	if _, exists := replaceSettings["region"]; exists {
		t.Fatalf("did not expect region key after replace_settings, got %#v", replaceSettings)
	}
	if _, exists := replaceSettings["global"]; exists {
		t.Fatalf("did not expect global key after replace_settings, got %#v", replaceSettings)
	}

	if got := atomic.LoadInt32(&reloadable.reloadCalls); got != 0 {
		t.Fatalf("expected no runtime reload calls during dry-run previews, got %d", got)
	}

	cfg, ok := pm.RuntimeConfig("dryrun_reloadable")
	if !ok {
		t.Fatal("expected runtime config for dryrun_reloadable")
	}
	if cfg.Timeout != 100*time.Millisecond {
		t.Fatalf("expected runtime timeout to remain 100ms after dry-run requests, got %s", cfg.Timeout)
	}
	if cfg.Settings["mode"] != "safe" {
		t.Fatalf("expected runtime mode to remain safe after dry-run requests, got %#v", cfg.Settings["mode"])
	}
	if cfg.Settings["region"] != "eu" {
		t.Fatalf("expected runtime region to remain eu after dry-run requests, got %#v", cfg.Settings["region"])
	}
	if _, exists := cfg.Settings["burst"]; exists {
		t.Fatalf("did not expect runtime burst setting after dry-run requests, got %#v", cfg.Settings)
	}
	if _, exists := cfg.Settings["global"]; exists {
		t.Fatalf("did not expect runtime global setting after dry-run requests, got %#v", cfg.Settings)
	}
}

func TestPluginAdminBulkEndpoints_HealthActionAndPartialFailures(t *testing.T) {
	gateway, _ := setupTestGateway(t)

	pm := plugin.NewManager(plugin.ManagerConfig{})
	reloadable := &pluginAdminReloadablePlugin{name: "bulk_health_reloadable"}
	lifecycle := &pluginAdminLifecyclePlugin{name: "bulk_health_lifecycle"}

	if err := pm.Register(reloadable, plugin.RuntimeConfig{Enabled: true}); err != nil {
		t.Fatalf("failed to register reloadable plugin: %v", err)
	}
	if err := pm.Register(lifecycle, plugin.RuntimeConfig{Enabled: true}); err != nil {
		t.Fatalf("failed to register lifecycle plugin: %v", err)
	}
	if err := pm.Start(context.Background()); err != nil {
		t.Fatalf("failed to start plugin manager: %v", err)
	}
	defer pm.Stop(context.Background())

	gateway.SetPluginManager(pm)
	adminToken := createTokenForRole(t, gateway, "admin")

	payload, _ := json.Marshal(map[string]interface{}{
		"action": "health",
		"items": []map[string]interface{}{
			{"name": "bulk_health_reloadable"},
			{"name": "bulk_missing_plugin"},
			{"name": "bulk_health_lifecycle"},
		},
		"continue_on_error": true,
		"include_runtime":   false,
		"include_stats":     false,
	})

	rec := makeGatewayRequest(t, gateway, http.MethodPost, "/admin/plugins/bulk", adminToken, payload)
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207 for bulk health with partial failures, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResponseMap(t, rec)
	if resp["success"] != false {
		t.Fatalf("expected success=false with partial health failures, got %#v", resp)
	}

	summary, ok := resp["summary"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected summary object, got %#v", resp["summary"])
	}
	if summary["requested"].(float64) != 3 {
		t.Fatalf("expected requested=3, got %#v", summary)
	}
	if summary["processed"].(float64) != 3 {
		t.Fatalf("expected processed=3, got %#v", summary)
	}
	if summary["succeeded"].(float64) != 2 {
		t.Fatalf("expected succeeded=2, got %#v", summary)
	}
	if summary["failed"].(float64) != 1 {
		t.Fatalf("expected failed=1, got %#v", summary)
	}

	results, ok := resp["results"].([]interface{})
	if !ok {
		t.Fatalf("expected results array, got %#v", resp["results"])
	}

	reloadableResult, found := findBulkResultByName(results, "bulk_health_reloadable")
	if !found {
		t.Fatalf("expected bulk_health_reloadable result, got %#v", results)
	}
	if reloadableResult["success"] != true {
		t.Fatalf("expected reloadable health result success=true, got %#v", reloadableResult)
	}
	if _, ok := reloadableResult["health"]; !ok {
		t.Fatalf("expected health payload for reloadable health result, got %#v", reloadableResult)
	}

	missingResult, found := findBulkResultByName(results, "bulk_missing_plugin")
	if !found {
		t.Fatalf("expected bulk_missing_plugin result, got %#v", results)
	}
	if missingResult["success"] != false {
		t.Fatalf("expected missing plugin health result success=false, got %#v", missingResult)
	}
}

func TestPluginAdminBulkEndpoints_RollbackOnErrorRestoresPriorChanges(t *testing.T) {
	gateway, _ := setupTestGateway(t)

	pm := plugin.NewManager(plugin.ManagerConfig{})
	first := &pluginAdminReloadablePlugin{name: "rollback_first"}
	third := &pluginAdminReloadablePlugin{name: "rollback_third"}
	nonReloadable := &pluginAdminLifecyclePlugin{name: "rollback_nonreloadable"}

	if err := pm.Register(first, plugin.RuntimeConfig{Enabled: true, Settings: map[string]interface{}{"mode": "safe"}}); err != nil {
		t.Fatalf("failed to register first plugin: %v", err)
	}
	if err := pm.Register(nonReloadable, plugin.RuntimeConfig{Enabled: true}); err != nil {
		t.Fatalf("failed to register non-reloadable plugin: %v", err)
	}
	if err := pm.Register(third, plugin.RuntimeConfig{Enabled: true, Settings: map[string]interface{}{"mode": "safe"}}); err != nil {
		t.Fatalf("failed to register third plugin: %v", err)
	}
	if err := pm.Start(context.Background()); err != nil {
		t.Fatalf("failed to start plugin manager: %v", err)
	}
	defer pm.Stop(context.Background())

	gateway.SetPluginManager(pm)
	adminToken := createTokenForRole(t, gateway, "admin")

	payload, _ := json.Marshal(map[string]interface{}{
		"action":            "reload",
		"rollback_on_error": true,
		"continue_on_error": true,
		"default_reload": map[string]interface{}{
			"settings": map[string]interface{}{
				"mode": "strict",
			},
		},
		"items": []map[string]interface{}{
			{"name": "rollback_first"},
			{"name": "rollback_nonreloadable"},
			{"name": "rollback_third"},
		},
	})

	rec := makeGatewayRequest(t, gateway, http.MethodPost, "/admin/plugins/bulk", adminToken, payload)
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("expected 207 for rollback-on-error scenario, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResponseMap(t, rec)
	if resp["success"] != false {
		t.Fatalf("expected success=false for rollback-on-error with failure, got %#v", resp)
	}

	summary, ok := resp["summary"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected summary object, got %#v", resp["summary"])
	}
	if summary["requested"].(float64) != 3 {
		t.Fatalf("expected requested=3, got %#v", summary)
	}
	if summary["processed"].(float64) != 2 {
		t.Fatalf("expected processed=2 due stop-on-failure rollback mode, got %#v", summary)
	}
	if summary["skipped"].(float64) != 1 {
		t.Fatalf("expected skipped=1 due rollback mode stop, got %#v", summary)
	}
	if summary["continue_on_error"] != false {
		t.Fatalf("expected effective continue_on_error=false when rollback_on_error=true, got %#v", summary)
	}
	if summary["continue_on_error_requested"] != true {
		t.Fatalf("expected continue_on_error_requested=true in summary, got %#v", summary)
	}
	if summary["rollback_performed"] != true {
		t.Fatalf("expected rollback_performed=true in summary, got %#v", summary)
	}

	rollback, ok := resp["rollback"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected rollback summary object, got %#v", resp["rollback"])
	}
	if rollback["attempted"].(float64) != 1 {
		t.Fatalf("expected rollback attempted=1, got %#v", rollback)
	}
	if rollback["succeeded"].(float64) != 1 {
		t.Fatalf("expected rollback succeeded=1, got %#v", rollback)
	}
	if rollback["failed"].(float64) != 0 {
		t.Fatalf("expected rollback failed=0, got %#v", rollback)
	}

	results, ok := resp["results"].([]interface{})
	if !ok {
		t.Fatalf("expected results array, got %#v", resp["results"])
	}
	if len(results) != 2 {
		t.Fatalf("expected two processed results, got %d", len(results))
	}

	firstResult, found := findBulkResultByName(results, "rollback_first")
	if !found {
		t.Fatalf("expected rollback_first result, got %#v", results)
	}
	if firstResult["success"] != true {
		t.Fatalf("expected first operation success=true before rollback, got %#v", firstResult)
	}
	if firstResult["rolled_back"] != true {
		t.Fatalf("expected first operation to be marked rolled_back=true, got %#v", firstResult)
	}

	firstCfg, ok := pm.RuntimeConfig("rollback_first")
	if !ok {
		t.Fatal("expected runtime config for rollback_first")
	}
	if firstCfg.Settings["mode"] != "safe" {
		t.Fatalf("expected rollback_first mode restored to safe, got %#v", firstCfg.Settings["mode"])
	}

	thirdCfg, ok := pm.RuntimeConfig("rollback_third")
	if !ok {
		t.Fatal("expected runtime config for rollback_third")
	}
	if thirdCfg.Settings["mode"] != "safe" {
		t.Fatalf("expected rollback_third to remain safe (skipped), got %#v", thirdCfg.Settings["mode"])
	}

	if got := atomic.LoadInt32(&first.reloadCalls); got != 2 {
		t.Fatalf("expected rollback_first reloadCalls=2 (forward+rollback), got %d", got)
	}
	if got := atomic.LoadInt32(&third.reloadCalls); got != 0 {
		t.Fatalf("expected rollback_third reloadCalls=0 (skipped), got %d", got)
	}
}

func TestPluginAdminAuditEndpoint_QueryFiltersAndPagination(t *testing.T) {
	gateway, _ := setupTestGateway(t)

	pm := plugin.NewManager(plugin.ManagerConfig{})
	auditToggle := &pluginAdminLifecyclePlugin{name: "audit_query_toggle"}
	nonReloadable := &pluginAdminLifecyclePlugin{name: "audit_query_nonreloadable"}

	if err := pm.Register(auditToggle, plugin.RuntimeConfig{Enabled: false}); err != nil {
		t.Fatalf("failed to register audit toggle plugin: %v", err)
	}
	if err := pm.Register(nonReloadable, plugin.RuntimeConfig{Enabled: true}); err != nil {
		t.Fatalf("failed to register non-reloadable plugin: %v", err)
	}
	if err := pm.Start(context.Background()); err != nil {
		t.Fatalf("failed to start plugin manager: %v", err)
	}
	defer pm.Stop(context.Background())

	gateway.SetPluginManager(pm)
	adminToken := createTokenForRole(t, gateway, "admin")

	enableRec := makeGatewayRequest(t, gateway, http.MethodPost, "/admin/plugins/audit_query_toggle/enable", adminToken, []byte("{}"))
	if enableRec.Code != http.StatusOK {
		t.Fatalf("expected enable to succeed, got %d: %s", enableRec.Code, enableRec.Body.String())
	}

	reloadFailRec := makeGatewayRequest(t, gateway, http.MethodPost, "/admin/plugins/audit_query_nonreloadable/reload", adminToken, []byte("{}"))
	if reloadFailRec.Code != http.StatusConflict {
		t.Fatalf("expected non-reloadable reload to fail with 409, got %d: %s", reloadFailRec.Code, reloadFailRec.Body.String())
	}

	healthPayload, _ := json.Marshal(map[string]interface{}{
		"action": "health",
		"items": []map[string]interface{}{
			{"name": "audit_query_toggle"},
		},
	})
	healthRec := makeGatewayRequest(t, gateway, http.MethodPost, "/admin/plugins/bulk", adminToken, healthPayload)
	if healthRec.Code != http.StatusOK {
		t.Fatalf("expected bulk health call to succeed, got %d: %s", healthRec.Code, healthRec.Body.String())
	}

	filteredRec := makeGatewayRequest(t, gateway, http.MethodGet, "/admin/plugins/audit?action=reload&success=false&limit=10", adminToken, nil)
	if filteredRec.Code != http.StatusOK {
		t.Fatalf("expected filtered audit query to succeed, got %d: %s", filteredRec.Code, filteredRec.Body.String())
	}
	filteredResp := decodeResponseMap(t, filteredRec)
	if filteredResp["success"] != true {
		t.Fatalf("expected success=true for filtered audit query, got %#v", filteredResp)
	}

	filteredResults, ok := filteredResp["records"].([]interface{})
	if !ok {
		t.Fatalf("expected records array in filtered audit query, got %#v", filteredResp["records"])
	}
	if len(filteredResults) == 0 {
		t.Fatalf("expected at least one failed reload audit record, got %#v", filteredResp)
	}
	for _, raw := range filteredResults {
		record, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("expected record object, got %#v", raw)
		}
		if record["action"] != "reload" {
			t.Fatalf("expected action=reload in filtered record, got %#v", record)
		}
		if record["success"] != false {
			t.Fatalf("expected success=false in filtered record, got %#v", record)
		}
	}

	pageOneRec := makeGatewayRequest(t, gateway, http.MethodGet, "/admin/plugins/audit?limit=1&offset=0&sort=desc", adminToken, nil)
	if pageOneRec.Code != http.StatusOK {
		t.Fatalf("expected first audit page query to succeed, got %d: %s", pageOneRec.Code, pageOneRec.Body.String())
	}
	pageTwoRec := makeGatewayRequest(t, gateway, http.MethodGet, "/admin/plugins/audit?limit=1&offset=1&sort=desc", adminToken, nil)
	if pageTwoRec.Code != http.StatusOK {
		t.Fatalf("expected second audit page query to succeed, got %d: %s", pageTwoRec.Code, pageTwoRec.Body.String())
	}

	pageOneResp := decodeResponseMap(t, pageOneRec)
	pageTwoResp := decodeResponseMap(t, pageTwoRec)
	pageOneRecords, _ := pageOneResp["records"].([]interface{})
	pageTwoRecords, _ := pageTwoResp["records"].([]interface{})
	if len(pageOneRecords) != 1 || len(pageTwoRecords) != 1 {
		t.Fatalf("expected one record per page, got page1=%d page2=%d", len(pageOneRecords), len(pageTwoRecords))
	}

	firstRecord, ok := pageOneRecords[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected page one record object, got %#v", pageOneRecords[0])
	}
	secondRecord, ok := pageTwoRecords[0].(map[string]interface{})
	if !ok {
		t.Fatalf("expected page two record object, got %#v", pageTwoRecords[0])
	}
	if firstRecord["id"] == secondRecord["id"] {
		t.Fatalf("expected different records across pagination pages, got page1=%#v page2=%#v", firstRecord, secondRecord)
	}

	invalidTimeRec := makeGatewayRequest(t, gateway, http.MethodGet, "/admin/plugins/audit?since=not-a-time", adminToken, nil)
	if invalidTimeRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid time filter, got %d: %s", invalidTimeRec.Code, invalidTimeRec.Body.String())
	}
}

func TestPluginAdminAuditPersistence_LoadsAcrossGatewayRestart(t *testing.T) {
	auditDir := t.TempDir()
	auditFile := filepath.Join(auditDir, "plugin-admin-audit.jsonl")

	t.Setenv(pluginAdminAuditPersistenceEnv, "true")
	t.Setenv(pluginAdminAuditFileEnv, auditFile)
	t.Setenv(pluginAdminAuditMemoryLimitEnv, "32")
	t.Setenv(pluginAdminAuditMaxRecordsEnv, "100")
	t.Setenv(pluginAdminAuditMaxAgeEnv, "720h")
	t.Setenv(pluginAdminAuditCompactEveryEnv, "1000")

	gatewayOne, _ := setupTestGateway(t)
	gatewayOne.appendPluginAdminAuditRecord(pluginAdminAuditRecord{
		ID:         "persist-restart-1",
		Timestamp:  time.Now().UTC(),
		Action:     "enable",
		Target:     "persist_restart_plugin",
		Success:    true,
		UserID:     "restart-user",
		Method:     http.MethodPost,
		Path:       "/admin/plugins/persist_restart_plugin/enable",
		RemoteAddr: "127.0.0.1",
	})

	gatewayTwo, _ := setupTestGateway(t)
	adminToken := createTokenForRole(t, gatewayTwo, "admin")

	rec := makeGatewayRequest(t, gatewayTwo, http.MethodGet, "/admin/plugins/audit?source=persistent&sort=asc", adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected persistent audit query to succeed, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResponseMap(t, rec)
	if resp["success"] != true {
		t.Fatalf("expected success=true for persistent audit query, got %#v", resp)
	}

	summary, ok := resp["summary"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected summary object, got %#v", resp["summary"])
	}
	if summary["source"] != string(pluginAuditSourcePersistent) {
		t.Fatalf("expected summary source=persistent, got %#v", summary["source"])
	}

	records, ok := resp["records"].([]interface{})
	if !ok {
		t.Fatalf("expected records array, got %#v", resp["records"])
	}
	if len(records) == 0 {
		t.Fatalf("expected at least one persisted audit record, got %#v", resp)
	}

	found := false
	for _, raw := range records {
		record, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("expected record object, got %#v", raw)
		}
		if record["id"] == "persist-restart-1" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected persisted record id persist-restart-1, got %#v", records)
	}

	persistence, ok := resp["persistence"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected persistence metadata object, got %#v", resp["persistence"])
	}
	if persistence["enabled"] != true {
		t.Fatalf("expected persistence metadata enabled=true, got %#v", persistence)
	}
}

func TestPluginAdminAuditPersistence_CompactionEnforcesRetentionPolicies(t *testing.T) {
	auditDir := t.TempDir()
	auditFile := filepath.Join(auditDir, "plugin-admin-audit.jsonl")

	t.Setenv(pluginAdminAuditPersistenceEnv, "true")
	t.Setenv(pluginAdminAuditFileEnv, auditFile)
	t.Setenv(pluginAdminAuditMemoryLimitEnv, "64")
	t.Setenv(pluginAdminAuditMaxRecordsEnv, "3")
	t.Setenv(pluginAdminAuditMaxAgeEnv, "1h")
	t.Setenv(pluginAdminAuditCompactEveryEnv, "1000")

	gateway, _ := setupTestGateway(t)
	now := time.Now().UTC()

	records := []pluginAdminAuditRecord{
		{ID: "retain-old-1", Timestamp: now.Add(-3 * time.Hour), Action: "enable", Target: "audit-old-1", Success: true, UserID: "u1", Method: http.MethodPost, Path: "/admin/plugins/audit-old-1/enable", RemoteAddr: "127.0.0.1"},
		{ID: "retain-old-2", Timestamp: now.Add(-2 * time.Hour), Action: "enable", Target: "audit-old-2", Success: true, UserID: "u1", Method: http.MethodPost, Path: "/admin/plugins/audit-old-2/enable", RemoteAddr: "127.0.0.1"},
		{ID: "retain-keep-1", Timestamp: now.Add(-50 * time.Minute), Action: "disable", Target: "audit-keep-1", Success: true, UserID: "u2", Method: http.MethodPost, Path: "/admin/plugins/audit-keep-1/disable", RemoteAddr: "127.0.0.1"},
		{ID: "retain-keep-2", Timestamp: now.Add(-40 * time.Minute), Action: "disable", Target: "audit-keep-2", Success: true, UserID: "u2", Method: http.MethodPost, Path: "/admin/plugins/audit-keep-2/disable", RemoteAddr: "127.0.0.1"},
		{ID: "retain-keep-3", Timestamp: now.Add(-30 * time.Minute), Action: "reload", Target: "audit-keep-3", Success: true, UserID: "u3", Method: http.MethodPost, Path: "/admin/plugins/audit-keep-3/reload", RemoteAddr: "127.0.0.1"},
		{ID: "retain-keep-4", Timestamp: now.Add(-20 * time.Minute), Action: "reload", Target: "audit-keep-4", Success: true, UserID: "u3", Method: http.MethodPost, Path: "/admin/plugins/audit-keep-4/reload", RemoteAddr: "127.0.0.1"},
	}

	for _, record := range records {
		gateway.appendPluginAdminAuditRecord(record)
	}

	adminToken := createTokenForRole(t, gateway, "admin")
	compactRec := makeGatewayRequest(t, gateway, http.MethodPost, "/admin/plugins/audit/compact", adminToken, []byte("{}"))
	if compactRec.Code != http.StatusOK {
		t.Fatalf("expected manual audit compaction to succeed, got %d: %s", compactRec.Code, compactRec.Body.String())
	}

	compactResp := decodeResponseMap(t, compactRec)
	if compactResp["success"] != true {
		t.Fatalf("expected success=true from compaction endpoint, got %#v", compactResp)
	}

	compaction, ok := compactResp["compaction"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected compaction object, got %#v", compactResp["compaction"])
	}
	if compaction["before_records"].(float64) != 6 {
		t.Fatalf("expected before_records=6, got %#v", compaction)
	}
	if compaction["after_records"].(float64) != 3 {
		t.Fatalf("expected after_records=3 after retention enforcement, got %#v", compaction)
	}

	queryRec := makeGatewayRequest(t, gateway, http.MethodGet, "/admin/plugins/audit?source=persistent&sort=asc&limit=20", adminToken, nil)
	if queryRec.Code != http.StatusOK {
		t.Fatalf("expected persistent audit query after compaction to succeed, got %d: %s", queryRec.Code, queryRec.Body.String())
	}

	queryResp := decodeResponseMap(t, queryRec)
	recorded, ok := queryResp["records"].([]interface{})
	if !ok {
		t.Fatalf("expected records array in persistent query response, got %#v", queryResp["records"])
	}
	if len(recorded) != 3 {
		t.Fatalf("expected three retained records after compaction, got %d (%#v)", len(recorded), recorded)
	}

	hasKeep3 := false
	hasKeep4 := false
	hasCompactAudit := false

	gotIDs := make([]string, 0, len(recorded))
	for _, raw := range recorded {
		record, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("expected record object, got %#v", raw)
		}
		id, _ := record["id"].(string)
		gotIDs = append(gotIDs, id)

		switch id {
		case "retain-old-1", "retain-old-2", "retain-keep-1", "retain-keep-2":
			t.Fatalf("did not expect retained stale or overflowed audit ID %q, got %v", id, gotIDs)
		case "retain-keep-3":
			hasKeep3 = true
		case "retain-keep-4":
			hasKeep4 = true
		default:
			if strings.HasPrefix(id, "pa-") {
				hasCompactAudit = true
			}
		}
	}

	if !hasKeep3 || !hasKeep4 || !hasCompactAudit {
		t.Fatalf("expected retained set to include keep-3, keep-4, and compact audit record, got %v", gotIDs)
	}
}

func TestPluginAdminAuditPersistence_AutoSourceFallsBackWhenStorageCorrupted(t *testing.T) {
	auditDir := t.TempDir()
	auditFile := filepath.Join(auditDir, "plugin-admin-audit.jsonl")
	if err := os.WriteFile(auditFile, []byte("not-json\n"), 0o644); err != nil {
		t.Fatalf("failed to seed corrupt audit file: %v", err)
	}

	t.Setenv(pluginAdminAuditPersistenceEnv, "true")
	t.Setenv(pluginAdminAuditFileEnv, auditFile)
	t.Setenv(pluginAdminAuditMemoryLimitEnv, "32")
	t.Setenv(pluginAdminAuditMaxRecordsEnv, "100")
	t.Setenv(pluginAdminAuditMaxAgeEnv, "720h")
	t.Setenv(pluginAdminAuditCompactEveryEnv, "1000")

	gateway, _ := setupTestGateway(t)
	adminToken := createTokenForRole(t, gateway, "admin")

	autoRec := makeGatewayRequest(t, gateway, http.MethodGet, "/admin/plugins/audit?source=auto", adminToken, nil)
	if autoRec.Code != http.StatusOK {
		t.Fatalf("expected auto source query to fall back to memory and succeed, got %d: %s", autoRec.Code, autoRec.Body.String())
	}

	autoResp := decodeResponseMap(t, autoRec)
	summary, ok := autoResp["summary"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected summary object for auto source response, got %#v", autoResp["summary"])
	}
	if summary["source"] != string(pluginAuditSourceMemory) {
		t.Fatalf("expected fallback source=memory for corrupt persistent store, got %#v", summary["source"])
	}
	if _, ok := summary["source_warning"]; !ok {
		t.Fatalf("expected source_warning when persistent store is corrupt, got %#v", summary)
	}

	persistentRec := makeGatewayRequest(t, gateway, http.MethodGet, "/admin/plugins/audit?source=persistent", adminToken, nil)
	if persistentRec.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected persistent source query to fail with 503 when store is corrupt, got %d: %s", persistentRec.Code, persistentRec.Body.String())
	}
}

func TestPluginAdminAuditPersistence_StartupRepairModeSkipsCorruptLines(t *testing.T) {
	auditDir := t.TempDir()
	auditFile := filepath.Join(auditDir, "plugin-admin-audit.jsonl")

	now := time.Now().UTC()
	first := pluginAdminAuditRecord{ID: "repair-1", Timestamp: now.Add(-2 * time.Minute), Action: "enable", Target: "repair-a", Success: true, UserID: "repair-user", Method: http.MethodPost, Path: "/admin/plugins/repair-a/enable", RemoteAddr: "127.0.0.1"}
	second := pluginAdminAuditRecord{ID: "repair-2", Timestamp: now.Add(-1 * time.Minute), Action: "reload", Target: "repair-b", Success: true, UserID: "repair-user", Method: http.MethodPost, Path: "/admin/plugins/repair-b/reload", RemoteAddr: "127.0.0.1"}

	firstPayload, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("failed to marshal first repair record: %v", err)
	}
	secondPayload, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("failed to marshal second repair record: %v", err)
	}

	seed := append([]byte{}, firstPayload...)
	seed = append(seed, '\n')
	seed = append(seed, []byte("{broken-json-line\n")...)
	seed = append(seed, secondPayload...)
	seed = append(seed, '\n')

	if err := os.WriteFile(auditFile, seed, 0o644); err != nil {
		t.Fatalf("failed to write startup repair seed file: %v", err)
	}

	t.Setenv(pluginAdminAuditPersistenceEnv, "true")
	t.Setenv(pluginAdminAuditFileEnv, auditFile)
	t.Setenv(pluginAdminAuditMemoryLimitEnv, "32")
	t.Setenv(pluginAdminAuditMaxRecordsEnv, "100")
	t.Setenv(pluginAdminAuditMaxAgeEnv, "720h")
	t.Setenv(pluginAdminAuditCompactEveryEnv, "1000")
	t.Setenv(pluginAdminAuditRepairModeEnv, string(pluginAuditRepairModeSkipBadLines))
	t.Setenv(pluginAdminAuditBackgroundCompactIntervalEnv, "0")

	gateway, _ := setupTestGateway(t)
	defer gateway.Stop()

	adminToken := createTokenForRole(t, gateway, "admin")
	rec := makeGatewayRequest(t, gateway, http.MethodGet, "/admin/plugins/audit?source=persistent&sort=asc&limit=10", adminToken, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected persistent audit query to succeed with startup repair mode, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeResponseMap(t, rec)
	records, ok := resp["records"].([]interface{})
	if !ok {
		t.Fatalf("expected records array in startup repair response, got %#v", resp["records"])
	}
	if len(records) != 2 {
		t.Fatalf("expected two repaired records, got %d (%#v)", len(records), records)
	}

	seen := map[string]bool{}
	for _, raw := range records {
		record, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("expected record object, got %#v", raw)
		}
		id, _ := record["id"].(string)
		seen[id] = true
	}
	if !seen["repair-1"] || !seen["repair-2"] {
		t.Fatalf("expected repaired records repair-1 and repair-2, got %#v", records)
	}

	persistence, ok := resp["persistence"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected persistence metadata object, got %#v", resp["persistence"])
	}
	if persistence["repair_mode"] != string(pluginAuditRepairModeSkipBadLines) {
		t.Fatalf("expected repair_mode=skip_bad_lines, got %#v", persistence["repair_mode"])
	}
	skipped, ok := persistence["skipped_corrupt_lines_total"].(float64)
	if !ok || skipped < 1 {
		t.Fatalf("expected skipped_corrupt_lines_total >= 1, got %#v", persistence["skipped_corrupt_lines_total"])
	}
	if _, ok := persistence["last_repair_at"]; !ok {
		t.Fatalf("expected last_repair_at in persistence metadata, got %#v", persistence)
	}

	sanitized, err := os.ReadFile(auditFile)
	if err != nil {
		t.Fatalf("failed to read sanitized audit file: %v", err)
	}
	if strings.Contains(string(sanitized), "broken-json-line") {
		t.Fatalf("expected startup repair compaction to rewrite and remove corrupt line, got %q", string(sanitized))
	}
}

func TestPluginAdminAuditPersistence_BackgroundCompactionScheduler(t *testing.T) {
	auditDir := t.TempDir()
	auditFile := filepath.Join(auditDir, "plugin-admin-audit.jsonl")

	t.Setenv(pluginAdminAuditPersistenceEnv, "true")
	t.Setenv(pluginAdminAuditFileEnv, auditFile)
	t.Setenv(pluginAdminAuditMemoryLimitEnv, "64")
	t.Setenv(pluginAdminAuditMaxRecordsEnv, "2")
	t.Setenv(pluginAdminAuditMaxAgeEnv, "720h")
	t.Setenv(pluginAdminAuditCompactEveryEnv, "100000")
	t.Setenv(pluginAdminAuditBackgroundCompactIntervalEnv, "1s")
	t.Setenv(pluginAdminAuditRepairModeEnv, string(pluginAuditRepairModeStrict))

	gateway, _ := setupTestGateway(t)
	defer gateway.Stop()

	now := time.Now().UTC()
	records := []pluginAdminAuditRecord{
		{ID: "bg-1", Timestamp: now.Add(-4 * time.Minute), Action: "enable", Target: "bg-a", Success: true, UserID: "bg-user", Method: http.MethodPost, Path: "/admin/plugins/bg-a/enable", RemoteAddr: "127.0.0.1"},
		{ID: "bg-2", Timestamp: now.Add(-3 * time.Minute), Action: "enable", Target: "bg-b", Success: true, UserID: "bg-user", Method: http.MethodPost, Path: "/admin/plugins/bg-b/enable", RemoteAddr: "127.0.0.1"},
		{ID: "bg-3", Timestamp: now.Add(-2 * time.Minute), Action: "reload", Target: "bg-c", Success: true, UserID: "bg-user", Method: http.MethodPost, Path: "/admin/plugins/bg-c/reload", RemoteAddr: "127.0.0.1"},
		{ID: "bg-4", Timestamp: now.Add(-1 * time.Minute), Action: "reload", Target: "bg-d", Success: true, UserID: "bg-user", Method: http.MethodPost, Path: "/admin/plugins/bg-d/reload", RemoteAddr: "127.0.0.1"},
	}
	for _, record := range records {
		gateway.appendPluginAdminAuditRecord(record)
	}

	adminToken := createTokenForRole(t, gateway, "admin")
	deadline := time.Now().Add(5 * time.Second)

	var lastResp map[string]interface{}
	schedulerObserved := false
	for time.Now().Before(deadline) {
		rec := makeGatewayRequest(t, gateway, http.MethodGet, "/admin/plugins/audit?source=persistent&sort=asc&limit=10", adminToken, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("expected background compaction query to succeed, got %d: %s", rec.Code, rec.Body.String())
		}
		lastResp = decodeResponseMap(t, rec)

		persistence, ok := lastResp["persistence"].(map[string]interface{})
		if ok {
			if _, exists := persistence["background_compaction_last_run"]; exists {
				schedulerObserved = true
			}
		}

		rawRecords, ok := lastResp["records"].([]interface{})
		if !ok {
			t.Fatalf("expected records array from background compaction query, got %#v", lastResp["records"])
		}

		if schedulerObserved && len(rawRecords) == 2 {
			ids := make([]string, 0, len(rawRecords))
			for _, raw := range rawRecords {
				record, ok := raw.(map[string]interface{})
				if !ok {
					t.Fatalf("expected record object, got %#v", raw)
				}
				id, _ := record["id"].(string)
				ids = append(ids, id)
			}

			if strings.Join(ids, ",") == "bg-3,bg-4" {
				rawFile, err := os.ReadFile(auditFile)
				if err != nil {
					t.Fatalf("failed reading persisted audit file during scheduler check: %v", err)
				}

				nonEmptyLines := 0
				for _, line := range strings.Split(string(rawFile), "\n") {
					if strings.TrimSpace(line) != "" {
						nonEmptyLines++
					}
				}

				if nonEmptyLines == 2 {
					break
				}
			}
		}

		time.Sleep(100 * time.Millisecond)
	}

	if lastResp == nil {
		t.Fatal("expected at least one background compaction query response")
	}

	finalRecords, ok := lastResp["records"].([]interface{})
	if !ok {
		t.Fatalf("expected final records array, got %#v", lastResp["records"])
	}
	if len(finalRecords) != 2 {
		t.Fatalf("expected background scheduler to compact to two persisted records, got %d (%#v)", len(finalRecords), finalRecords)
	}

	finalIDs := make([]string, 0, len(finalRecords))
	for _, raw := range finalRecords {
		record, ok := raw.(map[string]interface{})
		if !ok {
			t.Fatalf("expected final record object, got %#v", raw)
		}
		id, _ := record["id"].(string)
		finalIDs = append(finalIDs, id)
	}
	if strings.Join(finalIDs, ",") != "bg-3,bg-4" {
		t.Fatalf("expected scheduler-retained IDs bg-3,bg-4, got %v", finalIDs)
	}

	rawFile, err := os.ReadFile(auditFile)
	if err != nil {
		t.Fatalf("failed reading final background-compacted audit file: %v", err)
	}
	nonEmptyLines := 0
	for _, line := range strings.Split(string(rawFile), "\n") {
		if strings.TrimSpace(line) != "" {
			nonEmptyLines++
		}
	}
	if nonEmptyLines != 2 {
		t.Fatalf("expected persisted file to be compacted to two lines by scheduler, got %d lines in %q", nonEmptyLines, string(rawFile))
	}

	persistence, ok := lastResp["persistence"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected persistence metadata object in background scheduler response, got %#v", lastResp["persistence"])
	}
	if persistence["background_compaction_running"] != true {
		t.Fatalf("expected background_compaction_running=true, got %#v", persistence["background_compaction_running"])
	}
	if persistence["background_compaction_interval"] != "1s" {
		t.Fatalf("expected background compaction interval=1s, got %#v", persistence["background_compaction_interval"])
	}
	if _, ok := persistence["background_compaction_last_run"]; !ok {
		t.Fatalf("expected background_compaction_last_run metadata after scheduler tick, got %#v", persistence)
	}
	if !schedulerObserved {
		t.Fatalf("expected to observe scheduler metadata tick before asserting compaction result, got %#v", persistence)
	}
}

func TestPluginAdminSingleAction_EmitsAuditEvent(t *testing.T) {
	gateway, _ := setupTestGateway(t)

	pm := plugin.NewManager(plugin.ManagerConfig{})
	auditCollector := &pluginAdminAuditCollector{events: make(chan plugin.Event, 128)}
	toggle := &pluginAdminLifecyclePlugin{name: "audit_toggle"}

	if err := pm.Register(auditCollector, plugin.RuntimeConfig{Enabled: true}); err != nil {
		t.Fatalf("failed to register audit collector plugin: %v", err)
	}
	if err := pm.Register(toggle, plugin.RuntimeConfig{Enabled: false}); err != nil {
		t.Fatalf("failed to register toggle plugin: %v", err)
	}
	if err := pm.Start(context.Background()); err != nil {
		t.Fatalf("failed to start plugin manager: %v", err)
	}
	defer pm.Stop(context.Background())

	gateway.SetPluginManager(pm)
	adminToken := createTokenForRole(t, gateway, "admin")

	enableRec := makeGatewayRequest(t, gateway, http.MethodPost, "/admin/plugins/audit_toggle/enable", adminToken, []byte("{}"))
	if enableRec.Code != http.StatusOK {
		t.Fatalf("expected 200 from single enable endpoint, got %d: %s", enableRec.Code, enableRec.Body.String())
	}

	deadline := time.Now().Add(2 * time.Second)
	found := false
	for time.Now().Before(deadline) && !found {
		select {
		case event := <-auditCollector.events:
			if event.Type == "admin.plugin.enable" {
				found = true
			}
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}

	if !found {
		t.Fatal("expected admin.plugin.enable audit event for single-action endpoint")
	}
}
