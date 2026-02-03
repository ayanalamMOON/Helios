package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/helios/helios/internal/atlas"
	"github.com/helios/helios/internal/auth"
	"github.com/helios/helios/internal/auth/rbac"
	"github.com/helios/helios/internal/queue"
	"github.com/helios/helios/internal/rate"
)

// mockRaftManager implements the RaftManager interface for testing
type mockRaftManager struct {
	peers    map[string]string
	leader   string
	isLeader bool
	addError error
	rmError  error
}

func (m *mockRaftManager) AddPeer(id, address string) error {
	if m.addError != nil {
		return m.addError
	}
	m.peers[id] = address
	return nil
}

func (m *mockRaftManager) RemovePeer(id string) error {
	if m.rmError != nil {
		return m.rmError
	}
	delete(m.peers, id)
	return nil
}

func (m *mockRaftManager) GetPeers() ([]PeerInfo, error) {
	peers := make([]PeerInfo, 0, len(m.peers))
	for id, addr := range m.peers {
		peers = append(peers, PeerInfo{
			ID:      id,
			Address: addr,
			State:   "Active",
		})
	}
	return peers, nil
}

func (m *mockRaftManager) GetLeader() (string, error) {
	return m.leader, nil
}

func (m *mockRaftManager) IsLeader() bool {
	return m.isLeader
}

func (m *mockRaftManager) GetClusterStatus() (interface{}, error) {
	return map[string]interface{}{
		"node": map[string]interface{}{
			"id":    "node-1",
			"state": "Leader",
			"term":  uint64(1),
		},
		"leader": map[string]interface{}{
			"id":        m.leader,
			"address":   m.peers[m.leader],
			"is_leader": m.isLeader,
		},
		"indices": map[string]interface{}{
			"last_log":      uint64(10),
			"last_log_term": uint64(1),
			"commit_index":  uint64(10),
			"applied_index": uint64(10),
		},
		"peers": []interface{}{},
		"sessions": map[string]interface{}{
			"active_count": 0,
		},
		"tls": map[string]interface{}{
			"enabled":     false,
			"verify_peer": false,
		},
		"uptime": "1h0m0s",
	}, nil
}

// Latency metrics methods
func (m *mockRaftManager) GetPeerLatencyStats(peerID string) (interface{}, bool) {
	return map[string]interface{}{
		"peer_id": peerID,
		"address": m.peers[peerID],
		"append_entries": map[string]interface{}{
			"count":        int64(10),
			"avg_value_ms": 2.5,
		},
	}, true
}

func (m *mockRaftManager) GetAllPeerLatencyStats() interface{} {
	return map[string]interface{}{}
}

func (m *mockRaftManager) GetAggregatedLatencyStats() interface{} {
	return map[string]interface{}{
		"count":        int64(100),
		"avg_value_ms": 2.3,
	}
}

func (m *mockRaftManager) GetPeerHealthSummary() (healthy int, unhealthy int, total int) {
	return len(m.peers), 0, len(m.peers)
}

func (m *mockRaftManager) ResetPeerLatencyStats() {
	// No-op for mock
}

// Uptime methods
func (m *mockRaftManager) GetUptimeStats() interface{} {
	return map[string]interface{}{
		"node_id": "test-node",
		"current_session": map[string]interface{}{
			"uptime_seconds": 3600,
		},
	}
}

func (m *mockRaftManager) GetUptimeHistory(maxEvents, maxSessions int) interface{} {
	return map[string]interface{}{
		"events":   []interface{}{},
		"sessions": []interface{}{},
	}
}

func (m *mockRaftManager) ResetUptimeHistory() {
	// No-op for mock
}

// Snapshot methods
func (m *mockRaftManager) GetSnapshotStats() interface{} {
	return map[string]interface{}{
		"total_snapshots": 1,
		"has_snapshot":    true,
	}
}

func (m *mockRaftManager) GetSnapshotHistory(maxEvents, maxSnapshots int) interface{} {
	return map[string]interface{}{
		"events":    []interface{}{},
		"snapshots": []interface{}{},
	}
}

func (m *mockRaftManager) GetLatestSnapshotMetadata() interface{} {
	return nil
}

func (m *mockRaftManager) ResetSnapshotStats() {
	// No-op for mock
}

// setupTestGateway creates a gateway with mock services for testing
func setupTestGateway(t *testing.T) (*Gateway, *mockRaftManager) {
	// Create in-memory Atlas store
	cfg := &atlas.Config{
		DataDir:     t.TempDir(),
		AOFSyncMode: 0,
	}
	atlasInstance, err := atlas.New(cfg)
	if err != nil {
		t.Fatalf("Failed to create Atlas: %v", err)
	}
	t.Cleanup(func() { atlasInstance.Close() })

	// Create services
	authService := auth.NewService(atlasInstance)
	rbacService := rbac.NewService(atlasInstance)
	rateLimiter := rate.NewLimiter(atlasInstance)
	queueInstance := queue.NewQueue(atlasInstance, queue.DefaultConfig())

	// Initialize default RBAC roles
	if err := rbacService.InitializeDefaultRoles(); err != nil {
		t.Fatalf("Failed to initialize default roles: %v", err)
	}

	gateway := NewGateway(
		authService,
		rbacService,
		rateLimiter,
		queueInstance,
		rate.DefaultConfig(),
	)

	// Setup mock Raft manager
	mockRM := &mockRaftManager{
		peers:    map[string]string{"node-1": "127.0.0.1:7000"},
		leader:   "node-1",
		isLeader: true,
	}
	gateway.SetRaftManager(mockRM)

	return gateway, mockRM
}

func TestHandleClusterPeers_List(t *testing.T) {
	gateway, _ := setupTestGateway(t)

	// Create admin user and token
	user, err := gateway.authService.CreateUser("admin", "password123")
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}
	if err := gateway.rbacService.AssignRole(user.ID, "admin"); err != nil {
		t.Fatalf("Failed to assign admin role: %v", err)
	}
	token, err := gateway.authService.CreateToken(user.ID, 3600*1000000000)
	if err != nil {
		t.Fatalf("Failed to create token: %v", err)
	}

	// Create request
	req := httptest.NewRequest(http.MethodGet, "/admin/cluster/peers", nil)
	req.Header.Set("Authorization", "Bearer "+token.TokenHash)
	rec := httptest.NewRecorder()

	// Setup routes
	mux := http.NewServeMux()
	gateway.RegisterRoutes(mux)

	// Handle request
	mux.ServeHTTP(rec, req)

	// Check response
	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	peers, ok := resp["peers"].([]interface{})
	if !ok {
		t.Errorf("Expected peers array in response")
	}
	if len(peers) != 1 {
		t.Errorf("Expected 1 peer, got %d", len(peers))
	}
}

func TestHandleClusterPeers_Add(t *testing.T) {
	gateway, mockRM := setupTestGateway(t)

	// Create admin user and token
	user, _ := gateway.authService.CreateUser("admin", "password123")
	gateway.rbacService.AssignRole(user.ID, "admin")
	token, _ := gateway.authService.CreateToken(user.ID, 3600*1000000000)

	// Create request
	reqBody := map[string]string{
		"id":      "node-2",
		"address": "127.0.0.1:7001",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/admin/cluster/peers", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token.TokenHash)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	// Setup routes
	mux := http.NewServeMux()
	gateway.RegisterRoutes(mux)

	// Handle request
	mux.ServeHTTP(rec, req)

	// Check response
	if rec.Code != http.StatusCreated {
		t.Errorf("Expected status 201, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify peer was added
	if _, exists := mockRM.peers["node-2"]; !exists {
		t.Errorf("Peer was not added to cluster")
	}
}

func TestHandleClusterPeers_Remove(t *testing.T) {
	gateway, mockRM := setupTestGateway(t)
	mockRM.peers["node-2"] = "127.0.0.1:7001"

	// Create admin user and token
	user, _ := gateway.authService.CreateUser("admin", "password123")
	gateway.rbacService.AssignRole(user.ID, "admin")
	token, _ := gateway.authService.CreateToken(user.ID, 3600*1000000000)

	// Create request
	reqBody := map[string]string{"id": "node-2"}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodDelete, "/admin/cluster/peers", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token.TokenHash)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	// Setup routes
	mux := http.NewServeMux()
	gateway.RegisterRoutes(mux)

	// Handle request
	mux.ServeHTTP(rec, req)

	// Check response
	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	// Verify peer was removed
	if _, exists := mockRM.peers["node-2"]; exists {
		t.Errorf("Peer was not removed from cluster")
	}
}

func TestHandleClusterPeers_NonLeader(t *testing.T) {
	gateway, mockRM := setupTestGateway(t)
	mockRM.isLeader = false
	mockRM.leader = "node-2"

	// Create admin user and token
	user, _ := gateway.authService.CreateUser("admin", "password123")
	gateway.rbacService.AssignRole(user.ID, "admin")
	token, _ := gateway.authService.CreateToken(user.ID, 3600*1000000000)

	// Try to add peer as non-leader
	reqBody := map[string]string{
		"id":      "node-3",
		"address": "127.0.0.1:7002",
	}
	body, _ := json.Marshal(reqBody)
	req := httptest.NewRequest(http.MethodPost, "/admin/cluster/peers", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token.TokenHash)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	// Setup routes
	mux := http.NewServeMux()
	gateway.RegisterRoutes(mux)

	// Handle request
	mux.ServeHTTP(rec, req)

	// Should get redirect response
	if rec.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", rec.Code)
	}

	var resp map[string]interface{}
	json.NewDecoder(rec.Body).Decode(&resp)
	if resp["error"] != "Not the leader" {
		t.Errorf("Expected 'Not the leader' error")
	}
	if resp["leader"] != "node-2" {
		t.Errorf("Expected leader to be node-2, got %v", resp["leader"])
	}
}

func TestHandleClusterStatus(t *testing.T) {
	gateway, mockRM := setupTestGateway(t)
	mockRM.peers["node-2"] = "127.0.0.1:7001"
	mockRM.peers["node-3"] = "127.0.0.1:7002"

	// Create admin user and token
	user, _ := gateway.authService.CreateUser("admin", "password123")
	gateway.rbacService.AssignRole(user.ID, "admin")
	token, _ := gateway.authService.CreateToken(user.ID, 3600*1000000000)

	// Create request
	req := httptest.NewRequest(http.MethodGet, "/admin/cluster/status", nil)
	req.Header.Set("Authorization", "Bearer "+token.TokenHash)
	rec := httptest.NewRecorder()

	// Setup routes
	mux := http.NewServeMux()
	gateway.RegisterRoutes(mux)

	// Handle request
	mux.ServeHTTP(rec, req)

	// Check response
	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", rec.Code)
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Check node information
	node := resp["node"].(map[string]interface{})
	if node["id"] != "node-1" {
		t.Errorf("Expected node ID to be node-1, got %v", node["id"])
	}
	if node["state"] != "Leader" {
		t.Errorf("Expected node state to be Leader, got %v", node["state"])
	}

	// Check leader information
	leader := resp["leader"].(map[string]interface{})
	if leader["id"] != "node-1" {
		t.Errorf("Expected leader ID to be node-1, got %v", leader["id"])
	}
	if leader["is_leader"] != true {
		t.Errorf("Expected is_leader to be true")
	}

	// Check indices
	indices := resp["indices"].(map[string]interface{})
	if indices["commit_index"] == nil {
		t.Errorf("Expected commit_index to be present")
	}

	// Check sessions
	sessions := resp["sessions"].(map[string]interface{})
	if sessions["active_count"] == nil {
		t.Errorf("Expected active_count to be present")
	}

	// Check TLS status
	tls := resp["tls"].(map[string]interface{})
	if tls["enabled"] != false {
		t.Errorf("Expected TLS to be disabled")
	}

	// Check uptime
	if resp["uptime"] == nil {
		t.Errorf("Expected uptime to be present")
	}
}

func TestHandleClusterLatency_GetAll(t *testing.T) {
	gateway, _ := setupTestGateway(t)

	// Create admin user and token
	user, err := gateway.authService.CreateUser("admin", "password123")
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}
	if err := gateway.rbacService.AssignRole(user.ID, "admin"); err != nil {
		t.Fatalf("Failed to assign admin role: %v", err)
	}
	token, err := gateway.authService.CreateToken(user.ID, 3600*1000000000)
	if err != nil {
		t.Fatalf("Failed to create token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/cluster/latency", nil)
	req.Header.Set("Authorization", "Bearer "+token.TokenHash)
	rec := httptest.NewRecorder()

	mux := http.NewServeMux()
	gateway.RegisterRoutes(mux)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Check that response has required fields
	if resp["peers"] == nil {
		t.Error("Expected 'peers' field in response")
	}
	if resp["aggregated"] == nil {
		t.Error("Expected 'aggregated' field in response")
	}
	if resp["health_summary"] == nil {
		t.Error("Expected 'health_summary' field in response")
	}
}

func TestHandleClusterLatency_GetSpecificPeer(t *testing.T) {
	gateway, _ := setupTestGateway(t)

	// Create admin user and token
	user, err := gateway.authService.CreateUser("admin", "password123")
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}
	if err := gateway.rbacService.AssignRole(user.ID, "admin"); err != nil {
		t.Fatalf("Failed to assign admin role: %v", err)
	}
	token, err := gateway.authService.CreateToken(user.ID, 3600*1000000000)
	if err != nil {
		t.Fatalf("Failed to create token: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/admin/cluster/latency?peer_id=node-1", nil)
	req.Header.Set("Authorization", "Bearer "+token.TokenHash)
	rec := httptest.NewRecorder()

	mux := http.NewServeMux()
	gateway.RegisterRoutes(mux)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	// Check peer_id is in response
	if resp["peer_id"] != "node-1" {
		t.Errorf("Expected peer_id 'node-1', got %v", resp["peer_id"])
	}
}

func TestHandleClusterLatencyReset(t *testing.T) {
	gateway, _ := setupTestGateway(t)

	// Create admin user and token
	user, err := gateway.authService.CreateUser("admin", "password123")
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}
	if err := gateway.rbacService.AssignRole(user.ID, "admin"); err != nil {
		t.Fatalf("Failed to assign admin role: %v", err)
	}
	token, err := gateway.authService.CreateToken(user.ID, 3600*1000000000)
	if err != nil {
		t.Fatalf("Failed to create token: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/admin/cluster/latency/reset", nil)
	req.Header.Set("Authorization", "Bearer "+token.TokenHash)
	rec := httptest.NewRecorder()

	mux := http.NewServeMux()
	gateway.RegisterRoutes(mux)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]interface{}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if resp["message"] != "Peer latency metrics reset successfully" {
		t.Errorf("Unexpected message: %v", resp["message"])
	}
}

func TestHandleClusterLatency_MethodNotAllowed(t *testing.T) {
	gateway, _ := setupTestGateway(t)

	// Create admin user and token
	user, err := gateway.authService.CreateUser("admin", "password123")
	if err != nil {
		t.Fatalf("Failed to create user: %v", err)
	}
	if err := gateway.rbacService.AssignRole(user.ID, "admin"); err != nil {
		t.Fatalf("Failed to assign admin role: %v", err)
	}
	token, err := gateway.authService.CreateToken(user.ID, 3600*1000000000)
	if err != nil {
		t.Fatalf("Failed to create token: %v", err)
	}

	mux := http.NewServeMux()
	gateway.RegisterRoutes(mux)

	// Test POST on GET-only endpoint
	req := httptest.NewRequest(http.MethodPost, "/admin/cluster/latency", nil)
	req.Header.Set("Authorization", "Bearer "+token.TokenHash)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", rec.Code)
	}

	// Test GET on POST-only endpoint
	req = httptest.NewRequest(http.MethodGet, "/admin/cluster/latency/reset", nil)
	req.Header.Set("Authorization", "Bearer "+token.TokenHash)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("Expected status 405, got %d", rec.Code)
	}
}
