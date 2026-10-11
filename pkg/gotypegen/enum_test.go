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
	mustContain(t, out, "counts: { [key in Stage]?: number /* int */};")
	mustContain(t, out, "tags: { [key: Label]: string};")
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

// Go spreads one type's consts over several blocks; Python gets one class per
// type, or the later class shadows the earlier one and its members vanish.
func TestPyEnumMergesConstBlocks(t *testing.T) {
	for _, mode := range []string{"", "trace"} {
		t.Run("mode="+mode, func(t *testing.T) {
			conf := &PackageConfig{Mode: mode}
			if mode == "trace" {
				conf.EntryFiles = []string{"enums.go"}
				conf.ExtraTypes = []string{"Item"}
			}
			gen := loadFixtureDir(t, "testdata/enums", conf)
			out, err := gen.GeneratePython()
			if err != nil {
				t.Fatalf("GeneratePython: %v", err)
			}
			for _, class := range []string{"class Color(", "class Priority("} {
				if n := strings.Count(out, class); n != 1 {
					t.Errorf("%q emitted %d times, want 1:\n%s", class, n, out)
				}
			}
			mustContain(t, out, "class Color(str, Enum):\n"+
				"    RED = \"red\"\n"+
				"    BLUE = \"blue\"\n"+
				"    # ColorCrimson repeats a value and lives outside the group.\n"+
				"    CRIMSON = \"red\"\n")
			mustContain(t, out, "class Priority(IntEnum):\n"+
				"    LOW = 0\n"+
				"    HIGH = 1\n"+
				"    # PriorityUrgent is declared on its own.\n"+
				"    URGENT = 9\n")
		})
	}
}
