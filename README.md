# gotypegen

Generate TypeScript, Python, and JSON Schema types from Go source code.

Originally based on [tygo](https://github.com/gzuidhof/tygo) by Guido Zuidhof (MIT). Extended with Python TypedDict/Enum output, JSON Schema output, and dependency tracing.

## Install

```bash
go install github.com/inference-sh/gotypegen/cmd/gotypegen@latest
```

## Usage

```bash
gotypegen [--format=typescript,jsonschema,python] [config.yaml]
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

### Directives

Use `//gotypegen:emit` to inject raw output:

```go
//gotypegen:emit export type CustomType = string | number;
var _ = ""
```

## License

MIT — see [LICENSE](LICENSE) and [THIRD_PARTY](THIRD_PARTY).
