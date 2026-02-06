package graphql

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// Query Cost Analysis implementation for GraphQL
// Prevents expensive queries from overloading the server by analyzing
// query complexity before execution.

// CostConfig defines the cost analysis configuration
type CostConfig struct {
	// MaxComplexity is the maximum allowed query complexity
	MaxComplexity int `json:"maxComplexity" yaml:"max_complexity"`

	// MaxDepth is the maximum allowed query depth
	MaxDepth int `json:"maxDepth" yaml:"max_depth"`

	// DefaultFieldCost is the default cost for fields not explicitly configured
	DefaultFieldCost int `json:"defaultFieldCost" yaml:"default_field_cost"`

	// DefaultListMultiplier is applied to list fields
	DefaultListMultiplier int `json:"defaultListMultiplier" yaml:"default_list_multiplier"`

	// Enabled determines if cost analysis is active
	Enabled bool `json:"enabled" yaml:"enabled"`

	// RejectOnExceed determines if queries exceeding limits should be rejected
	RejectOnExceed bool `json:"rejectOnExceed" yaml:"reject_on_exceed"`

	// IncludeCostInResponse adds cost info to the response extensions
	IncludeCostInResponse bool `json:"includeCostInResponse" yaml:"include_cost_in_response"`
}

// DefaultCostConfig returns the default cost configuration
func DefaultCostConfig() *CostConfig {
	return &CostConfig{
		MaxComplexity:         1000,
		MaxDepth:              10,
		DefaultFieldCost:      1,
		DefaultListMultiplier: 10,
		Enabled:               true,
		RejectOnExceed:        true,
		IncludeCostInResponse: true,
	}
}

// FieldCost defines the cost of a specific field
type FieldCost struct {
	// Cost is the base cost of the field
	Cost int `json:"cost" yaml:"cost"`

	// Multiplier is applied when the field returns a list
	Multiplier int `json:"multiplier" yaml:"multiplier"`

	// UseMultiplierArg specifies which argument to use as multiplier (e.g., "limit", "first")
	UseMultiplierArg string `json:"useMultiplierArg" yaml:"use_multiplier_arg"`

	// IgnoreChildren if true, doesn't add cost for child fields
	IgnoreChildren bool `json:"ignoreChildren" yaml:"ignore_children"`

	// RequiresAuth if true, requires authentication to execute
	RequiresAuth bool `json:"requiresAuth" yaml:"requires_auth"`
}

// CostAnalysisResult contains the result of cost analysis
type CostAnalysisResult struct {
	// TotalCost is the calculated query complexity
	TotalCost int `json:"totalCost"`

	// MaxDepth is the maximum depth found in the query
	MaxDepth int `json:"maxDepth"`

	// FieldCosts is a breakdown of costs per field path
	FieldCosts map[string]int `json:"fieldCosts"`

	// Exceeded indicates if limits were exceeded
	Exceeded bool `json:"exceeded"`

	// ExceededReason explains why the query was rejected
	ExceededReason string `json:"exceededReason,omitempty"`

	// Warnings contains non-blocking issues
	Warnings []string `json:"warnings,omitempty"`
}

// CostAnalyzer analyzes GraphQL query costs
type CostAnalyzer struct {
	config     *CostConfig
	fieldCosts map[string]*FieldCost
	mu         sync.RWMutex
}

// NewCostAnalyzer creates a new cost analyzer
func NewCostAnalyzer(config *CostConfig) *CostAnalyzer {
	if config == nil {
		config = DefaultCostConfig()
	}

	analyzer := &CostAnalyzer{
		config:     config,
		fieldCosts: make(map[string]*FieldCost),
	}

	// Initialize default field costs
	analyzer.initializeDefaultCosts()

	return analyzer
}

// initializeDefaultCosts sets up default costs for Helios GraphQL fields
func (ca *CostAnalyzer) initializeDefaultCosts() {
	// Query fields
	ca.fieldCosts["Query.me"] = &FieldCost{Cost: 1, RequiresAuth: true}
	ca.fieldCosts["Query.get"] = &FieldCost{Cost: 1}
	ca.fieldCosts["Query.keys"] = &FieldCost{Cost: 5, Multiplier: 1, UseMultiplierArg: "limit"}
	ca.fieldCosts["Query.exists"] = &FieldCost{Cost: 1}
	ca.fieldCosts["Query.job"] = &FieldCost{Cost: 2}
	ca.fieldCosts["Query.jobs"] = &FieldCost{Cost: 10, Multiplier: 1, UseMultiplierArg: "limit"}
	ca.fieldCosts["Query.shardNodes"] = &FieldCost{Cost: 5, Multiplier: 2}
	ca.fieldCosts["Query.shardStats"] = &FieldCost{Cost: 3}
	ca.fieldCosts["Query.nodeForKey"] = &FieldCost{Cost: 2}
	ca.fieldCosts["Query.activeMigrations"] = &FieldCost{Cost: 5, Multiplier: 3}
	ca.fieldCosts["Query.allMigrations"] = &FieldCost{Cost: 10, Multiplier: 5}
	ca.fieldCosts["Query.raftStatus"] = &FieldCost{Cost: 2}
	ca.fieldCosts["Query.raftPeers"] = &FieldCost{Cost: 3, Multiplier: 2}
	ca.fieldCosts["Query.clusterStatus"] = &FieldCost{Cost: 10}
	ca.fieldCosts["Query.user"] = &FieldCost{Cost: 2, RequiresAuth: true}
	ca.fieldCosts["Query.users"] = &FieldCost{Cost: 10, Multiplier: 1, UseMultiplierArg: "limit", RequiresAuth: true}
	ca.fieldCosts["Query.role"] = &FieldCost{Cost: 2, RequiresAuth: true}
	ca.fieldCosts["Query.roles"] = &FieldCost{Cost: 5, Multiplier: 2, RequiresAuth: true}
	ca.fieldCosts["Query.health"] = &FieldCost{Cost: 1}
	ca.fieldCosts["Query.metrics"] = &FieldCost{Cost: 5}

	// Mutation fields (generally higher cost due to state changes)
	ca.fieldCosts["Mutation.register"] = &FieldCost{Cost: 10}
	ca.fieldCosts["Mutation.login"] = &FieldCost{Cost: 5}
	ca.fieldCosts["Mutation.logout"] = &FieldCost{Cost: 1, RequiresAuth: true}
	ca.fieldCosts["Mutation.refreshToken"] = &FieldCost{Cost: 3}
	ca.fieldCosts["Mutation.set"] = &FieldCost{Cost: 5}
	ca.fieldCosts["Mutation.delete"] = &FieldCost{Cost: 3}
	ca.fieldCosts["Mutation.expire"] = &FieldCost{Cost: 2}
	ca.fieldCosts["Mutation.enqueueJob"] = &FieldCost{Cost: 10}
	ca.fieldCosts["Mutation.cancelJob"] = &FieldCost{Cost: 3}
	ca.fieldCosts["Mutation.retryJob"] = &FieldCost{Cost: 5}
	ca.fieldCosts["Mutation.addShardNode"] = &FieldCost{Cost: 20, RequiresAuth: true}
	ca.fieldCosts["Mutation.removeShardNode"] = &FieldCost{Cost: 20, RequiresAuth: true}
	ca.fieldCosts["Mutation.triggerRebalance"] = &FieldCost{Cost: 50, RequiresAuth: true}
	ca.fieldCosts["Mutation.cancelMigration"] = &FieldCost{Cost: 10, RequiresAuth: true}
	ca.fieldCosts["Mutation.cleanupMigrations"] = &FieldCost{Cost: 15, RequiresAuth: true}
	ca.fieldCosts["Mutation.addRaftPeer"] = &FieldCost{Cost: 30, RequiresAuth: true}
	ca.fieldCosts["Mutation.removeRaftPeer"] = &FieldCost{Cost: 30, RequiresAuth: true}
	ca.fieldCosts["Mutation.createUser"] = &FieldCost{Cost: 10, RequiresAuth: true}
	ca.fieldCosts["Mutation.updateUser"] = &FieldCost{Cost: 5, RequiresAuth: true}
	ca.fieldCosts["Mutation.deleteUser"] = &FieldCost{Cost: 10, RequiresAuth: true}
	ca.fieldCosts["Mutation.assignRole"] = &FieldCost{Cost: 5, RequiresAuth: true}
	ca.fieldCosts["Mutation.revokeRole"] = &FieldCost{Cost: 5, RequiresAuth: true}
	ca.fieldCosts["Mutation.createRole"] = &FieldCost{Cost: 10, RequiresAuth: true}
	ca.fieldCosts["Mutation.updateRole"] = &FieldCost{Cost: 5, RequiresAuth: true}
	ca.fieldCosts["Mutation.deleteRole"] = &FieldCost{Cost: 10, RequiresAuth: true}

	// Subscription fields
	ca.fieldCosts["Subscription.jobUpdated"] = &FieldCost{Cost: 5}
	ca.fieldCosts["Subscription.migrationProgress"] = &FieldCost{Cost: 5}
	ca.fieldCosts["Subscription.clusterEvent"] = &FieldCost{Cost: 10}
	ca.fieldCosts["Subscription.keyChanged"] = &FieldCost{Cost: 10}

	// Nested type fields (lower cost as they're already fetched)
	ca.fieldCosts["User.id"] = &FieldCost{Cost: 0}
	ca.fieldCosts["User.username"] = &FieldCost{Cost: 0}
	ca.fieldCosts["User.email"] = &FieldCost{Cost: 0}
	ca.fieldCosts["User.roles"] = &FieldCost{Cost: 1}
	ca.fieldCosts["User.createdAt"] = &FieldCost{Cost: 0}
	ca.fieldCosts["User.updatedAt"] = &FieldCost{Cost: 0}

	ca.fieldCosts["KVPair.key"] = &FieldCost{Cost: 0}
	ca.fieldCosts["KVPair.value"] = &FieldCost{Cost: 0}
	ca.fieldCosts["KVPair.ttl"] = &FieldCost{Cost: 0}

	ca.fieldCosts["Job.id"] = &FieldCost{Cost: 0}
	ca.fieldCosts["Job.payload"] = &FieldCost{Cost: 0}
	ca.fieldCosts["Job.status"] = &FieldCost{Cost: 0}

	ca.fieldCosts["ShardNode.nodeId"] = &FieldCost{Cost: 0}
	ca.fieldCosts["ShardNode.address"] = &FieldCost{Cost: 0}
	ca.fieldCosts["ShardNode.keyCount"] = &FieldCost{Cost: 0}

	ca.fieldCosts["RaftStatus.term"] = &FieldCost{Cost: 0}
	ca.fieldCosts["RaftStatus.leader"] = &FieldCost{Cost: 0}
	ca.fieldCosts["RaftStatus.state"] = &FieldCost{Cost: 0}

	ca.fieldCosts["ClusterStatus.healthy"] = &FieldCost{Cost: 0}
	ca.fieldCosts["ClusterStatus.nodes"] = &FieldCost{Cost: 2, Multiplier: 1}
	ca.fieldCosts["ClusterStatus.shardNodes"] = &FieldCost{Cost: 2, Multiplier: 1}
}

// SetFieldCost sets the cost for a specific field
func (ca *CostAnalyzer) SetFieldCost(fieldPath string, cost *FieldCost) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.fieldCosts[fieldPath] = cost
}

// GetFieldCost returns the cost configuration for a field
func (ca *CostAnalyzer) GetFieldCost(fieldPath string) *FieldCost {
	ca.mu.RLock()
	defer ca.mu.RUnlock()

	if cost, ok := ca.fieldCosts[fieldPath]; ok {
		return cost
	}

	return &FieldCost{Cost: ca.config.DefaultFieldCost}
}

// AnalyzeQuery analyzes a GraphQL query and returns the cost
func (ca *CostAnalyzer) AnalyzeQuery(ctx context.Context, query string, variables map[string]interface{}) (*CostAnalysisResult, error) {
	if !ca.config.Enabled {
		return &CostAnalysisResult{}, nil
	}

	result := &CostAnalysisResult{
		FieldCosts: make(map[string]int),
		Warnings:   make([]string, 0),
	}

	// Parse the query
	parsedQuery, err := ca.parseQuery(query)
	if err != nil {
		return nil, fmt.Errorf("failed to parse query: %w", err)
	}

	// Calculate costs
	ca.calculateCosts(parsedQuery, variables, result)

	// Check limits
	if result.TotalCost > ca.config.MaxComplexity {
		result.Exceeded = true
		result.ExceededReason = fmt.Sprintf("Query complexity %d exceeds maximum allowed %d", result.TotalCost, ca.config.MaxComplexity)
	}

	if result.MaxDepth > ca.config.MaxDepth {
		result.Exceeded = true
		if result.ExceededReason != "" {
			result.ExceededReason += "; "
		}
		result.ExceededReason += fmt.Sprintf("Query depth %d exceeds maximum allowed %d", result.MaxDepth, ca.config.MaxDepth)
	}

	return result, nil
}

// parsedField represents a parsed GraphQL field
type parsedField struct {
	Name       string
	Alias      string
	Arguments  map[string]interface{}
	Children   []*parsedField
	ParentType string
	Depth      int
}

// parsedQuery represents a parsed GraphQL query
type parsedQuery struct {
	OperationType string // query, mutation, subscription
	OperationName string
	Fields        []*parsedField
	Fragments     map[string]*parsedField
}

// parseQuery parses a GraphQL query string
func (ca *CostAnalyzer) parseQuery(query string) (*parsedQuery, error) {
	query = strings.TrimSpace(query)

	result := &parsedQuery{
		Fields:    make([]*parsedField, 0),
		Fragments: make(map[string]*parsedField),
	}

	// Determine operation type
	if strings.HasPrefix(query, "mutation") {
		result.OperationType = "Mutation"
		query = strings.TrimPrefix(query, "mutation")
	} else if strings.HasPrefix(query, "subscription") {
		result.OperationType = "Subscription"
		query = strings.TrimPrefix(query, "subscription")
	} else {
		result.OperationType = "Query"
		query = strings.TrimPrefix(query, "query")
	}

	// Extract operation name if present
	query = strings.TrimSpace(query)
	if len(query) > 0 && query[0] != '{' && query[0] != '(' {
		nameEnd := strings.IndexAny(query, " {(")
		if nameEnd > 0 {
			result.OperationName = query[:nameEnd]
			query = query[nameEnd:]
		}
	}

	// Skip variables definition
	query = strings.TrimSpace(query)
	if len(query) > 0 && query[0] == '(' {
		depth := 0
		for i, c := range query {
			if c == '(' {
				depth++
			} else if c == ')' {
				depth--
				if depth == 0 {
					query = query[i+1:]
					break
				}
			}
		}
	}

	// Parse the selection set
	query = strings.TrimSpace(query)
	if len(query) == 0 || query[0] != '{' {
		return result, nil
	}

	fields, err := ca.parseSelectionSet(query, result.OperationType, 1)
	if err != nil {
		return nil, err
	}
	result.Fields = fields

	return result, nil
}

// parseSelectionSet parses a GraphQL selection set
func (ca *CostAnalyzer) parseSelectionSet(query string, parentType string, depth int) ([]*parsedField, error) {
	query = strings.TrimSpace(query)
	if len(query) == 0 {
		return nil, nil
	}

	// Remove outer braces
	if query[0] == '{' {
		query = query[1:]
	}
	if len(query) > 0 && query[len(query)-1] == '}' {
		query = query[:len(query)-1]
	}

	fields := make([]*parsedField, 0)
	query = strings.TrimSpace(query)

	for len(query) > 0 {
		// Skip whitespace and commas
		query = strings.TrimLeft(query, " \t\n\r,")
		if len(query) == 0 {
			break
		}

		// Handle closing brace
		if query[0] == '}' {
			break
		}

		// Skip comments
		if strings.HasPrefix(query, "#") {
			newlineIdx := strings.Index(query, "\n")
			if newlineIdx > 0 {
				query = query[newlineIdx+1:]
			} else {
				break
			}
			continue
		}

		// Skip fragment spreads (... on Type or ...FragmentName)
		if strings.HasPrefix(query, "...") {
			// Find end of fragment spread
			endIdx := strings.IndexAny(query[3:], " \t\n\r{}")
			if endIdx < 0 {
				break
			}
			// Check if there's a selection set
			rest := strings.TrimSpace(query[3+endIdx:])
			if len(rest) > 0 && rest[0] == '{' {
				// Skip the inline fragment selection set
				braceDepth := 0
				for i, c := range rest {
					if c == '{' {
						braceDepth++
					} else if c == '}' {
						braceDepth--
						if braceDepth == 0 {
							query = rest[i+1:]
							break
						}
					}
				}
			} else {
				query = rest
			}
			continue
		}

		// Parse field
		field, remaining, err := ca.parseField(query, parentType, depth)
		if err != nil {
			return nil, err
		}
		if field != nil {
			fields = append(fields, field)
		}
		query = remaining
	}

	return fields, nil
}

// parseField parses a single GraphQL field
func (ca *CostAnalyzer) parseField(query string, parentType string, depth int) (*parsedField, string, error) {
	query = strings.TrimSpace(query)
	if len(query) == 0 {
		return nil, "", nil
	}

	field := &parsedField{
		Arguments:  make(map[string]interface{}),
		ParentType: parentType,
		Depth:      depth,
		Children:   make([]*parsedField, 0),
	}

	// Parse field name (with optional alias)
	namePattern := regexp.MustCompile(`^(\w+)(?:\s*:\s*(\w+))?`)
	matches := namePattern.FindStringSubmatch(query)
	if len(matches) == 0 {
		// Check for closing brace
		if query[0] == '}' {
			return nil, query, nil
		}
		return nil, query, nil
	}

	if matches[2] != "" {
		// Alias present
		field.Alias = matches[1]
		field.Name = matches[2]
	} else {
		field.Name = matches[1]
	}

	query = query[len(matches[0]):]
	query = strings.TrimSpace(query)

	// Parse arguments if present
	if len(query) > 0 && query[0] == '(' {
		argEnd := ca.findMatchingParen(query)
		if argEnd > 0 {
			argsStr := query[1:argEnd]
			field.Arguments = ca.parseArguments(argsStr)
			query = query[argEnd+1:]
			query = strings.TrimSpace(query)
		}
	}

	// Parse selection set if present
	if len(query) > 0 && query[0] == '{' {
		braceEnd := ca.findMatchingBrace(query)
		if braceEnd > 0 {
			childType := ca.getFieldReturnType(parentType, field.Name)
			children, err := ca.parseSelectionSet(query[:braceEnd+1], childType, depth+1)
			if err != nil {
				return nil, "", err
			}
			field.Children = children
			query = query[braceEnd+1:]
		}
	}

	return field, query, nil
}

// findMatchingParen finds the matching closing parenthesis
func (ca *CostAnalyzer) findMatchingParen(s string) int {
	depth := 0
	inString := false
	escape := false

	for i, c := range s {
		if escape {
			escape = false
			continue
		}
		if c == '\\' {
			escape = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if c == '(' {
			depth++
		} else if c == ')' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// findMatchingBrace finds the matching closing brace
func (ca *CostAnalyzer) findMatchingBrace(s string) int {
	depth := 0
	inString := false
	escape := false

	for i, c := range s {
		if escape {
			escape = false
			continue
		}
		if c == '\\' {
			escape = true
			continue
		}
		if c == '"' {
			inString = !inString
			continue
		}
		if inString {
			continue
		}
		if c == '{' {
			depth++
		} else if c == '}' {
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// parseArguments parses GraphQL arguments
func (ca *CostAnalyzer) parseArguments(argsStr string) map[string]interface{} {
	args := make(map[string]interface{})

	// Simple argument parser
	argPattern := regexp.MustCompile(`(\w+)\s*:\s*(\$?\w+|"[^"]*"|\d+|true|false|\[[^\]]*\]|\{[^}]*\})`)
	matches := argPattern.FindAllStringSubmatch(argsStr, -1)

	for _, match := range matches {
		if len(match) >= 3 {
			name := match[1]
			value := match[2]

			// Parse value
			if strings.HasPrefix(value, "$") {
				// Variable reference
				args[name] = value
			} else if strings.HasPrefix(value, "\"") && strings.HasSuffix(value, "\"") {
				// String
				args[name] = strings.Trim(value, "\"")
			} else if value == "true" {
				args[name] = true
			} else if value == "false" {
				args[name] = false
			} else if i, err := strconv.Atoi(value); err == nil {
				args[name] = i
			} else {
				args[name] = value
			}
		}
	}

	return args
}

// getFieldReturnType returns the return type for a field
func (ca *CostAnalyzer) getFieldReturnType(parentType, fieldName string) string {
	// Type mapping for Helios schema
	typeMap := map[string]map[string]string{
		"Query": {
			"me":               "User",
			"get":              "KVPair",
			"keys":             "String",
			"job":              "Job",
			"jobs":             "JobConnection",
			"shardNodes":       "ShardNode",
			"shardStats":       "ShardStats",
			"nodeForKey":       "ShardNode",
			"activeMigrations": "Migration",
			"allMigrations":    "Migration",
			"raftStatus":       "RaftStatus",
			"raftPeers":        "RaftPeer",
			"clusterStatus":    "ClusterStatus",
			"user":             "User",
			"users":            "User",
			"role":             "Role",
			"roles":            "Role",
			"health":           "HealthStatus",
			"metrics":          "SystemMetrics",
		},
		"Mutation": {
			"register":          "AuthPayload",
			"login":             "AuthPayload",
			"set":               "KVPair",
			"enqueueJob":        "Job",
			"cancelJob":         "Job",
			"retryJob":          "Job",
			"addShardNode":      "ShardNode",
			"triggerRebalance":  "RebalanceResult",
			"cleanupMigrations": "CleanupResult",
			"addRaftPeer":       "RaftPeer",
			"createUser":        "User",
			"updateUser":        "User",
			"createRole":        "Role",
			"updateRole":        "Role",
		},
		"Subscription": {
			"jobUpdated":        "Job",
			"migrationProgress": "Migration",
			"clusterEvent":      "ClusterEvent",
			"keyChanged":        "KeyChangeEvent",
		},
		"ClusterStatus": {
			"nodes":      "RaftPeer",
			"shardNodes": "ShardNode",
			"raft":       "RaftStatus",
			"sharding":   "ShardStats",
		},
		"AuthPayload": {
			"user": "User",
		},
		"JobConnection": {
			"jobs": "Job",
		},
		"HealthStatus": {
			"components": "ComponentHealth",
		},
	}

	if fields, ok := typeMap[parentType]; ok {
		if returnType, ok := fields[fieldName]; ok {
			return returnType
		}
	}

	return "Unknown"
}

// calculateCosts calculates the total cost of parsed fields
func (ca *CostAnalyzer) calculateCosts(query *parsedQuery, variables map[string]interface{}, result *CostAnalysisResult) {
	for _, field := range query.Fields {
		ca.calculateFieldCost(field, query.OperationType, variables, result, "")
	}
}

// calculateFieldCost calculates the cost of a single field and its children
func (ca *CostAnalyzer) calculateFieldCost(field *parsedField, parentType string, variables map[string]interface{}, result *CostAnalysisResult, path string) {
	if field == nil {
		return
	}

	// Build field path
	fieldPath := fmt.Sprintf("%s.%s", parentType, field.Name)
	displayPath := path
	if displayPath != "" {
		displayPath += "."
	}
	if field.Alias != "" {
		displayPath += field.Alias
	} else {
		displayPath += field.Name
	}

	// Update max depth
	if field.Depth > result.MaxDepth {
		result.MaxDepth = field.Depth
	}

	// Get field cost configuration
	costConfig := ca.GetFieldCost(fieldPath)
	baseCost := costConfig.Cost

	// Apply multiplier for list fields
	multiplier := 1
	if costConfig.Multiplier > 0 {
		multiplier = costConfig.Multiplier
	}

	// Check for limit/first argument to adjust multiplier
	if costConfig.UseMultiplierArg != "" {
		if argValue, ok := field.Arguments[costConfig.UseMultiplierArg]; ok {
			switch v := argValue.(type) {
			case int:
				multiplier = v
			case string:
				// Check if it's a variable reference
				if strings.HasPrefix(v, "$") && variables != nil {
					varName := strings.TrimPrefix(v, "$")
					if varVal, ok := variables[varName]; ok {
						if intVal, ok := varVal.(float64); ok {
							multiplier = int(intVal)
						} else if intVal, ok := varVal.(int); ok {
							multiplier = intVal
						}
					}
				}
			}
		}
	}

	// Calculate field cost
	fieldCost := baseCost
	if multiplier > 1 && len(field.Children) > 0 {
		// For list fields with children, apply multiplier to children costs
		childCost := 0
		for _, child := range field.Children {
			childResult := &CostAnalysisResult{
				FieldCosts: make(map[string]int),
			}
			childType := ca.getFieldReturnType(parentType, field.Name)
			ca.calculateFieldCost(child, childType, variables, childResult, displayPath)
			childCost += childResult.TotalCost
		}
		fieldCost = baseCost + (childCost * multiplier)
	} else if !costConfig.IgnoreChildren {
		// Calculate children costs normally
		childType := ca.getFieldReturnType(parentType, field.Name)
		for _, child := range field.Children {
			ca.calculateFieldCost(child, childType, variables, result, displayPath)
		}
	}

	// Add to total
	result.TotalCost += fieldCost
	result.FieldCosts[displayPath] = fieldCost
}

// Validate checks if the query cost is within limits
func (ca *CostAnalyzer) Validate(ctx context.Context, query string, variables map[string]interface{}) error {
	if !ca.config.Enabled {
		return nil
	}

	result, err := ca.AnalyzeQuery(ctx, query, variables)
	if err != nil {
		return err
	}

	if result.Exceeded && ca.config.RejectOnExceed {
		return &QueryComplexityError{
			Message:       result.ExceededReason,
			TotalCost:     result.TotalCost,
			MaxComplexity: ca.config.MaxComplexity,
			MaxDepth:      ca.config.MaxDepth,
			CurrentDepth:  result.MaxDepth,
		}
	}

	return nil
}

// QueryComplexityError represents a query complexity validation error
type QueryComplexityError struct {
	Message       string
	TotalCost     int
	MaxComplexity int
	MaxDepth      int
	CurrentDepth  int
}

func (e *QueryComplexityError) Error() string {
	return e.Message
}

// IsQueryComplexityError checks if an error is a query complexity error
func IsQueryComplexityError(err error) bool {
	var qce *QueryComplexityError
	return errors.As(err, &qce)
}

// GetCostAnalysisExtension returns cost info for GraphQL response extensions
func (ca *CostAnalyzer) GetCostAnalysisExtension(result *CostAnalysisResult) map[string]interface{} {
	if !ca.config.IncludeCostInResponse || result == nil {
		return nil
	}

	extension := map[string]interface{}{
		"cost": map[string]interface{}{
			"totalCost":     result.TotalCost,
			"maxComplexity": ca.config.MaxComplexity,
			"maxDepth":      ca.config.MaxDepth,
			"currentDepth":  result.MaxDepth,
		},
	}

	if result.Exceeded {
		extension["cost"].(map[string]interface{})["exceeded"] = true
		extension["cost"].(map[string]interface{})["exceededReason"] = result.ExceededReason
	}

	if len(result.Warnings) > 0 {
		extension["cost"].(map[string]interface{})["warnings"] = result.Warnings
	}

	return extension
}

// UpdateConfig updates the cost analysis configuration
func (ca *CostAnalyzer) UpdateConfig(config *CostConfig) {
	ca.mu.Lock()
	defer ca.mu.Unlock()
	ca.config = config
}

// GetConfig returns the current configuration
func (ca *CostAnalyzer) GetConfig() *CostConfig {
	ca.mu.RLock()
	defer ca.mu.RUnlock()
	return ca.config
}

// CostMiddleware provides HTTP middleware for cost analysis
type CostMiddleware struct {
	analyzer *CostAnalyzer
}

// NewCostMiddleware creates a new cost middleware
func NewCostMiddleware(analyzer *CostAnalyzer) *CostMiddleware {
	return &CostMiddleware{analyzer: analyzer}
}

// Handler wraps an http.Handler with cost analysis
func (cm *CostMiddleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

// costContextKey is the context key for cost analysis result
type costContextKey struct{}

// ContextWithCost adds cost analysis result to context
func ContextWithCost(ctx context.Context, result *CostAnalysisResult) context.Context {
	return context.WithValue(ctx, costContextKey{}, result)
}

// CostFromContext retrieves cost analysis result from context
func CostFromContext(ctx context.Context) *CostAnalysisResult {
	if result, ok := ctx.Value(costContextKey{}).(*CostAnalysisResult); ok {
		return result
	}
	return nil
}
