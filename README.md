# gotypegen

Generate TypeScript, Python, JSON Schema, and Go types from Go source code.

Originally based on [tygo](https://github.com/gzuidhof/tygo) by Guido Zuidhof (MIT). Extended with Python TypedDict/pydantic output, JSON Schema output, Go source output with method tracing, and dependency tracing.

## Install

```bash
go install github.com/inference-sh/gotypegen/cmd/gotypegen@latest
```

## Usage

```bash
gotypegen [--format=typescript,jsonschema,python,go] [config.yaml]
```

Multiple formats can be comma-separated; each writes its own output file derived from `output_path`.

### Config

```yaml
packages:
  - path: "your/go/package"
    output_path: "gen/types.ts"
    type_mappings:
      time.Time: "string /* RFC3339 */"
      uuid.UUID: "string /* uuid */"
```

### Formats

- `typescript` (default) — TypeScript interfaces and const exports
- `python` — Python TypedDict or pydantic BaseModel classes, StrEnum/IntEnum
- `jsonschema` — JSON Schema 2020-12 definitions
- `go` — Go source with methods, tag stripping, and `go.mod` generation

### Python Output

By default, Python output uses `TypedDict` (suitable for wire types and SDK consumers). Set `python_style: "pydantic"` to emit pydantic `BaseModel` classes instead (suitable for app developers who need defaults, validation, and inheritance).

```yaml
packages:
  - path: "your/go/package"
    output_path: "gen/llm_types.py"
    python_style: "pydantic"
    mode: "trace"
    extra_types:
      - LLMOutput
      - LLMUsage
```

Pydantic mode differences from TypedDict mode:
- Structs emit as `class Foo(BaseModel)` instead of `class Foo(TypedDict, total=False)`
- Pointer fields (`*string`) become `Optional[str] = None`
- Non-pointer scalar fields (`string`, `int`, `float64`, `bool`) get Go zero-value defaults (`= ""`, `= 0`, `= 0.0`, `= False`)
- Forward references are resolved via `model_rebuild()` calls at the end of the file
- Fields with JSON names that aren't valid Python identifiers use `Field(alias="...")` instead of the functional TypedDict form

### Dependency Tracing

Only emit types reachable from specific entry files or phantom structs:

```yaml
packages:
  - path: "your/go/package"
    output_path: "gen/sdk.ts"
    mode: "trace"
    entry_files:
      - api.go
    extra_types:
      - SDKTypes          # phantom struct — fields are traced as roots
      - ApiAppRunRequest  # individual type names
```

### Go Output

Generate standalone Go packages from traced types, including methods and struct tag filtering:

```yaml
packages:
  - path: "your/go/package"
    output_path: "gen/sdk.go"
    mode: "trace"
    entry_files:
      - api.go
    go_package: "sdk"
    go_module: "github.com/you/sdk-go"
    keep_tags:
      - json
      - yaml
    inline_packages:
      - "your/go/package/shared"
```

- `go_package` — package name for generated code (default: `types`)
- `go_module` — if set, also generates a `go.mod`
- `keep_tags` — allowlist of struct tags to keep (strips all others, e.g. `gorm`, `validate`)
- `inline_packages` — import paths whose types are flattened into the output (e.g. `shared.TaskStatus` becomes `TaskStatus`)
- Methods on traced types are included if they only reference stdlib and other traced types

### All Config Options

| Option | Type | Default | Description |
|--------|------|---------|-------------|
| `path` | string | required | Go package import path |
| `output_path` | string | `index.ts` | Output file path |
| `mode` | string | `"all"` | `"all"` or `"trace"` |
| `entry_files` | []string | | Starting files for trace mode |
| `extra_types` | []string | | Additional type names to always include |
| `type_mappings` | map | | Custom Go→target type translations |
| `frontmatter` | string | | Content prepended to output |
| `exclude_files` | []string | | Go source files to skip |
| `include_files` | []string | | If set, only these files are processed |
| `python_style` | string | `"typeddict"` | `"typeddict"` or `"pydantic"` |
| `go_package` | string | `"types"` | Package name for Go output |
| `go_module` | string | | Module path for generated go.mod |
| `keep_tags` | []string | all | Struct tag allowlist for Go output |
| `inline_packages` | []string | | Packages to flatten into output |
| `flavor` | string | `"default"` | Key naming: `"default"` or `"yaml"` |
| `preserve_comments` | string | `"default"` | `"default"`, `"types"`, or `"none"` |
| `optional_type` | string | `"undefined"` | TS optional: `"undefined"` or `"null"` |
| `extends` | string | | Default interface for TS to extend |
| `fallback_type` | string | `"any"` | Type for unrecognized Go types |

### Directives

Use `//gotypegen:emit` to inject raw output:

```go
//gotypegen:emit export type CustomType = string | number;
var _ = ""
```

## License

MIT — see [LICENSE](LICENSE) and [THIRD_PARTY](THIRD_PARTY).
