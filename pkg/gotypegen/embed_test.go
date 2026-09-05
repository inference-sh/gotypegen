package gotypegen

import (
	"strings"
	"testing"
)

// Anonymous struct fields without a json name follow encoding/json: their
// fields are promoted onto the embedding type. With a json name they stay
// a nested field. tstype:",extends" keeps meaning inheritance.

func TestTsInlineEmbeddedStruct(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{FieldTags: []string{"merge"}})
	out, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	mustContain(t, out, "export interface CallInput {")
	mustContain(t, out, "  temperature?: number /* float64 */;")
	mustContain(t, out, "  max_tokens?: number /* int */;")
	mustContain(t, out, "  base: Base;")
	mustContain(t, out, "  prompt: string;")
	mustNotContain(t, out, "GenerationSettings: GenerationSettings")
	// promoted fields' tags are surfaced too
	mustContain(t, out, "export const CallInput_fieldTags = {")
	mustContain(t, out, `  temperature: {merge: "replace"},`)
	mustContain(t, out, `  prompt: {merge: "concat"},`)
}

func TestPyInlineEmbeddedStruct(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{PythonStyle: "pydantic", FieldTags: []string{"merge"}})
	out, err := gen.GeneratePython()
	if err != nil {
		t.Fatalf("GeneratePython: %v", err)
	}
	mustContain(t, out, "class CallInput(BaseModel):")
	mustContain(t, out, "    temperature: Optional[float] = None")
	mustContain(t, out, "    max_tokens: Optional[int] = None")
	mustContain(t, out, "    base: Base")
	mustContain(t, out, "    prompt: str = \"\"")
	mustContain(t, out, `"temperature": {"merge": "replace"}`)
	mustContain(t, out, `"prompt": {"merge": "concat"}`)
}

func TestExtendsStillInherits(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{PythonStyle: "pydantic"})
	out, err := gen.GeneratePython()
	if err != nil {
		t.Fatalf("GeneratePython: %v", err)
	}
	mustContain(t, out, "class LLMDelta(StreamDelta")
}

func TestJSONSchemaInlineEmbeddedStruct(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{})
	out, err := gen.GenerateJSONSchema()
	if err != nil {
		t.Fatalf("GenerateJSONSchema: %v", err)
	}
	mustContain(t, out, `"temperature"`)
	mustContain(t, out, `"max_tokens"`)
	mustContain(t, out, `"base"`)
	// The embedded type is still emitted on its own, but never as a property.
	mustContain(t, out, `"GenerationSettings": {`)
	mustNotContain(t, out, `"GenerationSettings": {"$ref"`)
	mustNotContain(t, out, `"$ref": "#/$defs/GenerationSettings"`)
}

func TestExtendsInheritsInTsAndPy_InlinesInJSONSchema(t *testing.T) {
	gen := loadFixture(t, &PackageConfig{PythonStyle: "pydantic"})
	ts, err := gen.Generate()
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	mustContain(t, ts, "export interface ExtendedCall extends GenerationSettings {")
	mustNotContain(t, ts, "export interface ExtendedCall extends GenerationSettings {\n  temperature")

	py, err := gen.GeneratePython()
	if err != nil {
		t.Fatalf("GeneratePython: %v", err)
	}
	mustContain(t, py, "class ExtendedCall(GenerationSettings")

	js, err := gen.GenerateJSONSchema()
	if err != nil {
		t.Fatalf("GenerateJSONSchema: %v", err)
	}
	// Locate the ExtendedCall definition and check the parent's properties are on it.
	i := strings.Index(js, `"ExtendedCall": {`)
	if i < 0 {
		t.Fatal("ExtendedCall missing from JSON schema")
	}
	block := js[i:]
	if j := strings.Index(block[1:], "\n    \""); j > 0 {
		block = block[:j+1]
	}
	mustContain(t, block, `"temperature"`)
	mustContain(t, block, `"max_tokens"`)
	mustContain(t, block, `"prompt"`)
}
