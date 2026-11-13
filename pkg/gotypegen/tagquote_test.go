package gotypegen

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// A tag value is written into the generated Python and TypeScript as a
// string literal: whatever the tag holds, it stays inside the quotes.
func TestQuoteTagValue(t *testing.T) {
	for in, want := range map[string]string{
		"concat":             `"concat"`,
		`a"b`:                `"a\"b"`,
		`a\b`:                `"a\\b"`,
		"a\nb\r":             `"a\u000ab\u000d"`,
		`x"}; import os  # `: `"x\"}; import os  # "`,
		"héllo":              `"héllo"`,
	} {
		if got := quoteTagValue(in); got != want {
			t.Errorf("quoteTagValue(%q) = %s, want %s", in, got, want)
		}
	}
}

// The value as the generator reads it from a struct tag.
func TestConfiguredFieldTagsQuoted(t *testing.T) {
	src := "package p\ntype T struct {\n\tA string `json:\"a\" merge:\"x\\\"}; evil()  # \"`\n}\n"
	f, err := parser.ParseFile(token.NewFileSet(), "p.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	field := f.Decls[0].(*ast.GenDecl).Specs[0].(*ast.TypeSpec).Type.(*ast.StructType).Fields.List[0]
	g := &PackageGenerator{conf: &PackageConfig{FieldTags: []string{"merge"}}}
	val := g.configuredFieldTags(field)["merge"]
	if val != `x"}; evil()  # ` {
		t.Fatalf("tag value read as %q", val)
	}
	if got, want := quoteTagValue(val), `"x\"}; evil()  # "`; got != want {
		t.Errorf("quoted %s, want %s", got, want)
	}
}
