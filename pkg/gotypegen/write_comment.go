package gotypegen

import (
	"go/ast"
	"strings"
	"unicode"
)

func (g *PackageGenerator) PreserveDocComments() bool {
	return g.conf.PreserveComments == "default"
}

func (g *PackageGenerator) PreserveTypeComments() bool {
	return g.conf.PreserveComments == "types" || g.conf.PreserveComments == "default"
}

func (g *PackageGenerator) writeCommentGroupIfNotNil(s *strings.Builder, f *ast.CommentGroup, depth int) {
	if f != nil {
		g.writeCommentGroup(s, f, depth)
	}
}

func (c *PackageGenerator) writeDirective(s *strings.Builder, cg *ast.CommentGroup) {
	for _, cm := range cg.List {
		if strings.HasPrefix(cm.Text, "//gotypegen:emit") {
			// remove the separator whitespace but leave extra whitespace for indentation
			s.WriteString(strings.TrimPrefix(cm.Text, "//gotypegen:emit")[1:])
			s.WriteString("\n")
		}
	}
}

func (g *PackageGenerator) writeCommentGroup(s *strings.Builder, cg *ast.CommentGroup, depth int) {
	docLines := strings.Split(cg.Text(), "\n")

	g.writeDirective(s, cg)
	if len(cg.List) > 0 && cg.Text() == "" { // This is a directive comment like //go:embed
		s.WriteByte('\n')
		return
	}

	if depth != 0 {
		g.writeIndent(s, depth)
	}
	s.WriteString("/**\n")

	for _, c := range docLines {
		if len(strings.TrimSpace(c)) == 0 {
			continue
		}
		g.writeIndent(s, depth)
		s.WriteString(" * ")
		s.WriteString(escapeBlockComment(c))
		s.WriteByte('\n')
	}
	g.writeIndent(s, depth)
	s.WriteString(" */\n")
}

// Outputs a comment like // hello world, ending the line.
//
// A trailing Go comment can span lines (a /* */ comment after a field), and
// its text can hold characters the target language reads as a line end. The
// text is folded onto one line so none of it lands outside the comment.
func (g *PackageGenerator) writeSingleLineComment(s *strings.Builder, cg *ast.CommentGroup) {
	if len(cg.List) > 0 && cg.Text() == "" { // This is a directive comment like //go:embed
		s.WriteByte('\n')
		return
	}

	s.WriteString(" // ")
	s.WriteString(singleLine(cg.Text()))
	s.WriteByte('\n')
}

// singleLine folds text onto one line for use inside a line comment. Every
// character that ends a line in one of the output languages becomes a space:
// control characters (\n, \r, \v, \f, NEL) and the Unicode line and
// paragraph separators, which end a // comment in TypeScript. Runs of
// whitespace created that way collapse to one space; leading and trailing
// whitespace is dropped.
func singleLine(text string) string {
	var b strings.Builder
	pendingSpace := false
	for _, r := range text {
		if r == '\u2028' || r == '\u2029' || (unicode.IsControl(r) && r != '\t') {
			pendingSpace = true
			continue
		}
		if pendingSpace && b.Len() > 0 {
			b.WriteByte(' ')
		}
		pendingSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

// escapeBlockComment keeps text inside a /* */ comment: a // comment in the
// Go source can contain the closing marker.
func escapeBlockComment(text string) string {
	return strings.ReplaceAll(text, "*/", "*\\/")
}
