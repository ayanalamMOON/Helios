package graphql

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/helios/helios/internal/auth"
	"github.com/helios/helios/internal/observability"
)

// Handler handles GraphQL HTTP requests
type Handler struct {
	resolver     *Resolver
	authService  *auth.Service
	logger       *observability.Logger
	costAnalyzer *CostAnalyzer
	rateLimiter  *ResolverRateLimiter
}

// HandlerOption is a function that configures a Handler
type HandlerOption func(*Handler)

// WithCostAnalyzer sets a custom cost analyzer
func WithCostAnalyzer(analyzer *CostAnalyzer) HandlerOption {
	return func(h *Handler) {
		h.costAnalyzer = analyzer
	}
}

// WithCostConfig sets the cost analysis configuration
func WithCostConfig(config *CostConfig) HandlerOption {
	return func(h *Handler) {
		h.costAnalyzer = NewCostAnalyzer(config)
	}
}

// WithRateLimiter sets a custom rate limiter
func WithRateLimiter(limiter *ResolverRateLimiter) HandlerOption {
	return func(h *Handler) {
		h.rateLimiter = limiter
	}
}

// WithRateLimitConfig sets the rate limit configuration
func WithRateLimitConfig(config *RateLimitConfig) HandlerOption {
	return func(h *Handler) {
		h.rateLimiter = NewResolverRateLimiter(config)
	}
}

// NewHandler creates a new GraphQL handler
func NewHandler(resolver *Resolver, authService *auth.Service, opts ...HandlerOption) *Handler {
	h := &Handler{
		resolver:     resolver,
		authService:  authService,
		logger:       observability.NewLogger("graphql"),
		costAnalyzer: NewCostAnalyzer(DefaultCostConfig()),
		rateLimiter:  NewResolverRateLimiter(DefaultRateLimitConfig()),
	}

	for _, opt := range opts {
		opt(h)
	}

	// Share the cost analyzer with the resolver for introspection queries
	if resolver != nil {
		resolver.SetCostAnalyzer(h.costAnalyzer)
		resolver.SetRateLimiter(h.rateLimiter)
	}

	return h
}

// GraphQLRequest represents an incoming GraphQL request
type GraphQLRequest struct {
	Query         string                 `json:"query"`
	OperationName string                 `json:"operationName,omitempty"`
	Variables     map[string]interface{} `json:"variables,omitempty"`
}

// GraphQLResponse represents a GraphQL response
type GraphQLResponse struct {
	Data       interface{}            `json:"data,omitempty"`
	Errors     []GraphQLError         `json:"errors,omitempty"`
	Extensions map[string]interface{} `json:"extensions,omitempty"`
}

// GraphQLError represents a GraphQL error
type GraphQLError struct {
	Message    string                 `json:"message"`
	Locations  []GraphQLErrorLocation `json:"locations,omitempty"`
	Path       []interface{}          `json:"path,omitempty"`
	Extensions map[string]interface{} `json:"extensions,omitempty"`
}

// GraphQLErrorLocation represents an error location in the query
type GraphQLErrorLocation struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// ServeHTTP handles GraphQL requests
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Set CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	// Only allow POST and GET
	if r.Method != "POST" && r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse request
	var req GraphQLRequest
	if r.Method == "POST" {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			h.respondWithError(w, "Failed to read request body", http.StatusBadRequest)
			return
		}
		defer r.Body.Close()

		if err := json.Unmarshal(body, &req); err != nil {
			h.respondWithError(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
	} else {
		// Handle GET requests (for GraphQL Playground)
		req.Query = r.URL.Query().Get("query")
		req.OperationName = r.URL.Query().Get("operationName")
	}

	// Create context with authentication
	ctx := h.authenticateRequest(r.Context(), r)

	// Create DataLoaders for this request (request-scoped)
	loaders := NewLoaders(
		h.resolver.atlasStore,
		h.resolver.shardedAtlas,
		h.resolver.authService,
		h.resolver.jobQueue,
		h.resolver.shardManager,
	)
	ctx = ContextWithLoaders(ctx, loaders)

	// Determine authentication status and client ID for rate limiting
	_, isAuthenticated := ctx.Value("user_id").(string)
	clientID := ExtractClientID(ctx, r, h.rateLimiter.config.IPBasedForAnonymous)

	// Determine the operation field for rate limiting
	operationField := h.extractOperationField(req.Query)

	// Perform rate limit check before executing query
	var rateLimitResult *RateLimitResult
	if h.rateLimiter != nil && h.rateLimiter.config.Enabled {
		rateLimitResult = h.rateLimiter.Check(ctx, operationField, clientID, isAuthenticated)

		// Add rate limit headers if configured
		if h.rateLimiter.config.IncludeHeadersInResponse {
			AddRateLimitHeaders(w, rateLimitResult)
		}

		// Reject if rate limit exceeded and rejection is enabled
		if !rateLimitResult.Allowed && h.rateLimiter.config.RejectOnExceed {
			response := &GraphQLResponse{
				Errors: []GraphQLError{
					{
						Message: NewRateLimitError(rateLimitResult).Error(),
						Extensions: map[string]interface{}{
							"code":       "RATE_LIMIT_EXCEEDED",
							"limit":      rateLimitResult.Limit,
							"remaining":  rateLimitResult.Remaining,
							"retryAfter": rateLimitResult.RetryAfter,
							"resetAt":    rateLimitResult.ResetAt.Format("2006-01-02T15:04:05Z07:00"),
							"field":      rateLimitResult.Field,
						},
					},
				},
				Extensions: GetRateLimitExtension(rateLimitResult),
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			json.NewEncoder(w).Encode(response)
			return
		}

		// Store rate limit result in context
		ctx = ContextWithRateLimit(ctx, rateLimitResult)
	}

	// Perform cost analysis before executing query
	var costResult *CostAnalysisResult
	if h.costAnalyzer != nil && h.costAnalyzer.config.Enabled {
		var err error
		costResult, err = h.costAnalyzer.AnalyzeQuery(ctx, req.Query, req.Variables)
		if err != nil {
			h.respondWithError(w, "Failed to analyze query complexity: "+err.Error(), http.StatusBadRequest)
			return
		}

		// Reject if query exceeds limits and rejection is enabled
		if costResult.Exceeded && h.costAnalyzer.config.RejectOnExceed {
			response := &GraphQLResponse{
				Errors: []GraphQLError{
					{
						Message: costResult.ExceededReason,
						Extensions: map[string]interface{}{
							"code":          "QUERY_COMPLEXITY_EXCEEDED",
							"totalCost":     costResult.TotalCost,
							"maxComplexity": h.costAnalyzer.config.MaxComplexity,
							"maxDepth":      h.costAnalyzer.config.MaxDepth,
							"currentDepth":  costResult.MaxDepth,
						},
					},
				},
			}
			// Add cost extension to response
			if h.costAnalyzer.config.IncludeCostInResponse {
				response.Extensions = h.costAnalyzer.GetCostAnalysisExtension(costResult)
			}
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK) // GraphQL always returns 200, errors in body
			json.NewEncoder(w).Encode(response)
			return
		}

		// Store cost result in context for later use
		ctx = ContextWithCost(ctx, costResult)
	}

	// Execute query
	response := h.executeQuery(ctx, &req)

	// Add cost extension to response if configured
	if costResult != nil && h.costAnalyzer.config.IncludeCostInResponse {
		if response.Extensions == nil {
			response.Extensions = make(map[string]interface{})
		}
		costExt := h.costAnalyzer.GetCostAnalysisExtension(costResult)
		for k, v := range costExt {
			response.Extensions[k] = v
		}
	}

	// Add rate limit extension to response if configured
	if rateLimitResult != nil && h.rateLimiter.config.IncludeHeadersInResponse {
		if response.Extensions == nil {
			response.Extensions = make(map[string]interface{})
		}
		rateLimitExt := GetRateLimitExtension(rateLimitResult)
		for k, v := range rateLimitExt {
			response.Extensions[k] = v
		}
	}

	// Send response
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// authenticateRequest extracts and validates the authentication token
func (h *Handler) authenticateRequest(ctx context.Context, r *http.Request) context.Context {
	// Extract token from Authorization header
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return ctx
	}

	// Check for Bearer token
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		return ctx
	}

	token := parts[1]

	// Validate token
	if h.authService != nil {
		userID, err := h.authService.ValidateToken(token)
		if err == nil {
			ctx = context.WithValue(ctx, "user_id", userID)
			ctx = context.WithValue(ctx, "token", token)
		}
	}

	return ctx
}

// executeQuery executes a GraphQL query
func (h *Handler) executeQuery(ctx context.Context, req *GraphQLRequest) *GraphQLResponse {
	// Handle introspection
	if strings.Contains(req.Query, "__schema") || strings.Contains(req.Query, "__type") {
		return h.handleIntrospection(req)
	}

	// Determine operation type
	query := strings.TrimSpace(req.Query)

	if strings.HasPrefix(query, "mutation") {
		return h.executeMutationOperation(ctx, req)
	} else if strings.HasPrefix(query, "subscription") {
		return &GraphQLResponse{
			Errors: []GraphQLError{
				{Message: "Subscriptions should use WebSocket protocol"},
			},
		}
	} else {
		return h.executeQueryOperation(ctx, req)
	}
}

// executeQueryOperation executes a query operation
func (h *Handler) executeQueryOperation(ctx context.Context, req *GraphQLRequest) *GraphQLResponse {
	// Simple query parsing and execution
	query := strings.TrimSpace(req.Query)

	// Extract query name - check with word boundaries
	if strings.Contains(query, "me {") || strings.Contains(query, "me{") || strings.Contains(query, "me }") || strings.Contains(query, "me}") || strings.HasSuffix(query, " me") || strings.HasSuffix(query, "\nme") {
		user, err := h.resolver.Me(ctx)
		if err != nil {
			return &GraphQLResponse{Errors: []GraphQLError{{Message: err.Error()}}}
		}
		return &GraphQLResponse{Data: map[string]interface{}{"me": user}}
	}

	if strings.Contains(query, "health") {
		health, err := h.resolver.Health(ctx)
		if err != nil {
			return &GraphQLResponse{Errors: []GraphQLError{{Message: err.Error()}}}
		}
		return &GraphQLResponse{Data: map[string]interface{}{"health": health}}
	}

	if strings.Contains(query, "metrics") {
		metrics, err := h.resolver.Metrics(ctx)
		if err != nil {
			return &GraphQLResponse{Errors: []GraphQLError{{Message: err.Error()}}}
		}
		return &GraphQLResponse{Data: map[string]interface{}{"metrics": metrics}}
	}

	if strings.Contains(query, "clusterStatus") {
		status, err := h.resolver.ClusterStatus(ctx)
		if err != nil {
			return &GraphQLResponse{Errors: []GraphQLError{{Message: err.Error()}}}
		}
		return &GraphQLResponse{Data: map[string]interface{}{"clusterStatus": status}}
	}

	if strings.Contains(query, "raftStatus") {
		status, err := h.resolver.RaftStatus(ctx)
		if err != nil {
			return &GraphQLResponse{Errors: []GraphQLError{{Message: err.Error()}}}
		}
		return &GraphQLResponse{Data: map[string]interface{}{"raftStatus": status}}
	}

	if strings.Contains(query, "shardNodes") {
		nodes, err := h.resolver.ShardNodes(ctx)
		if err != nil {
			return &GraphQLResponse{Errors: []GraphQLError{{Message: err.Error()}}}
		}
		return &GraphQLResponse{Data: map[string]interface{}{"shardNodes": nodes}}
	}

	if strings.Contains(query, "jobs") {
		args := struct {
			Status *string
			Limit  *int32
		}{}
		jobs, err := h.resolver.Jobs(ctx, args)
		if err != nil {
			return &GraphQLResponse{Errors: []GraphQLError{{Message: err.Error()}}}
		}
		return &GraphQLResponse{Data: map[string]interface{}{"jobs": jobs}}
	}

	if strings.Contains(query, "keys") {
		args := struct{ Pattern *string }{}
		keys, err := h.resolver.Keys(ctx, args)
		if err != nil {
			return &GraphQLResponse{Errors: []GraphQLError{{Message: err.Error()}}}
		}
		return &GraphQLResponse{Data: map[string]interface{}{"keys": keys}}
	}

	// Cost analysis queries
	if strings.Contains(query, "queryCostConfig") {
		config, err := h.resolver.QueryCostConfig(ctx)
		if err != nil {
			return &GraphQLResponse{Errors: []GraphQLError{{Message: err.Error()}}}
		}
		return &GraphQLResponse{Data: map[string]interface{}{"queryCostConfig": config}}
	}

	if strings.Contains(query, "estimateQueryCost") {
		// Extract the query argument from variables or inline
		queryToEstimate := ""
		if q, ok := req.Variables["query"].(string); ok {
			queryToEstimate = q
		}
		args := struct{ Query string }{Query: queryToEstimate}
		estimate, err := h.resolver.EstimateQueryCost(ctx, args)
		if err != nil {
			return &GraphQLResponse{Errors: []GraphQLError{{Message: err.Error()}}}
		}
		return &GraphQLResponse{Data: map[string]interface{}{"estimateQueryCost": estimate}}
	}

	// Rate limit queries
	if strings.Contains(query, "rateLimitConfig") {
		config, err := h.resolver.RateLimitConfig(ctx)
		if err != nil {
			return &GraphQLResponse{Errors: []GraphQLError{{Message: err.Error()}}}
		}
		return &GraphQLResponse{Data: map[string]interface{}{"rateLimitConfig": config}}
	}

	if strings.Contains(query, "rateLimitStatus") {
		field := ""
		if f, ok := req.Variables["field"].(string); ok {
			field = f
		}
		var clientID *string
		if c, ok := req.Variables["clientId"].(string); ok {
			clientID = &c
		}
		args := struct {
			Field    string
			ClientID *string
		}{Field: field, ClientID: clientID}
		status, err := h.resolver.RateLimitStatus(ctx, args)
		if err != nil {
			return &GraphQLResponse{Errors: []GraphQLError{{Message: err.Error()}}}
		}
		return &GraphQLResponse{Data: map[string]interface{}{"rateLimitStatus": status}}
	}

	if strings.Contains(query, "fieldRateLimits") {
		var fields *[]string
		if f, ok := req.Variables["fields"].([]interface{}); ok {
			fieldList := make([]string, len(f))
			for i, v := range f {
				fieldList[i], _ = v.(string)
			}
			fields = &fieldList
		}
		args := struct {
			Fields *[]string
		}{Fields: fields}
		limits, err := h.resolver.FieldRateLimits(ctx, args)
		if err != nil {
			return &GraphQLResponse{Errors: []GraphQLError{{Message: err.Error()}}}
		}
		return &GraphQLResponse{Data: map[string]interface{}{"fieldRateLimits": limits}}
	}

	return &GraphQLResponse{
		Errors: []GraphQLError{
			{Message: "Query not implemented. This is a simplified GraphQL handler."},
		},
	}
}

// executeMutationOperation executes a mutation operation
func (h *Handler) executeMutationOperation(ctx context.Context, req *GraphQLRequest) *GraphQLResponse {
	query := strings.TrimSpace(req.Query)

	if strings.Contains(query, "register") {
		// Extract variables
		var input RegisterInput
		if inputData, ok := req.Variables["input"].(map[string]interface{}); ok {
			input.Username, _ = inputData["username"].(string)
			input.Password, _ = inputData["password"].(string)
			if email, ok := inputData["email"].(string); ok {
				input.Email = &email
			}
		}

		args := struct{ Input RegisterInput }{Input: input}
		result, err := h.resolver.Register(ctx, args)
		if err != nil {
			return &GraphQLResponse{Errors: []GraphQLError{{Message: err.Error()}}}
		}
		return &GraphQLResponse{Data: map[string]interface{}{"register": result}}
	}

	if strings.Contains(query, "login") {
		var input LoginInput
		if inputData, ok := req.Variables["input"].(map[string]interface{}); ok {
			input.Username, _ = inputData["username"].(string)
			input.Password, _ = inputData["password"].(string)
		}

		args := struct{ Input LoginInput }{Input: input}
		result, err := h.resolver.Login(ctx, args)
		if err != nil {
			return &GraphQLResponse{Errors: []GraphQLError{{Message: err.Error()}}}
		}
		return &GraphQLResponse{Data: map[string]interface{}{"login": result}}
	}

	if strings.Contains(query, "set") {
		var input SetInput
		if inputData, ok := req.Variables["input"].(map[string]interface{}); ok {
			input.Key, _ = inputData["key"].(string)
			input.Value, _ = inputData["value"].(string)
			if ttl, ok := inputData["ttl"].(float64); ok {
				ttlInt := int32(ttl)
				input.TTL = &ttlInt
			}
		}

		args := struct{ Input SetInput }{Input: input}
		result, err := h.resolver.Set(ctx, args)
		if err != nil {
			return &GraphQLResponse{Errors: []GraphQLError{{Message: err.Error()}}}
		}
		return &GraphQLResponse{Data: map[string]interface{}{"set": result}}
	}

	if strings.Contains(query, "delete") {
		key, _ := req.Variables["key"].(string)
		args := struct{ Key string }{Key: key}
		result, err := h.resolver.Delete(ctx, args)
		if err != nil {
			return &GraphQLResponse{Errors: []GraphQLError{{Message: err.Error()}}}
		}
		return &GraphQLResponse{Data: map[string]interface{}{"delete": result}}
	}

	return &GraphQLResponse{
		Errors: []GraphQLError{
			{Message: "Mutation not implemented. This is a simplified GraphQL handler."},
		},
	}
}

// handleIntrospection handles GraphQL introspection queries
func (h *Handler) handleIntrospection(req *GraphQLRequest) *GraphQLResponse {
	// Return the schema for introspection
	return &GraphQLResponse{
		Data: map[string]interface{}{
			"__schema": map[string]interface{}{
				"queryType": map[string]interface{}{
					"name": "Query",
				},
				"mutationType": map[string]interface{}{
					"name": "Mutation",
				},
				"subscriptionType": map[string]interface{}{
					"name": "Subscription",
				},
				"types": []interface{}{},
			},
		},
	}
}

// respondWithError sends an error response
func (h *Handler) respondWithError(w http.ResponseWriter, message string, statusCode int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(GraphQLResponse{
		Errors: []GraphQLError{
			{Message: message},
		},
	})
}

// GetCostAnalyzer returns the cost analyzer for external configuration
func (h *Handler) GetCostAnalyzer() *CostAnalyzer {
	return h.costAnalyzer
}

// SetCostConfig updates the cost analysis configuration
func (h *Handler) SetCostConfig(config *CostConfig) {
	if h.costAnalyzer != nil {
		h.costAnalyzer.UpdateConfig(config)
	}
}

// EnableCostAnalysis enables or disables cost analysis
func (h *Handler) EnableCostAnalysis(enabled bool) {
	if h.costAnalyzer != nil {
		config := h.costAnalyzer.GetConfig()
		config.Enabled = enabled
		h.costAnalyzer.UpdateConfig(config)
	}
}

// GetRateLimiter returns the rate limiter for external configuration
func (h *Handler) GetRateLimiter() *ResolverRateLimiter {
	return h.rateLimiter
}

// SetRateLimitConfig updates the rate limit configuration
func (h *Handler) SetRateLimitConfig(config *RateLimitConfig) {
	if h.rateLimiter != nil {
		h.rateLimiter.UpdateConfig(config)
	}
}

// EnableRateLimiting enables or disables rate limiting
func (h *Handler) EnableRateLimiting(enabled bool) {
	if h.rateLimiter != nil {
		config := h.rateLimiter.GetConfig()
		config.Enabled = enabled
		h.rateLimiter.UpdateConfig(config)
	}
}

// Close cleans up handler resources
func (h *Handler) Close() {
	if h.rateLimiter != nil {
		h.rateLimiter.Close()
	}
}

// extractOperationField extracts the operation type and first field name from a GraphQL query
func (h *Handler) extractOperationField(query string) string {
	query = strings.TrimSpace(query)

	// Determine operation type
	operationType := "Query"
	if strings.HasPrefix(query, "mutation") {
		operationType = "Mutation"
	} else if strings.HasPrefix(query, "subscription") {
		operationType = "Subscription"
	}

	// Extract field name from query
	fieldName := h.extractFirstFieldName(query)

	return operationType + "." + fieldName
}

// extractFirstFieldName extracts the first field name from a GraphQL query
func (h *Handler) extractFirstFieldName(query string) string {
	// Find the opening brace
	braceIndex := strings.Index(query, "{")
	if braceIndex == -1 {
		return "unknown"
	}

	// Get content after the brace
	content := strings.TrimSpace(query[braceIndex+1:])

	// Extract the first word (field name)
	var fieldName strings.Builder
	for _, c := range content {
		if c == ' ' || c == '(' || c == '{' || c == '\n' || c == '\r' || c == '\t' {
			break
		}
		fieldName.WriteRune(c)
	}

	result := fieldName.String()
	if result == "" {
		return "unknown"
	}

	// Handle aliases (alias: fieldName)
	if colonIndex := strings.Index(result, ":"); colonIndex != -1 {
		// Get the actual field name after the colon
		afterColon := strings.TrimSpace(content[colonIndex+1:])
		var realFieldName strings.Builder
		for _, c := range afterColon {
			if c == ' ' || c == '(' || c == '{' || c == '\n' || c == '\r' || c == '\t' {
				break
			}
			realFieldName.WriteRune(c)
		}
		if realFieldName.Len() > 0 {
			return realFieldName.String()
		}
	}

	return result
}

// PlaygroundHandler returns the GraphQL Playground HTML
func PlaygroundHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(playgroundHTML))
	}
}

const playgroundHTML = `<!DOCTYPE html>
<html>
<head>
  <meta charset=utf-8/>
  <meta name="viewport" content="user-scalable=no, initial-scale=1.0, minimum-scale=1.0, maximum-scale=1.0, minimal-ui">
  <title>GraphQL Playground</title>
  <link rel="stylesheet" href="//cdn.jsdelivr.net/npm/graphql-playground-react/build/static/css/index.css" />
  <link rel="shortcut icon" href="//cdn.jsdelivr.net/npm/graphql-playground-react/build/favicon.png" />
  <script src="//cdn.jsdelivr.net/npm/graphql-playground-react/build/static/js/middleware.js"></script>
</head>
<body>
  <div id="root">
    <style>
      body {
        background-color: rgb(23, 42, 58);
        font-family: Open Sans, sans-serif;
        height: 90vh;
      }
      #root {
        height: 100%;
        width: 100%;
        display: flex;
        align-items: center;
        justify-content: center;
      }
      .loading {
        font-size: 32px;
        font-weight: 200;
        color: rgba(255, 255, 255, .6);
        margin-left: 20px;
      }
      img {
        width: 78px;
        height: 78px;
      }
      .title {
        font-weight: 400;
      }
    </style>
    <img src='//cdn.jsdelivr.net/npm/graphql-playground-react/build/logo.png' alt=''>
    <div class="loading"> Loading
      <span class="title">GraphQL Playground</span>
    </div>
  </div>
  <script>window.addEventListener('load', function (event) {
      GraphQLPlayground.init(document.getElementById('root'), {
        endpoint: '/graphql'
      })
    })</script>
</body>
</html>`
