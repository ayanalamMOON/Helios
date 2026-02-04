# WebSocket Real-Time Updates Guide

This guide demonstrates how to use WebSocket connections to receive real-time updates from the Helios cluster.

## Table of Contents
- [Overview](#overview)
- [Authentication](#authentication)
- [Connection Setup](#connection-setup)
- [Message Types](#message-types)
- [Subscription Management](#subscription-management)
- [Examples](#examples)

## Overview

The Helios API Gateway provides WebSocket support for real-time cluster status updates. WebSocket connections allow clients to receive immediate notifications when:

- Cluster state changes (Leader/Follower/Candidate)
- Peers are added or removed
- Custom metrics are updated
- Latency statistics change
- Uptime events occur
- Snapshots are created

## Authentication

WebSocket connections require authentication via bearer token. The token can be provided in two ways:

1. **Query Parameter**: `?token=<your-token>`
2. **Authorization Header**: `Authorization: Bearer <your-token>`

The user must have **admin** permissions to connect to the WebSocket endpoint.

## Connection Setup

### JavaScript Client

```javascript
// Generate token first via REST API
const loginResponse = await fetch('http://localhost:8080/api/auth/login', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({
        username: 'admin',
        password: 'admin123'
    })
});
const { token } = await loginResponse.json();

// Connect to WebSocket with token
const ws = new WebSocket(`ws://localhost:8080/ws/cluster?token=${token}`);

// Handle connection open
ws.onopen = () => {
    console.log('WebSocket connected');
};

// Handle incoming messages
ws.onmessage = (event) => {
    const message = JSON.parse(event.data);
    console.log('Received:', message.type, message.data);
};

// Handle errors
ws.onerror = (error) => {
    console.error('WebSocket error:', error);
};

// Handle connection close
ws.onclose = () => {
    console.log('WebSocket disconnected');
};
```

### Go Client

```go
package main

import (
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "net/url"

    "github.com/gorilla/websocket"
)

type WSMessage struct {
    Type      string          `json:"type"`
    Timestamp string          `json:"timestamp"`
    Data      json.RawMessage `json:"data,omitempty"`
}

func main() {
    // Get authentication token
    token := getAuthToken()

    // Connect to WebSocket
    u := url.URL{Scheme: "ws", Host: "localhost:8080", Path: "/ws/cluster"}
    q := u.Query()
    q.Set("token", token)
    u.RawQuery = q.Encode()

    c, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
    if err != nil {
        log.Fatal("dial:", err)
    }
    defer c.Close()

    // Read messages
    for {
        var msg WSMessage
        err := c.ReadJSON(&msg)
        if err != nil {
            log.Println("read:", err)
            return
        }

        fmt.Printf("Type: %s, Data: %s\n", msg.Type, string(msg.Data))
    }
}

func getAuthToken() string {
    // Implement authentication
    // Return JWT token
    return "your-jwt-token"
}
```

### Python Client

```python
import asyncio
import websockets
import json
import requests

async def connect_websocket():
    # Get authentication token
    response = requests.post('http://localhost:8080/api/auth/login', json={
        'username': 'admin',
        'password': 'admin123'
    })
    token = response.json()['token']

    # Connect to WebSocket
    uri = f"ws://localhost:8080/ws/cluster?token={token}"

    async with websockets.connect(uri) as websocket:
        print("Connected to WebSocket")

        # Receive messages
        async for message in websocket:
            data = json.loads(message)
            print(f"Type: {data['type']}, Data: {data.get('data')}")

# Run the client
asyncio.run(connect_websocket())
```

## Message Types

### Status Update
Sent automatically every 5 seconds with complete cluster status.

```json
{
    "type": "status",
    "timestamp": "2024-01-15T10:30:00Z",
    "data": {
        "node_id": "node1",
        "state": "Leader",
        "term": 5,
        "commit_index": 1000,
        "last_applied": 1000,
        "leader": "node1",
        "peers": ["node2", "node3"]
    }
}
```

### State Change
Sent when the Raft state changes (Follower → Leader, etc.).

```json
{
    "type": "state_change",
    "timestamp": "2024-01-15T10:31:00Z",
    "data": {
        "old_state": "Follower",
        "new_state": "Leader",
        "term": 6
    }
}
```

### Peer Change
Sent when peers are added or removed from the cluster.

```json
{
    "type": "peer_change",
    "timestamp": "2024-01-15T10:32:00Z",
    "data": {
        "event_type": "added",
        "peer_id": "node4",
        "peer_address": "localhost:8004"
    }
}
```

### Latency Update
Sent when latency statistics are available.

```json
{
    "type": "latency",
    "timestamp": "2024-01-15T10:33:00Z",
    "data": {
        "aggregated": {
            "append_entries": {
                "count": 1000,
                "avg_value_ms": 2.5,
                "min_value_ms": 1.0,
                "max_value_ms": 10.0
            }
        }
    }
}
```

### Uptime Update
Sent when uptime statistics change.

```json
{
    "type": "uptime",
    "timestamp": "2024-01-15T10:34:00Z",
    "data": {
        "current_uptime": "24h30m0s",
        "total_sessions": 5,
        "total_uptime": "120h15m30s"
    }
}
```

### Snapshot Update
Sent when a new snapshot is created.

```json
{
    "type": "snapshot",
    "timestamp": "2024-01-15T10:35:00Z",
    "data": {
        "index": 1000,
        "term": 5,
        "size_bytes": 1048576,
        "duration_ms": 150
    }
}
```

### Custom Metrics Update
Sent when custom metrics are created, updated, or deleted.

```json
{
    "type": "custom_metrics",
    "timestamp": "2024-01-15T10:36:00Z",
    "data": {
        "request_count": {
            "type": "counter",
            "help": "Total number of requests",
            "value": 1500
        }
    }
}
```

### Error
Sent when an error occurs.

```json
{
    "type": "error",
    "timestamp": "2024-01-15T10:37:00Z",
    "data": {
        "error": "Failed to retrieve cluster status"
    }
}
```

## Subscription Management

Clients can control which message types they want to receive by sending a subscription message.

### Subscribe to Specific Events

```javascript
// Subscribe only to state changes and peer changes
ws.send(JSON.stringify({
    type: 'subscribe',
    data: {
        status: false,
        latency: false,
        uptime: false,
        snapshot: false,
        custom_metrics: false,
        state_changes: true,
        peer_changes: true
    }
}));
```

### Unsubscribe from All Events

```javascript
ws.send(JSON.stringify({
    type: 'unsubscribe'
}));
```

### Default Subscription

By default, clients are subscribed to **all** message types.

## Examples

### React Dashboard Component

```jsx
import React, { useEffect, useState } from 'react';

function ClusterDashboard() {
    const [status, setStatus] = useState(null);
    const [events, setEvents] = useState([]);

    useEffect(() => {
        const token = localStorage.getItem('auth_token');
        const ws = new WebSocket(`ws://localhost:8080/ws/cluster?token=${token}`);

        ws.onmessage = (event) => {
            const message = JSON.parse(event.data);

            if (message.type === 'status') {
                setStatus(message.data);
            } else {
                // Log other events
                setEvents(prev => [...prev.slice(-9), message]);
            }
        };

        return () => ws.close();
    }, []);

    return (
        <div>
            <h2>Cluster Status</h2>
            {status && (
                <div>
                    <p>Node ID: {status.node_id}</p>
                    <p>State: {status.state}</p>
                    <p>Term: {status.term}</p>
                    <p>Leader: {status.leader}</p>
                </div>
            )}

            <h3>Recent Events</h3>
            <ul>
                {events.map((event, idx) => (
                    <li key={idx}>
                        {event.type} - {event.timestamp}
                    </li>
                ))}
            </ul>
        </div>
    );
}

export default ClusterDashboard;
```

### Monitoring Script

```bash
#!/bin/bash

# Simple bash script using websocat
# Install: brew install websocat (macOS) or cargo install websocat (Rust)

TOKEN=$(curl -s -X POST http://localhost:8080/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"admin123"}' | jq -r '.token')

echo "Connecting to WebSocket..."
websocat "ws://localhost:8080/ws/cluster?token=$TOKEN" | \
  while read -r line; do
    echo "$(date '+%Y-%m-%d %H:%M:%S') - $line"
  done
```

### Logging All Events

```go
package main

import (
    "encoding/json"
    "fmt"
    "log"
    "os"
    "time"

    "github.com/gorilla/websocket"
)

func main() {
    token := os.Getenv("AUTH_TOKEN")
    if token == "" {
        log.Fatal("AUTH_TOKEN environment variable required")
    }

    wsURL := fmt.Sprintf("ws://localhost:8080/ws/cluster?token=%s", token)
    c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
    if err != nil {
        log.Fatal("dial:", err)
    }
    defer c.Close()

    // Create log file
    file, err := os.Create(fmt.Sprintf("cluster-events-%d.log", time.Now().Unix()))
    if err != nil {
        log.Fatal(err)
    }
    defer file.Close()

    logger := log.New(file, "", log.LstdFlags)

    for {
        var msg map[string]interface{}
        err := c.ReadJSON(&msg)
        if err != nil {
            log.Println("read:", err)
            return
        }

        jsonData, _ := json.Marshal(msg)
        logger.Println(string(jsonData))
    }
}
```

## Best Practices

1. **Reconnection**: Implement automatic reconnection with exponential backoff
2. **Token Refresh**: Monitor token expiration and refresh before it expires
3. **Subscription**: Subscribe only to events you need to reduce bandwidth
4. **Error Handling**: Always handle connection errors and message parsing errors
5. **Heartbeat**: Monitor ping/pong messages to detect connection health

## Troubleshooting

### Connection Refused
- Verify the server is running and WebSocket endpoint is enabled
- Check network connectivity and firewall rules

### Authentication Failed
- Verify the token is valid and not expired
- Ensure the user has admin permissions
- Check token format (should not include "Bearer " prefix in query parameter)

### No Messages Received
- Check subscription settings
- Verify cluster is active and generating events
- Check for network issues or proxies that may interfere with WebSocket

### Connection Drops
- Implement reconnection logic
- Check server logs for errors
- Monitor network stability

## Security Considerations

1. Use **TLS** (wss://) in production environments
2. Store tokens securely (not in URL parameters for production)
3. Implement token rotation and refresh
4. Monitor for unauthorized connection attempts
5. Rate limit WebSocket connections
6. Validate all incoming subscription messages

## Performance Tips

1. Subscribe only to necessary events
2. Process messages asynchronously
3. Use message queues for high-throughput scenarios
4. Monitor connection and message latency
5. Implement connection pooling for multiple clients

## API Reference

### WebSocket Endpoint
```
ws://localhost:8080/ws/cluster
```

### Authentication Methods
- Query parameter: `?token=<jwt-token>`
- Authorization header: `Authorization: Bearer <jwt-token>`

### Client Message Types
- `ping`: Request health check
- `subscribe`: Update subscription preferences
- `unsubscribe`: Unsubscribe from all events

### Server Message Types
- `status`: Periodic cluster status (every 5 seconds)
- `status_update`: Manual status update
- `state_change`: Raft state transition
- `peer_change`: Peer added/removed
- `latency`: Latency statistics
- `uptime`: Uptime statistics
- `snapshot`: Snapshot created
- `custom_metrics`: Custom metrics updated
- `error`: Error occurred
- `pong`: Health check response
