package graphql

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// SchemaDocumentationConfig configures automatic schema documentation generation.
type SchemaDocumentationConfig struct {
	// Enabled controls whether schema documentation generation is active.
	Enabled bool `json:"enabled" yaml:"enabled"`

	// AutoGenerate generates documentation automatically on first use.
	AutoGenerate bool `json:"autoGenerate" yaml:"auto_generate"`

	// DefaultFormat is the default output format: "markdown" or "json".
	DefaultFormat string `json:"defaultFormat" yaml:"default_format"`

	// IncludeSchemaSDL includes the raw GraphQL SDL in generated documentation metadata.
	IncludeSchemaSDL bool `json:"includeSchemaSDL" yaml:"include_schema_sdl"`

	// EnableHTTPHandler enables exposing docs through a dedicated HTTP handler.
	EnableHTTPHandler bool `json:"enableHttpHandler" yaml:"enable_http_handler"`

	// AutoExport automatically exports docs to disk after generation/regeneration.
	AutoExport bool `json:"autoExport" yaml:"auto_export"`

	// ExportPath is the target file path for auto export.
	ExportPath string `json:"exportPath" yaml:"export_path"`
}

// DefaultSchemaDocumentationConfig returns the default schema documentation config.
func DefaultSchemaDocumentationConfig() *SchemaDocumentationConfig {
	return &SchemaDocumentationConfig{
		Enabled:           true,
		AutoGenerate:      true,
		DefaultFormat:     "markdown",
		IncludeSchemaSDL:  false,
		EnableHTTPHandler: true,
		AutoExport:        false,
		ExportPath:        "",
	}
}

// SchemaDocumentation is the generated schema documentation model.
type SchemaDocumentation struct {
	GeneratedAt string                        `json:"generatedAt"`
	SchemaHash  string                        `json:"schemaHash"`
	Summary     *SchemaDocumentationSummary   `json:"summary"`
	Operations  *SchemaOperationDocumentation `json:"operations"`
	Types       []*SchemaTypeDocumentation    `json:"types"`
	SchemaSDL   *string                       `json:"schemaSDL,omitempty"`
}

// SchemaDocumentationSummary captures high-level schema counts.
type SchemaDocumentationSummary struct {
	TotalTypes          int `json:"totalTypes"`
	TotalObjectTypes    int `json:"totalObjectTypes"`
	TotalInputTypes     int `json:"totalInputTypes"`
	TotalEnumTypes      int `json:"totalEnumTypes"`
	TotalScalarTypes    int `json:"totalScalarTypes"`
	TotalInterfaceTypes int `json:"totalInterfaceTypes"`
	TotalUnionTypes     int `json:"totalUnionTypes"`
	TotalFields         int `json:"totalFields"`
	TotalQueries        int `json:"totalQueries"`
	TotalMutations      int `json:"totalMutations"`
	TotalSubscriptions  int `json:"totalSubscriptions"`
}

// SchemaOperationDocumentation groups root operation fields.
type SchemaOperationDocumentation struct {
	Queries       []*SchemaFieldDocumentation `json:"queries"`
	Mutations     []*SchemaFieldDocumentation `json:"mutations"`
	Subscriptions []*SchemaFieldDocumentation `json:"subscriptions"`
}

// SchemaTypeDocumentation describes a GraphQL type/input/enum/scalar/interface/union.
type SchemaTypeDocumentation struct {
	Name        string                      `json:"name"`
	Kind        string                      `json:"kind"`
	Description string                      `json:"description,omitempty"`
	Fields      []*SchemaFieldDocumentation `json:"fields,omitempty"`
	EnumValues  []string                    `json:"enumValues,omitempty"`
	UnionTypes  []string                    `json:"unionTypes,omitempty"`
}

// SchemaFieldDocumentation describes a GraphQL field.
type SchemaFieldDocumentation struct {
	Name        string                         `json:"name"`
	Description string                         `json:"description,omitempty"`
	Signature   string                         `json:"signature"`
	ReturnType  string                         `json:"returnType"`
	Arguments   []*SchemaArgumentDocumentation `json:"arguments,omitempty"`
}

// SchemaArgumentDocumentation describes a GraphQL field argument.
type SchemaArgumentDocumentation struct {
	Name         string  `json:"name"`
	Type         string  `json:"type"`
	DefaultValue *string `json:"defaultValue,omitempty"`
}

// SchemaDocumentationGenerator generates automatic docs from GraphQL SDL.
type SchemaDocumentationGenerator struct {
	mu sync.RWMutex

	config       *SchemaDocumentationConfig
	schemaSource func() string

	cachedDoc      *SchemaDocumentation
	cachedMarkdown string
	cachedJSON     string
	lastGenerated  time.Time
}

// NewSchemaDocumentationGenerator creates a new generator using package Schema constant.
func NewSchemaDocumentationGenerator(config *SchemaDocumentationConfig) *SchemaDocumentationGenerator {
	return NewSchemaDocumentationGeneratorWithSource(config, func() string { return Schema })
}

// NewSchemaDocumentationGeneratorWithSource creates a new generator with a custom schema source.
func NewSchemaDocumentationGeneratorWithSource(config *SchemaDocumentationConfig, schemaSource func() string) *SchemaDocumentationGenerator {
	if config == nil {
		config = DefaultSchemaDocumentationConfig()
	}
	if schemaSource == nil {
		schemaSource = func() string { return Schema }
	}

	g := &SchemaDocumentationGenerator{
		config:       config,
		schemaSource: schemaSource,
	}

	if config.Enabled && config.AutoGenerate {
		_, _ = g.Regenerate()
	}

	return g
}

// GetConfig returns a copy of the current config.
func (g *SchemaDocumentationGenerator) GetConfig() *SchemaDocumentationConfig {
	g.mu.RLock()
	defer g.mu.RUnlock()
	clone := *g.config
	return &clone
}

// UpdateConfig updates generator configuration.
func (g *SchemaDocumentationGenerator) UpdateConfig(config *SchemaDocumentationConfig) {
	if config == nil {
		config = DefaultSchemaDocumentationConfig()
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.config = config
}

// Regenerate rebuilds docs from the current schema source and refreshes cache.
func (g *SchemaDocumentationGenerator) Regenerate() (*SchemaDocumentation, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.config == nil || !g.config.Enabled {
		return nil, fmt.Errorf("schema documentation is disabled")
	}

	doc, markdown, jsonOutput, err := g.generateLocked()
	if err != nil {
		return nil, err
	}

	g.cachedDoc = doc
	g.cachedMarkdown = markdown
	g.cachedJSON = jsonOutput
	g.lastGenerated = time.Now()

	if err := g.autoExportLocked(); err != nil {
		return nil, err
	}

	return cloneSchemaDocumentation(doc), nil
}

// GetStructuredDocumentation returns generated docs as a structured model.
func (g *SchemaDocumentationGenerator) GetStructuredDocumentation() (*SchemaDocumentation, error) {
	g.mu.RLock()
	if g.cachedDoc != nil {
		defer g.mu.RUnlock()
		return cloneSchemaDocumentation(g.cachedDoc), nil
	}
	g.mu.RUnlock()

	if _, err := g.Regenerate(); err != nil {
		return nil, err
	}

	g.mu.RLock()
	defer g.mu.RUnlock()
	if g.cachedDoc == nil {
		return nil, fmt.Errorf("schema documentation is unavailable")
	}
	return cloneSchemaDocumentation(g.cachedDoc), nil
}

// GetFormattedDocumentation returns generated docs in markdown or json format.
func (g *SchemaDocumentationGenerator) GetFormattedDocumentation(format string) (string, error) {
	normalized, err := normalizeDocFormat(format)
	if err != nil {
		return "", err
	}

	g.mu.RLock()
	if g.cachedDoc != nil {
		if normalized == "json" {
			defer g.mu.RUnlock()
			return g.cachedJSON, nil
		}
		defer g.mu.RUnlock()
		return g.cachedMarkdown, nil
	}
	g.mu.RUnlock()

	if _, err := g.Regenerate(); err != nil {
		return "", err
	}

	g.mu.RLock()
	defer g.mu.RUnlock()
	if normalized == "json" {
		return g.cachedJSON, nil
	}
	return g.cachedMarkdown, nil
}

// GetTypeDocumentation returns docs for a single type by name.
func (g *SchemaDocumentationGenerator) GetTypeDocumentation(typeName string) (*SchemaTypeDocumentation, bool, error) {
	typeName = strings.TrimSpace(typeName)
	if typeName == "" {
		return nil, false, fmt.Errorf("typeName is required")
	}

	doc, err := g.GetStructuredDocumentation()
	if err != nil {
		return nil, false, err
	}

	for _, t := range doc.Types {
		if t.Name == typeName {
			return cloneTypeDocumentation(t), true, nil
		}
	}

	return nil, false, nil
}

// ListTypesDocumentation returns all type docs, optionally filtered by kind with pagination.
func (g *SchemaDocumentationGenerator) ListTypesDocumentation(kind string, limit, offset int) ([]*SchemaTypeDocumentation, error) {
	doc, err := g.GetStructuredDocumentation()
	if err != nil {
		return nil, err
	}

	kind = strings.TrimSpace(strings.ToUpper(kind))
	filtered := make([]*SchemaTypeDocumentation, 0, len(doc.Types))
	for _, t := range doc.Types {
		if kind == "" || t.Kind == kind {
			filtered = append(filtered, cloneTypeDocumentation(t))
		}
	}

	if offset < 0 {
		offset = 0
	}
	if offset >= len(filtered) {
		return []*SchemaTypeDocumentation{}, nil
	}

	filtered = filtered[offset:]
	if limit > 0 && limit < len(filtered) {
		filtered = filtered[:limit]
	}

	return filtered, nil
}

// Export writes schema docs to a target path in markdown or json format.
func (g *SchemaDocumentationGenerator) Export(path, format string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return fmt.Errorf("export path is required")
	}

	normalized, err := normalizeDocFormat(format)
	if err != nil {
		return err
	}

	content, err := g.GetFormattedDocumentation(normalized)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create schema doc directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("failed to write schema documentation: %w", err)
	}

	return nil
}

func (g *SchemaDocumentationGenerator) autoExportLocked() error {
	if g.config == nil || !g.config.AutoExport {
		return nil
	}
	if strings.TrimSpace(g.config.ExportPath) == "" {
		return nil
	}

	format := g.config.DefaultFormat
	if format == "" {
		format = "markdown"
	}
	format, err := normalizeDocFormat(format)
	if err != nil {
		return err
	}

	content := g.cachedMarkdown
	if format == "json" {
		content = g.cachedJSON
	}

	path := strings.TrimSpace(g.config.ExportPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("failed to create schema doc directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("failed to auto-export schema documentation: %w", err)
	}

	return nil
}

func (g *SchemaDocumentationGenerator) generateLocked() (*SchemaDocumentation, string, string, error) {
	sdl := strings.TrimSpace(g.schemaSource())
	if sdl == "" {
		return nil, "", "", fmt.Errorf("schema source is empty")
	}

	doc := parseSchemaDocumentation(sdl)
	if g.config != nil && g.config.IncludeSchemaSDL {
		doc.SchemaSDL = &sdl
	}

	markdown := generateSchemaDocumentationMarkdown(doc)
	jsonRaw, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, "", "", fmt.Errorf("failed to marshal schema docs to JSON: %w", err)
	}

	return doc, markdown, string(jsonRaw), nil
}

func normalizeDocFormat(format string) (string, error) {
	format = strings.ToLower(strings.TrimSpace(format))
	if format == "" {
		return "markdown", nil
	}
	switch format {
	case "markdown", "md":
		return "markdown", nil
	case "json":
		return "json", nil
	default:
		return "", fmt.Errorf("unsupported schema documentation format: %s", format)
	}
}

func parseSchemaDocumentation(schemaSDL string) *SchemaDocumentation {
	h := sha256.Sum256([]byte(schemaSDL))
	hash := hex.EncodeToString(h[:])

	types := parseSchemaTypes(schemaSDL)
	ops := buildOperationDocumentation(types)
	summary := buildSchemaDocumentationSummary(types, ops)

	return &SchemaDocumentation{
		GeneratedAt: time.Now().Format(time.RFC3339),
		SchemaHash:  hash,
		Summary:     summary,
		Operations:  ops,
		Types:       types,
	}
}

func parseSchemaTypes(schemaSDL string) []*SchemaTypeDocumentation {
	scanner := bufio.NewScanner(strings.NewReader(schemaSDL))

	types := make([]*SchemaTypeDocumentation, 0)
	pendingDescription := make([]string, 0)
	var currentType *SchemaTypeDocumentation

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, "#") {
			comment := strings.TrimSpace(strings.TrimPrefix(line, "#"))
			if comment != "" {
				pendingDescription = append(pendingDescription, comment)
			}
			continue
		}

		if currentType == nil {
			typeDoc, standalone := parseTypeDeclarationLine(line, joinDescription(pendingDescription))
			if typeDoc != nil {
				types = append(types, typeDoc)
				pendingDescription = nil
				if !standalone {
					currentType = typeDoc
				}
				continue
			}

			// Reset orphan descriptions if a non-comment, non-type line appears.
			pendingDescription = nil
			continue
		}

		if line == "}" {
			currentType = nil
			pendingDescription = nil
			continue
		}

		// Parse members inside type/input/interface/enum definitions.
		switch currentType.Kind {
		case "ENUM":
			value := parseEnumValueLine(line)
			if value != "" {
				currentType.EnumValues = append(currentType.EnumValues, value)
			}
			pendingDescription = nil
		default:
			field := parseFieldDocumentationLine(line, joinDescription(pendingDescription))
			if field != nil {
				currentType.Fields = append(currentType.Fields, field)
			}
			pendingDescription = nil
		}
	}

	return types
}

func parseTypeDeclarationLine(line, description string) (*SchemaTypeDocumentation, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return nil, false
	}

	tokens := strings.Fields(line)
	if len(tokens) < 2 {
		return nil, false
	}

	kindToken := tokens[0]
	kind := ""
	switch kindToken {
	case "type":
		kind = "OBJECT"
	case "input":
		kind = "INPUT"
	case "enum":
		kind = "ENUM"
	case "scalar":
		kind = "SCALAR"
	case "interface":
		kind = "INTERFACE"
	case "union":
		kind = "UNION"
	default:
		return nil, false
	}

	name := strings.TrimSpace(tokens[1])
	name = strings.TrimSuffix(name, "{")
	if idx := strings.Index(name, "{"); idx >= 0 {
		name = strings.TrimSpace(name[:idx])
	}

	t := &SchemaTypeDocumentation{
		Name:        name,
		Kind:        kind,
		Description: description,
	}

	// standalone indicates this declaration is complete on one line.
	standalone := false

	switch kind {
	case "SCALAR":
		standalone = true
	case "UNION":
		standalone = true
		if idx := strings.Index(line, "="); idx >= 0 {
			right := strings.TrimSpace(line[idx+1:])
			parts := splitTopLevel(right, '|')
			for _, p := range parts {
				p = strings.TrimSpace(p)
				if p != "" {
					t.UnionTypes = append(t.UnionTypes, p)
				}
			}
		}
	default:
		standalone = strings.Contains(line, "}")
	}

	return t, standalone
}

func parseEnumValueLine(line string) string {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") || line == "}" {
		return ""
	}
	if idx := strings.Index(line, "@"); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	if idx := strings.Index(line, "#"); idx >= 0 {
		line = strings.TrimSpace(line[:idx])
	}
	return strings.TrimSpace(line)
}

func parseFieldDocumentationLine(line, description string) *SchemaFieldDocumentation {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") || line == "}" {
		return nil
	}

	colonIdx := indexTopLevelColon(line)
	if colonIdx == -1 {
		return nil
	}

	left := strings.TrimSpace(line[:colonIdx])
	right := strings.TrimSpace(line[colonIdx+1:])
	if left == "" || right == "" {
		return nil
	}

	name := left
	args := []*SchemaArgumentDocumentation{}

	if openParen := strings.Index(left, "("); openParen >= 0 {
		name = strings.TrimSpace(left[:openParen])
		argsRaw := extractBetweenParens(left[openParen:])
		args = parseFieldArguments(argsRaw)
	}

	returnType := parseReturnType(right)
	signature := strings.TrimSpace(line)

	return &SchemaFieldDocumentation{
		Name:        name,
		Description: description,
		Signature:   signature,
		ReturnType:  returnType,
		Arguments:   args,
	}
}

func parseReturnType(right string) string {
	right = strings.TrimSpace(right)
	if right == "" {
		return ""
	}

	if idx := strings.Index(right, "@"); idx >= 0 {
		right = strings.TrimSpace(right[:idx])
	}

	parts := strings.Fields(right)
	if len(parts) == 0 {
		return ""
	}

	return strings.TrimSpace(parts[0])
}

func parseFieldArguments(argsRaw string) []*SchemaArgumentDocumentation {
	argsRaw = strings.TrimSpace(argsRaw)
	if argsRaw == "" {
		return []*SchemaArgumentDocumentation{}
	}

	parts := splitTopLevel(argsRaw, ',')
	args := make([]*SchemaArgumentDocumentation, 0, len(parts))
	for _, p := range parts {
		arg := strings.TrimSpace(p)
		if arg == "" {
			continue
		}

		colonIdx := strings.Index(arg, ":")
		if colonIdx == -1 {
			continue
		}

		name := strings.TrimSpace(arg[:colonIdx])
		right := strings.TrimSpace(arg[colonIdx+1:])
		if name == "" || right == "" {
			continue
		}

		var defaultValue *string
		typeSpec := right
		if eqIdx := strings.Index(right, "="); eqIdx >= 0 {
			typeSpec = strings.TrimSpace(right[:eqIdx])
			d := strings.TrimSpace(right[eqIdx+1:])
			if d != "" {
				defaultValue = &d
			}
		}

		args = append(args, &SchemaArgumentDocumentation{
			Name:         name,
			Type:         strings.TrimSpace(typeSpec),
			DefaultValue: defaultValue,
		})
	}

	return args
}

func indexTopLevelColon(s string) int {
	depth := 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ':':
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func extractBetweenParens(s string) string {
	open := strings.Index(s, "(")
	if open == -1 {
		return ""
	}
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return s[open+1 : i]
			}
		}
	}
	return ""
}

func splitTopLevel(s string, sep rune) []string {
	parts := make([]string, 0)
	if strings.TrimSpace(s) == "" {
		return parts
	}

	start := 0
	parenDepth := 0
	bracketDepth := 0
	braceDepth := 0

	for i, r := range s {
		switch r {
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			bracketDepth++
		case ']':
			if bracketDepth > 0 {
				bracketDepth--
			}
		case '{':
			braceDepth++
		case '}':
			if braceDepth > 0 {
				braceDepth--
			}
		default:
			if r == sep && parenDepth == 0 && bracketDepth == 0 && braceDepth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}

	parts = append(parts, s[start:])
	return parts
}

func joinDescription(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.TrimSpace(strings.Join(lines, " "))
}

func buildOperationDocumentation(types []*SchemaTypeDocumentation) *SchemaOperationDocumentation {
	ops := &SchemaOperationDocumentation{
		Queries:       []*SchemaFieldDocumentation{},
		Mutations:     []*SchemaFieldDocumentation{},
		Subscriptions: []*SchemaFieldDocumentation{},
	}

	for _, t := range types {
		switch t.Name {
		case "Query":
			ops.Queries = cloneFieldDocumentationSlice(t.Fields)
		case "Mutation":
			ops.Mutations = cloneFieldDocumentationSlice(t.Fields)
		case "Subscription":
			ops.Subscriptions = cloneFieldDocumentationSlice(t.Fields)
		}
	}

	return ops
}

func buildSchemaDocumentationSummary(types []*SchemaTypeDocumentation, ops *SchemaOperationDocumentation) *SchemaDocumentationSummary {
	summary := &SchemaDocumentationSummary{}
	for _, t := range types {
		summary.TotalTypes++
		summary.TotalFields += len(t.Fields)
		switch t.Kind {
		case "OBJECT":
			summary.TotalObjectTypes++
		case "INPUT":
			summary.TotalInputTypes++
		case "ENUM":
			summary.TotalEnumTypes++
		case "SCALAR":
			summary.TotalScalarTypes++
		case "INTERFACE":
			summary.TotalInterfaceTypes++
		case "UNION":
			summary.TotalUnionTypes++
		}
	}

	if ops != nil {
		summary.TotalQueries = len(ops.Queries)
		summary.TotalMutations = len(ops.Mutations)
		summary.TotalSubscriptions = len(ops.Subscriptions)
	}

	return summary
}

func generateSchemaDocumentationMarkdown(doc *SchemaDocumentation) string {
	if doc == nil {
		return "# GraphQL Schema Documentation\n\nNo schema documentation is available.\n"
	}

	var b strings.Builder

	b.WriteString("# Helios GraphQL Schema Documentation\n\n")
	b.WriteString(fmt.Sprintf("Generated at: `%s`  \n", doc.GeneratedAt))
	b.WriteString(fmt.Sprintf("Schema hash: `%s`\n\n", doc.SchemaHash))

	if doc.Summary != nil {
		b.WriteString("## Summary\n\n")
		b.WriteString(fmt.Sprintf("- Total Types: **%d**\n", doc.Summary.TotalTypes))
		b.WriteString(fmt.Sprintf("- Object Types: **%d**\n", doc.Summary.TotalObjectTypes))
		b.WriteString(fmt.Sprintf("- Input Types: **%d**\n", doc.Summary.TotalInputTypes))
		b.WriteString(fmt.Sprintf("- Enum Types: **%d**\n", doc.Summary.TotalEnumTypes))
		b.WriteString(fmt.Sprintf("- Scalar Types: **%d**\n", doc.Summary.TotalScalarTypes))
		b.WriteString(fmt.Sprintf("- Interface Types: **%d**\n", doc.Summary.TotalInterfaceTypes))
		b.WriteString(fmt.Sprintf("- Union Types: **%d**\n", doc.Summary.TotalUnionTypes))
		b.WriteString(fmt.Sprintf("- Total Fields: **%d**\n", doc.Summary.TotalFields))
		b.WriteString(fmt.Sprintf("- Queries: **%d**\n", doc.Summary.TotalQueries))
		b.WriteString(fmt.Sprintf("- Mutations: **%d**\n", doc.Summary.TotalMutations))
		b.WriteString(fmt.Sprintf("- Subscriptions: **%d**\n\n", doc.Summary.TotalSubscriptions))
	}

	if doc.Operations != nil {
		writeOperationTable(&b, "Queries", doc.Operations.Queries)
		writeOperationTable(&b, "Mutations", doc.Operations.Mutations)
		writeOperationTable(&b, "Subscriptions", doc.Operations.Subscriptions)
	}

	b.WriteString("## Type Documentation\n\n")
	types := cloneTypeDocumentationSlice(doc.Types)
	sort.Slice(types, func(i, j int) bool {
		if types[i].Kind == types[j].Kind {
			return types[i].Name < types[j].Name
		}
		return types[i].Kind < types[j].Kind
	})

	for _, t := range types {
		b.WriteString(fmt.Sprintf("### %s `%s`\n\n", t.Kind, t.Name))
		if t.Description != "" {
			b.WriteString(t.Description)
			b.WriteString("\n\n")
		}

		switch t.Kind {
		case "ENUM":
			if len(t.EnumValues) == 0 {
				b.WriteString("_No enum values defined._\n\n")
			} else {
				for _, v := range t.EnumValues {
					b.WriteString(fmt.Sprintf("- `%s`\n", v))
				}
				b.WriteString("\n")
			}
		case "UNION":
			if len(t.UnionTypes) == 0 {
				b.WriteString("_No union members defined._\n\n")
			} else {
				b.WriteString("Members:\n")
				for _, member := range t.UnionTypes {
					b.WriteString(fmt.Sprintf("- `%s`\n", member))
				}
				b.WriteString("\n")
			}
		case "SCALAR":
			b.WriteString("_Scalar type._\n\n")
		default:
			if len(t.Fields) == 0 {
				b.WriteString("_No fields defined._\n\n")
				continue
			}
			b.WriteString("| Field | Type | Arguments | Description |\n")
			b.WriteString("| ----- | ---- | --------- | ----------- |\n")
			for _, f := range t.Fields {
				b.WriteString(fmt.Sprintf("| `%s` | `%s` | %s | %s |\n",
					f.Name,
					f.ReturnType,
					formatArgumentsForMarkdown(f.Arguments),
					escapeMarkdownTableCell(f.Description),
				))
			}
			b.WriteString("\n")
		}
	}

	if doc.SchemaSDL != nil {
		b.WriteString("## Raw Schema SDL\n\n")
		b.WriteString("```graphql\n")
		b.WriteString(*doc.SchemaSDL)
		b.WriteString("\n```\n")
	}

	return b.String()
}

func writeOperationTable(b *strings.Builder, title string, fields []*SchemaFieldDocumentation) {
	b.WriteString(fmt.Sprintf("## %s\n\n", title))
	if len(fields) == 0 {
		b.WriteString("_No fields defined._\n\n")
		return
	}

	b.WriteString("| Field | Return Type | Arguments | Description |\n")
	b.WriteString("| ----- | ----------- | --------- | ----------- |\n")
	for _, f := range fields {
		b.WriteString(fmt.Sprintf("| `%s` | `%s` | %s | %s |\n",
			f.Name,
			f.ReturnType,
			formatArgumentsForMarkdown(f.Arguments),
			escapeMarkdownTableCell(f.Description),
		))
	}
	b.WriteString("\n")
}

func formatArgumentsForMarkdown(args []*SchemaArgumentDocumentation) string {
	if len(args) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(args))
	for _, a := range args {
		if a == nil {
			continue
		}
		item := fmt.Sprintf("`%s: %s`", a.Name, a.Type)
		if a.DefaultValue != nil {
			item = fmt.Sprintf("%s = `%s`", item, *a.DefaultValue)
		}
		parts = append(parts, item)
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, "<br/>")
}

func escapeMarkdownTableCell(s string) string {
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.TrimSpace(s)
	if s == "" {
		return "-"
	}
	return s
}

func cloneSchemaDocumentation(doc *SchemaDocumentation) *SchemaDocumentation {
	if doc == nil {
		return nil
	}
	clone := &SchemaDocumentation{
		GeneratedAt: doc.GeneratedAt,
		SchemaHash:  doc.SchemaHash,
		Summary:     cloneSummary(doc.Summary),
		Operations:  cloneOperations(doc.Operations),
		Types:       cloneTypeDocumentationSlice(doc.Types),
	}
	if doc.SchemaSDL != nil {
		s := *doc.SchemaSDL
		clone.SchemaSDL = &s
	}
	return clone
}

func cloneSummary(s *SchemaDocumentationSummary) *SchemaDocumentationSummary {
	if s == nil {
		return nil
	}
	clone := *s
	return &clone
}

func cloneOperations(o *SchemaOperationDocumentation) *SchemaOperationDocumentation {
	if o == nil {
		return nil
	}
	return &SchemaOperationDocumentation{
		Queries:       cloneFieldDocumentationSlice(o.Queries),
		Mutations:     cloneFieldDocumentationSlice(o.Mutations),
		Subscriptions: cloneFieldDocumentationSlice(o.Subscriptions),
	}
}

func cloneTypeDocumentationSlice(in []*SchemaTypeDocumentation) []*SchemaTypeDocumentation {
	if len(in) == 0 {
		return []*SchemaTypeDocumentation{}
	}
	out := make([]*SchemaTypeDocumentation, 0, len(in))
	for _, t := range in {
		out = append(out, cloneTypeDocumentation(t))
	}
	return out
}

func cloneTypeDocumentation(t *SchemaTypeDocumentation) *SchemaTypeDocumentation {
	if t == nil {
		return nil
	}
	clone := &SchemaTypeDocumentation{
		Name:        t.Name,
		Kind:        t.Kind,
		Description: t.Description,
		Fields:      cloneFieldDocumentationSlice(t.Fields),
		EnumValues:  append([]string{}, t.EnumValues...),
		UnionTypes:  append([]string{}, t.UnionTypes...),
	}
	return clone
}

func cloneFieldDocumentationSlice(in []*SchemaFieldDocumentation) []*SchemaFieldDocumentation {
	if len(in) == 0 {
		return []*SchemaFieldDocumentation{}
	}
	out := make([]*SchemaFieldDocumentation, 0, len(in))
	for _, f := range in {
		if f == nil {
			continue
		}
		clone := &SchemaFieldDocumentation{
			Name:        f.Name,
			Description: f.Description,
			Signature:   f.Signature,
			ReturnType:  f.ReturnType,
			Arguments:   cloneArgumentDocumentationSlice(f.Arguments),
		}
		out = append(out, clone)
	}
	return out
}

func cloneArgumentDocumentationSlice(in []*SchemaArgumentDocumentation) []*SchemaArgumentDocumentation {
	if len(in) == 0 {
		return []*SchemaArgumentDocumentation{}
	}
	out := make([]*SchemaArgumentDocumentation, 0, len(in))
	for _, a := range in {
		if a == nil {
			continue
		}
		clone := &SchemaArgumentDocumentation{
			Name: a.Name,
			Type: a.Type,
		}
		if a.DefaultValue != nil {
			d := *a.DefaultValue
			clone.DefaultValue = &d
		}
		out = append(out, clone)
	}
	return out
}
