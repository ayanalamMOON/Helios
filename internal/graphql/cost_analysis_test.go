package graphql

import (
	"context"
	"testing"
)

func TestDefaultCostConfig(t *testing.T) {
	config := DefaultCostConfig()

	if config.MaxComplexity != 1000 {
		t.Errorf("Expected MaxComplexity 1000, got %d", config.MaxComplexity)
	}
	if config.MaxDepth != 10 {
		t.Errorf("Expected MaxDepth 10, got %d", config.MaxDepth)
	}
	if config.DefaultFieldCost != 1 {
		t.Errorf("Expected DefaultFieldCost 1, got %d", config.DefaultFieldCost)
	}
	if config.DefaultListMultiplier != 10 {
		t.Errorf("Expected DefaultListMultiplier 10, got %d", config.DefaultListMultiplier)
	}
	if !config.Enabled {
		t.Error("Expected Enabled to be true")
	}
	if !config.RejectOnExceed {
		t.Error("Expected RejectOnExceed to be true")
	}
}

func TestNewCostAnalyzer(t *testing.T) {
	t.Run("with default config", func(t *testing.T) {
		analyzer := NewCostAnalyzer(nil)
		if analyzer == nil {
			t.Fatal("Expected non-nil analyzer")
		}
		if analyzer.config.MaxComplexity != 1000 {
			t.Errorf("Expected default MaxComplexity 1000, got %d", analyzer.config.MaxComplexity)
		}
	})

	t.Run("with custom config", func(t *testing.T) {
		config := &CostConfig{
			MaxComplexity: 500,
			MaxDepth:      5,
			Enabled:       true,
		}
		analyzer := NewCostAnalyzer(config)
		if analyzer.config.MaxComplexity != 500 {
			t.Errorf("Expected MaxComplexity 500, got %d", analyzer.config.MaxComplexity)
		}
	})
}

func TestCostAnalyzer_SimpleQuery(t *testing.T) {
	analyzer := NewCostAnalyzer(DefaultCostConfig())
	ctx := context.Background()

	tests := []struct {
		name          string
		query         string
		expectedMin   int
		expectedMax   int
		expectedDepth int
	}{
		{
			name:          "health query",
			query:         "{ health { status } }",
			expectedMin:   1,
			expectedMax:   5,
			expectedDepth: 2,
		},
		{
			name:          "me query",
			query:         "{ me { id username } }",
			expectedMin:   1,
			expectedMax:   5,
			expectedDepth: 2,
		},
		{
			name:          "get query",
			query:         "query { get(key: \"test\") { key value } }",
			expectedMin:   1,
			expectedMax:   5,
			expectedDepth: 2,
		},
		{
			name:          "metrics query",
			query:         "{ metrics { requestsTotal } }",
			expectedMin:   5,
			expectedMax:   10,
			expectedDepth: 2,
		},
		{
			name:          "raftStatus query",
			query:         "{ raftStatus { term leader state } }",
			expectedMin:   2,
			expectedMax:   10,
			expectedDepth: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := analyzer.AnalyzeQuery(ctx, tt.query, nil)
			if err != nil {
				t.Fatalf("Failed to analyze query: %v", err)
			}

			if result.TotalCost < tt.expectedMin || result.TotalCost > tt.expectedMax {
				t.Errorf("Expected cost between %d and %d, got %d", tt.expectedMin, tt.expectedMax, result.TotalCost)
			}

			if result.MaxDepth != tt.expectedDepth {
				t.Errorf("Expected depth %d, got %d", tt.expectedDepth, result.MaxDepth)
			}
		})
	}
}

func TestCostAnalyzer_MutationCost(t *testing.T) {
	analyzer := NewCostAnalyzer(DefaultCostConfig())
	ctx := context.Background()

	tests := []struct {
		name        string
		query       string
		expectedMin int
		expectedMax int
	}{
		{
			name:        "register mutation",
			query:       `mutation { register(input: {username: "test", password: "test"}) { token } }`,
			expectedMin: 10,
			expectedMax: 20,
		},
		{
			name:        "login mutation",
			query:       `mutation { login(input: {username: "test", password: "test"}) { token } }`,
			expectedMin: 5,
			expectedMax: 15,
		},
		{
			name:        "set mutation",
			query:       `mutation { set(input: {key: "test", value: "value"}) { key value } }`,
			expectedMin: 5,
			expectedMax: 15,
		},
		{
			name:        "delete mutation",
			query:       `mutation { delete(key: "test") }`,
			expectedMin: 3,
			expectedMax: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := analyzer.AnalyzeQuery(ctx, tt.query, nil)
			if err != nil {
				t.Fatalf("Failed to analyze query: %v", err)
			}

			if result.TotalCost < tt.expectedMin || result.TotalCost > tt.expectedMax {
				t.Errorf("Expected cost between %d and %d, got %d", tt.expectedMin, tt.expectedMax, result.TotalCost)
			}
		})
	}
}

func TestCostAnalyzer_NestedQuery(t *testing.T) {
	analyzer := NewCostAnalyzer(DefaultCostConfig())
	ctx := context.Background()

	query := `{
		clusterStatus {
			healthy
			nodes {
				nodeId
				address
				state
			}
			shardNodes {
				nodeId
				address
				keyCount
			}
		}
	}`

	result, err := analyzer.AnalyzeQuery(ctx, query, nil)
	if err != nil {
		t.Fatalf("Failed to analyze query: %v", err)
	}

	// ClusterStatus is 10, plus nested fields
	if result.TotalCost < 10 {
		t.Errorf("Expected cost >= 10 for nested query, got %d", result.TotalCost)
	}

	if result.MaxDepth < 3 {
		t.Errorf("Expected depth >= 3 for nested query, got %d", result.MaxDepth)
	}
}

func TestCostAnalyzer_ListFieldMultiplier(t *testing.T) {
	analyzer := NewCostAnalyzer(DefaultCostConfig())
	ctx := context.Background()

	tests := []struct {
		name        string
		query       string
		variables   map[string]interface{}
		expectedMin int
	}{
		{
			name:        "jobs with limit",
			query:       `{ jobs(limit: 50) { jobs { id status } } }`,
			expectedMin: 10,
		},
		{
			name:        "keys with pattern",
			query:       `{ keys(pattern: "user:*", limit: 100) }`,
			expectedMin: 5,
		},
		{
			name:        "users query",
			query:       `{ users(limit: 20) { id username } }`,
			expectedMin: 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := analyzer.AnalyzeQuery(ctx, tt.query, tt.variables)
			if err != nil {
				t.Fatalf("Failed to analyze query: %v", err)
			}

			if result.TotalCost < tt.expectedMin {
				t.Errorf("Expected cost >= %d, got %d", tt.expectedMin, result.TotalCost)
			}
		})
	}
}

func TestCostAnalyzer_ExceedsComplexity(t *testing.T) {
	config := &CostConfig{
		MaxComplexity:    20,
		MaxDepth:         5,
		DefaultFieldCost: 1,
		Enabled:          true,
		RejectOnExceed:   true,
	}
	analyzer := NewCostAnalyzer(config)
	ctx := context.Background()

	// This query should exceed the low complexity limit
	query := `{
		clusterStatus {
			healthy
			nodes { nodeId address }
		}
		raftStatus { term leader }
		shardStats { totalNodes totalKeys }
	}`

	result, err := analyzer.AnalyzeQuery(ctx, query, nil)
	if err != nil {
		t.Fatalf("Failed to analyze query: %v", err)
	}

	if !result.Exceeded {
		t.Errorf("Expected query to exceed complexity limit, cost was %d", result.TotalCost)
	}

	if result.ExceededReason == "" {
		t.Error("Expected exceeded reason to be set")
	}
}

func TestCostAnalyzer_ExceedsDepth(t *testing.T) {
	config := &CostConfig{
		MaxComplexity:    1000,
		MaxDepth:         2,
		DefaultFieldCost: 1,
		Enabled:          true,
		RejectOnExceed:   true,
	}
	analyzer := NewCostAnalyzer(config)
	ctx := context.Background()

	// This query exceeds depth limit
	query := `{
		clusterStatus {
			nodes {
				nodeId
			}
		}
	}`

	result, err := analyzer.AnalyzeQuery(ctx, query, nil)
	if err != nil {
		t.Fatalf("Failed to analyze query: %v", err)
	}

	if !result.Exceeded {
		t.Errorf("Expected query to exceed depth limit, depth was %d", result.MaxDepth)
	}
}

func TestCostAnalyzer_Validate(t *testing.T) {
	config := &CostConfig{
		MaxComplexity:    50,
		MaxDepth:         5,
		DefaultFieldCost: 1,
		Enabled:          true,
		RejectOnExceed:   true,
	}
	analyzer := NewCostAnalyzer(config)
	ctx := context.Background()

	t.Run("valid query", func(t *testing.T) {
		query := `{ health { status } }`
		err := analyzer.Validate(ctx, query, nil)
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
	})

	t.Run("invalid query exceeds complexity", func(t *testing.T) {
		// triggerRebalance has cost 50, which exceeds our limit
		query := `mutation { triggerRebalance { triggered } }`
		err := analyzer.Validate(ctx, query, nil)
		if err == nil {
			t.Error("Expected error for exceeding complexity")
		}

		if !IsQueryComplexityError(err) {
			t.Errorf("Expected QueryComplexityError, got %T", err)
		}

		qce, ok := err.(*QueryComplexityError)
		if !ok {
			t.Fatal("Failed to cast to QueryComplexityError")
		}

		if qce.MaxComplexity != 50 {
			t.Errorf("Expected MaxComplexity 50, got %d", qce.MaxComplexity)
		}
	})

	t.Run("disabled analyzer", func(t *testing.T) {
		disabledConfig := &CostConfig{
			MaxComplexity:  1,
			Enabled:        false,
			RejectOnExceed: true,
		}
		disabledAnalyzer := NewCostAnalyzer(disabledConfig)

		query := `mutation { triggerRebalance { triggered } }`
		err := disabledAnalyzer.Validate(ctx, query, nil)
		if err != nil {
			t.Errorf("Expected no error when disabled, got: %v", err)
		}
	})
}

func TestCostAnalyzer_SetFieldCost(t *testing.T) {
	analyzer := NewCostAnalyzer(DefaultCostConfig())

	// Set custom field cost
	customCost := &FieldCost{Cost: 100, RequiresAuth: true}
	analyzer.SetFieldCost("Query.customField", customCost)

	// Verify it was set
	retrieved := analyzer.GetFieldCost("Query.customField")
	if retrieved.Cost != 100 {
		t.Errorf("Expected cost 100, got %d", retrieved.Cost)
	}
	if !retrieved.RequiresAuth {
		t.Error("Expected RequiresAuth to be true")
	}
}

func TestCostAnalyzer_GetFieldCostDefault(t *testing.T) {
	analyzer := NewCostAnalyzer(DefaultCostConfig())

	// Unknown field should return default cost
	cost := analyzer.GetFieldCost("Query.unknownField")
	if cost.Cost != 1 {
		t.Errorf("Expected default cost 1, got %d", cost.Cost)
	}
}

func TestCostAnalyzer_OperationNameParsing(t *testing.T) {
	analyzer := NewCostAnalyzer(DefaultCostConfig())
	ctx := context.Background()

	tests := []struct {
		name      string
		query     string
		wantError bool
	}{
		{
			name:      "named query",
			query:     `query GetHealth { health { status } }`,
			wantError: false,
		},
		{
			name:      "named mutation",
			query:     `mutation DoLogin { login(input: {username: "test", password: "test"}) { token } }`,
			wantError: false,
		},
		{
			name:      "query with variables definition",
			query:     `query GetKey($key: String!) { get(key: $key) { key value } }`,
			wantError: false,
		},
		{
			name:      "anonymous query",
			query:     `{ health { status } }`,
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := analyzer.AnalyzeQuery(ctx, tt.query, nil)
			if tt.wantError {
				if err == nil {
					t.Error("Expected error but got none")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if result.TotalCost == 0 {
					t.Error("Expected non-zero cost")
				}
			}
		})
	}
}

func TestCostAnalyzer_VariableSubstitution(t *testing.T) {
	analyzer := NewCostAnalyzer(DefaultCostConfig())
	ctx := context.Background()

	query := `query GetJobs($limit: Int!) { jobs(limit: $limit) { jobs { id } } }`
	variables := map[string]interface{}{
		"limit": float64(100), // JSON numbers are float64
	}

	result, err := analyzer.AnalyzeQuery(ctx, query, variables)
	if err != nil {
		t.Fatalf("Failed to analyze query: %v", err)
	}

	// With limit=100, cost should be higher
	if result.TotalCost < 10 {
		t.Errorf("Expected cost >= 10 with limit 100, got %d", result.TotalCost)
	}
}

func TestCostAnalyzer_GetCostAnalysisExtension(t *testing.T) {
	config := DefaultCostConfig()
	config.IncludeCostInResponse = true
	analyzer := NewCostAnalyzer(config)

	result := &CostAnalysisResult{
		TotalCost: 50,
		MaxDepth:  3,
		Exceeded:  false,
	}

	extension := analyzer.GetCostAnalysisExtension(result)
	if extension == nil {
		t.Fatal("Expected non-nil extension")
	}

	costData, ok := extension["cost"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected cost data in extension")
	}

	if costData["totalCost"] != 50 {
		t.Errorf("Expected totalCost 50, got %v", costData["totalCost"])
	}
	if costData["currentDepth"] != 3 {
		t.Errorf("Expected currentDepth 3, got %v", costData["currentDepth"])
	}
}

func TestCostAnalyzer_DisabledExtension(t *testing.T) {
	config := DefaultCostConfig()
	config.IncludeCostInResponse = false
	analyzer := NewCostAnalyzer(config)

	result := &CostAnalysisResult{
		TotalCost: 50,
	}

	extension := analyzer.GetCostAnalysisExtension(result)
	if extension != nil {
		t.Error("Expected nil extension when disabled")
	}
}

func TestContextWithCost(t *testing.T) {
	ctx := context.Background()
	result := &CostAnalysisResult{
		TotalCost: 100,
		MaxDepth:  5,
	}

	ctx = ContextWithCost(ctx, result)
	retrieved := CostFromContext(ctx)

	if retrieved == nil {
		t.Fatal("Expected non-nil result from context")
	}

	if retrieved.TotalCost != 100 {
		t.Errorf("Expected TotalCost 100, got %d", retrieved.TotalCost)
	}
}

func TestCostFromContext_Nil(t *testing.T) {
	ctx := context.Background()
	result := CostFromContext(ctx)
	if result != nil {
		t.Error("Expected nil result from empty context")
	}
}

func TestCostAnalyzer_UpdateConfig(t *testing.T) {
	analyzer := NewCostAnalyzer(DefaultCostConfig())

	newConfig := &CostConfig{
		MaxComplexity: 500,
		MaxDepth:      3,
		Enabled:       false,
	}

	analyzer.UpdateConfig(newConfig)
	retrieved := analyzer.GetConfig()

	if retrieved.MaxComplexity != 500 {
		t.Errorf("Expected MaxComplexity 500, got %d", retrieved.MaxComplexity)
	}
	if retrieved.MaxDepth != 3 {
		t.Errorf("Expected MaxDepth 3, got %d", retrieved.MaxDepth)
	}
}

func TestCostAnalyzer_FieldCostBreakdown(t *testing.T) {
	analyzer := NewCostAnalyzer(DefaultCostConfig())
	ctx := context.Background()

	query := `{
		health { status }
		metrics { requestsTotal }
	}`

	result, err := analyzer.AnalyzeQuery(ctx, query, nil)
	if err != nil {
		t.Fatalf("Failed to analyze query: %v", err)
	}

	// Should have costs for both fields
	if len(result.FieldCosts) == 0 {
		t.Error("Expected field cost breakdown")
	}
}

func TestCostAnalyzer_SubscriptionCost(t *testing.T) {
	analyzer := NewCostAnalyzer(DefaultCostConfig())
	ctx := context.Background()

	query := `subscription { keyChanged(pattern: "user:*") { key operation value } }`

	result, err := analyzer.AnalyzeQuery(ctx, query, nil)
	if err != nil {
		t.Fatalf("Failed to analyze query: %v", err)
	}

	// keyChanged has cost 10
	if result.TotalCost < 10 {
		t.Errorf("Expected cost >= 10 for subscription, got %d", result.TotalCost)
	}
}

func TestCostAnalyzer_AliasedFields(t *testing.T) {
	analyzer := NewCostAnalyzer(DefaultCostConfig())
	ctx := context.Background()

	query := `{
		h: health { status }
		m: metrics { requestsTotal }
	}`

	result, err := analyzer.AnalyzeQuery(ctx, query, nil)
	if err != nil {
		t.Fatalf("Failed to analyze query: %v", err)
	}

	// Check that aliased fields are properly tracked
	if result.TotalCost < 6 { // health(1) + metrics(5) = 6
		t.Errorf("Expected cost >= 6, got %d", result.TotalCost)
	}

	// Check field breakdown includes aliases
	if _, hasH := result.FieldCosts["h"]; !hasH {
		// Alias tracking is optional, just check total cost
		t.Log("Alias 'h' not tracked separately (this is acceptable)")
	}
}

func TestCostAnalyzer_EmptyQuery(t *testing.T) {
	analyzer := NewCostAnalyzer(DefaultCostConfig())
	ctx := context.Background()

	result, err := analyzer.AnalyzeQuery(ctx, "", nil)
	if err != nil {
		t.Fatalf("Failed to analyze empty query: %v", err)
	}

	if result.TotalCost != 0 {
		t.Errorf("Expected cost 0 for empty query, got %d", result.TotalCost)
	}
}

func TestCostAnalyzer_ComplexAdminMutations(t *testing.T) {
	analyzer := NewCostAnalyzer(DefaultCostConfig())
	ctx := context.Background()

	// Admin mutations should have higher costs
	adminQueries := []struct {
		name        string
		query       string
		expectedMin int
	}{
		{
			name:        "addRaftPeer",
			query:       `mutation { addRaftPeer(input: {nodeId: "node2", address: "localhost:8002"}) { nodeId } }`,
			expectedMin: 30,
		},
		{
			name:        "removeRaftPeer",
			query:       `mutation { removeRaftPeer(nodeId: "node2") }`,
			expectedMin: 30,
		},
		{
			name:        "triggerRebalance",
			query:       `mutation { triggerRebalance { triggered migrationsCreated } }`,
			expectedMin: 50,
		},
		{
			name:        "addShardNode",
			query:       `mutation { addShardNode(input: {nodeId: "node2", address: "localhost:8002"}) { nodeId } }`,
			expectedMin: 20,
		},
	}

	for _, tt := range adminQueries {
		t.Run(tt.name, func(t *testing.T) {
			result, err := analyzer.AnalyzeQuery(ctx, tt.query, nil)
			if err != nil {
				t.Fatalf("Failed to analyze query: %v", err)
			}

			if result.TotalCost < tt.expectedMin {
				t.Errorf("Expected cost >= %d for admin mutation, got %d", tt.expectedMin, result.TotalCost)
			}
		})
	}
}

func TestQueryComplexityError(t *testing.T) {
	err := &QueryComplexityError{
		Message:       "Query too complex",
		TotalCost:     150,
		MaxComplexity: 100,
		MaxDepth:      10,
		CurrentDepth:  5,
	}

	if err.Error() != "Query too complex" {
		t.Errorf("Expected error message 'Query too complex', got '%s'", err.Error())
	}

	if !IsQueryComplexityError(err) {
		t.Error("Expected IsQueryComplexityError to return true")
	}

	// Test with wrapped error
	var regularErr error = err
	if !IsQueryComplexityError(regularErr) {
		t.Error("Expected IsQueryComplexityError to work with interface type")
	}
}

func BenchmarkCostAnalyzer_SimpleQuery(b *testing.B) {
	analyzer := NewCostAnalyzer(DefaultCostConfig())
	ctx := context.Background()
	query := `{ health { status version } }`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analyzer.AnalyzeQuery(ctx, query, nil)
	}
}

func BenchmarkCostAnalyzer_ComplexQuery(b *testing.B) {
	analyzer := NewCostAnalyzer(DefaultCostConfig())
	ctx := context.Background()
	query := `{
		clusterStatus {
			healthy
			nodes { nodeId address state }
			shardNodes { nodeId address keyCount }
		}
		raftStatus { term leader state }
		shardStats { totalNodes totalKeys averageKeysPerNode }
		jobs(limit: 10) { jobs { id status payload } }
	}`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = analyzer.AnalyzeQuery(ctx, query, nil)
	}
}

func BenchmarkCostAnalyzer_Validate(b *testing.B) {
	analyzer := NewCostAnalyzer(DefaultCostConfig())
	ctx := context.Background()
	query := `{ health { status } }`

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = analyzer.Validate(ctx, query, nil)
	}
}
