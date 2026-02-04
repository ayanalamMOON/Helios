package api

import (
	"context"
	"encoding/json"
	"log"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// WSMessageType represents the type of WebSocket message
type WSMessageType string

const (
	WSMessageTypeStatus        WSMessageType = "status"
	WSMessageTypeStatusUpdate  WSMessageType = "status_update"
	WSMessageTypeLatency       WSMessageType = "latency"
	WSMessageTypeUptime        WSMessageType = "uptime"
	WSMessageTypeSnapshot      WSMessageType = "snapshot"
	WSMessageTypeCustomMetrics WSMessageType = "custom_metrics"
	WSMessageTypeStateChange   WSMessageType = "state_change"
	WSMessageTypePeerChange    WSMessageType = "peer_change"
	WSMessageTypeError         WSMessageType = "error"
	WSMessageTypePing          WSMessageType = "ping"
	WSMessageTypePong          WSMessageType = "pong"
	WSMessageTypeSubscribe     WSMessageType = "subscribe"
	WSMessageTypeUnsubscribe   WSMessageType = "unsubscribe"
)

// WSMessage represents a WebSocket message
type WSMessage struct {
	Type      WSMessageType   `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// WSSubscription represents a client's subscription preferences
type WSSubscription struct {
	Status        bool `json:"status"`
	Latency       bool `json:"latency"`
	Uptime        bool `json:"uptime"`
	Snapshot      bool `json:"snapshot"`
	CustomMetrics bool `json:"custom_metrics"`
	StateChanges  bool `json:"state_changes"`
	PeerChanges   bool `json:"peer_changes"`
}

// DefaultSubscription returns default subscription settings (all enabled)
func DefaultSubscription() *WSSubscription {
	return &WSSubscription{
		Status:        true,
		Latency:       true,
		Uptime:        true,
		Snapshot:      true,
		CustomMetrics: true,
		StateChanges:  true,
		PeerChanges:   true,
	}
}

// WSClient represents a connected WebSocket client
type WSClient struct {
	id           string
	conn         *websocket.Conn
	send         chan *WSMessage
	subscription *WSSubscription
	mu           sync.RWMutex
	userID       string // Authenticated user ID
	role         string // User role for authorization
}

// NewWSClient creates a new WebSocket client
func NewWSClient(id string, conn *websocket.Conn, userID, role string) *WSClient {
	return &WSClient{
		id:           id,
		conn:         conn,
		send:         make(chan *WSMessage, 256),
		subscription: DefaultSubscription(),
		userID:       userID,
		role:         role,
	}
}

// UpdateSubscription updates the client's subscription preferences
func (c *WSClient) UpdateSubscription(sub *WSSubscription) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.subscription = sub
}

// GetSubscription returns the client's current subscription
func (c *WSClient) GetSubscription() *WSSubscription {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.subscription
}

// IsSubscribedTo checks if the client is subscribed to a message type
func (c *WSClient) IsSubscribedTo(msgType WSMessageType) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	switch msgType {
	case WSMessageTypeStatus, WSMessageTypeStatusUpdate:
		return c.subscription.Status
	case WSMessageTypeLatency:
		return c.subscription.Latency
	case WSMessageTypeUptime:
		return c.subscription.Uptime
	case WSMessageTypeSnapshot:
		return c.subscription.Snapshot
	case WSMessageTypeCustomMetrics:
		return c.subscription.CustomMetrics
	case WSMessageTypeStateChange:
		return c.subscription.StateChanges
	case WSMessageTypePeerChange:
		return c.subscription.PeerChanges
	default:
		return true // Always send errors, pings, pongs
	}
}

// WSHub manages all WebSocket connections and broadcasting
type WSHub struct {
	clients    map[string]*WSClient
	broadcast  chan *WSMessage
	register   chan *WSClient
	unregister chan *WSClient
	mu         sync.RWMutex
	ctx        context.Context
	cancel     context.CancelFunc
	raftMgr    RaftManager
	logger     *log.Logger
}

// NewWSHub creates a new WebSocket hub
func NewWSHub(raftMgr RaftManager, logger *log.Logger) *WSHub {
	ctx, cancel := context.WithCancel(context.Background())
	return &WSHub{
		clients:    make(map[string]*WSClient),
		broadcast:  make(chan *WSMessage, 256),
		register:   make(chan *WSClient),
		unregister: make(chan *WSClient),
		ctx:        ctx,
		cancel:     cancel,
		raftMgr:    raftMgr,
		logger:     logger,
	}
}

// Start starts the WebSocket hub
func (h *WSHub) Start() {
	go h.run()
	go h.statusBroadcaster()
}

// Stop stops the WebSocket hub
func (h *WSHub) Stop() {
	h.cancel()

	// Close all client connections
	h.mu.Lock()
	for _, client := range h.clients {
		close(client.send)
		client.conn.Close()
	}
	h.mu.Unlock()
}

// run handles the main hub loop
func (h *WSHub) run() {
	for {
		select {
		case <-h.ctx.Done():
			return

		case client := <-h.register:
			h.mu.Lock()
			h.clients[client.id] = client
			h.mu.Unlock()

			h.logger.Printf("[WebSocket] Client registered: %s (user: %s, role: %s)",
				client.id, client.userID, client.role)

			// Send initial status
			go h.sendInitialStatus(client)

		case client := <-h.unregister:
			h.mu.Lock()
			if _, ok := h.clients[client.id]; ok {
				delete(h.clients, client.id)
				close(client.send)
				h.mu.Unlock()
				h.logger.Printf("[WebSocket] Client unregistered: %s", client.id)
			} else {
				h.mu.Unlock()
			}

		case message := <-h.broadcast:
			h.mu.RLock()
			for _, client := range h.clients {
				if client.IsSubscribedTo(message.Type) {
					select {
					case client.send <- message:
					default:
						// Client's send buffer is full, skip this message
						h.logger.Printf("[WebSocket] Client %s buffer full, skipping message", client.id)
					}
				}
			}
			h.mu.RUnlock()
		}
	}
}

// statusBroadcaster periodically broadcasts cluster status updates
func (h *WSHub) statusBroadcaster() {
	ticker := time.NewTicker(5 * time.Second) // Update every 5 seconds
	defer ticker.Stop()

	for {
		select {
		case <-h.ctx.Done():
			return

		case <-ticker.C:
			if h.raftMgr == nil {
				continue
			}

			// Get cluster status
			status, err := h.raftMgr.GetClusterStatus()
			if err != nil {
				h.logger.Printf("[WebSocket] Failed to get cluster status: %v", err)
				continue
			}

			// Marshal status to JSON
			data, err := json.Marshal(status)
			if err != nil {
				h.logger.Printf("[WebSocket] Failed to marshal status: %v", err)
				continue
			}

			// Broadcast status update
			msg := &WSMessage{
				Type:      WSMessageTypeStatusUpdate,
				Timestamp: time.Now(),
				Data:      data,
			}

			select {
			case h.broadcast <- msg:
			case <-h.ctx.Done():
				return
			}
		}
	}
}

// sendInitialStatus sends the initial cluster status to a newly connected client
func (h *WSHub) sendInitialStatus(client *WSClient) {
	if h.raftMgr == nil {
		return
	}

	// Get cluster status
	status, err := h.raftMgr.GetClusterStatus()
	if err != nil {
		h.logger.Printf("[WebSocket] Failed to get initial status for client %s: %v", client.id, err)
		return
	}

	// Marshal status to JSON
	data, err := json.Marshal(status)
	if err != nil {
		h.logger.Printf("[WebSocket] Failed to marshal initial status: %v", err)
		return
	}

	// Send initial status
	msg := &WSMessage{
		Type:      WSMessageTypeStatus,
		Timestamp: time.Now(),
		Data:      data,
	}

	select {
	case client.send <- msg:
	default:
		h.logger.Printf("[WebSocket] Failed to send initial status to client %s", client.id)
	}
}

// BroadcastStateChange broadcasts a Raft state change event
func (h *WSHub) BroadcastStateChange(oldState, newState string, term uint64) {
	data, _ := json.Marshal(map[string]interface{}{
		"old_state": oldState,
		"new_state": newState,
		"term":      term,
	})

	msg := &WSMessage{
		Type:      WSMessageTypeStateChange,
		Timestamp: time.Now(),
		Data:      data,
	}

	select {
	case h.broadcast <- msg:
	case <-h.ctx.Done():
	}
}

// BroadcastPeerChange broadcasts a peer addition/removal event
func (h *WSHub) BroadcastPeerChange(eventType, peerID, peerAddr string) {
	data, _ := json.Marshal(map[string]interface{}{
		"event":   eventType,
		"peer_id": peerID,
		"address": peerAddr,
	})

	msg := &WSMessage{
		Type:      WSMessageTypePeerChange,
		Timestamp: time.Now(),
		Data:      data,
	}

	select {
	case h.broadcast <- msg:
	case <-h.ctx.Done():
	}
}

// BroadcastLatencyUpdate broadcasts peer latency metrics
func (h *WSHub) BroadcastLatencyUpdate() {
	if h.raftMgr == nil {
		return
	}

	latencyStats := h.raftMgr.GetAllPeerLatencyStats()
	data, err := json.Marshal(latencyStats)
	if err != nil {
		return
	}

	msg := &WSMessage{
		Type:      WSMessageTypeLatency,
		Timestamp: time.Now(),
		Data:      data,
	}

	select {
	case h.broadcast <- msg:
	case <-h.ctx.Done():
	}
}

// BroadcastUptimeUpdate broadcasts uptime statistics
func (h *WSHub) BroadcastUptimeUpdate() {
	if h.raftMgr == nil {
		return
	}

	uptimeStats := h.raftMgr.GetUptimeStats()
	data, err := json.Marshal(uptimeStats)
	if err != nil {
		return
	}

	msg := &WSMessage{
		Type:      WSMessageTypeUptime,
		Timestamp: time.Now(),
		Data:      data,
	}

	select {
	case h.broadcast <- msg:
	case <-h.ctx.Done():
	}
}

// BroadcastSnapshotUpdate broadcasts snapshot statistics
func (h *WSHub) BroadcastSnapshotUpdate() {
	if h.raftMgr == nil {
		return
	}

	snapshotStats := h.raftMgr.GetSnapshotStats()
	data, err := json.Marshal(snapshotStats)
	if err != nil {
		return
	}

	msg := &WSMessage{
		Type:      WSMessageTypeSnapshot,
		Timestamp: time.Now(),
		Data:      data,
	}

	select {
	case h.broadcast <- msg:
	case <-h.ctx.Done():
	}
}

// BroadcastCustomMetricsUpdate broadcasts custom metrics
func (h *WSHub) BroadcastCustomMetricsUpdate() {
	if h.raftMgr == nil {
		return
	}

	metricsStats := h.raftMgr.GetCustomMetricsStats()
	if metricsStats == nil {
		return
	}

	data, err := json.Marshal(metricsStats)
	if err != nil {
		return
	}

	msg := &WSMessage{
		Type:      WSMessageTypeCustomMetrics,
		Timestamp: time.Now(),
		Data:      data,
	}

	select {
	case h.broadcast <- msg:
	case <-h.ctx.Done():
	}
}

// GetClientCount returns the number of connected clients
func (h *WSHub) GetClientCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}

// RegisterClient registers a new WebSocket client
func (h *WSHub) RegisterClient(client *WSClient) {
	h.register <- client
}

// UnregisterClient unregisters a WebSocket client
func (h *WSHub) UnregisterClient(client *WSClient) {
	h.unregister <- client
}

// ReadPump pumps messages from the WebSocket connection to the hub
func (c *WSClient) ReadPump(hub *WSHub) {
	defer func() {
		hub.UnregisterClient(c)
		c.conn.Close()
	}()

	c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		return nil
	})

	for {
		var msg WSMessage
		err := c.conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				hub.logger.Printf("[WebSocket] Unexpected close error: %v", err)
			}
			break
		}

		// Handle client messages
		c.handleMessage(hub, &msg)
	}
}

// WritePump pumps messages from the hub to the WebSocket connection
func (c *WSClient) WritePump() {
	ticker := time.NewTicker(30 * time.Second)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.conn.WriteJSON(message); err != nil {
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}

// handleMessage handles incoming messages from the client
func (c *WSClient) handleMessage(hub *WSHub, msg *WSMessage) {
	switch msg.Type {
	case WSMessageTypePing:
		// Respond with pong
		pong := &WSMessage{
			Type:      WSMessageTypePong,
			Timestamp: time.Now(),
		}
		select {
		case c.send <- pong:
		default:
		}

	case WSMessageTypeSubscribe:
		// Update subscription preferences
		var sub WSSubscription
		if err := json.Unmarshal(msg.Data, &sub); err == nil {
			c.UpdateSubscription(&sub)
			hub.logger.Printf("[WebSocket] Client %s updated subscription", c.id)
		}

	case WSMessageTypeUnsubscribe:
		// Unsubscribe from all
		c.UpdateSubscription(&WSSubscription{})
		hub.logger.Printf("[WebSocket] Client %s unsubscribed from all", c.id)
	}
}

// SendError sends an error message to the client
func (c *WSClient) SendError(errMsg string) {
	data, _ := json.Marshal(map[string]string{
		"error": errMsg,
	})

	msg := &WSMessage{
		Type:      WSMessageTypeError,
		Timestamp: time.Now(),
		Data:      data,
	}

	select {
	case c.send <- msg:
	default:
	}
}

// EventListener implementation for RaftAtlas

// OnStateChange implements atlas.EventListener interface
func (h *WSHub) OnStateChange(state string) {
	// Get current state from raftMgr
	if h.raftMgr == nil {
		return
	}

	status, err := h.raftMgr.GetClusterStatus()
	if err != nil {
		return
	}

	// Extract term from status if available
	var term uint64
	if statusMap, ok := status.(map[string]interface{}); ok {
		if termVal, ok := statusMap["term"].(uint64); ok {
			term = termVal
		}
	}

	// Broadcast state change
	h.BroadcastStateChange("", state, term)
}

// OnPeerChange implements atlas.EventListener interface
func (h *WSHub) OnPeerChange(peerID string, action string) {
	// Broadcast peer change
	h.BroadcastPeerChange(action, peerID, "")
}

// OnMetricsUpdate implements atlas.EventListener interface
func (h *WSHub) OnMetricsUpdate() {
	// Broadcast custom metrics update
	h.BroadcastCustomMetricsUpdate()
}
