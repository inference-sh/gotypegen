package gotypegen

import (
	"fmt"
	"go/ast"
	"strings"
)

// pyConst is one exported const with the type Go gives it.
type pyConst struct {
	name  string
	doc   *ast.CommentGroup // the spec's doc comment
	value ast.Expr          // nil when the spec repeats the previous one implicitly
	iota  int
	typ   ast.Expr // nil for an untyped const
}

// pyConstsOf lists the exported consts of a const decl. Types follow Go: a
// spec with a type has it, a spec with values but no type is untyped, and a
// spec with neither repeats the previous spec's type.
func pyConstsOf(decl *ast.GenDecl) []pyConst {
	var out []pyConst
	var typ ast.Expr
	for iota, spec := range decl.Specs {
		vs, ok := spec.(*ast.ValueSpec)
		if !ok {
			continue
		}
		if vs.Type != nil {
			typ = vs.Type
		} else if len(vs.Values) > 0 {
			typ = nil
		}
		for i, name := range vs.Names {
			if name.Name == "_" || !name.IsExported() {
				continue
			}
			c := pyConst{name: name.Name, doc: vs.Doc, iota: iota, typ: typ}
			if i < len(vs.Values) {
				c.value = vs.Values[i]
			}
			out = append(out, c)
		}
	}
	return out
}

// constEnumType returns the string/int alias type a const belongs to, or "".
func constEnumType(c pyConst, stringTypeAliases, intTypeAliases map[string]bool) string {
	ident, ok := c.typ.(*ast.Ident)
	if !ok {
		return ""
	}
	if stringTypeAliases[ident.Name] || intTypeAliases[ident.Name] {
		return ident.Name
	}
	return ""
}

// pyEnumMember is one member of a generated enum class.
type pyEnumMember struct {
	name  string
	value string
	doc   string // comment lines, without the leading "# "
}

// writePyConstEntries emits the const decls. Go spreads the values of one
// type over as many const blocks as it likes, so every string/int alias type
// becomes a single enum class holding the members of all its blocks, written
// where its first block is. Consts of other types are written as they come.
// included limits the enum classes to traced types; nil means all.
func (g *PackageGenerator) writePyConstEntries(
	s *strings.Builder,
	entries []pyConstEntry,
	stringTypeAliases, intTypeAliases map[string]bool,
	enumMembers map[string]enumMemberInfo,
	included map[string]bool,
	writtenHeaders map[string]bool,
) {
	members := make(map[string][]pyEnumMember)
	home := make(map[string]int) // enum type -> index of the entry that writes it
	for idx, entry := range entries {
		blockDocPending := entry.decl.Doc != nil
		for _, c := range pyConstsOf(entry.decl) {
			typeName := constEnumType(c, stringTypeAliases, intTypeAliases)
			if typeName == "" || (included != nil && !included[typeName]) {
				continue
			}
			if _, ok := home[typeName]; !ok {
				home[typeName] = idx
			}
			var doc []string
			// A later block's own doc would otherwise be lost: it heads the
			// block's first member inside the merged class.
			if blockDocPending && home[typeName] != idx {
				doc = append(doc, strings.TrimSpace(entry.decl.Doc.Text()))
			}
			blockDocPending = false
			if c.doc != nil {
				doc = append(doc, strings.TrimSpace(c.doc.Text()))
			}
			members[typeName] = append(members[typeName], pyEnumMember{
				name:  toUpperSnake(enumMemberBase(c.name, typeName)),
				value: pyEnumValue(g, c, intTypeAliases[typeName]),
				doc:   strings.Join(doc, "\n"),
			})
		}
	}

	for idx, entry := range entries {
		var classes []string
		var loose []pyConst
		seen := make(map[string]bool)
		for _, c := range pyConstsOf(entry.decl) {
			typeName := constEnumType(c, stringTypeAliases, intTypeAliases)
			if typeName == "" {
				loose = append(loose, c)
				continue
			}
			if home[typeName] == idx && !seen[typeName] {
				if _, ok := members[typeName]; ok {
					seen[typeName] = true
					classes = append(classes, typeName)
				}
			}
		}
		if len(classes) == 0 && len(loose) == 0 {
			continue
		}

		if writtenHeaders != nil {
			if !writtenHeaders[entry.file] {
				writePyFileHeader(s, entry.file)
				writtenHeaders[entry.file] = true
			}
		} else {
			writePyFileHeader(s, entry.file)
		}

		if entry.decl.Doc != nil {
			writePyComment(s, "", strings.TrimSpace(entry.decl.Doc.Text()))
		}
		for _, typeName := range classes {
			base := "(str, Enum)"
			if intTypeAliases[typeName] {
				base = "(IntEnum)"
			}
			writePyEnumClass(s, typeName, base, members[typeName])
		}
		if len(loose) > 0 {
			for _, c := range loose {
				g.writePyLooseConst(s, c, enumMembers)
			}
			s.WriteString("\n")
		}
	}
}

func writePyFileHeader(s *strings.Builder, file string) {
	parts := strings.Split(file, "/")
	fmt.Fprintf(s, "\n##########\n# source: %s\n\n", parts[len(parts)-1])
}

func writePyComment(s *strings.Builder, indent, doc string) {
	s.WriteString(indent)
	s.WriteString("# ")
	s.WriteString(strings.ReplaceAll(doc, "\n", "\n"+indent+"# "))
	s.WriteString("\n")
}

// pyEnumValue renders an enum member's value. A string member without a value
// is "", an int member without one takes its iota.
func pyEnumValue(g *PackageGenerator, c pyConst, isInt bool) string {
	if c.value == nil {
		if isInt {
			return fmt.Sprintf("%d", c.iota)
		}
		return `""`
	}
	if isInt {
		return g.getPyConstValue(c.value, c.iota, nil)
	}
	return g.getPyConstValue(c.value, 0, nil)
}

// writePyEnumClass writes a str or int enum. Uses (str, Enum) instead of
// StrEnum for Python 3.10 compatibility.
func writePyEnumClass(s *strings.Builder, typeName, base string, members []pyEnumMember) {
	s.WriteString("class ")
	s.WriteString(typeName)
	s.WriteString(base)
	s.WriteString(":\n")
	for _, m := range members {
		if m.doc != "" {
			writePyComment(s, "    ", m.doc)
		}
		s.WriteString("    ")
		s.WriteString(m.name)
		s.WriteString(" = ")
		s.WriteString(m.value)
		s.WriteString("\n")
	}
	if len(members) == 0 {
		s.WriteString("    pass\n")
	}
	s.WriteString("\n")
}

// writePyLooseConst writes a const that is not an enum member as a module
// level assignment.
func (g *PackageGenerator) writePyLooseConst(s *strings.Builder, c pyConst, enumMembers map[string]enumMemberInfo) {
	if c.doc != nil {
		writePyComment(s, "", strings.TrimSpace(c.doc.Text()))
	}
	s.WriteString(c.name)
	if c.typ != nil {
		s.WriteString(": ")
		if ident, ok := c.typ.(*ast.Ident); ok {
			s.WriteString(goBaseToPython(ident.Name))
		} else {
			s.WriteString(g.exprToPythonType(c.typ))
		}
	}
	s.WriteString(" = ")
	if c.value != nil {
		s.WriteString(g.getPyConstValue(c.value, c.iota, enumMembers))
	} else {
		fmt.Fprintf(s, "%d", c.iota)
	}
	s.WriteString("\n")
}
