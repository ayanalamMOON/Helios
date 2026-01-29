package raft

// FSM (Finite State Machine) is an interface that represents the application state machine
type FSM interface {
	// Apply is called when a log entry is committed
	// It should apply the command to the state machine and return a result
	Apply(command interface{}) interface{}

	// Snapshot creates a snapshot of the current state
	Snapshot() ([]byte, error)

	// Restore restores the state from a snapshot
	Restore(snapshot []byte) error
}

// MockFSM is a simple in-memory FSM for testing
type MockFSM struct {
	data map[string]string
}

// NewMockFSM creates a new mock FSM
func NewMockFSM() *MockFSM {
	return &MockFSM{
		data: make(map[string]string),
	}
}

// Apply applies a command to the FSM
func (m *MockFSM) Apply(command interface{}) interface{} {
	if cmd, ok := command.(map[string]interface{}); ok {
		op := cmd["op"].(string)
		key := cmd["key"].(string)

		switch op {
		case "set":
			value := cmd["value"].(string)
			m.data[key] = value
			return "OK"
		case "get":
			if val, exists := m.data[key]; exists {
				return val
			}
			return nil
		case "delete":
			delete(m.data, key)
			return "OK"
		}
	}

	return nil
}

// Snapshot creates a snapshot of the FSM
func (m *MockFSM) Snapshot() ([]byte, error) {
	// In a real implementation, this would serialize the state
	// For now, just return empty snapshot
	return []byte{}, nil
}

// Restore restores the FSM from a snapshot
func (m *MockFSM) Restore(snapshot []byte) error {
	// In a real implementation, this would deserialize and restore the state
	return nil
}
