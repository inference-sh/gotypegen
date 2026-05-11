# gotypegen

Generate TypeScript, Python, JSON Schema, and Go types from Go source code.

Originally based on [tygo](https://github.com/gzuidhof/tygo) by Guido Zuidhof (MIT). Extended with Python TypedDict/Enum output, JSON Schema output, Go source output with method tracing, and dependency tracing.

## Install

```bash
go install github.com/inference-sh/gotypegen/cmd/gotypegen@latest
```

## Usage

```bash
gotypegen [--format=typescript,jsonschema,python,go] [config.yaml]
```

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
- `python` — Python TypedDict classes and StrEnum/IntEnum
- `jsonschema` — JSON Schema 2020-12 definitions
- `go` — Go source with methods, tag stripping, and `go.mod` generation

### Dependency Tracing

Only emit types reachable from specific entry files:

```yaml
packages:
  - path: "your/go/package"
    output_path: "gen/sdk.ts"
    mode: "trace"
    entry_files:
      - api.go
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
```

- `go_package` — package name for generated code (default: `types`)
- `go_module` — if set, also generates a `go.mod`
- `keep_tags` — allowlist of struct tags to keep (strips all others, e.g. `gorm`, `validate`)
- Methods on traced types are included if they only reference stdlib and other traced types

### Directives

Use `//gotypegen:emit` to inject raw output:

```go
//gotypegen:emit export type CustomType = string | number;
var _ = ""
```

## License

MIT — see [LICENSE](LICENSE) and [THIRD_PARTY](THIRD_PARTY).
