package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"time"

	"github.com/gorilla/websocket"
)

// WSMessage represents a WebSocket message
type WSMessage struct {
	Type      string          `json:"type"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// AuthResponse represents the authentication response
type AuthResponse struct {
	Token string `json:"token"`
}

var (
	apiURL   = flag.String("api", "http://localhost:8080", "API base URL")
	username = flag.String("user", "admin", "Username for authentication")
	password = flag.String("pass", "admin123", "Password for authentication")
	verbose  = flag.Bool("v", false, "Verbose output")
)

func main() {
	flag.Parse()

	// Authenticate and get token
	token, err := authenticate(*apiURL, *username, *password)
	if err != nil {
		log.Fatalf("Authentication failed: %v", err)
	}
	log.Println("✓ Authenticated successfully")

	// Connect to WebSocket
	wsURL := getWebSocketURL(*apiURL, token)
	log.Printf("Connecting to WebSocket: %s", wsURL)

	c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		log.Fatalf("WebSocket connection failed: %v", err)
	}
	defer c.Close()
	log.Println("✓ WebSocket connected")

	// Handle interrupt signal
	interrupt := make(chan os.Signal, 1)
	signal.Notify(interrupt, os.Interrupt)

	// Channel for receiving messages
	done := make(chan struct{})

	// Start reading messages
	go func() {
		defer close(done)
		for {
			var msg WSMessage
			err := c.ReadJSON(&msg)
			if err != nil {
				log.Println("Read error:", err)
				return
			}
			handleMessage(&msg)
		}
	}()

	// Send ping periodically
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			log.Println("Connection closed")
			return
		case <-ticker.C:
			if *verbose {
				log.Println("Sending ping...")
			}
			pingMsg := WSMessage{
				Type:      "ping",
				Timestamp: time.Now(),
			}
			if err := c.WriteJSON(pingMsg); err != nil {
				log.Println("Write error:", err)
				return
			}
		case <-interrupt:
			log.Println("Interrupt received, closing connection...")

			// Send close message
			err := c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
			if err != nil {
				log.Println("Write close error:", err)
				return
			}

			select {
			case <-done:
			case <-time.After(time.Second):
			}
			return
		}
	}
}

func authenticate(baseURL, username, password string) (string, error) {
	loginURL := fmt.Sprintf("%s/api/auth/login", baseURL)

	payload := map[string]string{
		"username": username,
		"password": password,
	}
	jsonData, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	resp, err := http.Post(loginURL, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("authentication failed with status: %d", resp.StatusCode)
	}

	var authResp AuthResponse
	if err := json.NewDecoder(resp.Body).Decode(&authResp); err != nil {
		return "", err
	}

	return authResp.Token, nil
}

func getWebSocketURL(baseURL, token string) string {
	u, err := url.Parse(baseURL)
	if err != nil {
		log.Fatal(err)
	}

	// Convert http/https to ws/wss
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}

	u.Path = "/ws/cluster"
	q := u.Query()
	q.Set("token", token)
	u.RawQuery = q.Encode()

	return u.String()
}

func handleMessage(msg *WSMessage) {
	timestamp := msg.Timestamp.Format("15:04:05")

	switch msg.Type {
	case "status", "status_update":
		if *verbose {
			var data map[string]interface{}
			if err := json.Unmarshal(msg.Data, &data); err == nil {
				log.Printf("[%s] 📊 Status Update: Node=%s, State=%s, Term=%v",
					timestamp,
					data["node_id"],
					data["state"],
					data["term"])
			}
		}

	case "state_change":
		var data map[string]interface{}
		if err := json.Unmarshal(msg.Data, &data); err == nil {
			log.Printf("[%s] 🔄 State Change: %s → %s (Term: %v)",
				timestamp,
				data["old_state"],
				data["new_state"],
				data["term"])
		}

	case "peer_change":
		var data map[string]interface{}
		if err := json.Unmarshal(msg.Data, &data); err == nil {
			log.Printf("[%s] 👥 Peer Change: %s %s",
				timestamp,
				data["event_type"],
				data["peer_id"])
		}

	case "latency":
		log.Printf("[%s] ⏱️  Latency Update", timestamp)
		if *verbose {
			printJSON(msg.Data)
		}

	case "uptime":
		var data map[string]interface{}
		if err := json.Unmarshal(msg.Data, &data); err == nil {
			log.Printf("[%s] ⏰ Uptime: %s", timestamp, data["current_uptime"])
		}

	case "snapshot":
		var data map[string]interface{}
		if err := json.Unmarshal(msg.Data, &data); err == nil {
			log.Printf("[%s] 📸 Snapshot: Index=%v, Term=%v",
				timestamp,
				data["index"],
				data["term"])
		}

	case "custom_metrics":
		log.Printf("[%s] 📈 Custom Metrics Updated", timestamp)
		if *verbose {
			printJSON(msg.Data)
		}

	case "error":
		var data map[string]interface{}
		if err := json.Unmarshal(msg.Data, &data); err == nil {
			log.Printf("[%s] ❌ Error: %s", timestamp, data["error"])
		}

	case "pong":
		if *verbose {
			log.Printf("[%s] 🏓 Pong received", timestamp)
		}

	default:
		if *verbose {
			log.Printf("[%s] Unknown message type: %s", timestamp, msg.Type)
		}
	}
}

func printJSON(data json.RawMessage) {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, data, "", "  "); err == nil {
		fmt.Println(pretty.String())
	}
}
