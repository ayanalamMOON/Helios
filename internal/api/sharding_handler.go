package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/helios/helios/internal/sharding"
)

// ShardingHandler handles sharding-related API endpoints
type ShardingHandler struct {
	shardManager *sharding.ShardManager
}

// NewShardingHandler creates a new sharding handler
func NewShardingHandler(manager *sharding.ShardManager) *ShardingHandler {
	return &ShardingHandler{
		shardManager: manager,
	}
}

// HandleGetNodes returns all nodes in the cluster
func (h *ShardingHandler) HandleGetNodes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	nodes := h.shardManager.GetAllNodes()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"nodes": nodes,
		"count": len(nodes),
	})
}

// HandleAddNode adds a new node to the cluster
func (h *ShardingHandler) HandleAddNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		NodeID      string `json:"node_id"`
		Address     string `json:"address"`
		RaftEnabled bool   `json:"raft_enabled"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if req.NodeID == "" || req.Address == "" {
		http.Error(w, "node_id and address are required", http.StatusBadRequest)
		return
	}

	if err := h.shardManager.AddNode(req.NodeID, req.Address, req.RaftEnabled); err != nil {
		http.Error(w, fmt.Sprintf("Failed to add node: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Node %s added successfully", req.NodeID),
	})
}

// HandleRemoveNode removes a node from the cluster
func (h *ShardingHandler) HandleRemoveNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		NodeID string `json:"node_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if req.NodeID == "" {
		http.Error(w, "node_id is required", http.StatusBadRequest)
		return
	}

	if err := h.shardManager.RemoveNode(req.NodeID); err != nil {
		http.Error(w, fmt.Sprintf("Failed to remove node: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Node %s removed successfully", req.NodeID),
	})
}

// HandleGetKeyNode returns the node responsible for a key
func (h *ShardingHandler) HandleGetKeyNode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	key := r.URL.Query().Get("key")
	if key == "" {
		http.Error(w, "key parameter is required", http.StatusBadRequest)
		return
	}

	nodeID, err := h.shardManager.GetNodeForKey(key)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get node for key: %v", err), http.StatusInternalServerError)
		return
	}

	address, err := h.shardManager.GetNodeAddress(nodeID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to get node address: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"key":     key,
		"node_id": nodeID,
		"address": address,
	})
}

// HandleGetClusterStats returns cluster-wide statistics
func (h *ShardingHandler) HandleGetClusterStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	stats := h.shardManager.GetClusterStats()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// HandleMigrateKeys initiates a key migration task
func (h *ShardingHandler) HandleMigrateKeys(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SourceNode string `json:"source_node"`
		TargetNode string `json:"target_node"`
		KeyPattern string `json:"key_pattern"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if req.SourceNode == "" || req.TargetNode == "" {
		http.Error(w, "source_node and target_node are required", http.StatusBadRequest)
		return
	}

	if req.KeyPattern == "" {
		req.KeyPattern = "*" // Default to all keys
	}

	task, err := h.shardManager.CreateMigrationTask(req.SourceNode, req.TargetNode, req.KeyPattern)
	if err != nil {
		http.Error(w, fmt.Sprintf("Failed to create migration task: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"task":    task,
	})
}

// HandleGetMigrationTask returns the status of a migration task
func (h *ShardingHandler) HandleGetMigrationTask(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	taskID := r.URL.Query().Get("task_id")
	if taskID == "" {
		http.Error(w, "task_id parameter is required", http.StatusBadRequest)
		return
	}

	task, err := h.shardManager.GetMigrationTask(taskID)
	if err != nil {
		http.Error(w, fmt.Sprintf("Migration task not found: %v", err), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(task)
}

// HandleTriggerRebalance manually triggers cluster rebalancing
func (h *ShardingHandler) HandleTriggerRebalance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := h.shardManager.TriggerManualRebalance(); err != nil {
		http.Error(w, fmt.Sprintf("Failed to trigger rebalancing: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": "Rebalancing triggered successfully",
	})
}

// HandleGetActiveMigrations returns all active migrations
func (h *ShardingHandler) HandleGetActiveMigrations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	migrations := h.shardManager.GetActiveMigrations()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"migrations": migrations,
		"count":      len(migrations),
	})
}

// HandleGetAllMigrations returns all migrations
func (h *ShardingHandler) HandleGetAllMigrations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	migrations := h.shardManager.GetAllMigrations()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"migrations": migrations,
		"count":      len(migrations),
	})
}

// HandleCancelMigration cancels an active migration
func (h *ShardingHandler) HandleCancelMigration(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TaskID string `json:"task_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("Invalid request: %v", err), http.StatusBadRequest)
		return
	}

	if req.TaskID == "" {
		http.Error(w, "task_id is required", http.StatusBadRequest)
		return
	}

	if err := h.shardManager.CancelMigration(req.TaskID); err != nil {
		http.Error(w, fmt.Sprintf("Failed to cancel migration: %v", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"message": fmt.Sprintf("Migration %s cancelled successfully", req.TaskID),
	})
}

// HandleCleanupMigrations cleans up old completed migrations
func (h *ShardingHandler) HandleCleanupMigrations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		OlderThanHours int `json:"older_than_hours"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.OlderThanHours = 24 // Default to 24 hours
	}

	if req.OlderThanHours <= 0 {
		req.OlderThanHours = 24
	}

	olderThan := time.Duration(req.OlderThanHours) * time.Hour
	cleaned := h.shardManager.CleanupCompletedMigrations(olderThan)

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"cleaned": cleaned,
		"message": fmt.Sprintf("Cleaned up %d completed migrations", cleaned),
	})
}
