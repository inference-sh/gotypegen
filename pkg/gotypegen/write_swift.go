package gotypegen

import (
	"fmt"
	"go/ast"
	"go/token"
	"sort"
	"strings"

	"github.com/fatih/structtag"
)

// GenerateSwift generates Swift Codable types.
//
// Mapping:
//   - struct               → public struct ... : Codable (public init, CodingKeys keep the JSON names)
//   - struct on a reference cycle (A → *B → *A) → public final class, since Swift value types cannot recurse
//   - `type X string` / int → RawRepresentable struct with `static let` members for every const of that type
//     (open enum: unknown wire values still decode)
//   - other aliases        → public typealias
//   - any / interface{} / json.RawMessage → JSONValue (emitted in the header)
//   - pointer, omitempty, slice, map → Optional (nil slices/maps encode as null in Go)
//   - tstype:",extends" and inline embeds are flattened onto the child
func (g *PackageGenerator) GenerateSwift() (string, error) {
	w := &swiftWriter{g: g}
	if g.conf.IsTraceMode() {
		w.included = g.TraceTypes(g.BuildTypeGraph())
	}
	return w.generate()
}

type swiftWriter struct {
	g        *PackageGenerator
	included map[string]bool // nil → emit everything

	stringAliases map[string]bool
	intAliases    map[string]bool
	aliasTargets  map[string]string // typealias name → target ident (for cycle detection)
	structs       map[string]*ast.StructType
	classes       map[string]bool // structs emitted as final class
	typeParams    map[string]bool // active generic params while writing a struct

	members map[string][]swiftMember // enum type → consts
	types   []swiftTypeEntry
	loose   []swiftLooseConst
}

type swiftTypeEntry struct {
	spec *ast.TypeSpec
	doc  string
	file string
}

type swiftMember struct {
	name  string
	value string
	doc   string
}

type swiftLooseConst struct {
	name  string
	value string
	doc   string
	file  string
}

type swiftField struct {
	jsonName string
	name     string
	typ      string
	optional bool
	doc      string
	tags     map[string]string
}

func (w *swiftWriter) generate() (string, error) {
	w.collect()
	w.detectCycles()

	s := new(strings.Builder)
	w.writeHeader(s)

	lastFile := ""
	for _, e := range w.types {
		if e.file != lastFile {
			fmt.Fprintf(s, "// MARK: - %s\n\n", baseName(e.file))
			lastFile = e.file
		}
		w.writeType(s, e)
	}
	if len(w.loose) > 0 {
		s.WriteString("// MARK: - Constants\n\n")
		for _, c := range w.loose {
			writeSwiftDoc(s, c.doc, "")
			fmt.Fprintf(s, "public let %s = %s\n", c.name, c.value)
		}
		s.WriteString("\n")
	}
	return s.String(), nil
}

// collect walks the package once: aliases, types, const groups.
func (w *swiftWriter) collect() {
	g := w.g
	w.stringAliases = map[string]bool{}
	w.intAliases = map[string]bool{}
	w.aliasTargets = map[string]string{}
	w.structs = map[string]*ast.StructType{}
	w.classes = map[string]bool{}
	w.members = map[string][]swiftMember{}

	// Pass 1: type kinds (needed before consts can be classified).
	for i, file := range g.pkg.Syntax {
		if g.conf.IsFileIgnored(g.GoFiles[i]) {
			continue
		}
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, spec := range gd.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				switch t := ts.Type.(type) {
				case *ast.Ident:
					switch {
					case t.Name == "string":
						w.stringAliases[ts.Name.Name] = true
					case isGoIntType(t.Name):
						w.intAliases[ts.Name.Name] = true
					case !isBuiltinType(t.Name) && t.Name != "any":
						w.aliasTargets[ts.Name.Name] = t.Name
					}
				case *ast.StructType:
					w.structs[ts.Name.Name] = t
				}
			}
		}
	}

	// Pass 2: entries.
	seen := map[string]bool{}
	for i, file := range g.pkg.Syntax {
		if g.conf.IsFileIgnored(g.GoFiles[i]) {
			continue
		}
		filename := g.GoFiles[i]
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok {
				continue
			}
			switch gd.Tok {
			case token.TYPE:
				for _, spec := range gd.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || !ts.Name.IsExported() || !w.isIncluded(ts.Name.Name) || seen[ts.Name.Name] {
						continue
					}
					seen[ts.Name.Name] = true
					doc := ""
					if ts.Doc != nil {
						doc = strings.TrimSpace(ts.Doc.Text())
					} else if gd.Doc != nil {
						doc = strings.TrimSpace(gd.Doc.Text())
					}
					w.types = append(w.types, swiftTypeEntry{spec: ts, doc: doc, file: filename})
				}
			case token.CONST:
				w.collectConsts(gd, filename)
			}
		}
	}
}

func (w *swiftWriter) isIncluded(name string) bool {
	return w.included == nil || w.included[name]
}

// collectConsts classifies each const: a member of a string/int alias type
// (→ static let on that type) or a loose literal (→ public let, all mode only).
func (w *swiftWriter) collectConsts(gd *ast.GenDecl, filename string) {
	var curType ast.Expr
	iota := -1
	for _, spec := range gd.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		iota++
		if vs.Type != nil {
			curType = vs.Type
		} else if len(vs.Values) > 0 {
			curType = nil // untyped const
		}
		groupDoc := ""
		if vs.Doc != nil {
			groupDoc = strings.TrimSpace(vs.Doc.Text())
		} else if vs.Comment != nil {
			groupDoc = strings.TrimSpace(vs.Comment.Text())
		}

		typeName := ""
		if ident, ok := curType.(*ast.Ident); ok && (w.stringAliases[ident.Name] || w.intAliases[ident.Name]) {
			typeName = ident.Name
		}

		for i, name := range vs.Names {
			if name.Name == "_" || !name.IsExported() {
				continue
			}
			var valueExpr ast.Expr
			if i < len(vs.Values) {
				valueExpr = vs.Values[i]
			}

			if typeName != "" {
				if !w.isIncluded(typeName) {
					continue
				}
				value := w.constValue(valueExpr, iota, typeName)
				if value == "" {
					continue
				}
				w.members[typeName] = append(w.members[typeName], swiftMember{
					name:  swiftMemberName(name.Name, typeName),
					value: value,
					doc:   groupDoc,
				})
				continue
			}

			// Loose const: literal only, all mode only.
			if w.included != nil || valueExpr == nil {
				continue
			}
			if lit, ok := valueExpr.(*ast.BasicLit); ok {
				if v := swiftLiteral(lit); v != "" {
					w.loose = append(w.loose, swiftLooseConst{name: name.Name, value: v, doc: groupDoc, file: filename})
				}
			}
		}
	}
}

// constValue renders a const initializer as a Swift expression, or "" when it
// cannot be expressed (references to other packages, calls, …).
func (w *swiftWriter) constValue(expr ast.Expr, iota int, typeName string) string {
	if expr == nil {
		if w.intAliases[typeName] {
			return fmt.Sprintf("%d", iota)
		}
		return ""
	}
	switch v := expr.(type) {
	case *ast.BasicLit:
		return swiftLiteral(v)
	case *ast.Ident:
		if v.Name == "iota" {
			return fmt.Sprintf("%d", iota)
		}
		// Reference to a sibling member of the same type.
		for _, m := range w.members[typeName] {
			if m.name == swiftMemberName(v.Name, typeName) {
				return fmt.Sprintf("%s.%s.rawValue", typeName, m.name)
			}
		}
		return ""
	case *ast.BinaryExpr:
		l := w.constValue(v.X, iota, typeName)
		r := w.constValue(v.Y, iota, typeName)
		if l == "" || r == "" {
			return ""
		}
		return fmt.Sprintf("%s %s %s", l, v.Op.String(), r)
	case *ast.UnaryExpr:
		x := w.constValue(v.X, iota, typeName)
		if x == "" {
			return ""
		}
		return v.Op.String() + x
	case *ast.ParenExpr:
		x := w.constValue(v.X, iota, typeName)
		if x == "" {
			return ""
		}
		return "(" + x + ")"
	case *ast.CallExpr:
		// Conversions like ToolType("x")
		if len(v.Args) == 1 {
			return w.constValue(v.Args[0], iota, typeName)
		}
	}
	return ""
}

// swiftLiteral converts a Go literal to a Swift literal.
func swiftLiteral(lit *ast.BasicLit) string {
	switch lit.Kind {
	case token.STRING:
		raw := lit.Value
		var val string
		if strings.HasPrefix(raw, "`") {
			val = strings.Trim(raw, "`")
		} else {
			// Interpreted string: Go and Swift escapes overlap for the common cases.
			val = raw[1 : len(raw)-1]
			val = strings.ReplaceAll(val, `\x`, `\u{`) // best effort; rare
		}
		val = strings.ReplaceAll(val, `\`, `\\`)
		val = strings.ReplaceAll(val, `"`, `\"`)
		val = strings.ReplaceAll(val, "\n", `\n`)
		if strings.HasPrefix(raw, `"`) {
			// undo the double escaping for sequences that were already escapes
			val = strings.ReplaceAll(val, `\\n`, `\n`)
			val = strings.ReplaceAll(val, `\\t`, `\t`)
			val = strings.ReplaceAll(val, `\\"`, `\"`)
			val = strings.ReplaceAll(val, `\\\\`, `\\`)
		}
		return `"` + val + `"`
	case token.INT, token.FLOAT:
		return lit.Value
	case token.CHAR:
		return "" // not representable as a scalar literal
	}
	return ""
}

// swiftMemberName strips the type prefix and lowerCamels the rest:
// TaskStatusRunning (TaskStatus) → running; ScopeAgentsRead (Scope) → agentsRead.
func swiftMemberName(constName, typeName string) string {
	name := constName
	if strings.HasPrefix(name, typeName) && len(name) > len(typeName) {
		name = name[len(typeName):]
	}
	return swiftIdent(lowerCamel(name))
}

// detectCycles marks structs that can reach themselves through value or
// pointer fields (not through arrays or maps, which box their elements).
func (w *swiftWriter) detectCycles() {
	refs := map[string][]string{}
	for name, st := range w.structs {
		var out []string
		if st.Fields != nil {
			for _, f := range w.g.expandAllEmbeds(st.Fields.List) {
				out = append(out, w.directRefs(f.Type)...)
			}
		}
		refs[name] = out
	}
	for name := range w.structs {
		if w.reaches(name, name, refs, map[string]bool{}) {
			w.classes[name] = true
		}
	}
}

func (w *swiftWriter) directRefs(expr ast.Expr) []string {
	switch t := expr.(type) {
	case *ast.Ident:
		name := t.Name
		for i := 0; i < 8; i++ { // follow typealias chains
			target, ok := w.aliasTargets[name]
			if !ok {
				break
			}
			name = target
		}
		if _, ok := w.structs[name]; ok {
			return []string{name}
		}
	case *ast.StarExpr:
		return w.directRefs(t.X)
	case *ast.ParenExpr:
		return w.directRefs(t.X)
	case *ast.IndexExpr:
		return w.directRefs(t.X)
	case *ast.IndexListExpr:
		return w.directRefs(t.X)
	}
	return nil
}

func (w *swiftWriter) reaches(from, target string, refs map[string][]string, visited map[string]bool) bool {
	for _, r := range refs[from] {
		if r == target {
			return true
		}
		if visited[r] {
			continue
		}
		visited[r] = true
		if w.reaches(r, target, refs, visited) {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Emission
// ---------------------------------------------------------------------------

func (w *swiftWriter) writeHeader(s *strings.Builder) {
	s.WriteString(`// Code generated by gotypegen. DO NOT EDIT.

import Foundation

/// Untyped JSON — what Go's ` + "`any`, `interface{}` and `json.RawMessage`" + ` become.
public enum JSONValue: Codable, Hashable, Sendable {
    case string(String)
    case number(Double)
    case bool(Bool)
    case null
    case array([JSONValue])
    case object([String: JSONValue])

    public init(from decoder: Decoder) throws {
        let c = try decoder.singleValueContainer()
        if c.decodeNil() { self = .null; return }
        if let v = try? c.decode(Bool.self) { self = .bool(v); return }
        if let v = try? c.decode(Double.self) { self = .number(v); return }
        if let v = try? c.decode(String.self) { self = .string(v); return }
        if let v = try? c.decode([JSONValue].self) { self = .array(v); return }
        if let v = try? c.decode([String: JSONValue].self) { self = .object(v); return }
        throw DecodingError.dataCorruptedError(in: c, debugDescription: "Unsupported JSON value")
    }

    public func encode(to encoder: Encoder) throws {
        var c = encoder.singleValueContainer()
        switch self {
        case .string(let v): try c.encode(v)
        case .number(let v): try c.encode(v)
        case .bool(let v): try c.encode(v)
        case .null: try c.encodeNil()
        case .array(let v): try c.encode(v)
        case .object(let v): try c.encode(v)
        }
    }

    public var stringValue: String? { if case .string(let v) = self { return v }; return nil }
    public var doubleValue: Double? { if case .number(let v) = self { return v }; return nil }
    public var boolValue: Bool? { if case .bool(let v) = self { return v }; return nil }
    public var arrayValue: [JSONValue]? { if case .array(let v) = self { return v }; return nil }
    public var objectValue: [String: JSONValue]? { if case .object(let v) = self { return v }; return nil }
    public var isNull: Bool { if case .null = self { return true }; return false }
    public subscript(key: String) -> JSONValue? { objectValue?[key] }
    public subscript(index: Int) -> JSONValue? {
        guard let a = arrayValue, index >= 0, index < a.count else { return nil }
        return a[index]
    }
}

extension JSONValue: ExpressibleByStringLiteral, ExpressibleByIntegerLiteral, ExpressibleByFloatLiteral,
    ExpressibleByBooleanLiteral, ExpressibleByNilLiteral, ExpressibleByArrayLiteral, ExpressibleByDictionaryLiteral {
    public init(stringLiteral value: String) { self = .string(value) }
    public init(integerLiteral value: Int) { self = .number(Double(value)) }
    public init(floatLiteral value: Double) { self = .number(value) }
    public init(booleanLiteral value: Bool) { self = .bool(value) }
    public init(nilLiteral: ()) { self = .null }
    public init(arrayLiteral elements: JSONValue...) { self = .array(elements) }
    public init(dictionaryLiteral elements: (String, JSONValue)...) {
        self = .object(Dictionary(elements, uniquingKeysWith: { _, last in last }))
    }
}

`)
}

func (w *swiftWriter) writeType(s *strings.Builder, e swiftTypeEntry) {
	name := e.spec.Name.Name
	switch t := e.spec.Type.(type) {
	case *ast.Ident:
		switch {
		case w.stringAliases[name]:
			w.writeEnum(s, name, "String", e.doc)
		case w.intAliases[name]:
			w.writeEnum(s, name, "Int", e.doc)
		default:
			typ, _ := w.typeOf(t)
			writeSwiftDoc(s, e.doc, "")
			fmt.Fprintf(s, "public typealias %s = %s\n\n", name, typ)
		}
	case *ast.StructType:
		w.writeStruct(s, name, e.spec, t, e.doc)
	case *ast.ArrayType, *ast.MapType, *ast.InterfaceType, *ast.SelectorExpr:
		typ, _ := w.typeOf(t)
		writeSwiftDoc(s, e.doc, "")
		fmt.Fprintf(s, "public typealias %s = %s\n\n", name, typ)
	case *ast.StarExpr:
		typ, _ := w.typeOf(t.X)
		writeSwiftDoc(s, e.doc, "")
		fmt.Fprintf(s, "public typealias %s = %s?\n\n", name, typ)
	}
}

func (w *swiftWriter) writeEnum(s *strings.Builder, name, raw, doc string) {
	writeSwiftDoc(s, doc, "")
	fmt.Fprintf(s, "public struct %s: RawRepresentable, Codable, Hashable, Sendable {\n", name)
	fmt.Fprintf(s, "    public let rawValue: %s\n", raw)
	fmt.Fprintf(s, "    public init(rawValue: %s) { self.rawValue = rawValue }\n", raw)
	if raw == "String" {
		s.WriteString("    public init(_ value: String) { self.rawValue = value }\n")
	}
	members := w.members[name]
	if len(members) > 0 {
		s.WriteString("\n")
	}
	used := map[string]int{}
	for _, m := range members {
		mname := m.name
		if n := used[mname]; n > 0 {
			mname = fmt.Sprintf("%s%d", strings.Trim(mname, "`"), n+1)
		}
		used[m.name]++
		writeSwiftDoc(s, m.doc, "    ")
		fmt.Fprintf(s, "    public static let %s = %s(rawValue: %s)\n", mname, name, m.value)
	}
	s.WriteString("}\n\n")
}

func (w *swiftWriter) writeStruct(s *strings.Builder, name string, ts *ast.TypeSpec, st *ast.StructType, doc string) {
	// Generic params
	generics := ""
	w.typeParams = map[string]bool{}
	if ts.TypeParams != nil {
		var names []string
		for _, f := range ts.TypeParams.List {
			for _, n := range f.Names {
				w.typeParams[n.Name] = true
				names = append(names, n.Name+": Codable")
			}
		}
		generics = "<" + strings.Join(names, ", ") + ">"
	}
	defer func() { w.typeParams = nil }()

	fields := w.collectFields(st)
	kind := "struct"
	if w.classes[name] {
		kind = "final class"
	}

	writeSwiftDoc(s, doc, "")
	fmt.Fprintf(s, "public %s %s%s: Codable {\n", kind, name, generics)

	for _, f := range fields {
		writeSwiftDoc(s, f.doc, "    ")
		fmt.Fprintf(s, "    public var %s: %s%s\n", f.name, f.typ, optMark(f.optional))
	}

	// init
	if len(fields) == 0 {
		s.WriteString("    public init() {}\n")
	} else {
		s.WriteString("\n    public init(\n")
		for i, f := range fields {
			sep := ","
			if i == len(fields)-1 {
				sep = ""
			}
			def := ""
			if f.optional {
				def = " = nil"
			} else if zv := swiftZeroValue(f.typ); zv != "" {
				def = " = " + zv // Go zero value; the server treats it as unset
			}
			fmt.Fprintf(s, "        %s: %s%s%s%s\n", f.name, f.typ, optMark(f.optional), def, sep)
		}
		s.WriteString("    ) {\n")
		for _, f := range fields {
			fmt.Fprintf(s, "        self.%s = %s\n", f.name, f.name)
		}
		s.WriteString("    }\n")

		s.WriteString("\n    enum CodingKeys: String, CodingKey {\n")
		for _, f := range fields {
			fmt.Fprintf(s, "        case %s = %q\n", f.name, f.jsonName)
		}
		s.WriteString("    }\n")
	}

	w.writeFieldTags(s, fields)
	s.WriteString("}\n\n")
}

func (w *swiftWriter) writeFieldTags(s *strings.Builder, fields []swiftField) {
	has := false
	for _, f := range fields {
		if len(f.tags) > 0 {
			has = true
			break
		}
	}
	if !has {
		return
	}
	s.WriteString("\n    public static let fieldTags: [String: [String: String]] = [\n")
	for _, f := range fields {
		if len(f.tags) == 0 {
			continue
		}
		var parts []string
		for _, k := range w.g.conf.FieldTags {
			if v, ok := f.tags[k]; ok {
				parts = append(parts, fmt.Sprintf("%q: %q", k, v))
			}
		}
		fmt.Fprintf(s, "        %q: [%s],\n", f.jsonName, strings.Join(parts, ", "))
	}
	s.WriteString("    ]\n")
}

func (w *swiftWriter) collectFields(st *ast.StructType) []swiftField {
	if st.Fields == nil {
		return nil
	}
	var out []swiftField
	usedNames := map[string]bool{}
	for _, field := range w.g.expandAllEmbeds(st.Fields.List) {
		names := field.Names
		if len(names) == 0 {
			// Anonymous field that survived expansion: json-named embed, or an
			// unresolvable/foreign type. Keep only the json-named case.
			if w.g.isInlineEmbed(field) || w.g.isExtendsEmbed(field) {
				continue
			}
			typeName, ok := getAnonymousFieldName(field.Type)
			if !ok {
				continue
			}
			if jsonName, _ := w.g.getPyFieldInfo(field); jsonName == "" || jsonName == "-" {
				continue
			}
			names = []*ast.Ident{ast.NewIdent(typeName)}
		}
		for _, ident := range names {
			if !ident.IsExported() {
				continue
			}
			jsonName, omitempty := w.g.getPyFieldInfo(field)
			if jsonName == "-" {
				continue
			}
			if jsonName == "" {
				jsonName = ident.Name
			}
			name := swiftIdent(lowerCamel(jsonName))
			for usedNames[name] {
				name = strings.Trim(name, "`") + "_"
			}
			usedNames[name] = true

			typ, nullable := w.typeOf(field.Type)

			doc := ""
			if field.Doc != nil {
				doc = strings.TrimSpace(field.Doc.Text())
			} else if field.Comment != nil {
				doc = strings.TrimSpace(field.Comment.Text())
			}

			var tags map[string]string
			if len(w.g.conf.FieldTags) > 0 && field.Tag != nil {
				if parsed, err := structtag.Parse(field.Tag.Value[1 : len(field.Tag.Value)-1]); err == nil {
					for _, k := range w.g.conf.FieldTags {
						if t, err := parsed.Get(k); err == nil {
							if tags == nil {
								tags = map[string]string{}
							}
							tags[k] = t.Name
						}
					}
				}
			}

			out = append(out, swiftField{
				jsonName: jsonName,
				name:     name,
				typ:      typ,
				optional: nullable || omitempty,
				doc:      doc,
				tags:     tags,
			})
		}
	}
	return out
}

// typeOf maps a Go type expression to (Swift type, nullable-on-the-wire).
func (w *swiftWriter) typeOf(expr ast.Expr) (string, bool) {
	switch t := expr.(type) {
	case *ast.Ident:
		return w.identType(t.Name), false
	case *ast.SelectorExpr:
		full := fmt.Sprintf("%s.%s", t.X, t.Sel.Name)
		if m, ok := w.g.conf.TypeMappings[full]; ok {
			return tsMappingToSwift(m)
		}
		if full == "time.Time" {
			return "String", false // RFC3339 on the wire
		}
		if w.g.conf.InlinePackageLocalName(full) != "" {
			return t.Sel.Name, false
		}
		// Types from inlined packages are referenced as pkg.Name in source.
		if x, ok := t.X.(*ast.Ident); ok {
			for _, p := range w.g.conf.InlinePackages {
				if strings.HasSuffix(p, "/"+x.Name) || p == x.Name {
					return t.Sel.Name, false
				}
			}
		}
		return "JSONValue", false
	case *ast.StarExpr:
		typ, _ := w.typeOf(t.X)
		return typ, true
	case *ast.ParenExpr:
		return w.typeOf(t.X)
	case *ast.ArrayType:
		if ident, ok := t.Elt.(*ast.Ident); ok && (ident.Name == "byte" || ident.Name == "uint8") {
			if m, ok := w.g.conf.TypeMappings["[]byte"]; ok {
				typ, _ := tsMappingToSwift(m)
				return typ, true
			}
			return "Data", true
		}
		typ, _ := w.typeOf(t.Elt)
		return "[" + typ + "]", true
	case *ast.MapType:
		key, _ := w.typeOf(t.Key)
		if key != "Int" {
			key = "String" // Codable only round-trips String/Int keys as JSON objects
		}
		val, _ := w.typeOf(t.Value)
		return "[" + key + ": " + val + "]", true
	case *ast.IndexExpr:
		base, _ := w.typeOf(t.X)
		arg, _ := w.typeOf(t.Index)
		return base + "<" + arg + ">", false
	case *ast.IndexListExpr:
		base, _ := w.typeOf(t.X)
		var args []string
		for _, ix := range t.Indices {
			a, _ := w.typeOf(ix)
			args = append(args, a)
		}
		return base + "<" + strings.Join(args, ", ") + ">", false
	case *ast.InterfaceType:
		return "JSONValue", false
	case *ast.StructType:
		return "[String: JSONValue]", false
	}
	return "JSONValue", false
}

func (w *swiftWriter) identType(name string) string {
	if w.typeParams[name] {
		return name
	}
	if m, ok := w.g.conf.TypeMappings[name]; ok {
		typ, _ := tsMappingToSwift(m)
		return typ
	}
	switch name {
	case "string":
		return "String"
	case "bool":
		return "Bool"
	case "float32", "float64":
		return "Double"
	case "any", "interface{}", "error":
		return "JSONValue"
	}
	if isGoIntType(name) || name == "byte" || name == "rune" {
		return "Int"
	}
	if len(name) == 1 && name[0] >= 'A' && name[0] <= 'Z' {
		return "JSONValue" // stray generic param
	}
	if name == "" || !ast.IsExported(name) {
		return "JSONValue"
	}
	return name
}

func tsMappingToSwift(mapping string) (string, bool) {
	base, _, nullable := parseTypeMappingForSchema(mapping)
	switch strings.TrimSpace(base) {
	case "string":
		return "String", nullable
	case "number":
		return "Double", nullable
	case "boolean":
		return "Bool", nullable
	}
	return "JSONValue", nullable
}

// ---------------------------------------------------------------------------
// Naming helpers
// ---------------------------------------------------------------------------

func isGoIntType(name string) bool {
	switch name {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return true
	}
	return false
}

// lowerCamel: "chat_id" → "chatId", "io.modelcontextprotocol/serverInfo" → "ioModelcontextprotocolServerInfo",
// "ID" → "id", "URL" → "url", "HTTPServer" → "httpServer", "ttlMs" → "ttlMs".
func lowerCamel(s string) string {
	var parts []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			parts = append(parts, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case r == '_' || r == '-' || r == '.' || r == '/' || r == ' ' || r == '$' || r == ':':
			flush()
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			cur.WriteRune(r)
		default:
			flush()
		}
	}
	flush()
	if len(parts) == 0 {
		return "value"
	}
	var b strings.Builder
	for i, p := range parts {
		if i == 0 {
			b.WriteString(lowerFirstWord(p))
		} else {
			b.WriteString(strings.ToUpper(p[:1]) + p[1:])
		}
	}
	out := b.String()
	if out[0] >= '0' && out[0] <= '9' {
		out = "_" + out
	}
	return out
}

// lowerFirstWord lowercases the leading acronym/word: "ID"→"id", "HTTPServer"→"httpServer", "Name"→"name".
func lowerFirstWord(p string) string {
	runes := []rune(p)
	n := 0
	for n < len(runes) && runes[n] >= 'A' && runes[n] <= 'Z' {
		n++
	}
	if n == 0 {
		return p
	}
	if n == len(runes) {
		return strings.ToLower(p)
	}
	if n == 1 {
		return strings.ToLower(string(runes[0])) + string(runes[1:])
	}
	// acronym followed by a word: keep the last capital as the word start
	return strings.ToLower(string(runes[:n-1])) + string(runes[n-1:])
}

var swiftKeywords = map[string]bool{
	"associatedtype": true, "class": true, "deinit": true, "enum": true, "extension": true,
	"fileprivate": true, "func": true, "import": true, "init": true, "inout": true,
	"internal": true, "let": true, "open": true, "operator": true, "private": true,
	"precedencegroup": true, "protocol": true, "public": true, "rethrows": true,
	"static": true, "struct": true, "subscript": true, "typealias": true, "var": true,
	"break": true, "case": true, "catch": true, "continue": true, "default": true,
	"defer": true, "do": true, "else": true, "fallthrough": true, "for": true,
	"guard": true, "if": true, "in": true, "repeat": true, "return": true, "throw": true,
	"switch": true, "where": true, "while": true, "Any": true, "as": true, "await": true,
	"false": true, "is": true, "nil": true, "self": true, "Self": true, "super": true,
	"throws": true, "true": true, "try": true, "Type": true, "Protocol": true,
	"rawValue": true, "fieldTags": true,
}

func swiftIdent(name string) string {
	if swiftKeywords[name] {
		return "`" + name + "`"
	}
	return name
}

// swiftZeroValue gives scalar init defaults so request types are ergonomic
// (LLMInput(text: "hi")) while decoding still requires the field, which Go
// always emits for non-omitempty scalars.
func swiftZeroValue(typ string) string {
	switch typ {
	case "String":
		return `""`
	case "Int":
		return "0"
	case "Double":
		return "0"
	case "Bool":
		return "false"
	case "JSONValue":
		return ".null"
	}
	return ""
}

func optMark(optional bool) string {
	if optional {
		return "?"
	}
	return ""
}

func writeSwiftDoc(s *strings.Builder, doc, indent string) {
	if doc == "" {
		return
	}
	for _, line := range strings.Split(doc, "\n") {
		s.WriteString(indent)
		s.WriteString("/// ")
		s.WriteString(line)
		s.WriteString("\n")
	}
}

func baseName(path string) string {
	parts := strings.Split(path, "/")
	return parts[len(parts)-1]
}

// sortedKeys is used for deterministic output where map iteration would leak.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
