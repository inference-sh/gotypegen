package gotypegen

import "testing"

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
