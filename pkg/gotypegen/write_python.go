package gotypegen

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"regexp"
	"strings"

	"github.com/fatih/structtag"
	"golang.org/x/tools/go/packages"
)

var validPyNameRegexp = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// enumMemberInfo stores the Python enum type and member name for a Go const
type enumMemberInfo struct {
	typeName   string
	memberName string
}

// GeneratePython generates Python type definitions using TypedDict
func (g *PackageGenerator) GeneratePython() (string, error) {
	// Use traced generation if in trace mode
	if g.conf.IsTraceMode() {
		return g.GeneratePythonTraced()
	}

	s := new(strings.Builder)

	g.writePyHeader(s)

	// Track seen type names to skip duplicates
	seenTypes := make(map[string]bool)
	// String/int type aliases become StrEnum/IntEnum
	stringTypeAliases, intTypeAliases := g.collectScalarAliases()

	// Second pass: build enum member map (Go const name -> Python enum reference)
	enumMembers := g.buildEnumMemberMap(stringTypeAliases, intTypeAliases)

	// Collect all type entries and const entries
	var typeEntries []pyTypeEntry
	var constEntries []pyConstEntry

	for i, file := range g.pkg.Syntax {
		if g.conf.IsFileIgnored(g.GoFiles[i]) {
			continue
		}

		filename := g.GoFiles[i]

		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}

			if genDecl.Tok == token.TYPE {
				for _, spec := range genDecl.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok && ts.Name.IsExported() {
						if seenTypes[ts.Name.Name] {
							continue
						}
						seenTypes[ts.Name.Name] = true
						if stringTypeAliases[ts.Name.Name] || intTypeAliases[ts.Name.Name] {
							continue
						}

						doc := specDoc(ts, genDecl)

						var deps []string
						switch t := ts.Type.(type) {
						case *ast.StructType:
							if t.Fields != nil {
								deps = g.collectPyParentTypes(t.Fields.List)
							}
						case *ast.Ident:
							if !isBuiltinType(t.Name) && t.Name != "any" {
								deps = []string{t.Name}
							}
						}

						typeEntries = append(typeEntries, pyTypeEntry{
							spec:    ts,
							doc:     doc,
							file:    filename,
							parents: deps,
						})
					}
				}
			} else if genDecl.Tok == token.CONST {
				constEntries = append(constEntries, pyConstEntry{decl: genDecl, file: filename})
			}
		}
	}

	// Topologically sort types so parents are emitted before children
	typeEntries = topSortTypes(typeEntries)

	// Emit types
	lastFile := ""
	for _, entry := range typeEntries {
		if entry.file != lastFile {
			parts := strings.Split(entry.file, "/")
			fileComment := fmt.Sprintf("\n##########\n# source: %s\n\n", parts[len(parts)-1])
			s.WriteString(fileComment)
			lastFile = entry.file
		}
		g.writePyType(s, entry.spec, entry.doc)
	}

	// Emit constants
	g.writePyConstEntries(s, constEntries, stringTypeAliases, intTypeAliases, enumMembers, nil, nil)

	// Pydantic: rebuild models to resolve forward references
	if g.conf.IsPydantic() {
		g.writePyModelRebuilds(s, typeEntries, stringTypeAliases, intTypeAliases)
	}

	return s.String(), nil
}

// buildEnumMemberMap creates a mapping from Go const names to Python enum references
func (g *PackageGenerator) buildEnumMemberMap(stringTypeAliases, intTypeAliases map[string]bool) map[string]enumMemberInfo {
	enumMembers := make(map[string]enumMemberInfo)

	for i, file := range g.pkg.Syntax {
		if g.conf.IsFileIgnored(g.GoFiles[i]) {
			continue
		}
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.CONST {
				continue
			}

			for _, c := range pyConstsOf(genDecl) {
				typeName := constEnumType(c, stringTypeAliases, intTypeAliases)
				if typeName == "" {
					continue
				}
				memberName := toUpperSnake(enumMemberBase(c.name, typeName))
				enumMembers[c.name] = enumMemberInfo{typeName, memberName}
			}
		}
	}

	return enumMembers
}

// pyTypeEntry holds a collected type spec with its metadata for ordering
type pyTypeEntry struct {
	spec    *ast.TypeSpec
	doc     string
	file    string
	parents []string // parent type names from tstype:",extends"
}

// pyConstEntry holds a collected const decl for emission
type pyConstEntry struct {
	decl *ast.GenDecl
	file string
}

// topSortTypes performs a topological sort on types so parents are emitted before children.
// Falls back to original order for types without parent dependencies.
func topSortTypes(entries []pyTypeEntry) []pyTypeEntry {
	// Build name -> index map
	nameIdx := make(map[string]int, len(entries))
	for i, e := range entries {
		nameIdx[e.spec.Name.Name] = i
	}

	// Kahn's algorithm
	inDegree := make([]int, len(entries))
	children := make([][]int, len(entries))
	for i, e := range entries {
		for _, p := range e.parents {
			if pi, ok := nameIdx[p]; ok {
				inDegree[i]++
				children[pi] = append(children[pi], i)
			}
		}
	}

	// Seed queue with zero in-degree entries (in original order for stability)
	queue := make([]int, 0, len(entries))
	for i, deg := range inDegree {
		if deg == 0 {
			queue = append(queue, i)
		}
	}

	sorted := make([]pyTypeEntry, 0, len(entries))
	for len(queue) > 0 {
		idx := queue[0]
		queue = queue[1:]
		sorted = append(sorted, entries[idx])
		for _, child := range children[idx] {
			inDegree[child]--
			if inDegree[child] == 0 {
				queue = append(queue, child)
			}
		}
	}

	// Append any remaining (cycle) entries at the end
	if len(sorted) < len(entries) {
		for i, deg := range inDegree {
			if deg > 0 {
				sorted = append(sorted, entries[i])
			}
		}
	}

	return sorted
}

// GeneratePythonTraced generates Python types with dependency tracing
func (g *PackageGenerator) GeneratePythonTraced() (string, error) {
	s := new(strings.Builder)

	g.writePyHeader(s)

	// Build type graph and trace dependencies
	graph := g.BuildTypeGraph()
	includedTypes := g.TraceTypes(graph)

	// Track seen type names to skip duplicates
	seenTypes := make(map[string]bool)
	// String/int type aliases become StrEnum/IntEnum
	stringTypeAliases, intTypeAliases := g.collectScalarAliases()

	// Build enum member map (Go const name -> Python enum reference)
	enumMembers := g.buildEnumMemberMap(stringTypeAliases, intTypeAliases)

	// Collect all type entries and const entries
	var typeEntries []pyTypeEntry
	var constEntries []pyConstEntry

	for i, file := range g.pkg.Syntax {
		if g.conf.IsFileIgnored(g.GoFiles[i]) {
			continue
		}

		filename := g.GoFiles[i]

		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}

			if genDecl.Tok == token.TYPE {
				for _, spec := range genDecl.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || !ts.Name.IsExported() {
						continue
					}
					if !includedTypes[ts.Name.Name] {
						continue
					}
					if seenTypes[ts.Name.Name] {
						continue
					}
					seenTypes[ts.Name.Name] = true
					if stringTypeAliases[ts.Name.Name] || intTypeAliases[ts.Name.Name] {
						continue
					}

					doc := specDoc(ts, genDecl)

					var deps []string
					switch t := ts.Type.(type) {
					case *ast.StructType:
						if t.Fields != nil {
							deps = g.collectPyParentTypes(t.Fields.List)
						}
					case *ast.Ident:
						// Type alias (e.g. type Foo = Bar) depends on target
						if !isBuiltinType(t.Name) && t.Name != "any" {
							deps = []string{t.Name}
						}
					}

					typeEntries = append(typeEntries, pyTypeEntry{
						spec:    ts,
						doc:     doc,
						file:    filename,
						parents: deps,
					})
				}
			} else if genDecl.Tok == token.CONST {
				shouldEmit := false
				for _, spec := range genDecl.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok && vs.Type != nil {
						if ident, ok := vs.Type.(*ast.Ident); ok {
							if includedTypes[ident.Name] {
								shouldEmit = true
								break
							}
						}
					}
				}
				if shouldEmit {
					constEntries = append(constEntries, pyConstEntry{decl: genDecl, file: filename})
				}
			}
		}
	}

	// Topologically sort types so parents/targets are emitted before dependents
	typeEntries = topSortTypes(typeEntries)

	// Emit types
	writtenHeaders := make(map[string]bool)
	lastFile := ""
	for _, entry := range typeEntries {
		if entry.file != lastFile {
			if !writtenHeaders[entry.file] {
				parts := strings.Split(entry.file, "/")
				fileComment := fmt.Sprintf("\n##########\n# source: %s\n\n", parts[len(parts)-1])
				s.WriteString(fileComment)
				writtenHeaders[entry.file] = true
			}
			lastFile = entry.file
		}
		g.writePyType(s, entry.spec, entry.doc)
	}

	// Emit constants
	g.writePyConstEntries(s, constEntries, stringTypeAliases, intTypeAliases, enumMembers, includedTypes, writtenHeaders)

	// Pydantic: rebuild models to resolve forward references
	if g.conf.IsPydantic() {
		g.writePyModelRebuilds(s, typeEntries, stringTypeAliases, intTypeAliases)
	}

	return s.String(), nil
}

func (g *PackageGenerator) writePyHeader(s *strings.Builder) {
	s.WriteString("# Code generated by gotypegen. DO NOT EDIT.\n\n")
	s.WriteString("from __future__ import annotations\n\n")
	s.WriteString("from enum import Enum, IntEnum\n")
	if g.conf.IsPydantic() {
		s.WriteString("from typing import Any, ClassVar, Dict, List, Optional, Union\n")
		s.WriteString("from pydantic import BaseModel\n")
	} else {
		s.WriteString("from typing import Any, Dict, List, Optional, TypedDict, Union\n")
	}
	s.WriteString("from datetime import datetime\n\n")
}

func (g *PackageGenerator) writePyType(s *strings.Builder, ts *ast.TypeSpec, doc string) {
	switch t := ts.Type.(type) {
	case *ast.Ident:
		g.writePyTypeAlias(s, ts.Name.Name, t, doc)
	case *ast.StructType:
		g.writePyTypedDict(s, ts.Name.Name, t, doc)
	case *ast.ArrayType:
		g.writePyArrayAlias(s, ts.Name.Name, t, doc)
	case *ast.MapType:
		g.writePyMapAlias(s, ts.Name.Name, t, doc)
	}
}

func (g *PackageGenerator) writePyTypeAlias(s *strings.Builder, name string, ident *ast.Ident, doc string) {
	if doc != "" {
		s.WriteString("# ")
		s.WriteString(strings.ReplaceAll(doc, "\n", "\n# "))
		s.WriteString("\n")
	}
	// Use base Go type directly (don't apply type mappings for type definitions)
	pyType := goBaseToPython(ident.Name)
	s.WriteString(name)
	s.WriteString(" = ")
	s.WriteString(pyType)
	s.WriteString("\n\n")
}

func (g *PackageGenerator) writePyArrayAlias(s *strings.Builder, name string, arr *ast.ArrayType, doc string) {
	if doc != "" {
		s.WriteString("# ")
		s.WriteString(strings.ReplaceAll(doc, "\n", "\n# "))
		s.WriteString("\n")
	}
	s.WriteString(name)
	s.WriteString(" = ")
	s.WriteString(g.exprToPythonTypeQuoted(arr))
	s.WriteString("\n\n")
}

func (g *PackageGenerator) writePyMapAlias(s *strings.Builder, name string, m *ast.MapType, doc string) {
	if doc != "" {
		s.WriteString("# ")
		s.WriteString(strings.ReplaceAll(doc, "\n", "\n# "))
		s.WriteString("\n")
	}
	s.WriteString(name)
	s.WriteString(" = ")
	s.WriteString(g.exprToPythonTypeQuoted(m))
	s.WriteString("\n\n")
}

// pyFieldInfo holds the resolved names for a single struct field.
type pyFieldInfo struct {
	jsonName   string
	pyName     string
	pyType     string
	doc        string
	needsAlias bool
	// keyword: jsonName is a Python keyword (e.g. "async"). A class-form
	// TypedDict would have to rename the key (async_), which changes the wire
	// key, so TypedDict output uses the functional form for it.
	keyword bool
	tags    map[string]string // configured field_tags values (e.g. "merge" -> "concat")
}

func (g *PackageGenerator) writePyTypedDict(s *strings.Builder, name string, st *ast.StructType, doc string) {
	if doc != "" {
		s.WriteString("# ")
		s.WriteString(strings.ReplaceAll(doc, "\n", "\n# "))
		s.WriteString("\n")
	}

	pydantic := g.conf.IsPydantic()

	// A pydantic class inherits a parent's fields *before* its own, whatever
	// the Go source says, so the pydantic writer inlines a tstype:",extends"
	// parent at the embed's position instead: the generated field order is
	// the source order (and so is the JSON schema pydantic derives from it).
	// A parent with no fields (a marker) and one that cannot be resolved are
	// still inherited; see expandPydanticEmbeds.
	parentFields := st.Fields.List
	if pydantic {
		parentFields = g.expandPydanticEmbeds(st.Fields.List)
	}
	parents := g.collectPyParentTypes(parentFields)

	// Collect fields and check if any need the functional TypedDict form.
	fields := g.collectPyFields(st)
	needsFunctional := false
	for _, f := range fields {
		if (f.needsAlias || f.keyword) && !pydantic {
			needsFunctional = true
			break
		}
	}

	if needsFunctional {
		g.writePyFunctionalTypedDict(s, name, fields, parents)
		return
	}

	s.WriteString("class ")
	s.WriteString(name)
	s.WriteString("(")
	for _, p := range parents {
		s.WriteString(p)
		s.WriteString(", ")
	}
	if pydantic {
		s.WriteString("BaseModel):\n")
	} else {
		s.WriteString("TypedDict, total=False):\n")
	}

	if len(fields) == 0 {
		s.WriteString("    pass\n\n")
		return
	}

	for _, f := range fields {
		if f.doc != "" {
			s.WriteString("    # ")
			s.WriteString(strings.ReplaceAll(f.doc, "\n", "\n    # "))
			s.WriteString("\n")
		}

		s.WriteString("    ")
		s.WriteString(f.pyName)
		s.WriteString(": ")

		isOptional := strings.HasPrefix(f.pyType, "Optional[")

		if pydantic && f.needsAlias {
			s.WriteString(f.pyType)
			s.WriteString(" = Field(")
			if isOptional {
				s.WriteString("default=None, ")
			} else if zv := pyZeroValue(f.pyType); zv != "" {
				s.WriteString("default=")
				s.WriteString(zv)
				s.WriteString(", ")
			}
			s.WriteString("alias=\"")
			s.WriteString(f.jsonName)
			s.WriteString("\")")
		} else {
			s.WriteString(f.pyType)
			if pydantic {
				if isOptional {
					s.WriteString(" = None")
				} else if zv := pyZeroValue(f.pyType); zv != "" {
					s.WriteString(" = ")
					s.WriteString(zv)
				}
			}
		}

		s.WriteString("\n")
	}

	if pydantic {
		// Emit _field_tags if any fields have configured tags
		g.writePyFieldTags(s, name, fields)
		s.WriteString("\n")
		return
	}
	s.WriteString("\n")
	g.writePyFieldTags(s, name, fields)
}

// writePyFunctionalTypedDict emits the functional form:
//
//	Name = TypedDict('Name', {'non.identifier': Type, ...}, total=False)
//
// Used when a struct has JSON tags that are not valid Python identifiers.
func (g *PackageGenerator) writePyFunctionalTypedDict(s *strings.Builder, name string, fields []pyFieldInfo, parents []string) {
	if len(parents) > 0 {
		// Functional form doesn't support inheritance directly; emit a base
		// class and inherit from it plus the functional dict.
		s.WriteString(name)
		s.WriteString(" = TypedDict('")
		s.WriteString(name)
		s.WriteString("', {")
	} else {
		s.WriteString(name)
		s.WriteString(" = TypedDict('")
		s.WriteString(name)
		s.WriteString("', {")
	}

	if len(fields) == 0 {
		s.WriteString("}, total=False)\n\n")
		return
	}

	defer g.writePyFieldTags(s, name, fields)

	s.WriteString("\n")
	for _, f := range fields {
		if f.doc != "" {
			s.WriteString("    # ")
			s.WriteString(strings.ReplaceAll(f.doc, "\n", "\n    # "))
			s.WriteString("\n")
		}
		s.WriteString("    '")
		s.WriteString(f.jsonName)
		s.WriteString("': '")
		s.WriteString(f.pyType)
		s.WriteString("',\n")
	}
	s.WriteString("}, total=False)\n\n")
}

// collectPyFields extracts the field list for a struct, resolving JSON names
// and detecting fields that need aliasing.
func (g *PackageGenerator) collectPyFields(st *ast.StructType) []pyFieldInfo {
	if st.Fields == nil {
		return nil
	}
	var fields []pyFieldInfo
	expanded := g.expandInlineFields(st.Fields.List)
	if g.conf.IsPydantic() {
		// pydantic: extends parents are inlined in place too, see writePyTypedDict.
		expanded = g.expandPydanticEmbeds(st.Fields.List)
	}
	for _, field := range expanded {
		names := field.Names
		if len(names) == 0 {
			// Anonymous field that survived expansion: either a json-named
			// embed (emitted as a nested field, as encoding/json does) or an
			// extends/"-" directive handled elsewhere.
			if g.isInlineEmbed(field) {
				continue
			}
			name, ok := getAnonymousFieldName(field.Type)
			if !ok {
				continue
			}
			if jsonName, _ := g.getPyFieldInfo(field); jsonName == "" || jsonName == "-" {
				continue
			}
			names = []*ast.Ident{ast.NewIdent(name)}
		}
		for _, fieldName := range names {
			if !fieldName.IsExported() {
				continue
			}

			jsonName, omitempty := g.getPyFieldInfo(field)
			if jsonName == "-" {
				continue
			}
			if jsonName == "" {
				jsonName = fieldName.Name
			}

			pyName := jsonName
			needsAlias := false
			keyword := false
			if !validPyNameRegexp.MatchString(jsonName) {
				pyName = sanitizePyName(jsonName)
				needsAlias = true
			} else if isPythonKeyword(jsonName) {
				pyName = jsonName + "_"
				keyword = true
			}

			var docStr string
			if field.Doc != nil {
				docStr = strings.TrimSpace(field.Doc.Text())
			}

			fields = append(fields, pyFieldInfo{
				jsonName:   jsonName,
				pyName:     pyName,
				pyType:     g.pyFieldType(field.Type, omitempty),
				doc:        docStr,
				needsAlias: needsAlias,
				keyword:    keyword,
				tags:       g.configuredFieldTags(field),
			})
		}
	}
	return fields
}

// sanitizePyName replaces characters invalid in Python identifiers with
// underscores and ensures the result starts with a letter or underscore.
func sanitizePyName(name string) string {
	var b strings.Builder
	for _, r := range name {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r == '_' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	s := b.String()
	if s == "" || (s[0] >= '0' && s[0] <= '9') {
		s = "_" + s
	}
	return s
}

// writePyFieldTags emits the _field_tags metadata: configured struct tag
// values for each field. Only emitted when at least one field has a tag from
// the field_tags config.
//
// pydantic: a `_field_tags: ClassVar[dict]` class variable inside the class.
// TypedDict: a TypedDict body may only declare keys (type checkers reject a
// value there, and ClassVar inside a TypedDict), so the dict is set on the
// class after its definition, written right after it. The runtime result is
// the same class attribute: Name.__dict__["_field_tags"].
func (g *PackageGenerator) writePyFieldTags(s *strings.Builder, typeName string, fields []pyFieldInfo) {
	// Check if any fields have tag metadata
	hasMeta := false
	for _, f := range fields {
		if len(f.tags) > 0 {
			hasMeta = true
			break
		}
	}
	if !hasMeta {
		return
	}

	indent := ""
	if g.conf.IsPydantic() {
		indent = "    "
		s.WriteString("\n    _field_tags: ClassVar[dict] = {\n")
	} else {
		s.WriteString(typeName)
		s.WriteString("._field_tags = {  # type: ignore[attr-defined]\n")
	}
	for _, f := range fields {
		if len(f.tags) == 0 {
			continue
		}
		s.WriteString(indent)
		s.WriteString("    \"")
		s.WriteString(f.jsonName)
		s.WriteString("\": {")
		first := true
		for _, tagKey := range g.conf.FieldTags {
			val, ok := f.tags[tagKey]
			if !ok {
				continue
			}
			if !first {
				s.WriteString(", ")
			}
			s.WriteString("\"")
			s.WriteString(tagKey)
			s.WriteString("\": \"")
			s.WriteString(val)
			s.WriteString("\"")
			first = false
		}
		s.WriteString("},\n")
	}
	s.WriteString(indent)
	s.WriteString("}\n")
	if !g.conf.IsPydantic() {
		s.WriteString("\n")
	}
}

// collectPyParentTypes extracts parent type names from embedded fields
// that have a tstype:",extends" tag, mirroring the TypeScript inheritance behavior.
func (g *PackageGenerator) collectPyParentTypes(fields []*ast.Field) []string {
	var parents []string
	for _, f := range fields {
		if f.Type == nil || f.Tag == nil {
			continue
		}
		tags, err := structtag.Parse(f.Tag.Value[1 : len(f.Tag.Value)-1])
		if err != nil {
			continue
		}
		tstypeTag, err := tags.Get("tstype")
		if err != nil || !tstypeTag.HasOption("extends") {
			continue
		}
		name, valid := getAnonymousFieldName(f.Type)
		if valid {
			parents = append(parents, name)
		}
	}
	return parents
}

// toUpperSnake converts CamelCase to UPPER_SNAKE_CASE.
// Consecutive uppercase runs (acronyms) are kept together:
//
//	"HTTP" -> "HTTP", "MCP" -> "MCP", "HTTPServer" -> "HTTP_SERVER",
//	"ToolType" -> "TOOL_TYPE", "getHTTPResponse" -> "GET_HTTP_RESPONSE"
func toUpperSnake(s string) string {
	runes := []rune(s)
	var result strings.Builder
	for i, r := range runes {
		if i > 0 && r >= 'A' && r <= 'Z' {
			prev := runes[i-1]
			// Insert _ when transitioning from lowercase to uppercase (e.g. tool|T)
			if prev >= 'a' && prev <= 'z' {
				result.WriteByte('_')
			} else if prev >= 'A' && prev <= 'Z' && i+1 < len(runes) && runes[i+1] >= 'a' && runes[i+1] <= 'z' {
				// Insert _ before the last letter of an acronym followed by lowercase
				// e.g. HT|Tp in "HTTPServer" -> HTTP_SERVER
				result.WriteByte('_')
			}
		}
		if r >= 'a' && r <= 'z' {
			result.WriteByte(byte(r - 'a' + 'A'))
		} else {
			result.WriteRune(r)
		}
	}
	name := result.String()
	if len(name) > 0 && name[0] >= '0' && name[0] <= '9' {
		name = "_" + name
	}
	return name
}

func (g *PackageGenerator) getPyConstValue(expr ast.Expr, iotaValue int, enumMembers map[string]enumMemberInfo) string {
	switch v := expr.(type) {
	case *ast.BasicLit:
		return v.Value
	case *ast.Ident:
		if v.Name == "iota" {
			return fmt.Sprintf("%d", iotaValue)
		}
		if info, ok := enumMembers[v.Name]; ok {
			return fmt.Sprintf("%s.%s", info.typeName, info.memberName)
		}
		return v.Name
	case *ast.BinaryExpr:
		left := g.getPyConstValue(v.X, iotaValue, enumMembers)
		right := g.getPyConstValue(v.Y, iotaValue, enumMembers)
		return fmt.Sprintf("%s %s %s", left, v.Op.String(), right)
	case *ast.UnaryExpr:
		val := g.getPyConstValue(v.X, iotaValue, enumMembers)
		return fmt.Sprintf("%s%s", v.Op.String(), val)
	case *ast.ParenExpr:
		val := g.getPyConstValue(v.X, iotaValue, enumMembers)
		return fmt.Sprintf("(%s)", val)
	default:
		return fmt.Sprintf("%d", iotaValue)
	}
}

func (g *PackageGenerator) exprToPythonTypeQuoted(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		pyType := g.goTypeToPython(t.Name)
		if pyType != "str" && pyType != "int" && pyType != "float" && pyType != "bool" && pyType != "Any" && pyType != "bytes" {
			return fmt.Sprintf("\"%s\"", pyType)
		}
		return pyType
	case *ast.SelectorExpr:
		typeName := fmt.Sprintf("%s.%s", t.X, t.Sel.Name)
		if mapped, ok := g.conf.TypeMappings[typeName]; ok {
			return g.tsMappingToPython(mapped)
		}
		return "Any"
	case *ast.StarExpr:
		inner := g.exprToPythonTypeQuoted(t.X)
		return fmt.Sprintf("Optional[%s]", inner)
	case *ast.ArrayType:
		if ident, ok := t.Elt.(*ast.Ident); ok && ident.Name == "byte" {
			return "bytes"
		}
		inner := g.exprToPythonTypeQuoted(t.Elt)
		return fmt.Sprintf("List[%s]", inner)
	case *ast.MapType:
		keyType := g.exprToPythonTypeQuoted(t.Key)
		valType := g.exprToPythonTypeQuoted(t.Value)
		return fmt.Sprintf("Dict[%s, %s]", keyType, valType)
	case *ast.InterfaceType:
		return "Any"
	case *ast.StructType:
		return "Dict[str, Any]"
	default:
		return "Any"
	}
}

func (g *PackageGenerator) exprToPythonType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return g.goTypeToPython(t.Name)
	case *ast.SelectorExpr:
		typeName := fmt.Sprintf("%s.%s", t.X, t.Sel.Name)
		if mapped, ok := g.conf.TypeMappings[typeName]; ok {
			return g.tsMappingToPython(mapped)
		}
		return "Any"
	case *ast.StarExpr:
		inner := g.exprToPythonType(t.X)
		return fmt.Sprintf("Optional[%s]", inner)
	case *ast.ArrayType:
		if ident, ok := t.Elt.(*ast.Ident); ok && ident.Name == "byte" {
			return "bytes"
		}
		inner := g.exprToPythonType(t.Elt)
		return fmt.Sprintf("List[%s]", inner)
	case *ast.MapType:
		keyType := g.exprToPythonType(t.Key)
		valType := g.exprToPythonType(t.Value)
		return fmt.Sprintf("Dict[%s, %s]", keyType, valType)
	case *ast.InterfaceType:
		return "Any"
	case *ast.StructType:
		return "Dict[str, Any]"
	default:
		return "Any"
	}
}

func goBaseToPython(goType string) string {
	switch goType {
	case "string":
		return "str"
	case "float32", "float64":
		return "float"
	case "bool":
		return "bool"
	case "any", "interface{}":
		return "Any"
	}
	if isGoIntType(goType) {
		return "int"
	}
	return goType
}

func (g *PackageGenerator) goTypeToPython(goType string) string {
	if mapped, ok := g.conf.TypeMappings[goType]; ok {
		return g.tsMappingToPython(mapped)
	}

	switch goType {
	case "string":
		return "str"
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return "int"
	case "float32", "float64":
		return "float"
	case "bool":
		return "bool"
	case "any", "interface{}":
		return "Any"
	default:
		// Generic type parameters (single uppercase letter like T, K, V) -> Any
		if len(goType) == 1 && goType[0] >= 'A' && goType[0] <= 'Z' {
			return "Any"
		}
		return goType
	}
}

func (g *PackageGenerator) tsMappingToPython(mapping string) string {
	baseType, _, nullable := parseTypeMappingForSchema(mapping)

	var pyType string
	switch baseType {
	case "string":
		pyType = "str"
	case "number":
		pyType = "float"
	case "boolean":
		pyType = "bool"
	case "any":
		pyType = "Any"
	default:
		pyType = "Any"
	}

	if nullable {
		return fmt.Sprintf("Optional[%s]", pyType)
	}
	return pyType
}

// writePyModelRebuilds emits model_rebuild() calls for all pydantic BaseModel
// classes to resolve forward references created by `from __future__ import annotations`.
func (g *PackageGenerator) writePyModelRebuilds(s *strings.Builder, entries []pyTypeEntry, stringAliases, intAliases map[string]bool) {
	var models []string
	for _, e := range entries {
		name := e.spec.Name.Name
		if stringAliases[name] || intAliases[name] {
			continue
		}
		if _, ok := e.spec.Type.(*ast.StructType); ok {
			models = append(models, name)
		}
	}
	if len(models) == 0 {
		return
	}
	s.WriteString("\n# Resolve forward references\n")
	for _, m := range models {
		s.WriteString(m)
		s.WriteString(".model_rebuild()\n")
	}
	s.WriteString("\n")
}

// pyZeroValue returns the Python default literal for scalar types that
// correspond to Go value types (non-pointer). Returns "" for complex
// types (lists, dicts, custom classes) which stay required.
// pyFieldType is the Python type of a struct field. In pydantic mode an
// omitempty enum, slice or map is Optional: Go leaves it off the wire when
// empty, so a model that required it would reject what Go sends. Scalars keep
// their zero-value default, and structs stay required because encoding/json
// never omits a struct.
func (g *PackageGenerator) pyFieldType(expr ast.Expr, omitempty bool) string {
	pyType := g.exprToPythonType(expr)
	if !omitempty || !g.conf.IsPydantic() || strings.HasPrefix(pyType, "Optional[") || pyZeroValue(pyType) != "" {
		return pyType
	}
	if g.isStructExpr(expr) {
		return pyType
	}
	return fmt.Sprintf("Optional[%s]", pyType)
}

// isStructExpr reports whether expr has a struct underlying type, looking in
// the generated package and the packages inlined into it.
func (g *PackageGenerator) isStructExpr(expr ast.Expr) bool {
	pkgs := append([]*packages.Package{g.pkg}, g.inlinePkgs...)
	for _, pkg := range pkgs {
		if pkg == nil || pkg.TypesInfo == nil {
			continue
		}
		if t := pkg.TypesInfo.TypeOf(expr); t != nil {
			_, ok := t.Underlying().(*types.Struct)
			return ok
		}
	}
	return false
}

func pyZeroValue(pyType string) string {
	switch pyType {
	case "str":
		return `""`
	case "int":
		return "0"
	case "float":
		return "0.0"
	case "bool":
		return "False"
	default:
		return ""
	}
}

var pythonKeywords = map[string]bool{
	"False": true, "None": true, "True": true, "and": true, "as": true,
	"assert": true, "async": true, "await": true, "break": true, "class": true,
	"continue": true, "def": true, "del": true, "elif": true, "else": true,
	"except": true, "finally": true, "for": true, "from": true, "global": true,
	"if": true, "import": true, "in": true, "is": true, "lambda": true,
	"nonlocal": true, "not": true, "or": true, "pass": true, "raise": true,
	"return": true, "try": true, "while": true, "with": true, "yield": true,
}

func isPythonKeyword(s string) bool {
	return pythonKeywords[s]
}

func (g *PackageGenerator) getPyFieldInfo(field *ast.Field) (name string, optional bool) {
	if field.Tag == nil {
		return "", false
	}

	tags, err := structtag.Parse(field.Tag.Value[1 : len(field.Tag.Value)-1])
	if err != nil {
		return "", false
	}

	if jsonTag, err := tags.Get("json"); err == nil {
		name = jsonTag.Name
		if name == "-" {
			return "-", false
		}
		optional = jsonTag.HasOption("omitempty")
		return
	}

	if yamlTag, err := tags.Get("yaml"); err == nil && yamlTag.Name != "-" {
		name = yamlTag.Name
		optional = yamlTag.HasOption("omitempty")
	}

	return
}
