package graphql

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultSchemaDocumentationConfig(t *testing.T) {
	cfg := DefaultSchemaDocumentationConfig()
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if !cfg.Enabled {
		t.Fatal("expected schema docs enabled by default")
	}
	if cfg.DefaultFormat != "markdown" {
		t.Fatalf("expected default format markdown, got %s", cfg.DefaultFormat)
	}
	if !cfg.EnableHTTPHandler {
		t.Fatal("expected http handler enabled by default")
	}
}

func TestSchemaDocumentationGenerator_GenerateStructured(t *testing.T) {
	gen := NewSchemaDocumentationGenerator(DefaultSchemaDocumentationConfig())
	doc, err := gen.GetStructuredDocumentation()
	if err != nil {
		t.Fatalf("GetStructuredDocumentation failed: %v", err)
	}
	if doc == nil {
		t.Fatal("expected non-nil documentation")
	}
	if doc.SchemaHash == "" {
		t.Fatal("expected schema hash")
	}
	if doc.Summary == nil {
		t.Fatal("expected summary")
	}
	if doc.Summary.TotalTypes == 0 {
		t.Fatal("expected non-zero types")
	}
	if doc.Operations == nil {
		t.Fatal("expected operations")
	}
	if len(doc.Operations.Queries) == 0 {
		t.Fatal("expected query operations")
	}
}

func TestSchemaDocumentationGenerator_GetFormattedDocumentation(t *testing.T) {
	gen := NewSchemaDocumentationGenerator(DefaultSchemaDocumentationConfig())

	md, err := gen.GetFormattedDocumentation("markdown")
	if err != nil {
		t.Fatalf("markdown generation failed: %v", err)
	}
	if !strings.Contains(md, "# Helios GraphQL Schema Documentation") {
		t.Fatal("markdown output missing title")
	}
	if !strings.Contains(md, "## Queries") {
		t.Fatal("markdown output missing queries section")
	}

	jsonOut, err := gen.GetFormattedDocumentation("json")
	if err != nil {
		t.Fatalf("json generation failed: %v", err)
	}
	if !strings.Contains(jsonOut, "\"schemaHash\"") {
		t.Fatal("json output missing schemaHash")
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(jsonOut), &parsed); err != nil {
		t.Fatalf("invalid json output: %v", err)
	}
}

func TestSchemaDocumentationGenerator_GetTypeDocumentation(t *testing.T) {
	gen := NewSchemaDocumentationGenerator(DefaultSchemaDocumentationConfig())

	typeDoc, found, err := gen.GetTypeDocumentation("Query")
	if err != nil {
		t.Fatalf("GetTypeDocumentation failed: %v", err)
	}
	if !found {
		t.Fatal("expected Query type to exist")
	}
	if typeDoc.Name != "Query" {
		t.Fatalf("expected Query type, got %s", typeDoc.Name)
	}
	if len(typeDoc.Fields) == 0 {
		t.Fatal("expected Query fields")
	}

	_, found, err = gen.GetTypeDocumentation("NoSuchType")
	if err != nil {
		t.Fatalf("GetTypeDocumentation unexpected error: %v", err)
	}
	if found {
		t.Fatal("expected NoSuchType to be missing")
	}
}

func TestSchemaDocumentationGenerator_ListTypesDocumentation(t *testing.T) {
	gen := NewSchemaDocumentationGenerator(DefaultSchemaDocumentationConfig())

	all, err := gen.ListTypesDocumentation("", 0, 0)
	if err != nil {
		t.Fatalf("ListTypesDocumentation failed: %v", err)
	}
	if len(all) == 0 {
		t.Fatal("expected non-empty type list")
	}

	objects, err := gen.ListTypesDocumentation("OBJECT", 0, 0)
	if err != nil {
		t.Fatalf("ListTypesDocumentation OBJECT failed: %v", err)
	}
	if len(objects) == 0 {
		t.Fatal("expected object types")
	}
	for _, o := range objects {
		if o.Kind != "OBJECT" {
			t.Fatalf("expected OBJECT kind, got %s", o.Kind)
		}
	}

	paged, err := gen.ListTypesDocumentation("", 2, 1)
	if err != nil {
		t.Fatalf("ListTypesDocumentation paged failed: %v", err)
	}
	if len(paged) > 2 {
		t.Fatalf("expected <=2 paged results, got %d", len(paged))
	}
}

func TestSchemaDocumentationGenerator_Export(t *testing.T) {
	gen := NewSchemaDocumentationGenerator(DefaultSchemaDocumentationConfig())
	tmpPath := filepath.Join(t.TempDir(), "schema-docs.md")

	if err := gen.Export(tmpPath, "markdown"); err != nil {
		t.Fatalf("Export failed: %v", err)
	}

	raw, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("failed reading export: %v", err)
	}
	if !strings.Contains(string(raw), "Helios GraphQL Schema Documentation") {
		t.Fatal("export content missing expected title")
	}
}

func TestSchemaDocumentationGenerator_AutoExport(t *testing.T) {
	tmpPath := filepath.Join(t.TempDir(), "schema-docs.json")
	cfg := DefaultSchemaDocumentationConfig()
	cfg.AutoExport = true
	cfg.ExportPath = tmpPath
	cfg.DefaultFormat = "json"

	gen := NewSchemaDocumentationGenerator(cfg)
	if _, err := gen.Regenerate(); err != nil {
		t.Fatalf("Regenerate failed: %v", err)
	}

	raw, err := os.ReadFile(tmpPath)
	if err != nil {
		t.Fatalf("failed reading auto-export file: %v", err)
	}
	if !bytes.Contains(raw, []byte("\"summary\"")) {
		t.Fatal("auto-export json missing summary")
	}
}

func TestSchemaDocumentationGenerator_CustomSchemaParsing(t *testing.T) {
	customSchema := `
# Custom Query
	type Query {
		# Search users
		searchUsers(limit: Int = 10, roles: [String!]): [User!]!
	}

	type User {
		id: ID!
		name: String!
	}

	enum Role {
		ADMIN
		USER
	}
`

	gen := NewSchemaDocumentationGeneratorWithSource(DefaultSchemaDocumentationConfig(), func() string {
		return customSchema
	})

	doc, err := gen.GetStructuredDocumentation()
	if err != nil {
		t.Fatalf("GetStructuredDocumentation failed: %v", err)
	}
	if doc.Summary.TotalTypes < 3 {
		t.Fatalf("expected at least 3 types, got %d", doc.Summary.TotalTypes)
	}

	queryType, found, err := gen.GetTypeDocumentation("Query")
	if err != nil {
		t.Fatalf("GetTypeDocumentation failed: %v", err)
	}
	if !found {
		t.Fatal("expected Query type")
	}
	if len(queryType.Fields) != 1 {
		t.Fatalf("expected 1 Query field, got %d", len(queryType.Fields))
	}

	field := queryType.Fields[0]
	if field.Name != "searchUsers" {
		t.Fatalf("expected searchUsers field, got %s", field.Name)
	}
	if len(field.Arguments) != 2 {
		t.Fatalf("expected 2 arguments, got %d", len(field.Arguments))
	}
	if field.Arguments[0].DefaultValue == nil || *field.Arguments[0].DefaultValue != "10" {
		t.Fatal("expected default value 10 on first argument")
	}
}

func TestNormalizeDocFormat(t *testing.T) {
	tests := []struct {
		input   string
		expects string
		ok      bool
	}{
		{"", "markdown", true},
		{"markdown", "markdown", true},
		{"md", "markdown", true},
		{"json", "json", true},
		{"XML", "", false},
	}

	for _, tt := range tests {
		got, err := normalizeDocFormat(tt.input)
		if tt.ok && err != nil {
			t.Fatalf("expected success for %q, got error %v", tt.input, err)
		}
		if !tt.ok && err == nil {
			t.Fatalf("expected error for %q", tt.input)
		}
		if tt.ok && got != tt.expects {
			t.Fatalf("expected %q for %q, got %q", tt.expects, tt.input, got)
		}
	}
}

func TestHandlerSchemaDocumentationSummaryQuery(t *testing.T) {
	handler, _ := createTestHandler(t)

	req := GraphQLRequest{
		Query: `query { schemaDocumentationSummary { totalTypes totalQueries totalMutations } }`,
	}
	body, _ := json.Marshal(req)
	httpReq := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewBuffer(body))
	httpReq.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}

	var resp GraphQLResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected graphql errors: %+v", resp.Errors)
	}

	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected response data map, got %#v", resp.Data)
	}
	if _, ok := data["schemaDocumentationSummary"]; !ok {
		t.Fatalf("missing schemaDocumentationSummary in data: %#v", data)
	}
}

func TestHandlerSchemaDocumentationQuery(t *testing.T) {
	handler, _ := createTestHandler(t)

	req := GraphQLRequest{
		Query: `query($format: String) { schemaDocumentation(format: $format) }`,
		Variables: map[string]interface{}{
			"format": "markdown",
		},
	}
	body, _ := json.Marshal(req)
	httpReq := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewBuffer(body))
	httpReq.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}

	var resp GraphQLResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected graphql errors: %+v", resp.Errors)
	}

	data, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("expected response data map, got %#v", resp.Data)
	}
	v, ok := data["schemaDocumentation"].(string)
	if !ok {
		t.Fatalf("expected schemaDocumentation string, got %#v", data["schemaDocumentation"])
	}
	if !strings.Contains(v, "Helios GraphQL Schema Documentation") {
		t.Fatal("schemaDocumentation output missing expected title")
	}
}

func TestHandlerSchemaDocumentationMutationRegenerate(t *testing.T) {
	handler, _ := createTestHandler(t)

	req := GraphQLRequest{
		Query: `mutation { regenerateSchemaDocumentation { totalTypes totalFields } }`,
	}
	body, _ := json.Marshal(req)
	httpReq := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewBuffer(body))
	httpReq.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}

	var resp GraphQLResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}
	if len(resp.Errors) > 0 {
		t.Fatalf("unexpected graphql errors: %+v", resp.Errors)
	}
}

func TestHandlerSchemaDocumentationMutationExport(t *testing.T) {
	handler, _ := createTestHandler(t)
	exportPath := filepath.Join(t.TempDir(), "graphql-docs.md")

	req := GraphQLRequest{
		Query: `mutation($path: String, $format: String) { exportSchemaDocumentation(path: $path, format: $format) }`,
		Variables: map[string]interface{}{
			"path":   exportPath,
			"format": "markdown",
		},
	}
	body, _ := json.Marshal(req)
	httpReq := httptest.NewRequest(http.MethodPost, "/graphql", bytes.NewBuffer(body))
	httpReq.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()

	handler.ServeHTTP(recorder, httpReq)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recorder.Code)
	}

	raw, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatalf("expected export file: %v", err)
	}
	if !strings.Contains(string(raw), "Helios GraphQL Schema Documentation") {
		t.Fatal("exported docs missing expected title")
	}
}

func TestHandlerSchemaDocumentationHTTPHandler(t *testing.T) {
	handler, _ := createTestHandler(t)
	docsHandler := handler.SchemaDocumentationHTTPHandler()

	// Markdown response
	req := httptest.NewRequest(http.MethodGet, "/graphql/docs", nil)
	rec := httptest.NewRecorder()
	docsHandler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if !strings.Contains(rec.Header().Get("Content-Type"), "text/markdown") {
		t.Fatalf("expected markdown content-type, got %s", rec.Header().Get("Content-Type"))
	}
	if !strings.Contains(rec.Body.String(), "Helios GraphQL Schema Documentation") {
		t.Fatal("markdown docs missing expected title")
	}

	// JSON response
	reqJSON := httptest.NewRequest(http.MethodGet, "/graphql/docs?format=json", nil)
	recJSON := httptest.NewRecorder()
	docsHandler.ServeHTTP(recJSON, reqJSON)
	if recJSON.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", recJSON.Code)
	}
	if !strings.Contains(recJSON.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("expected json content-type, got %s", recJSON.Header().Get("Content-Type"))
	}

	var parsed map[string]interface{}
	if err := json.Unmarshal(recJSON.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("expected valid json docs, got error: %v", err)
	}
}

func TestHandlerSchemaDocumentationHTTPDisabled(t *testing.T) {
	handler, _ := createTestHandler(t)
	cfg := DefaultSchemaDocumentationConfig()
	cfg.Enabled = true
	cfg.EnableHTTPHandler = false
	handler.SetSchemaDocumentationConfig(cfg)

	docsHandler := handler.SchemaDocumentationHTTPHandler()
	req := httptest.NewRequest(http.MethodGet, "/graphql/docs", nil)
	rec := httptest.NewRecorder()
	docsHandler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when disabled, got %d", rec.Code)
	}
}
