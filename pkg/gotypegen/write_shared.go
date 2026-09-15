package gotypegen

import (
	"go/ast"
	"go/token"
	"strings"

	"github.com/fatih/structtag"
)

// Helpers shared by the Python and Swift writers.

func isGoIntType(name string) bool {
	switch name {
	case "int", "int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64":
		return true
	}
	return false
}

// collectScalarAliases finds exported `type X string` and `type X int*`
// declarations in the non-ignored files: the types that become enums.
func (g *PackageGenerator) collectScalarAliases() (stringAliases, intAliases map[string]bool) {
	stringAliases = make(map[string]bool)
	intAliases = make(map[string]bool)
	for i, file := range g.pkg.Syntax {
		if g.conf.IsFileIgnored(g.GoFiles[i]) {
			continue
		}
		for _, decl := range file.Decls {
			genDecl, ok := decl.(*ast.GenDecl)
			if !ok || genDecl.Tok != token.TYPE {
				continue
			}
			for _, spec := range genDecl.Specs {
				ts, ok := spec.(*ast.TypeSpec)
				if !ok || !ts.Name.IsExported() {
					continue
				}
				ident, ok := ts.Type.(*ast.Ident)
				if !ok {
					continue
				}
				switch {
				case ident.Name == "string":
					stringAliases[ts.Name.Name] = true
				case isGoIntType(ident.Name):
					intAliases[ts.Name.Name] = true
				}
			}
		}
	}
	return stringAliases, intAliases
}

// specDoc returns the type's own doc comment, falling back to the group's.
func specDoc(ts *ast.TypeSpec, gd *ast.GenDecl) string {
	if ts.Doc != nil {
		return strings.TrimSpace(ts.Doc.Text())
	}
	if gd.Doc != nil {
		return strings.TrimSpace(gd.Doc.Text())
	}
	return ""
}

// enumMemberBase strips the enum type name prefix from a const name:
// TaskStatusRunning (TaskStatus) → Running.
func enumMemberBase(constName, typeName string) string {
	return strings.TrimPrefix(constName, typeName)
}

// configuredFieldTags reads the struct tag keys listed in field_tags for one
// field, or nil when none apply.
func (g *PackageGenerator) configuredFieldTags(field *ast.Field) map[string]string {
	if len(g.conf.FieldTags) == 0 || field.Tag == nil {
		return nil
	}
	tags, err := structtag.Parse(field.Tag.Value[1 : len(field.Tag.Value)-1])
	if err != nil {
		return nil
	}
	var out map[string]string
	for _, key := range g.conf.FieldTags {
		tag, err := tags.Get(key)
		if err != nil {
			continue
		}
		if out == nil {
			out = make(map[string]string)
		}
		out[key] = tag.Name
	}
	return out
}
