// gotypegen generates TypeScript, JSON Schema, and Python types from Go source code.
//
// Usage: gotypegen [--format=typescript,jsonschema,python] [config.yaml]
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/inference-sh/gotypegen/pkg/gotypegen"
)

func main() {
	formatFlag := flag.String("format", "typescript", "Output formats (comma-separated): typescript, jsonschema, python")
	flag.Parse()

	configPath := "gotypegen.yaml"
	if flag.NArg() > 0 {
		configPath = flag.Arg(0)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error reading config %s: %v\n", configPath, err)
		os.Exit(1)
	}

	var config gotypegen.Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing config: %v\n", err)
		os.Exit(1)
	}

	formats := strings.Split(*formatFlag, ",")
	for i := range formats {
		formats[i] = strings.TrimSpace(formats[i])
	}

	gen := gotypegen.New(&config)
	if err := gen.GenerateWithFormats(formats); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
