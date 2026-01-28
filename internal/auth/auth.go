package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// User represents a user in the system
type User struct {
	ID           string                 `json:"id"`
	Username     string                 `json:"username"`
	PasswordHash string                 `json:"password_hash"`
	Salt         string                 `json:"salt"`
	CreatedAt    time.Time              `json:"created_at"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// Token represents an authentication token
type Token struct {
	UserID    string    `json:"user_id"`
	TokenHash string    `json:"token_hash"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// Store defines the interface for auth storage
type Store interface {
	Get(key string) ([]byte, bool)
	Set(key string, value []byte, ttl int64)
	Delete(key string) bool
}

// Service handles authentication logic
type Service struct {
	store      Store
	bcryptCost int
}

// NewService creates a new auth service
func NewService(store Store) *Service {
	return &Service{
		store:      store,
		bcryptCost: bcrypt.DefaultCost,
	}
}

// CreateUser creates a new user
func (s *Service) CreateUser(username, password string) (*User, error) {
	// Check if username exists
	usernameKey := fmt.Sprintf("auth:username:%s", username)
	if _, exists := s.store.Get(usernameKey); exists {
		return nil, fmt.Errorf("username already exists")
	}

	// Generate user ID
	userID := generateID()

	// Hash password
	passwordHash, err := bcrypt.GenerateFromPassword([]byte(password), s.bcryptCost)
	if err != nil {
		return nil, fmt.Errorf("failed to hash password: %w", err)
	}

	// Create user
	user := &User{
		ID:           userID,
		Username:     username,
		PasswordHash: string(passwordHash),
		CreatedAt:    time.Now(),
		Metadata:     make(map[string]interface{}),
	}

	// Store user
	userKey := fmt.Sprintf("auth:user:%s", userID)
	userData, err := json.Marshal(user)
	if err != nil {
		return nil, err
	}

	s.store.Set(userKey, userData, 0)
	s.store.Set(usernameKey, []byte(userID), 0)

	return user, nil
}

// GetUser retrieves a user by ID
func (s *Service) GetUser(userID string) (*User, error) {
	userKey := fmt.Sprintf("auth:user:%s", userID)
	data, exists := s.store.Get(userKey)
	if !exists {
		return nil, fmt.Errorf("user not found")
	}

	var user User
	if err := json.Unmarshal(data, &user); err != nil {
		return nil, err
	}

	return &user, nil
}

// GetUserByUsername retrieves a user by username
func (s *Service) GetUserByUsername(username string) (*User, error) {
	usernameKey := fmt.Sprintf("auth:username:%s", username)
	userIDData, exists := s.store.Get(usernameKey)
	if !exists {
		return nil, fmt.Errorf("user not found")
	}

	return s.GetUser(string(userIDData))
}

// ValidatePassword checks if a password is correct
func (s *Service) ValidatePassword(user *User, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password))
	return err == nil
}

// CreateToken creates a new authentication token
func (s *Service) CreateToken(userID string, duration time.Duration) (*Token, error) {
	// Generate token
	tokenStr := generateToken()
	tokenHash := hashToken(tokenStr)

	token := &Token{
		UserID:    userID,
		TokenHash: tokenHash,
		ExpiresAt: time.Now().Add(duration),
		CreatedAt: time.Now(),
	}

	// Store token
	tokenKey := fmt.Sprintf("auth:token:%s", tokenHash)
	tokenData, err := json.Marshal(token)
	if err != nil {
		return nil, err
	}

	ttl := int64(duration.Seconds())
	s.store.Set(tokenKey, tokenData, ttl)

	// Return token with plain text (only time it's visible)
	token.TokenHash = tokenStr
	return token, nil
}

// ValidateToken validates a token and returns the user ID
func (s *Service) ValidateToken(tokenStr string) (string, error) {
	tokenHash := hashToken(tokenStr)
	tokenKey := fmt.Sprintf("auth:token:%s", tokenHash)

	data, exists := s.store.Get(tokenKey)
	if !exists {
		return "", fmt.Errorf("invalid token")
	}

	var token Token
	if err := json.Unmarshal(data, &token); err != nil {
		return "", err
	}

	if time.Now().After(token.ExpiresAt) {
		s.store.Delete(tokenKey)
		return "", fmt.Errorf("token expired")
	}

	return token.UserID, nil
}

// RevokeToken revokes a token
func (s *Service) RevokeToken(tokenStr string) error {
	tokenHash := hashToken(tokenStr)
	tokenKey := fmt.Sprintf("auth:token:%s", tokenHash)
	s.store.Delete(tokenKey)
	return nil
}

// Helper functions

func generateID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func generateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
