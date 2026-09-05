package gotypegen

import (
	"go/ast"

	"github.com/fatih/structtag"
	"golang.org/x/tools/go/packages"
)

// expandInlineFields returns fields with anonymous (embedded) struct fields
// replaced by their own fields, recursively, following encoding/json:
//
//   - an anonymous struct field with no json name is inlined — its exported
//     fields are promoted to the outer object
//   - an anonymous field with a json name stays a named field
//   - tstype:",extends" and tstype:"-" anonymous fields are left in place;
//     the writers turn ",extends" into inheritance and skip "-"
//
// Only structs declared in this package (or its inline packages) can be
// inlined; an unresolvable embedded type is left as is.
func (g *PackageGenerator) expandInlineFields(fields []*ast.Field) []*ast.Field {
	return g.expandEmbeds(fields, false)
}

// expandAllEmbeds also inlines tstype:",extends" parents. JSON Schema has no
// inheritance, so for that output a parent's fields must appear on the child.
func (g *PackageGenerator) expandAllEmbeds(fields []*ast.Field) []*ast.Field {
	return g.expandEmbeds(fields, true)
}

func (g *PackageGenerator) expandEmbeds(fields []*ast.Field, includeExtends bool) []*ast.Field {
	var out []*ast.Field
	for _, f := range fields {
		if len(f.Names) != 0 || !(g.isInlineEmbed(f) || (includeExtends && g.isExtendsEmbed(f))) {
			out = append(out, f)
			continue
		}
		name, ok := getAnonymousFieldName(f.Type)
		if !ok {
			out = append(out, f)
			continue
		}
		st := g.structTypeByName(name)
		if st == nil || st.Fields == nil {
			out = append(out, f)
			continue
		}
		out = append(out, g.expandEmbeds(st.Fields.List, includeExtends)...)
	}
	return out
}

// isExtendsEmbed reports whether an anonymous field carries tstype:",extends".
func (g *PackageGenerator) isExtendsEmbed(f *ast.Field) bool {
	if f.Tag == nil {
		return false
	}
	tags, err := structtag.Parse(f.Tag.Value[1 : len(f.Tag.Value)-1])
	if err != nil {
		return false
	}
	tsTag, err := tags.Get("tstype")
	return err == nil && tsTag.HasOption("extends")
}

// isInlineEmbed reports whether an anonymous field has encoding/json inline
// semantics: no json name and no tstype directive claiming it.
func (g *PackageGenerator) isInlineEmbed(f *ast.Field) bool {
	if f.Tag == nil {
		return true
	}
	tags, err := structtag.Parse(f.Tag.Value[1 : len(f.Tag.Value)-1])
	if err != nil {
		return false
	}
	if jsonTag, err := tags.Get("json"); err == nil && jsonTag.Name != "" {
		return false // named field
	}
	if tsTag, err := tags.Get("tstype"); err == nil {
		if tsTag.Name == "-" || tsTag.HasOption("extends") {
			return false
		}
	}
	return true
}

// structTypeByName finds a struct type declaration by name in this package
// and its inline packages.
func (g *PackageGenerator) structTypeByName(name string) *ast.StructType {
	pkgs := append([]*packages.Package{g.pkg}, g.inlinePkgs...)
	for _, pkg := range pkgs {
		if pkg == nil {
			continue
		}
		for _, file := range pkg.Syntax {
			for _, decl := range file.Decls {
				gd, ok := decl.(*ast.GenDecl)
				if !ok {
					continue
				}
				for _, spec := range gd.Specs {
					ts, ok := spec.(*ast.TypeSpec)
					if !ok || ts.Name.Name != name {
						continue
					}
					if st, ok := ts.Type.(*ast.StructType); ok {
						return st
					}
					return nil
				}
			}
		}
	}
	return nil
}
