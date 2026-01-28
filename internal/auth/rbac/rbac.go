package rbac

import (
	"encoding/json"
	"fmt"
)

// Permission represents a permission string
type Permission string

// Common permissions
const (
	PermKVGet     Permission = "kv:get"
	PermKVSet     Permission = "kv:set"
	PermKVDelete  Permission = "kv:delete"
	PermJobCreate Permission = "job:create"
	PermJobView   Permission = "job:view"
	PermJobCancel Permission = "job:cancel"
	PermAdmin     Permission = "admin:*"
)

// Role represents a role with permissions
type Role struct {
	Name        string       `json:"name"`
	Permissions []Permission `json:"permissions"`
	Description string       `json:"description"`
}

// Store defines the interface for RBAC storage
type Store interface {
	Get(key string) ([]byte, bool)
	Set(key string, value []byte, ttl int64)
	Delete(key string) bool
}

// Service handles RBAC logic
type Service struct {
	store Store
}

// NewService creates a new RBAC service
func NewService(store Store) *Service {
	return &Service{
		store: store,
	}
}

// CreateRole creates a new role
func (s *Service) CreateRole(name string, permissions []Permission, description string) error {
	role := &Role{
		Name:        name,
		Permissions: permissions,
		Description: description,
	}

	roleKey := fmt.Sprintf("rbac:role:%s", name)
	data, err := json.Marshal(role)
	if err != nil {
		return err
	}

	s.store.Set(roleKey, data, 0)
	return nil
}

// GetRole retrieves a role by name
func (s *Service) GetRole(name string) (*Role, error) {
	roleKey := fmt.Sprintf("rbac:role:%s", name)
	data, exists := s.store.Get(roleKey)
	if !exists {
		return nil, fmt.Errorf("role not found")
	}

	var role Role
	if err := json.Unmarshal(data, &role); err != nil {
		return nil, err
	}

	return &role, nil
}

// DeleteRole deletes a role
func (s *Service) DeleteRole(name string) error {
	roleKey := fmt.Sprintf("rbac:role:%s", name)
	s.store.Delete(roleKey)
	return nil
}

// AssignRole assigns a role to a user
func (s *Service) AssignRole(userID, roleName string) error {
	// Get current roles
	roles := s.GetUserRoles(userID)

	// Check if already assigned
	for _, r := range roles {
		if r == roleName {
			return nil // already assigned
		}
	}

	// Add new role
	roles = append(roles, roleName)

	// Store
	rolesKey := fmt.Sprintf("rbac:user_roles:%s", userID)
	data, err := json.Marshal(roles)
	if err != nil {
		return err
	}

	s.store.Set(rolesKey, data, 0)
	return nil
}

// RevokeRole revokes a role from a user
func (s *Service) RevokeRole(userID, roleName string) error {
	roles := s.GetUserRoles(userID)

	// Remove role
	newRoles := make([]string, 0)
	for _, r := range roles {
		if r != roleName {
			newRoles = append(newRoles, r)
		}
	}

	// Store
	rolesKey := fmt.Sprintf("rbac:user_roles:%s", userID)
	data, err := json.Marshal(newRoles)
	if err != nil {
		return err
	}

	s.store.Set(rolesKey, data, 0)
	return nil
}

// GetUserRoles returns all roles for a user
func (s *Service) GetUserRoles(userID string) []string {
	rolesKey := fmt.Sprintf("rbac:user_roles:%s", userID)
	data, exists := s.store.Get(rolesKey)
	if !exists {
		return []string{}
	}

	var roles []string
	if err := json.Unmarshal(data, &roles); err != nil {
		return []string{}
	}

	return roles
}

// HasPermission checks if a user has a specific permission
func (s *Service) HasPermission(userID string, permission Permission) bool {
	roles := s.GetUserRoles(userID)

	for _, roleName := range roles {
		role, err := s.GetRole(roleName)
		if err != nil {
			continue
		}

		// Check for admin wildcard
		for _, p := range role.Permissions {
			if p == PermAdmin {
				return true
			}
			if p == permission {
				return true
			}
		}
	}

	return false
}

// InitializeDefaultRoles creates default roles
func (s *Service) InitializeDefaultRoles() error {
	// Admin role
	if err := s.CreateRole("admin", []Permission{PermAdmin}, "Administrator with all permissions"); err != nil {
		return err
	}

	// User role
	if err := s.CreateRole("user", []Permission{
		PermKVGet,
		PermKVSet,
		PermJobCreate,
		PermJobView,
	}, "Standard user"); err != nil {
		return err
	}

	// ReadOnly role
	if err := s.CreateRole("readonly", []Permission{
		PermKVGet,
		PermJobView,
	}, "Read-only access"); err != nil {
		return err
	}

	return nil
}
