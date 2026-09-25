package gotypegen

import (
	"strings"
	"testing"
)

func TestTsStringEnumsAreLiteralUnions(t *testing.T) {
	gen := loadFixtureDir(t, "testdata/enums", &PackageConfig{})
	out, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	mustContain(t, out, `export type Color = "red" | "blue";`)
	mustContain(t, out, "export type Stage =\n  | \"queued\"\n  | \"running\"\n  | \"done\"\n  | \"failed\"\n  | \"skipped\";")
	mustContain(t, out, "export type Label = string;")
	mustContain(t, out, "export type Name = string;")
}

func TestTsOpenStringEnums(t *testing.T) {
	gen := loadFixtureDir(t, "testdata/enums", &PackageConfig{StringEnums: "open"})
	out, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	mustContain(t, out, `export type Color = "red" | "blue" | (string & {});`)
	mustContain(t, out, "  | \"skipped\"\n  | (string & {});")
}

func TestUntypedConstGroupsWarn(t *testing.T) {
	gen := loadFixtureDir(t, "testdata/enums", &PackageConfig{Mode: "trace"})
	warnings := gen.untypedConstGroups()
	if len(warnings) != 1 {
		t.Fatalf("want 1 warning, got %d: %v", len(warnings), warnings)
	}
	if !strings.Contains(warnings[0], "/enums.go:34: untyped string consts GovernedBySelf, GovernedByOrg") {
		t.Errorf("unexpected warning: %s", warnings[0])
	}
}
