package gotypegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/tools/go/packages"
	"gotest.tools/v3/golden"
)

// loadFixture loads the testdata/fixture package and returns a PackageGenerator.
func loadFixture(t *testing.T, conf *PackageConfig) *PackageGenerator {
	t.Helper()

	fixtureDir, err := filepath.Abs("testdata/fixture")
	if err != nil {
		t.Fatalf("abs path: %v", err)
	}

	normalized, err := conf.Normalize()
	if err != nil {
		t.Fatalf("normalize config: %v", err)
	}

	pkgs, err := packages.Load(&packages.Config{
		Mode: packages.NeedSyntax | packages.NeedFiles | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
		Dir:  fixtureDir,
	}, ".")
	if err != nil {
		t.Fatalf("load packages: %v", err)
	}
	if len(pkgs) == 0 {
		t.Fatal("no packages loaded")
	}
	if len(pkgs[0].Errors) > 0 {
		t.Fatalf("package errors: %v", pkgs[0].Errors)
	}

	return &PackageGenerator{
		conf:    &normalized,
		pkg:     pkgs[0],
		GoFiles: pkgs[0].GoFiles,
	}
}

// ============================================================
// Golden file (snapshot) tests
//
// Update all golden files:  go test ./pkg/gotypegen/ -update
// Update specific format:   go test ./pkg/gotypegen/ -run Golden/go -update
// ============================================================

func TestGolden(t *testing.T) {
	// --- Go output ---

	t.Run("go/all", func(t *testing.T) {
		gen := loadFixture(t, &PackageConfig{GoPackage: "types"})
		code, err := gen.GenerateGo()
		if err != nil {
			t.Fatal(err)
		}
		golden.Assert(t, code, "go_all.go.golden")
	})

	t.Run("go/traced", func(t *testing.T) {
		gen := loadFixture(t, &PackageConfig{
			GoPackage:  "types",
			Mode:       "trace",
			EntryFiles: []string{"api.go"},
			KeepTags:   []string{"json"},
		})
		code, err := gen.GenerateGo()
		if err != nil {
			t.Fatal(err)
		}
		golden.Assert(t, code, "go_traced.go.golden")
	})

	// --- TypeScript output ---

	t.Run("ts/all", func(t *testing.T) {
		gen := loadFixture(t, &PackageConfig{})
		code, err := gen.Generate()
		if err != nil {
			t.Fatal(err)
		}
		golden.Assert(t, code, "ts_all.ts.golden")
	})

	t.Run("ts/traced", func(t *testing.T) {
		gen := loadFixture(t, &PackageConfig{
			Mode:       "trace",
			EntryFiles: []string{"api.go"},
		})
		code, err := gen.Generate()
		if err != nil {
			t.Fatal(err)
		}
		golden.Assert(t, code, "ts_traced.ts.golden")
	})

	// --- Python output ---

	t.Run("py/all", func(t *testing.T) {
		gen := loadFixture(t, &PackageConfig{})
		code, err := gen.GeneratePython()
		if err != nil {
			t.Fatal(err)
		}
		golden.Assert(t, code, "py_all.py.golden")
	})

	t.Run("py/traced", func(t *testing.T) {
		gen := loadFixture(t, &PackageConfig{
			Mode:       "trace",
			EntryFiles: []string{"api.go"},
		})
		code, err := gen.GeneratePython()
		if err != nil {
			t.Fatal(err)
		}
		golden.Assert(t, code, "py_traced.py.golden")
	})

	// --- JSON Schema output ---

	t.Run("jsonschema/all", func(t *testing.T) {
		gen := loadFixture(t, &PackageConfig{})
		code, err := gen.GenerateJSONSchema()
		if err != nil {
			t.Fatal(err)
		}
		golden.Assert(t, code, "jsonschema_all.json.golden")
	})

	t.Run("jsonschema/traced", func(t *testing.T) {
		gen := loadFixture(t, &PackageConfig{
			Mode:       "trace",
			EntryFiles: []string{"api.go"},
		})
		code, err := gen.GenerateJSONSchema()
		if err != nil {
			t.Fatal(err)
		}
		golden.Assert(t, code, "jsonschema_traced.json.golden")
	})
}

// ============================================================
// Behavioral tests — Go backend specifics
// ============================================================

func TestGoEmitsTypes(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{GoPackage: "types"})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	mustContain(t, code, "package types")
	mustContain(t, code, "type App struct {")
	mustContain(t, code, "type AppVersion struct {")
	mustContain(t, code, "type Base struct {")
	mustContain(t, code, "type AppCategory string")
	mustContain(t, code, "type TaskStatus int")
	mustNotContain(t, code, "unexportedType")
}

func TestGoEmitsConsts(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{GoPackage: "types"})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	mustContain(t, code, "AppCategoryImage")
	mustContain(t, code, `"image"`)
	mustContain(t, code, "TaskStatusQueued")
	mustContain(t, code, "iota")
}

func TestGoEmitsGenerics(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{GoPackage: "types"})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	mustContain(t, code, "type APIResponse[T any] struct {")
	mustContain(t, code, "Data T")
}

func TestGoEmitsEmbeddedTypes(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{GoPackage: "types"})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	mustContain(t, code, "type Base struct {")
	mustContain(t, code, "type App struct {")
}

// --- Tag stripping tests ---

func TestGoTagStripping(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{GoPackage: "types", KeepTags: []string{"json"}})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	mustContain(t, code, `json:"name"`)
	mustContain(t, code, `json:"id"`)
	mustNotContain(t, code, "gorm:")
	mustNotContain(t, code, "primaryKey")
	mustNotContain(t, code, "validate:")
}

func TestGoTagStrippingKeepMultiple(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{GoPackage: "types", KeepTags: []string{"json", "validate"}})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	mustContain(t, code, `json:"name"`)
	mustContain(t, code, `validate:"required"`)
	mustNotContain(t, code, "gorm:")
}

func TestGoTagStrippingNoFilter(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{GoPackage: "types"})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	mustContain(t, code, `json:"name"`)
	mustContain(t, code, "gorm:")
}

// --- Trace mode ---

func TestGoTracedMode(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{
		GoPackage: "types", Mode: "trace", EntryFiles: []string{"api.go"}, KeepTags: []string{"json"},
	})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	mustContain(t, code, "type App struct {")
	mustContain(t, code, "type AppVersion struct {")
	mustContain(t, code, "type AppCategory string")
	mustContain(t, code, "type Base struct {")
	mustContain(t, code, "type TaskStatus int")
	mustContain(t, code, "type APIResponse[T any]")
	mustNotContain(t, code, "type Unreferenced struct")
}

func TestGoTracedMethodFiltering(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{
		GoPackage: "types", Mode: "trace", EntryFiles: []string{"api.go"}, KeepTags: []string{"json"},
	})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	mustContain(t, code, "func (a App) FullName() string")
	mustContain(t, code, "func (a *App) Ref() string")
	mustContain(t, code, "func (v AppVersion) String() string")
	mustContain(t, code, "func (ts TaskStatus) String() string")
	mustContain(t, code, "func (a App) MarshalApp()")
}

func TestGoTracedExternalMethodFiltered(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{
		GoPackage: "types", Mode: "trace", EntryFiles: []string{"api.go"}, KeepTags: []string{"json"},
	})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	mustNotContain(t, code, "ParseTags")
	mustContain(t, code, "FullName")
}

func TestGoTracedLocalTypeRefFiltered(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{
		GoPackage: "types", Mode: "trace", EntryFiles: []string{"api.go"}, KeepTags: []string{"json"},
	})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	// GetUnreferenced references Unreferenced which is not in trace set — should be filtered
	mustNotContain(t, code, "GetUnreferenced")
	// But FullName (stdlib only) and MarshalApp (stdlib only) should still be present
	mustContain(t, code, "FullName")
	mustContain(t, code, "MarshalApp")
}

func TestGoTracedPkgFuncFiltered(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{
		GoPackage: "types", Mode: "trace", EntryFiles: []string{"api.go"}, KeepTags: []string{"json"},
	})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	// UsesHelper calls helperFunc (package-level) — should be filtered
	mustNotContain(t, code, "UsesHelper")
	mustNotContain(t, code, "helperFunc")
}

func TestGoTracedPkgVarFiltered(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{
		GoPackage: "types", Mode: "trace", EntryFiles: []string{"api.go"}, KeepTags: []string{"json"},
	})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	// Label() uses statusLabels (package-level var) — should be filtered
	mustNotContain(t, code, "func (ts TaskStatus) Label()")
	mustNotContain(t, code, "statusLabels")
}

func TestGoTracedCascadingMethodFiltered(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{
		GoPackage: "types", Mode: "trace", EntryFiles: []string{"api.go"}, KeepTags: []string{"json"},
	})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	// IsTerminal uses statusLabels (filtered) → CanTransition calls IsTerminal → also filtered
	mustNotContain(t, code, "IsTerminal")
	mustNotContain(t, code, "CanTransition")
	// But String() uses only consts (emitted) — should be present
	mustContain(t, code, "func (ts TaskStatus) String() string")
}

func TestGoTracedLocalTypeInMethodBody(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{
		GoPackage: "types", Mode: "trace", EntryFiles: []string{"api.go"}, KeepTags: []string{"json"},
	})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	// MarshalLocal uses a local type (type Local struct{}) — should compile
	mustContain(t, code, "MarshalLocal")
	mustContain(t, code, "type Local struct")
}

func TestGoTracedConstsInMethodAllowed(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{
		GoPackage: "types", Mode: "trace", EntryFiles: []string{"api.go"}, KeepTags: []string{"json"},
	})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	// TaskStatus.String() uses TaskStatusQueued etc (consts for an included type) — allowed
	mustContain(t, code, "func (ts TaskStatus) String() string")
	mustContain(t, code, "TaskStatusQueued")
	mustContain(t, code, "TaskStatusFailed")
}

func TestGoTracedShadowedVarName(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{
		GoPackage: "types", Mode: "trace", EntryFiles: []string{"api.go"}, KeepTags: []string{"json"},
	})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	// ShadowedName has a local var named "App" — should NOT be filtered.
	// Old heuristics would false-positive match the var name against the type.
	// go/types knows it's a local string variable, not a type reference.
	mustContain(t, code, "func (a App) ShadowedName() string")
}

func TestGoTracedMethodWithTracedParam(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{
		GoPackage: "types", Mode: "trace", EntryFiles: []string{"api.go"}, KeepTags: []string{"json"},
	})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	// HasVersion takes AppVersion (a traced type) as param — should be included
	mustContain(t, code, "func (a App) HasVersion(v AppVersion) bool")
}

func TestGoTracedConstsIncluded(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{
		GoPackage: "types", Mode: "trace", EntryFiles: []string{"api.go"},
	})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	mustContain(t, code, "AppCategoryImage")
	mustContain(t, code, "TaskStatusQueued")
}

// --- go.mod ---

func TestGoModGeneration(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{GoPackage: "types", GoModule: "example.com/types"})
	mod := gen.GenerateGoMod()
	mustContain(t, mod, "module example.com/types")
	mustContain(t, mod, "go 1.21")
}

func TestGoModNotGeneratedWhenEmpty(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{GoPackage: "types"})
	if mod := gen.GenerateGoMod(); mod != "" {
		t.Errorf("expected empty go.mod, got: %s", mod)
	}
}

// --- Package declaration ---

func TestGoPackageDeclaration(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{GoPackage: "mypkg"})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	mustContain(t, code, "package mypkg")
	mustContain(t, code, "// Code generated by gotypegen. DO NOT EDIT.")
}

func TestGoDefaultPackageName(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, code, "package types")
}

// --- Imports ---

func TestGoImportsCollected(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{GoPackage: "types"})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}
	mustContain(t, code, `"time"`)
}

// --- Round-trip compilation ---

func TestGoOutputCompiles(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{
		GoPackage: "types", GoModule: "example.com/roundtrip", KeepTags: []string{"json"},
	})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(code), 0644)
	os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(gen.GenerateGoMod()), 0644)

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = tmpDir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated code does not compile:\n%s\n\nCode:\n%s", output, code)
	}
}

func TestGoTracedOutputCompiles(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{
		GoPackage: "types", GoModule: "example.com/roundtrip-traced",
		Mode: "trace", EntryFiles: []string{"api.go"}, KeepTags: []string{"json"},
	})
	code, err := gen.GenerateGo()
	if err != nil {
		t.Fatal(err)
	}

	tmpDir := t.TempDir()
	os.WriteFile(filepath.Join(tmpDir, "types.go"), []byte(code), 0644)
	os.WriteFile(filepath.Join(tmpDir, "go.mod"), []byte(gen.GenerateGoMod()), 0644)

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = tmpDir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated code does not compile:\n%s\n\nCode:\n%s", output, code)
	}
}

// ============================================================
// Unit tests
// ============================================================

func TestExtractTagKeys(t *testing.T) {
	tests := []struct {
		tag  string
		want []string
	}{
		{`json:"name" gorm:"not null"`, []string{"json", "gorm"}},
		{`json:"id" gorm:"primaryKey" yaml:"id"`, []string{"json", "gorm", "yaml"}},
		{`json:"-"`, []string{"json"}},
		{``, nil},
		{`json:"name,omitempty"`, []string{"json"}},
	}

	for _, tt := range tests {
		got := extractTagKeys(tt.tag)
		if len(got) != len(tt.want) {
			t.Errorf("extractTagKeys(%q) = %v, want %v", tt.tag, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("extractTagKeys(%q)[%d] = %q, want %q", tt.tag, i, got[i], tt.want[i])
			}
		}
	}
}

func TestFilterStructTag(t *testing.T) {
	gen := &PackageGenerator{conf: &PackageConfig{KeepTags: []string{"json"}}}
	tests := []struct {
		input, contains, excludes string
	}{
		{"`json:\"name\" gorm:\"not null\"`", `json:"name"`, "gorm"},
		{"`json:\"id\" gorm:\"primaryKey\" yaml:\"id\"`", `json:"id"`, "gorm"},
		{"`gorm:\"primaryKey\"`", "", "gorm"},
	}
	for _, tt := range tests {
		got := gen.filterStructTag(tt.input)
		if tt.contains != "" && !strings.Contains(got, tt.contains) {
			t.Errorf("filterStructTag(%q) = %q, want to contain %q", tt.input, got, tt.contains)
		}
		if tt.excludes != "" && strings.Contains(got, tt.excludes) {
			t.Errorf("filterStructTag(%q) = %q, should not contain %q", tt.input, got, tt.excludes)
		}
	}
}

func TestIsStdlib(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"fmt", true}, {"encoding/json", true}, {"strings", true}, {"time", true},
		{"github.com/foo/bar", false}, {"golang.org/x/tools", false}, {"example.com/pkg", false},
	}
	for _, tt := range tests {
		if got := isStdlib(tt.path); got != tt.want {
			t.Errorf("isStdlib(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestShouldKeepTag(t *testing.T) {
	conf := PackageConfig{KeepTags: []string{"json", "yaml"}}
	if !conf.ShouldKeepTag("json") {
		t.Error("should keep json")
	}
	if conf.ShouldKeepTag("gorm") {
		t.Error("should not keep gorm")
	}
	if empty := (PackageConfig{}); !empty.ShouldKeepTag("anything") {
		t.Error("empty KeepTags should keep everything")
	}
}

func TestReceiverTypeName(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{GoPackage: "types"})
	graph := gen.BuildTypeGraph()

	for _, name := range []string{"App", "AppVersion", "TaskStatus"} {
		if methods, ok := graph.Methods[name]; !ok || len(methods) == 0 {
			t.Errorf("expected methods on %s type", name)
		}
	}
}

func TestFilterMethodsOnlyStdlib(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{
		GoPackage: "types", Mode: "trace", EntryFiles: []string{"api.go"},
	})

	graph := gen.BuildTypeGraph()
	filtered := FilterMethods(graph, gen.TraceTypes(graph), gen.pkg.TypesInfo, gen.pkg.Types.Scope())

	names := make(map[string]bool)
	for _, m := range filtered["App"] {
		names[m.FuncDecl.Name.Name] = true
	}
	for _, expected := range []string{"FullName", "Ref", "MarshalApp"} {
		if !names[expected] {
			t.Errorf("expected %s method to be included", expected)
		}
	}
}

func TestGoOutputDeterministic(t *testing.T) {
	conf := &PackageConfig{GoPackage: "types", KeepTags: []string{"json"}}
	gen1 := loadFixture(t, conf)
	code1, _ := gen1.GenerateGo()
	gen2 := loadFixture(t, conf)
	code2, _ := gen2.GenerateGo()
	if code1 != code2 {
		t.Error("expected identical output from two generation runs")
	}
}

// ============================================================
// helpers
// ============================================================

func mustContain(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("output should contain %q but doesn't.\nFirst 2000 chars:\n%s", substr, truncate(s, 2000))
	}
}

func mustNotContain(t *testing.T, s, substr string) {
	t.Helper()
	if strings.Contains(s, substr) {
		t.Errorf("output should NOT contain %q but does.\nFirst 2000 chars:\n%s", substr, truncate(s, 2000))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "\n... (truncated)"
}
