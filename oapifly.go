// Package oapifly generates OpenAPI 3.0 specifications on the fly by scanning
// Go source files for swaggo-style annotations at runtime.
//
// Unlike traditional OpenAPI tooling that generates code from a spec, oapifly
// works in reverse: it reads your annotated Go source code and produces a live
// OpenAPI spec. This means your API documentation is always in sync with your
// source code, with zero build steps.
//
// Usage:
//
//	generator := oapifly.New(oapifly.Config{
//		Title:        "My API",
//		Version:      "1.0.0",
//		ScanPatterns: []string{"internal/controllers/**/*.go"},
//	})
//
//	// Get the spec as a map
//	spec := generator.Generate()
//
//	// Or as serialized bytes
//	jsonBytes, _ := generator.JSON()
//	yamlBytes, _ := generator.YAML()
package oapifly

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"

	yaml "gopkg.in/yaml.v2"
)

// Generator scans Go source files for swaggo-style annotations
// and produces an OpenAPI 3.0 specification at runtime.
type Generator struct {
	Config Config
}

// New creates a new Generator with the given config.
func New(config Config) *Generator {
	if config.Version == "" {
		config.Version = "dev"
	}
	return &Generator{Config: config}
}

// Generate builds and returns the OpenAPI spec as a map.
func (g *Generator) Generate() map[string]interface{} {
	paths := map[string]map[string]PathItem{}
	schemaTypes := map[string]map[string]interface{}{}

	files := []string{}
	for _, pattern := range g.Config.ScanPatterns {
		matches, err := filepath.Glob(pattern)
		if err == nil && len(matches) > 0 {
			files = append(files, matches...)
		}
	}
	if len(files) == 0 {
		return map[string]interface{}{"error": "No files found for scan patterns: " + strings.Join(g.Config.ScanPatterns, ", ")}
	}
	for _, file := range files {
		astFile, err := parseFile(file)
		if err != nil {
			continue
		}
		handlers := extractHandlerDocs(astFile)
		for _, tags := range handlers {
			path, method := parseRouterTag(tags["Router"])
			if path == "" || method == "" {
				continue
			}
			if _, ok := paths[path]; !ok {
				paths[path] = map[string]PathItem{}
			}
			paths[path][method] = buildPathItemWithSchemas(tags, schemaTypes)
		}
		schemaStructs := extractSchemaAnnotatedStructs(astFile)
		for _, structName := range schemaStructs {
			if _, ok := schemaTypes[structName]; !ok {
				typeFile := findTypeFile(structName)
				if typeFile != "" {
					var structType reflect.Type
					if t := findReflectTypeByName(structName); t != nil {
						structType = t
					}
					if structType != nil {
						schemaTypes[structName] = generateSchemaForTypeReflect(structType)
					} else {
						schemaTypes[structName] = map[string]interface{}{"type": "object"}
					}
				} else {
					schemaTypes[structName] = map[string]interface{}{"type": "object"}
				}
			}
		}
	}

	components := map[string]interface{}{}
	if len(schemaTypes) > 0 {
		components["schemas"] = schemaTypes
	}

	return map[string]interface{}{
		"openapi":    "3.0.0",
		"info":       map[string]string{"title": g.Config.Title, "version": g.Config.Version},
		"paths":      paths,
		"components": components,
	}
}

// JSON returns the OpenAPI spec as JSON bytes.
func (g *Generator) JSON() ([]byte, error) {
	return json.Marshal(g.Generate())
}

// YAML returns the OpenAPI spec as YAML bytes.
func (g *Generator) YAML() ([]byte, error) {
	return yaml.Marshal(g.Generate())
}
