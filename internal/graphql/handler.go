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
	resolver    *Resolver
	authService *auth.Service
	logger      *observability.Logger
}

// NewHandler creates a new GraphQL handler
func NewHandler(resolver *Resolver, authService *auth.Service) *Handler {
	return &Handler{
		resolver:    resolver,
		authService: authService,
		logger:      observability.NewLogger("graphql"),
	}
}

// GraphQLRequest represents an incoming GraphQL request
type GraphQLRequest struct {
	Query         string                 `json:"query"`
	OperationName string                 `json:"operationName,omitempty"`
	Variables     map[string]interface{} `json:"variables,omitempty"`
}

// GraphQLResponse represents a GraphQL response
type GraphQLResponse struct {
	Data   interface{}    `json:"data,omitempty"`
	Errors []GraphQLError `json:"errors,omitempty"`
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

	// Execute query
	response := h.executeQuery(ctx, &req)

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
