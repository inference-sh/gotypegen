package gotypegen

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"reflect"
	"strings"
)

// JSONSchema represents a JSON Schema document
type JSONSchema struct {
	Schema      string             `json:"$schema,omitempty"`
	Definitions map[string]*Schema `json:"$defs,omitempty"`
}

// Schema represents a JSON Schema type definition
type Schema struct {
	Type                 interface{}        `json:"type,omitempty"`
	Format               string             `json:"format,omitempty"`
	Description          string             `json:"description,omitempty"`
	Properties           map[string]*Schema `json:"properties,omitempty"`
	Required             []string           `json:"required,omitempty"`
	Items                *Schema            `json:"items,omitempty"`
	Ref                  string             `json:"$ref,omitempty"`
	Enum                 []interface{}      `json:"enum,omitempty"`
	AdditionalProperties interface{}        `json:"additionalProperties,omitempty"`
	ContentEncoding      string             `json:"contentEncoding,omitempty"`
}

// GenerateJSONSchema generates JSON Schema definitions for the package
func (g *PackageGenerator) GenerateJSONSchema() (string, error) {
	// Use traced generation if in trace mode
	if g.conf.IsTraceMode() {
		return g.GenerateJSONSchemaTraced()
	}

	schema := &JSONSchema{
		Schema:      "https://json-schema.org/draft/2020-12/schema",
		Definitions: make(map[string]*Schema),
	}

	for i, file := range g.pkg.Syntax {
		if g.conf.IsFileIgnored(g.GoFiles[i]) {
			continue
		}
		g.processJSONSchemaFile(schema, file)
	}

	output, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling schema: %w", err)
	}

	return string(output), nil
}

// GenerateJSONSchemaTraced generates JSON Schema with dependency tracing
func (g *PackageGenerator) GenerateJSONSchemaTraced() (string, error) {
	schema := &JSONSchema{
		Schema:      "https://json-schema.org/draft/2020-12/schema",
		Definitions: make(map[string]*Schema),
	}

	// Build type graph and trace dependencies
	graph := g.BuildTypeGraph()
	includedTypes := g.TraceTypes(graph)

	for i, file := range g.pkg.Syntax {
		if g.conf.IsFileIgnored(g.GoFiles[i]) {
			continue
		}
		g.processJSONSchemaFileTraced(schema, file, includedTypes)
	}

	output, err := json.MarshalIndent(schema, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling schema: %w", err)
	}

	return string(output), nil
}

func (g *PackageGenerator) processJSONSchemaFileTraced(schema *JSONSchema, file *ast.File, includedTypes map[string]bool) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.GenDecl:
			if x.Tok == token.IMPORT || x.Tok == token.VAR {
				return false
			}
			g.processJSONSchemaDeclTraced(schema, x, includedTypes)
			return false
		}
		return true
	})
}

func (g *PackageGenerator) processJSONSchemaDeclTraced(schema *JSONSchema, decl *ast.GenDecl, includedTypes map[string]bool) {
	groupComment := ""
	if decl.Doc != nil {
		groupComment = strings.TrimSpace(decl.Doc.Text())
	}

	for _, spec := range decl.Specs {
		switch sp := spec.(type) {
		case *ast.TypeSpec:
			if !sp.Name.IsExported() {
				continue
			}

			// Only include traced types
			if !includedTypes[sp.Name.Name] {
				continue
			}

			description := groupComment
			if sp.Doc != nil {
				description = strings.TrimSpace(sp.Doc.Text())
			}

			switch t := sp.Type.(type) {
			case *ast.StructType:
				schema.Definitions[sp.Name.Name] = g.structToJSONSchema(t, description)
			case *ast.Ident:
				schema.Definitions[sp.Name.Name] = g.goTypeToJSONSchema(t.Name, description)
			case *ast.ArrayType:
				schema.Definitions[sp.Name.Name] = g.arrayToJSONSchema(t, description)
			case *ast.MapType:
				schema.Definitions[sp.Name.Name] = g.mapToJSONSchema(t, description)
			}
		}
	}
}

func (g *PackageGenerator) processJSONSchemaFile(schema *JSONSchema, file *ast.File) {
	ast.Inspect(file, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.GenDecl:
			if x.Tok == token.IMPORT || x.Tok == token.VAR {
				return false
			}
			g.processJSONSchemaDecl(schema, x)
			return false
		}
		return true
	})
}

func (g *PackageGenerator) processJSONSchemaDecl(schema *JSONSchema, decl *ast.GenDecl) {
	groupComment := ""
	if decl.Doc != nil {
		groupComment = strings.TrimSpace(decl.Doc.Text())
	}

	for _, spec := range decl.Specs {
		switch sp := spec.(type) {
		case *ast.TypeSpec:
			if !sp.Name.IsExported() {
				continue
			}

			description := groupComment
			if sp.Doc != nil {
				description = strings.TrimSpace(sp.Doc.Text())
			}

			switch t := sp.Type.(type) {
			case *ast.StructType:
				schema.Definitions[sp.Name.Name] = g.structToJSONSchema(t, description)
			case *ast.Ident:
				schema.Definitions[sp.Name.Name] = g.goTypeToJSONSchema(t.Name, description)
			case *ast.ArrayType:
				schema.Definitions[sp.Name.Name] = g.arrayToJSONSchema(t, description)
			case *ast.MapType:
				schema.Definitions[sp.Name.Name] = g.mapToJSONSchema(t, description)
			}
		}
	}
}

func (g *PackageGenerator) structToJSONSchema(st *ast.StructType, description string) *Schema {
	schema := &Schema{
		Type:        "object",
		Description: description,
		Properties:  make(map[string]*Schema),
	}

	if st.Fields == nil {
		return schema
	}

	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			continue // Skip embedded fields
		}

		for _, name := range field.Names {
			if !name.IsExported() {
				continue
			}

			jsonName, required := g.getJSONFieldInfo(field)
			if jsonName == "-" {
				continue
			}
			if jsonName == "" {
				jsonName = name.Name
			}

			fieldDesc := ""
			if field.Doc != nil {
				fieldDesc = strings.TrimSpace(field.Doc.Text())
			}

			fieldSchema := g.exprToJSONSchema(field.Type, fieldDesc)
			schema.Properties[jsonName] = fieldSchema

			if required {
				schema.Required = append(schema.Required, jsonName)
			}
		}
	}

	return schema
}

func (g *PackageGenerator) exprToJSONSchema(expr ast.Expr, description string) *Schema {
	switch t := expr.(type) {
	case *ast.Ident:
		return g.goTypeToJSONSchema(t.Name, description)
	case *ast.SelectorExpr:
		return g.selectorToJSONSchema(t, description)
	case *ast.StarExpr:
		// Check for pointer type mapping first
		if sel, ok := t.X.(*ast.SelectorExpr); ok {
			ptrTypeName := fmt.Sprintf("*%s.%s", sel.X, sel.Sel.Name)
			if mapped, ok := g.conf.TypeMappings[ptrTypeName]; ok {
				return g.mappingToJSONSchema(mapped, description)
			}
		}
		// Make underlying type nullable
		s := g.exprToJSONSchema(t.X, description)
		if s.Type != nil && s.Ref == "" {
			if typeStr, ok := s.Type.(string); ok {
				s.Type = []string{typeStr, "null"}
			}
		}
		return s
	case *ast.ArrayType:
		return g.arrayToJSONSchema(t, description)
	case *ast.MapType:
		return g.mapToJSONSchema(t, description)
	case *ast.StructType:
		return g.structToJSONSchema(t, description)
	case *ast.InterfaceType:
		return &Schema{Description: description}
	default:
		return &Schema{Description: description}
	}
}

func (g *PackageGenerator) goTypeToJSONSchema(typeName string, description string) *Schema {
	if mapped, ok := g.conf.TypeMappings[typeName]; ok {
		return g.mappingToJSONSchema(mapped, description)
	}

	switch typeName {
	case "string":
		return &Schema{Type: "string", Description: description}
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return &Schema{Type: "integer", Description: description}
	case "float32", "float64":
		return &Schema{Type: "number", Description: description}
	case "bool":
		return &Schema{Type: "boolean", Description: description}
	case "any", "interface{}":
		return &Schema{Description: description}
	default:
		return &Schema{Ref: "#/$defs/" + typeName, Description: description}
	}
}

func (g *PackageGenerator) selectorToJSONSchema(sel *ast.SelectorExpr, description string) *Schema {
	typeName := fmt.Sprintf("%s.%s", sel.X, sel.Sel.Name)
	if mapped, ok := g.conf.TypeMappings[typeName]; ok {
		return g.mappingToJSONSchema(mapped, description)
	}
	return &Schema{Type: "string", Description: description}
}

func (g *PackageGenerator) arrayToJSONSchema(arr *ast.ArrayType, description string) *Schema {
	if ident, ok := arr.Elt.(*ast.Ident); ok && ident.Name == "byte" {
		if mapped, ok := g.conf.TypeMappings["[]byte"]; ok {
			return g.mappingToJSONSchema(mapped, description)
		}
		return &Schema{
			Type:            "string",
			ContentEncoding: "base64",
			Description:     description,
		}
	}

	return &Schema{
		Type:        "array",
		Description: description,
		Items:       g.exprToJSONSchema(arr.Elt, ""),
	}
}

func (g *PackageGenerator) mapToJSONSchema(m *ast.MapType, description string) *Schema {
	return &Schema{
		Type:                 "object",
		Description:          description,
		AdditionalProperties: g.exprToJSONSchema(m.Value, ""),
	}
}

func (g *PackageGenerator) mappingToJSONSchema(mapping string, description string) *Schema {
	schema := &Schema{Description: description}

	baseType, format, nullable := parseTypeMappingForSchema(mapping)

	if baseType == "any" {
		return schema
	}

	switch baseType {
	case "number":
		baseType = "number"
	case "boolean":
		baseType = "boolean"
	case "string":
		baseType = "string"
	}

	if nullable {
		schema.Type = []string{baseType, "null"}
	} else {
		schema.Type = baseType
	}

	if format != "" {
		schema.Format = format
	}

	return schema
}

// parseTypeMappingForSchema parses TypeScript type mapping format into JSON Schema components
func parseTypeMappingForSchema(mapping string) (baseType string, format string, nullable bool) {
	// Handle "null | string" or "string | null" format
	if strings.Contains(mapping, " | ") {
		parts := strings.Split(mapping, " | ")
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "null" || p == "undefined" {
				nullable = true
			} else {
				baseType, format = extractFormatHint(p)
			}
		}
		return
	}

	baseType, format = extractFormatHint(mapping)
	return
}

// extractFormatHint extracts format from TypeScript comment like "string /* RFC3339 */"
func extractFormatHint(s string) (baseType string, format string) {
	s = strings.TrimSpace(s)

	if idx := strings.Index(s, "/*"); idx != -1 {
		baseType = strings.TrimSpace(s[:idx])
		comment := s[idx:]
		if endIdx := strings.Index(comment, "*/"); endIdx != -1 {
			hint := strings.TrimSpace(comment[2:endIdx])
			hint = strings.ToLower(hint)
			switch {
			case strings.Contains(hint, "rfc3339") || strings.Contains(hint, "date-time"):
				format = "date-time"
			case strings.Contains(hint, "uuid"):
				format = "uuid"
			case strings.Contains(hint, "uri") || strings.Contains(hint, "url"):
				format = "uri"
			case strings.Contains(hint, "email"):
				format = "email"
			case strings.Contains(hint, "base64"):
				format = "byte"
			}
		}
		return
	}
	return s, ""
}

func (g *PackageGenerator) getJSONFieldInfo(field *ast.Field) (name string, required bool) {
	required = true

	if field.Tag == nil {
		return "", required
	}

	tag := reflect.StructTag(strings.Trim(field.Tag.Value, "`"))

	// Check json tag first
	jsonTag := tag.Get("json")
	if jsonTag != "" {
		parts := strings.Split(jsonTag, ",")
		name = parts[0]
		if name == "-" {
			return "-", false
		}
		for _, part := range parts[1:] {
			if part == "omitempty" {
				required = false
			}
		}
		return
	}

	// Fall back to yaml tag only if json not present
	yamlTag := tag.Get("yaml")
	if yamlTag != "" && yamlTag != "-" {
		parts := strings.Split(yamlTag, ",")
		name = parts[0]
		for _, p := range parts[1:] {
			if p == "omitempty" {
				required = false
			}
		}
	}

	return
}
