package gotypegen

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func TestSingleLine(t *testing.T) {
	for in, want := range map[string]string{
		"hello\n":                   "hello",
		"a\nb\n":                    "a b",
		"a\r\nb":                    "a b",
		"a\u2028b\u2029c":           "a b c",
		"a\vb\fc\u0085d":            "a b c d",
		"a\n\n  b":                  "a   b",
		"keeps\ttabs":               "keeps\ttabs",
		"\n":                        "",
		"":                          "",
		"x */ y":                    "x */ y",
		"b\nimport os.go":           "b import os.go",
		"b.go\r\nconsole.log(1);//": "b.go console.log(1);//",
	} {
		if got := singleLine(in); got != want {
			t.Errorf("singleLine(%q) = %q, want %q", in, got, want)
		}
	}
}

// tsCodeLines returns the lines of generated TypeScript that are not inside
// a comment: everything a // or /* */ comment does not cover.
func tsCodeLines(t *testing.T, ts string) []string {
	t.Helper()
	var out []string
	inBlock := false
	for _, line := range strings.FieldsFunc(ts, func(r rune) bool {
		return r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029'
	}) {
		rest := line
		var code strings.Builder
		for rest != "" {
			if inBlock {
				end := strings.Index(rest, "*/")
				if end < 0 {
					rest = ""
					break
				}
				rest = rest[end+2:]
				inBlock = false
				continue
			}
			line := strings.Index(rest, "//")
			block := strings.Index(rest, "/*")
			if line >= 0 && (block < 0 || line < block) {
				code.WriteString(rest[:line])
				rest = ""
				break
			}
			if block >= 0 {
				code.WriteString(rest[:block])
				rest = rest[block+2:]
				inBlock = true
				continue
			}
			code.WriteString(rest)
			rest = ""
		}
		if c := strings.TrimSpace(code.String()); c != "" {
			out = append(out, c)
		}
	}
	return out
}

func assertNoInjectedCode(t *testing.T, ts string) {
	t.Helper()
	for _, line := range tsCodeLines(t, ts) {
		if strings.Contains(line, "INJECTED") {
			t.Errorf("comment text reached TypeScript code: %q\n--- output ---\n%s", line, ts)
		}
	}
}

// A trailing /* */ comment that spans lines used to be written after " // "
// with its newlines, so every line after the first was TypeScript code.
func TestTrailingMultilineCommentStaysInComment(t *testing.T) {
	cases := map[string]string{
		"struct field":             "type T struct {\n\tA string `json:\"a\"` /* first\nINJECTED(); */\n\tB string `json:\"b\"`\n}\n",
		"interface field fallback": "type T struct {\n\tA func() /* first\nINJECTED(); */\n}\n",
		"type alias":               "type U []int /* first\nINJECTED(); */\n",
		"const":                    "const C = \"c\" /* first\nINJECTED(); */\n",
		"line separator":           "type T struct {\n\tA string `json:\"a\"` // first\u2028INJECTED();\n}\n",
		"paragraph separator":      "const C = \"c\" // first\u2029INJECTED();\n",
		"block close in line":      "type T struct {\n\tA string `json:\"a\"` // first */ INJECTED();\n}\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			ts, err := ConvertGoToTypescript(src, PackageConfig{})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(ts, "INJECTED") {
				t.Fatalf("comment text missing from output, the case tests nothing:\n%s", ts)
			}
			assertNoInjectedCode(t, ts)
		})
	}
}

// The output for an ordinary trailing comment is unchanged.
func TestTrailingCommentOutputUnchanged(t *testing.T) {
	src := "type T struct {\n\tA string `json:\"a\"` // the a\n\tB string `json:\"b\"`\n}\nconst C = \"c\" // the c\n"
	ts, err := ConvertGoToTypescript(src, PackageConfig{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"  a: string; // the a\n  b: string;\n", "export const C = \"c\"; // the c\n"} {
		if !strings.Contains(ts, want) {
			t.Errorf("output lacks %q:\n%s", want, ts)
		}
	}
}

// A file doc comment is written inside /* */ in the TypeScript header; a //
// doc comment can contain the closing marker.
func TestFileDocCommentStaysInComment(t *testing.T) {
	src := "// Package p. */ INJECTED(); /*\npackage p\n"
	f, err := parser.ParseFile(token.NewFileSet(), "p.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	g := &PackageGenerator{conf: &PackageConfig{PreserveComments: "default"}}
	var s strings.Builder
	g.writeFileSourceHeader(&s, "p.go", f)
	if !strings.Contains(s.String(), "INJECTED") {
		t.Fatalf("doc comment missing from header:\n%s", s.String())
	}
	assertNoInjectedCode(t, s.String())
}

// A source file name can contain a newline (go list accepts it). In every
// output language the name is written into a line comment; nothing of it may
// start a new line.
func TestSourceFileNameStaysOnHeaderLine(t *testing.T) {
	names := []string{
		"dir/b\nINJECTED.go",
		"dir/b\r\nINJECTED.go",
		"dir/b\rINJECTED.go",
		"dir/b\u2028INJECTED.go",
		"dir/b\u2029INJECTED.go",
	}
	isLineEnd := func(r rune) bool { return r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029' }

	check := func(t *testing.T, lang, out, commentPrefix string) {
		t.Helper()
		if !strings.Contains(out, "INJECTED") {
			t.Fatalf("%s: file name missing from header: %q", lang, out)
		}
		for _, line := range strings.FieldsFunc(out, isLineEnd) {
			if strings.Contains(line, "INJECTED") && !strings.HasPrefix(strings.TrimSpace(line), commentPrefix) {
				t.Errorf("%s: file name text outside the comment: %q in %q", lang, line, out)
			}
		}
	}

	for _, name := range names {
		t.Run(strings.ToValidUTF8(strings.Map(func(r rune) rune {
			if isLineEnd(r) {
				return '_'
			}
			return r
		}, name), "_"), func(t *testing.T) {
			f, err := parser.ParseFile(token.NewFileSet(), "p.go", "package p\n", parser.ParseComments)
			if err != nil {
				t.Fatal(err)
			}

			var ts strings.Builder
			(&PackageGenerator{conf: &PackageConfig{}}).writeFileSourceHeader(&ts, name, f)
			check(t, "typescript", ts.String(), "//")

			var py strings.Builder
			writePyFileHeader(&py, name)
			check(t, "python", py.String(), "#")

			// Swift and Go write the same sanitised name into a // comment.
			check(t, "swift", "// MARK: - "+sourceFileName(name)+"\n\n", "//")
			check(t, "go", "\n// --------------------\n// source: "+sourceFileName(name)+"\n// --------------------\n", "//")
		})
	}
}

func TestSourceFileNameOrdinary(t *testing.T) {
	if got := sourceFileName("a/b/types.go"); got != "types.go" {
		t.Errorf("sourceFileName = %q", got)
	}
}
