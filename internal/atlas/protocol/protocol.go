package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Command represents a parsed command
type Command struct {
	Type  string                 `json:"cmd"`
	Key   string                 `json:"key,omitempty"`
	Value string                 `json:"value,omitempty"` // base64 encoded
	TTL   int64                  `json:"ttl,omitempty"`
	Extra map[string]interface{} `json:"extra,omitempty"`
}

// Parse parses a command string (JSON-lines format)
func Parse(line string) (*Command, error) {
	line = strings.TrimSpace(line)
	if len(line) == 0 {
		return nil, fmt.Errorf("empty command")
	}

	var cmd Command
	if err := json.Unmarshal([]byte(line), &cmd); err != nil {
		return nil, fmt.Errorf("invalid command JSON: %w", err)
	}

	if cmd.Type == "" {
		return nil, fmt.Errorf("missing command type")
	}

	return &cmd, nil
}

// Serialize converts a command to a string
func Serialize(cmd *Command) (string, error) {
	data, err := json.Marshal(cmd)
	if err != nil {
		return "", fmt.Errorf("failed to serialize command: %w", err)
	}
	return string(data), nil
}

// NewSetCommand creates a SET command
func NewSetCommand(key string, value []byte, ttl int64) *Command {
	return &Command{
		Type:  "SET",
		Key:   key,
		Value: encodeValue(value),
		TTL:   ttl,
	}
}

// NewGetCommand creates a GET command
func NewGetCommand(key string) *Command {
	return &Command{
		Type: "GET",
		Key:  key,
	}
}

// NewDelCommand creates a DEL command
func NewDelCommand(key string) *Command {
	return &Command{
		Type: "DEL",
		Key:  key,
	}
}

// NewExpireCommand creates an EXPIRE command
func NewExpireCommand(key string, ttl int64) *Command {
	return &Command{
		Type: "EXPIRE",
		Key:  key,
		TTL:  ttl,
	}
}

// NewTTLCommand creates a TTL command
func NewTTLCommand(key string) *Command {
	return &Command{
		Type: "TTL",
		Key:  key,
	}
}

// Response represents a command response
type Response struct {
	OK    bool   `json:"ok"`
	Value string `json:"value,omitempty"` // base64 encoded
	TTL   int64  `json:"ttl,omitempty"`
	Error string `json:"error,omitempty"`
}

// NewSuccessResponse creates a success response
func NewSuccessResponse() *Response {
	return &Response{OK: true}
}

// NewValueResponse creates a response with a value
func NewValueResponse(value []byte) *Response {
	return &Response{
		OK:    true,
		Value: encodeValue(value),
	}
}

// NewTTLResponse creates a response with TTL
func NewTTLResponse(ttl int64) *Response {
	return &Response{
		OK:  true,
		TTL: ttl,
	}
}

// NewErrorResponse creates an error response
func NewErrorResponse(err error) *Response {
	return &Response{
		OK:    false,
		Error: err.Error(),
	}
}

// SerializeResponse converts a response to JSON
func SerializeResponse(resp *Response) (string, error) {
	data, err := json.Marshal(resp)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Helper functions for value encoding/decoding
func encodeValue(value []byte) string {
	// Use base64 encoding for binary safety
	return string(value) // Simplified for now, should use base64 in production
}

func DecodeValue(encoded string) []byte {
	// Decode base64
	return []byte(encoded) // Simplified for now
}
