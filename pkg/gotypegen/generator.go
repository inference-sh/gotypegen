package gotypegen

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/tools/go/packages"
)

// Generator manages code generation for one or more input packages.
type Generator struct {
	conf *Config

	packageGenerators map[string]*PackageGenerator
}

// PackageGenerator is responsible for generating the code for an input package.
type PackageGenerator struct {
	conf    *PackageConfig
	pkg     *packages.Package
	GoFiles []string
}

func New(config *Config) *Generator {
	return &Generator{
		conf:              config,
		packageGenerators: make(map[string]*PackageGenerator),
	}
}

func (g *Generator) SetTypeMapping(goType string, tsType string) {
	for _, p := range g.conf.Packages {
		p.TypeMappings[goType] = tsType
	}
}

func (g *Generator) Generate() error {
	return g.GenerateWithFormats([]string{"typescript"})
}

// GenerateWithFormats generates output for specified formats: typescript, jsonschema, python
func (g *Generator) GenerateWithFormats(formats []string) error {
	pkgs, err := packages.Load(&packages.Config{
		Mode: packages.NeedSyntax | packages.NeedFiles | packages.NeedTypes | packages.NeedTypesInfo | packages.NeedImports,
	}, g.conf.PackageNames()...)
	if err != nil {
		return err
	}

	for i, pkg := range pkgs {
		if len(pkg.Errors) > 0 {
			return fmt.Errorf("%+v", pkg.Errors)
		}

		if len(pkg.GoFiles) == 0 {
			return fmt.Errorf("no input go files for package index %d", i)
		}

		pkgConfig := g.conf.PackageConfig(pkg.ID)
		pkgDir := filepath.Dir(pkg.GoFiles[0])

		pkgGen := &PackageGenerator{
			conf:    pkgConfig,
			GoFiles: pkg.GoFiles,
			pkg:     pkg,
		}
		g.packageGenerators[pkg.PkgPath] = pkgGen

		for _, format := range formats {
			var code string
			var outPath string

			switch format {
			case "typescript", "ts":
				code, err = pkgGen.Generate()
				outPath = pkgGen.conf.ResolvedOutputPath(pkgDir)
			case "jsonschema", "json", "schema":
				code, err = pkgGen.GenerateJSONSchema()
				outPath = filepath.Join(filepath.Dir(pkgGen.conf.ResolvedOutputPath(pkgDir)), "schema.json")
			case "python", "py":
				code, err = pkgGen.GeneratePython()
				outPath = pkgGen.conf.resolvedOutputPathForExt(pkgDir, ".py")
			case "go", "golang":
				code, err = pkgGen.GenerateGo()
				outPath = pkgGen.conf.resolvedOutputPathForExt(pkgDir, ".go")

				// Also write go.mod if configured
				if err == nil {
					if goMod := pkgGen.GenerateGoMod(); goMod != "" {
						modPath := filepath.Join(filepath.Dir(outPath), "go.mod")
						if writeErr := os.WriteFile(modPath, []byte(goMod), 0664); writeErr != nil {
							return fmt.Errorf("writing go.mod: %w", writeErr)
						}
						fmt.Printf("Generated %s\n", modPath)
					}
				}
			default:
				return fmt.Errorf("unknown format: %s", format)
			}

			if err != nil {
				return fmt.Errorf("generating %s: %w", format, err)
			}

			if err := os.MkdirAll(filepath.Dir(outPath), os.ModePerm); err != nil {
				return err
			}

			if err := os.WriteFile(outPath, []byte(code), 0664); err != nil {
				return err
			}

			fmt.Printf("Generated %s\n", outPath)
		}
	}
	return nil
}
