package gotypegen

import (
	"go/ast"
	"go/token"
	"strings"
)

// TypeInfo holds information about a type definition
type TypeInfo struct {
	Name       string
	File       string
	TypeSpec   *ast.TypeSpec
	GenDecl    *ast.GenDecl
	References []string // Type names this type references
}

// MethodInfo holds information about a method on a type
type MethodInfo struct {
	ReceiverType string         // The type name the method is on
	FuncDecl     *ast.FuncDecl  // The full function declaration
	File         string         // Source file
	ExternalPkgs map[string]bool // Non-stdlib packages referenced in the body
}

// TypeGraph builds a dependency graph of all types in the package
type TypeGraph struct {
	Types     map[string]*TypeInfo    // type name -> info
	FileTypes map[string][]string     // file -> type names defined in it
	Methods   map[string][]*MethodInfo // receiver type name -> methods
	Funcs     map[string]bool         // package-level function names
}

// BuildTypeGraph parses all files and builds a type dependency graph
func (g *PackageGenerator) BuildTypeGraph() *TypeGraph {
	graph := &TypeGraph{
		Types:     make(map[string]*TypeInfo),
		FileTypes: make(map[string][]string),
		Methods:   make(map[string][]*MethodInfo),
		Funcs:     make(map[string]bool),
	}

	// First pass: collect all type definitions and methods
	for i, file := range g.pkg.Syntax {
		filepath := g.GoFiles[i]
		basename := filepath

		// Collect import map for this file (ident -> path)
		fileImports := buildImportMap(file)

		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if !typeSpec.Name.IsExported() {
						continue
					}

					typeName := typeSpec.Name.Name
					info := &TypeInfo{
						Name:       typeName,
						File:       basename,
						TypeSpec:   typeSpec,
						GenDecl:    d,
						References: []string{},
					}
					info.References = collectTypeReferences(typeSpec.Type, g.conf.TypeMappings)

					graph.Types[typeName] = info
					graph.FileTypes[basename] = append(graph.FileTypes[basename], typeName)
				}

			case *ast.FuncDecl:
				if d.Recv == nil || len(d.Recv.List) == 0 {
					// Package-level function (not a method)
					if d.Name.IsExported() {
						graph.Funcs[d.Name.Name] = true
					}
					continue
				}
				if !d.Name.IsExported() {
					continue
				}

				recvType := receiverTypeName(d.Recv.List[0].Type)
				if recvType == "" {
					continue
				}

				extPkgs := collectExternalPackages(d, fileImports)
				mi := &MethodInfo{
					ReceiverType: recvType,
					FuncDecl:     d,
					File:         basename,
					ExternalPkgs: extPkgs,
				}
				graph.Methods[recvType] = append(graph.Methods[recvType], mi)
			}
		}
	}

	return graph
}

// buildImportMap returns a map from local package name to import path for a file.
func buildImportMap(file *ast.File) map[string]string {
	imports := make(map[string]string)
	for _, imp := range file.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		var name string
		if imp.Name != nil {
			name = imp.Name.Name
		} else {
			// Use last element of path as default name
			parts := strings.Split(path, "/")
			name = parts[len(parts)-1]
		}
		imports[name] = path
	}
	return imports
}

// receiverTypeName extracts the type name from a method receiver expression.
// Handles both value receivers (T) and pointer receivers (*T).
func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	case *ast.IndexExpr:
		// Generic receiver: T[K]
		return receiverTypeName(t.X)
	case *ast.IndexListExpr:
		// Generic receiver: T[K, V]
		return receiverTypeName(t.X)
	}
	return ""
}

// collectExternalPackages walks a FuncDecl and returns the set of non-stdlib
// import paths referenced via selector expressions (pkg.Ident).
func collectExternalPackages(fn *ast.FuncDecl, fileImports map[string]string) map[string]bool {
	pkgs := make(map[string]bool)

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if importPath, exists := fileImports[ident.Name]; exists {
			if !isStdlib(importPath) {
				pkgs[importPath] = true
			}
		}
		return true
	})

	// Also check the signature (params + returns) for external types
	if fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			collectExternalFromExpr(field.Type, fileImports, pkgs)
		}
	}
	if fn.Type.Results != nil {
		for _, field := range fn.Type.Results.List {
			collectExternalFromExpr(field.Type, fileImports, pkgs)
		}
	}

	return pkgs
}

// collectExternalFromExpr collects external package refs from a type expression.
func collectExternalFromExpr(expr ast.Expr, fileImports map[string]string, pkgs map[string]bool) {
	ast.Inspect(expr, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if importPath, exists := fileImports[ident.Name]; exists {
			if !isStdlib(importPath) {
				pkgs[importPath] = true
			}
		}
		return true
	})
}

// isStdlib returns true if the import path is a Go standard library package.
// Stdlib packages don't contain a dot in the first path element.
func isStdlib(importPath string) bool {
	firstSlash := strings.IndexByte(importPath, '/')
	first := importPath
	if firstSlash > 0 {
		first = importPath[:firstSlash]
	}
	return !strings.Contains(first, ".")
}

// FilterMethods returns methods on included types that compile in isolation:
// only stdlib and trace-set types in their signatures and bodies.
// Methods that reference non-traced types or package-level functions are excluded.
func FilterMethods(graph *TypeGraph, includedTypes map[string]bool) map[string][]*MethodInfo {
	result := make(map[string][]*MethodInfo)
	for typeName, methods := range graph.Methods {
		if !includedTypes[typeName] {
			continue
		}
		for _, m := range methods {
			if len(m.ExternalPkgs) > 0 {
				continue
			}
			// Check that all same-package references are resolvable
			typeRefs, funcRefs := collectLocalRefs(m.FuncDecl, graph.Types, graph.Funcs)
			allResolved := true
			for ref := range typeRefs {
				if ref == typeName {
					continue // self-reference is fine
				}
				if !includedTypes[ref] {
					allResolved = false
					break
				}
			}
			// Methods calling package-level functions can't compile standalone
			if allResolved && len(funcRefs) > 0 {
				allResolved = false
			}
			if allResolved {
				result[typeName] = append(result[typeName], m)
			}
		}
	}
	return result
}

// collectLocalRefs walks a FuncDecl's signature and body, returning
// identifiers that refer to types or functions defined in the same package.
func collectLocalRefs(fn *ast.FuncDecl, knownTypes map[string]*TypeInfo, knownFuncs map[string]bool) (typeRefs, funcRefs map[string]bool) {
	typeRefs = make(map[string]bool)
	funcRefs = make(map[string]bool)

	check := func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.Ident:
			if _, ok := knownTypes[x.Name]; ok {
				typeRefs[x.Name] = true
			}
			if knownFuncs[x.Name] {
				funcRefs[x.Name] = true
			}
		case *ast.SelectorExpr:
			// pkg.Type — skip, these are external references handled by ExternalPkgs
			return false
		}
		return true
	}

	// Walk signature
	if fn.Type.Params != nil {
		for _, field := range fn.Type.Params.List {
			ast.Inspect(field.Type, check)
		}
	}
	if fn.Type.Results != nil {
		for _, field := range fn.Type.Results.List {
			ast.Inspect(field.Type, check)
		}
	}
	// Walk body
	if fn.Body != nil {
		ast.Inspect(fn.Body, check)
	}

	return typeRefs, funcRefs
}

// collectTypeReferences extracts all type names referenced by an expression
func collectTypeReferences(expr ast.Expr, typeMappings map[string]string) []string {
	refs := []string{}

	var collect func(e ast.Expr)
	collect = func(e ast.Expr) {
		if e == nil {
			return
		}

		switch t := e.(type) {
		case *ast.Ident:
			// Skip builtin types
			if !isBuiltinType(t.Name) && t.Name != "any" {
				refs = append(refs, t.Name)
			}
		case *ast.StarExpr:
			collect(t.X)
		case *ast.ArrayType:
			collect(t.Elt)
		case *ast.MapType:
			collect(t.Key)
			collect(t.Value)
		case *ast.StructType:
			if t.Fields != nil {
				for _, field := range t.Fields.List {
					collect(field.Type)
				}
			}
		case *ast.InterfaceType:
			if t.Methods != nil {
				for _, method := range t.Methods.List {
					collect(method.Type)
				}
			}
		case *ast.SelectorExpr:
			// External package reference (e.g., time.Time) - check if mapped
			// If not mapped, we don't need to trace it
		case *ast.IndexExpr:
			// Generic type: T[U]
			collect(t.X)
			collect(t.Index)
		case *ast.IndexListExpr:
			// Generic type with multiple params: T[U, V]
			collect(t.X)
			for _, idx := range t.Indices {
				collect(idx)
			}
		}
	}

	collect(expr)
	return refs
}

// isBuiltinType returns true if the type is a Go builtin that maps to a TS primitive
func isBuiltinType(name string) bool {
	builtins := map[string]bool{
		"bool":       true,
		"string":     true,
		"int":        true,
		"int8":       true,
		"int16":      true,
		"int32":      true,
		"int64":      true,
		"uint":       true,
		"uint8":      true,
		"uint16":     true,
		"uint32":     true,
		"uint64":     true,
		"float32":    true,
		"float64":    true,
		"complex64":  true,
		"complex128": true,
		"byte":       true,
		"rune":       true,
		"error":      true,
	}
	return builtins[name]
}

// TraceTypes performs BFS from entry files to collect all reachable types
func (g *PackageGenerator) TraceTypes(graph *TypeGraph) map[string]bool {
	included := make(map[string]bool)
	queue := []string{}

	// Start with types from entry files
	for i := range g.pkg.Syntax {
		filepath := g.GoFiles[i]
		if !g.conf.IsEntryFile(filepath) {
			continue
		}

		// Add all exported types from entry files
		basename := filepath
		for _, typeName := range graph.FileTypes[basename] {
			if !included[typeName] {
				included[typeName] = true
				queue = append(queue, typeName)
			}
		}
	}

	// Add extra types specified in config
	for _, typeName := range g.conf.ExtraTypes {
		if _, exists := graph.Types[typeName]; exists && !included[typeName] {
			included[typeName] = true
			queue = append(queue, typeName)
		}
	}

	// BFS through dependencies
	for len(queue) > 0 {
		typeName := queue[0]
		queue = queue[1:]

		info, exists := graph.Types[typeName]
		if !exists {
			continue
		}

		// Add all referenced types
		for _, ref := range info.References {
			if !included[ref] {
				if _, exists := graph.Types[ref]; exists {
					included[ref] = true
					queue = append(queue, ref)
				}
			}
		}
	}

	return included
}

// TraceConstants traces constants associated with included types
func (g *PackageGenerator) TraceConstants(graph *TypeGraph, includedTypes map[string]bool) map[string]bool {
	includedConsts := make(map[string]bool)

	for i, file := range g.pkg.Syntax {
		if g.conf.IsFileIgnored(g.GoFiles[i]) {
			continue
		}

		ast.Inspect(file, func(n ast.Node) bool {
			genDecl, ok := n.(*ast.GenDecl)
			if !ok {
				return true
			}

			// Check var declarations (for const-like vars)
			for _, spec := range genDecl.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}

				// Check if this constant's type is included
				if valueSpec.Type != nil {
					if ident, ok := valueSpec.Type.(*ast.Ident); ok {
						if includedTypes[ident.Name] {
							for _, name := range valueSpec.Names {
								if name.IsExported() {
									includedConsts[name.Name] = true
								}
							}
						}
					}
				}
			}

			return true
		})
	}

	return includedConsts
}

// GenerateTraced generates output with only traced types
func (g *PackageGenerator) GenerateTraced() (string, error) {
	s := new(strings.Builder)

	g.writeFileCodegenHeader(s)
	g.writeFileFrontmatter(s)

	// Build type graph and trace dependencies
	graph := g.BuildTypeGraph()
	includedTypes := g.TraceTypes(graph)

	// Track which files we've written headers for
	writtenHeaders := make(map[string]bool)

	for i, file := range g.pkg.Syntax {
		filepath := g.GoFiles[i]
		if g.conf.IsFileIgnored(filepath) {
			continue
		}

		g.generateFileTraced(s, file, filepath, includedTypes, graph, writtenHeaders)
	}

	return s.String(), nil
}

// generateFileTraced generates output for a file, filtering to only included types
func (g *PackageGenerator) generateFileTraced(
	s *strings.Builder,
	file *ast.File,
	filepath string,
	includedTypes map[string]bool,
	graph *TypeGraph,
	writtenHeaders map[string]bool,
) {
	wroteHeader := false

	ast.Inspect(file, func(n ast.Node) bool {
		genDecl, ok := n.(*ast.GenDecl)
		if !ok {
			return true
		}

		if genDecl.Tok == token.IMPORT {
			return false
		}

		// Handle var declarations (emit vars)
		if genDecl.Tok == token.VAR {
			if g.isEmitVar(genDecl) {
				// Check if any of the vars are for included types
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
					if !wroteHeader {
						g.writeFileSourceHeader(s, filepath, file)
						wroteHeader = true
						writtenHeaders[filepath] = true
					}
					g.emitVar(s, genDecl)
				}
			}
			return false
		}

		// Handle const declarations - include constants whose type is included
		if genDecl.Tok == token.CONST {
			// Check if any const in this group has an included type
			shouldEmit := false
			var groupType string

			for _, spec := range genDecl.Specs {
				if vs, ok := spec.(*ast.ValueSpec); ok {
					// Track the group type (for iota-style declarations)
					if vs.Type != nil {
						if ident, ok := vs.Type.(*ast.Ident); ok {
							groupType = ident.Name
						}
					}
					// Check if current type (or inherited group type) is included
					if groupType != "" && includedTypes[groupType] {
						for _, name := range vs.Names {
							if name.IsExported() {
								shouldEmit = true
								break
							}
						}
					}
				}
				if shouldEmit {
					break
				}
			}

			if shouldEmit {
				if !wroteHeader {
					g.writeFileSourceHeader(s, filepath, file)
					wroteHeader = true
					writtenHeaders[filepath] = true
				}
				g.writeGroupDecl(s, genDecl)
			}
			return false
		}

		// Handle type declarations
		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}

			if !typeSpec.Name.IsExported() {
				continue
			}

			// Only emit if this type is included
			if !includedTypes[typeSpec.Name.Name] {
				continue
			}

			if !wroteHeader {
				g.writeFileSourceHeader(s, filepath, file)
				wroteHeader = true
				writtenHeaders[filepath] = true
			}

			// Create a single-spec GenDecl for this type
			singleDecl := &ast.GenDecl{
				Doc:    genDecl.Doc,
				TokPos: genDecl.TokPos,
				Tok:    genDecl.Tok,
				Specs:  []ast.Spec{spec},
			}
			g.writeGroupDecl(s, singleDecl)
		}

		return false
	})
}
