package gotypegen

import (
	"go/ast"
	"go/token"
	"go/types"
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
	ReceiverType string        // The type name the method is on
	FuncDecl     *ast.FuncDecl // The full function declaration
	File         string        // Source file
}

// TypeGraph builds a dependency graph of all types in the package
type TypeGraph struct {
	Types     map[string]*TypeInfo    // type name -> info
	FileTypes map[string][]string     // file -> type names defined in it
	Methods   map[string][]*MethodInfo // receiver type name -> methods
}

// BuildTypeGraph parses all files and builds a type dependency graph
func (g *PackageGenerator) BuildTypeGraph() *TypeGraph {
	graph := &TypeGraph{
		Types:   make(map[string]*TypeInfo),
		FileTypes: make(map[string][]string),
		Methods: make(map[string][]*MethodInfo),
	}

	// Build set of inline package local names for reference tracing
	inlineLocalNames := g.buildInlineLocalNames()

	for i, file := range g.pkg.Syntax {
		filepath := g.GoFiles[i]

		for _, decl := range file.Decls {
			switch d := decl.(type) {
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || !ts.Name.IsExported() {
						continue
					}

					typeName := ts.Name.Name
					info := &TypeInfo{
						Name:       typeName,
						File:       filepath,
						TypeSpec:   ts,
						GenDecl:    d,
						References: collectTypeReferences(ts.Type, g.conf.TypeMappings, inlineLocalNames),
					}
					graph.Types[typeName] = info
					graph.FileTypes[filepath] = append(graph.FileTypes[filepath], typeName)
				}

			case *ast.FuncDecl:
				if d.Recv == nil || len(d.Recv.List) == 0 {
					continue // package-level function, not a method
				}
				if !d.Name.IsExported() {
					continue
				}
				recvType := receiverTypeName(d.Recv.List[0].Type)
				if recvType == "" {
					continue
				}
				mi := &MethodInfo{
					ReceiverType: recvType,
					FuncDecl:     d,
					File:         filepath,
				}
				graph.Methods[recvType] = append(graph.Methods[recvType], mi)
			}
		}
	}

	return graph
}

// buildInlineLocalNames returns a set of local package names that are inlined.
// E.g., if InlinePackages contains "foo/bar/shared", this returns {"shared": true}.
func (g *PackageGenerator) buildInlineLocalNames() map[string]bool {
	if len(g.conf.InlinePackages) == 0 {
		return nil
	}
	names := make(map[string]bool)
	for _, path := range g.conf.InlinePackages {
		parts := strings.Split(path, "/")
		names[parts[len(parts)-1]] = true
	}
	return names
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
		return receiverTypeName(t.X)
	case *ast.IndexListExpr:
		return receiverTypeName(t.X)
	}
	return ""
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

// FilterMethods returns methods on included types that compile in isolation.
// Uses go/types for exact identifier resolution instead of name-matching heuristics.
// Iteratively filters: if method A calls method B which is filtered, A is also filtered.
// Optional allowedPkgPaths are external packages whose types are considered available (e.g., inlined packages).
func FilterMethods(graph *TypeGraph, includedTypes map[string]bool, info *types.Info, pkgScope *types.Scope, allowedPkgPaths ...map[string]bool) map[string][]*MethodInfo {
	var allowed map[string]bool
	if len(allowedPkgPaths) > 0 {
		allowed = allowedPkgPaths[0]
	}

	// First pass: check each method can compile standalone
	result := make(map[string][]*MethodInfo)
	for typeName, methods := range graph.Methods {
		if !includedTypes[typeName] {
			continue
		}
		for _, m := range methods {
			if methodCanCompile(m.FuncDecl, includedTypes, info, pkgScope, allowed) {
				result[typeName] = append(result[typeName], m)
			}
		}
	}

	// Iterative pass: filter methods that call other methods not in the result set.
	for {
		changed := false
		emittedMethods := buildMethodSet(result)
		for typeName, methods := range result {
			var kept []*MethodInfo
			for _, m := range methods {
				calls := collectMethodCalls(m.FuncDecl, info, pkgScope)
				allPresent := true
				for _, call := range calls {
					if !emittedMethods[call] {
						allPresent = false
						break
					}
				}
				if allPresent {
					kept = append(kept, m)
				} else {
					changed = true
				}
			}
			if len(kept) == 0 {
				delete(result, typeName)
			} else {
				result[typeName] = kept
			}
		}
		if !changed {
			break
		}
	}

	return result
}

// methodCanCompile checks if a method's body and signature only reference
// things that will be available in the generated output: stdlib packages,
// included types, constants, builtins, and local variables.
// allowedPkgs is an optional set of external package paths that are considered available
// (e.g., inline packages whose types are emitted into the output).
func methodCanCompile(fn *ast.FuncDecl, includedTypes map[string]bool, info *types.Info, pkgScope *types.Scope, allowedPkgs map[string]bool) bool {
	canCompile := true

	check := func(n ast.Node) bool {
		if !canCompile {
			return false
		}
		ident, ok := n.(*ast.Ident)
		if !ok {
			return true
		}

		obj, exists := info.Uses[ident]
		if !exists {
			return true // definition site, label, or unresolved
		}

		// Builtins (len, append, etc.) and universe scope (error, any, nil)
		if obj.Pkg() == nil {
			return true
		}

		// Check if the object is in our package
		if obj.Pkg().Scope() != pkgScope {
			// External package — allow stdlib and inline packages
			pkgPath := obj.Pkg().Path()
			if !isStdlib(pkgPath) && !allowedPkgs[pkgPath] {
				canCompile = false
				return false
			}
			return true
		}

		// Same package — classify the object
		switch o := obj.(type) {
		case *types.Func:
			// Package-level function (not a method) — won't be in output
			if o.Parent() == pkgScope {
				canCompile = false
			}
		case *types.Var:
			// Package-level var — won't be in output
			if o.Parent() == pkgScope {
				canCompile = false
			}
		case *types.TypeName:
			// Package-level type not in trace set
			if o.Parent() == pkgScope && !includedTypes[o.Name()] {
				canCompile = false
			}
		case *types.Const:
			// Constants are always OK — emitted with their type
		}

		return canCompile
	}

	// Walk the entire function (signature + body)
	ast.Inspect(fn, check)

	return canCompile
}

// buildMethodSet returns "TypeName.MethodName" strings for all methods in the map.
func buildMethodSet(methods map[string][]*MethodInfo) map[string]bool {
	set := make(map[string]bool)
	for typeName, ms := range methods {
		for _, m := range ms {
			set[typeName+"."+m.FuncDecl.Name.Name] = true
		}
	}
	return set
}

// collectMethodCalls finds calls to methods on same-package types in a FuncDecl.
// Uses go/types to resolve the receiver type exactly.
// Returns "TypeName.MethodName" strings.
func collectMethodCalls(fn *ast.FuncDecl, info *types.Info, pkgScope *types.Scope) []string {
	var calls []string
	if fn.Body == nil {
		return calls
	}

	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		// Look up the method being called via types.Info
		obj, exists := info.Uses[sel.Sel]
		if !exists {
			return true
		}

		fn, ok := obj.(*types.Func)
		if !ok {
			return true
		}

		// Check if it's a method on a same-package type
		sig := fn.Signature()
		recv := sig.Recv()
		if recv == nil {
			return true // not a method
		}

		// Extract the named type from the receiver (unwrap pointer)
		recvType := recv.Type()
		if ptr, ok := recvType.(*types.Pointer); ok {
			recvType = ptr.Elem()
		}
		named, ok := recvType.(*types.Named)
		if !ok {
			return true
		}

		// Only care about methods on types in our package
		if named.Obj().Pkg() == nil || named.Obj().Pkg().Scope() != pkgScope {
			return true
		}

		calls = append(calls, named.Obj().Name()+"."+fn.Name())
		return true
	})

	return calls
}

// collectTypeReferences extracts all type names referenced by an expression.
// inlineLocalNames is the set of local package names (e.g. "shared") that are inlined.
// References to inlined packages (e.g. shared.TaskStatus) are traced as "TaskStatus".
func collectTypeReferences(expr ast.Expr, typeMappings map[string]string, inlineLocalNames ...map[string]bool) []string {
	refs := []string{}
	var inlineNames map[string]bool
	if len(inlineLocalNames) > 0 {
		inlineNames = inlineLocalNames[0]
	}

	var collect func(e ast.Expr)
	collect = func(e ast.Expr) {
		if e == nil {
			return
		}

		switch t := e.(type) {
		case *ast.Ident:
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
			// If the package is inlined, trace the selector as a local type reference
			if ident, ok := t.X.(*ast.Ident); ok && inlineNames != nil && inlineNames[ident.Name] {
				refs = append(refs, t.Sel.Name)
			}
			// Otherwise: external package reference — not traced
		case *ast.IndexExpr:
			collect(t.X)
			collect(t.Index)
		case *ast.IndexListExpr:
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
		"bool": true, "string": true,
		"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
		"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
		"float32": true, "float64": true, "complex64": true, "complex128": true,
		"byte": true, "rune": true, "error": true,
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
		for _, typeName := range graph.FileTypes[filepath] {
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

			for _, spec := range genDecl.Specs {
				valueSpec, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
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

	graph := g.BuildTypeGraph()
	includedTypes := g.TraceTypes(graph)

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

		if genDecl.Tok == token.VAR {
			if g.isEmitVar(genDecl) {
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

		if genDecl.Tok == token.CONST {
			shouldEmit := false
			var groupType string
			for _, spec := range genDecl.Specs {
				if vs, ok := spec.(*ast.ValueSpec); ok {
					if vs.Type != nil {
						if ident, ok := vs.Type.(*ast.Ident); ok {
							groupType = ident.Name
						}
					}
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

		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || !typeSpec.Name.IsExported() {
				continue
			}
			if !includedTypes[typeSpec.Name.Name] {
				continue
			}
			if !wroteHeader {
				g.writeFileSourceHeader(s, filepath, file)
				wroteHeader = true
				writtenHeaders[filepath] = true
			}
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
