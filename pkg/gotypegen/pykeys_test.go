package gotypegen

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A Python keyword JSON key ("async") cannot be a class-form TypedDict key
// without renaming it (async_), which changes the wire key. TypedDict output
// uses the functional form, which keeps the key.
func TestPyTypedDictKeywordKeyUsesFunctionalForm(t *testing.T) {
	gen := loadFixtureDir(t, "testdata/pykeys", &PackageConfig{FieldTags: []string{"merge"}})
	out, err := gen.GeneratePython()
	if err != nil {
		t.Fatalf("GeneratePython: %v", err)
	}
	mustContain(t, out, "Hook = TypedDict('Hook', {\n"+
		"    'event': 'str',\n"+
		"    # Async runs the hook without blocking.\n"+
		"    'async': 'bool',\n"+
		"    'timeout': 'int',\n"+
		"}, total=False)\n")
	if strings.Contains(out, "async_") {
		t.Errorf("TypedDict output renamed the async key:\n%s", out)
	}
	mustContain(t, out, "KeywordDelta = TypedDict('KeywordDelta', {\n"+
		"    'from': 'str',\n"+
		"}, total=False)\n\n"+
		"KeywordDelta._field_tags = {  # type: ignore[attr-defined]\n"+
		"    \"from\": {\"merge\": \"concat\"},\n"+
		"}\n")
}

// A TypedDict body may only declare keys: type checkers reject a value there
// and ClassVar inside a TypedDict. _field_tags is set on the class after it.
func TestPyTypedDictFieldTagsAfterClass(t *testing.T) {
	gen := loadFixtureDir(t, "testdata/pykeys", &PackageConfig{FieldTags: []string{"merge"}})
	out, err := gen.GeneratePython()
	if err != nil {
		t.Fatalf("GeneratePython: %v", err)
	}
	mustContain(t, out, "class Delta(TypedDict, total=False):\n"+
		"    index: int\n"+
		"    name: str\n"+
		"    arguments: str\n"+
		"\n"+
		"Delta._field_tags = {  # type: ignore[attr-defined]\n"+
		"    \"name\": {\"merge\": \"replace\"},\n"+
		"    \"arguments\": {\"merge\": \"concat\"},\n"+
		"}\n")
	if strings.Contains(out, "    _field_tags") {
		t.Errorf("_field_tags emitted inside a TypedDict body:\n%s", out)
	}
}

// pydantic keeps the ClassVar inside the class.
func TestPyPydanticFieldTagsClassVar(t *testing.T) {
	gen := loadFixtureDir(t, "testdata/pykeys", &PackageConfig{PythonStyle: "pydantic", FieldTags: []string{"merge"}})
	out, err := gen.GeneratePython()
	if err != nil {
		t.Fatalf("GeneratePython: %v", err)
	}
	mustContain(t, out, "    _field_tags: ClassVar[dict] = {\n"+
		"        \"name\": {\"merge\": \"replace\"},\n"+
		"        \"arguments\": {\"merge\": \"concat\"},\n"+
		"    }\n")
}

// The generated TypedDict module imports, keeps the wire keys and the tags, and
// type-checks clean under mypy --strict when mypy is installed.
func TestPyTypedDictKeywordKeyRuntimeAndMypy(t *testing.T) {
	// PYTHON picks an interpreter (e.g. a venv with mypy); default python3.
	py := os.Getenv("PYTHON")
	if py == "" {
		var err error
		if py, err = exec.LookPath("python3"); err != nil {
			t.Skip("python3 not on PATH")
		}
	}
	gen := loadFixtureDir(t, "testdata/pykeys", &PackageConfig{FieldTags: []string{"merge"}})
	out, err := gen.GeneratePython()
	if err != nil {
		t.Fatalf("GeneratePython: %v", err)
	}
	dir := t.TempDir()
	mod := filepath.Join(dir, "pykeys_gen.py")
	if err := os.WriteFile(mod, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	check := filepath.Join(dir, "check_gen.py")
	if err := os.WriteFile(check, []byte(`from pykeys_gen import Delta, Hook, KeywordDelta
h: Hook = {"event": "e", "async": True}
assert set(Hook.__annotations__) == {"event", "async", "timeout"}, Hook.__annotations__
assert Delta.__dict__["_field_tags"] == {"name": {"merge": "replace"}, "arguments": {"merge": "concat"}}
assert KeywordDelta.__dict__["_field_tags"] == {"from": {"merge": "concat"}}
assert "_field_tags" not in Delta.__annotations__
`), 0o644); err != nil {
		t.Fatal(err)
	}
	// The script's directory is on sys.path, so it imports the module next to it.
	cmd := exec.Command(py, check)
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("python: %v\n%s\n%s", err, b, out)
	}
	if exec.Command(py, "-m", "mypy", "--version").Run() != nil {
		t.Log("mypy not installed, skipping type check")
		return
	}
	cmd = exec.Command(py, "-m", "mypy", "--strict", "--no-incremental", "--cache-dir", filepath.Join(dir, ".mypy_cache"), mod, check)
	cmd.Dir = dir
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mypy: %v\n%s\n%s", err, b, out)
	}
}
