package gotypegen

import (
	"fmt"
	"go/parser"
	"go/token"
	"strings"
)

// ConvertGoToTypescript converts Go code string to Typescript.
// Mostly useful for testing purposes.
func ConvertGoToTypescript(goCode string, pkgConfig PackageConfig) (string, error) {
	src := fmt.Sprintf(`package gotypegen_test

%s`, goCode)

	fset := token.NewFileSet()

	f, err := parser.ParseFile(fset, "", src, parser.AllErrors|parser.ParseComments)
	if err != nil {
		return "", fmt.Errorf("failed to parse source: %w", err)
	}

	pkgConfig, err = pkgConfig.Normalize()
	if err != nil {
		return "", fmt.Errorf("failed to normalize package config: %w", err)
	}

	pkgGen := &PackageGenerator{
		conf: &pkgConfig,
		pkg:  nil,
	}

	s := new(strings.Builder)

	pkgGen.generateFile(s, f, "")
	code := s.String()

	return code, nil
}
