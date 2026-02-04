package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

type graphQLRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables,omitempty"`
}

type graphQLResponse struct {
	Data   interface{}    `json:"data,omitempty"`
	Errors []graphQLError `json:"errors,omitempty"`
}

type graphQLError struct {
	Message string   `json:"message"`
	Path    []string `json:"path,omitempty"`
}

var (
	graphqlEndpoint string
	graphqlQuery    string
	graphqlVars     string
	authToken       string
)

var graphqlCmd = &cobra.Command{
	Use:   "graphql",
	Short: "Execute GraphQL operations",
	Long:  "Execute GraphQL queries, mutations, and subscriptions against the Helios API",
}

var graphqlQueryCmd = &cobra.Command{
	Use:   "query [query]",
	Short: "Execute a GraphQL query",
	Long: `Execute a GraphQL query against the Helios API.

Examples:
  helios-cli graphql query "{ health { status } }"
  helios-cli graphql query "{ me { username } }" --token <token>
  helios-cli graphql query "{ get(key: \"mykey\") { key value } }"`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		query := strings.Join(args, " ")
		executeGraphQL(query, graphqlVars, authToken)
	},
}

var graphqlMutateCmd = &cobra.Command{
	Use:   "mutate [mutation]",
	Short: "Execute a GraphQL mutation",
	Long: `Execute a GraphQL mutation against the Helios API.

Examples:
  helios-cli graphql mutate "mutation { set(input: { key: \"test\", value: \"value\" }) { key value } }"
  helios-cli graphql mutate "mutation { register(input: { username: \"user\", password: \"pass\" }) { token } }"
  helios-cli graphql mutate "mutation { login(input: { username: \"user\", password: \"pass\" }) { token } }"`,
	Args: cobra.MinimumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		mutation := strings.Join(args, " ")
		executeGraphQL(mutation, graphqlVars, authToken)
	},
}

var graphqlIntrospectCmd = &cobra.Command{
	Use:   "introspect",
	Short: "Introspect the GraphQL schema",
	Long:  "Retrieve the complete GraphQL schema using introspection",
	Run: func(cmd *cobra.Command, args []string) {
		query := `
			query IntrospectionQuery {
				__schema {
					queryType { name }
					mutationType { name }
					subscriptionType { name }
					types {
						name
						kind
						description
						fields {
							name
							description
							args {
								name
								description
								type { name kind ofType { name kind } }
							}
							type { name kind ofType { name kind } }
						}
					}
				}
			}
		`
		executeGraphQL(query, "", authToken)
	},
}

func executeGraphQL(query, varsJSON, token string) {
	// Parse variables if provided
	var variables map[string]interface{}
	if varsJSON != "" {
		if err := json.Unmarshal([]byte(varsJSON), &variables); err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing variables: %v\n", err)
			os.Exit(1)
		}
	}

	// Create request
	reqBody := graphQLRequest{
		Query:     query,
		Variables: variables,
	}

	reqBytes, err := json.Marshal(reqBody)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error marshaling request: %v\n", err)
		os.Exit(1)
	}

	// Create HTTP request
	req, err := http.NewRequest("POST", graphqlEndpoint, bytes.NewBuffer(reqBytes))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating request: %v\n", err)
		os.Exit(1)
	}

	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	// Execute request
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error executing request: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	// Read response
	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading response: %v\n", err)
		os.Exit(1)
	}

	// Parse response
	var graphqlResp graphQLResponse
	if err := json.Unmarshal(respBytes, &graphqlResp); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing response: %v\n", err)
		os.Exit(1)
	}

	// Check for errors
	if len(graphqlResp.Errors) > 0 {
		fmt.Fprintln(os.Stderr, "GraphQL Errors:")
		for _, err := range graphqlResp.Errors {
			fmt.Fprintf(os.Stderr, "  - %s\n", err.Message)
			if len(err.Path) > 0 {
				fmt.Fprintf(os.Stderr, "    Path: %v\n", err.Path)
			}
		}
		os.Exit(1)
	}

	// Pretty print data
	prettyJSON, err := json.MarshalIndent(graphqlResp.Data, "", "  ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error formatting response: %v\n", err)
		os.Exit(1)
	}

	fmt.Println(string(prettyJSON))
}

func init() {
	// Add GraphQL command to root
	graphqlCmd.AddCommand(graphqlQueryCmd)
	graphqlCmd.AddCommand(graphqlMutateCmd)
	graphqlCmd.AddCommand(graphqlIntrospectCmd)

	// Flags for all GraphQL commands
	graphqlCmd.PersistentFlags().StringVarP(&graphqlEndpoint, "endpoint", "e", "http://localhost:8443/graphql", "GraphQL endpoint URL")
	graphqlCmd.PersistentFlags().StringVarP(&authToken, "token", "t", "", "Authentication token")

	// Flags for query and mutate commands
	graphqlQueryCmd.Flags().StringVarP(&graphqlVars, "vars", "v", "", "Variables as JSON string")
	graphqlMutateCmd.Flags().StringVarP(&graphqlVars, "vars", "v", "", "Variables as JSON string")
}
