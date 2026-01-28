package protocol

import (
	"fmt"
	"testing"
)

func TestCommandParsing(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:    "Valid SET command",
			input:   `{"cmd":"SET","key":"foo","value":"bar","ttl":0}`,
			wantErr: false,
		},
		{
			name:    "Valid GET command",
			input:   `{"cmd":"GET","key":"foo"}`,
			wantErr: false,
		},
		{
			name:    "Valid DEL command",
			input:   `{"cmd":"DEL","key":"foo"}`,
			wantErr: false,
		},
		{
			name:    "Empty string",
			input:   "",
			wantErr: true,
		},
		{
			name:    "Invalid JSON",
			input:   `{invalid}`,
			wantErr: true,
		},
		{
			name:    "Missing cmd field",
			input:   `{"key":"foo"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := Parse(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && cmd == nil {
				t.Error("Parse() returned nil command")
			}
		})
	}
}

func TestCommandSerialization(t *testing.T) {
	cmd := &Command{
		Type:  "SET",
		Key:   "testkey",
		Value: "testvalue",
		TTL:   60,
	}

	serialized, err := Serialize(cmd)
	if err != nil {
		t.Fatalf("Serialize() error = %v", err)
	}

	// Parse it back
	parsed, err := Parse(serialized)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if parsed.Type != cmd.Type {
		t.Errorf("Type mismatch: got %s, want %s", parsed.Type, cmd.Type)
	}
	if parsed.Key != cmd.Key {
		t.Errorf("Key mismatch: got %s, want %s", parsed.Key, cmd.Key)
	}
	if parsed.TTL != cmd.TTL {
		t.Errorf("TTL mismatch: got %d, want %d", parsed.TTL, cmd.TTL)
	}
}

func TestCommandConstructors(t *testing.T) {
	t.Run("NewSetCommand", func(t *testing.T) {
		cmd := NewSetCommand("key1", []byte("value1"), 30)
		if cmd.Type != "SET" {
			t.Errorf("Expected type SET, got %s", cmd.Type)
		}
		if cmd.Key != "key1" {
			t.Errorf("Expected key key1, got %s", cmd.Key)
		}
		if cmd.TTL != 30 {
			t.Errorf("Expected TTL 30, got %d", cmd.TTL)
		}
	})

	t.Run("NewGetCommand", func(t *testing.T) {
		cmd := NewGetCommand("key1")
		if cmd.Type != "GET" {
			t.Errorf("Expected type GET, got %s", cmd.Type)
		}
		if cmd.Key != "key1" {
			t.Errorf("Expected key key1, got %s", cmd.Key)
		}
	})

	t.Run("NewDelCommand", func(t *testing.T) {
		cmd := NewDelCommand("key1")
		if cmd.Type != "DEL" {
			t.Errorf("Expected type DEL, got %s", cmd.Type)
		}
		if cmd.Key != "key1" {
			t.Errorf("Expected key key1, got %s", cmd.Key)
		}
	})
}

func TestResponseSerialization(t *testing.T) {
	resp := NewSuccessResponse()
	serialized, err := SerializeResponse(resp)
	if err != nil {
		t.Fatalf("SerializeResponse() error = %v", err)
	}

	if serialized == "" {
		t.Error("Expected non-empty serialized response")
	}
}

func TestResponseConstructors(t *testing.T) {
	t.Run("NewSuccessResponse", func(t *testing.T) {
		resp := NewSuccessResponse()
		if !resp.OK {
			t.Error("Expected OK to be true")
		}
	})

	t.Run("NewValueResponse", func(t *testing.T) {
		resp := NewValueResponse([]byte("testvalue"))
		if !resp.OK {
			t.Error("Expected OK to be true")
		}
		if resp.Value == "" {
			t.Error("Expected non-empty value")
		}
	})

	t.Run("NewErrorResponse", func(t *testing.T) {
		err := fmt.Errorf("test error")
		resp := NewErrorResponse(err)
		if resp.OK {
			t.Error("Expected OK to be false")
		}
		if resp.Error != "test error" {
			t.Errorf("Expected error message 'test error', got '%s'", resp.Error)
		}
	})

	t.Run("NewTTLResponse", func(t *testing.T) {
		resp := NewTTLResponse(120)
		if !resp.OK {
			t.Error("Expected OK to be true")
		}
		if resp.TTL != 120 {
			t.Errorf("Expected TTL 120, got %d", resp.TTL)
		}
	})
}
