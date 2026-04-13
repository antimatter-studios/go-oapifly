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
	"fmt"
	"path/filepath"
	"strings"

	yaml "gopkg.in/yaml.v2"
)

// Generator scans Go source files for swaggo-style annotations
// and produces an OpenAPI 3.0 specification at runtime.
type Generator struct {
	Config Config

	// Warnings collects non-fatal issues encountered during generation
	// (e.g. files that failed to parse). Reset on each call to Generate.
	Warnings []string
}

// New creates a new Generator with the given config.
func New(config Config) *Generator {
	if config.Version == "" {
		config.Version = "dev"
	}
	if len(config.TypeDirs) == 0 {
		config.TypeDirs = []string{"types"}
	}
	return &Generator{Config: config}
}

// resolveFiles expands glob patterns and returns matching file paths.
func resolveFiles(patterns []string) []string {
	var files []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err == nil {
			files = append(files, matches...)
		}
	}
	return files
}

// Generate builds and returns the OpenAPI spec as a map.
func (g *Generator) Generate() map[string]interface{} {
	g.Warnings = nil

	reg := newSchemaRegistry(g.Config.TypeDirs)
	paths := map[string]map[string]PathItem{}

	files := resolveFiles(g.Config.ScanPatterns)
	if len(files) == 0 {
		return map[string]interface{}{"error": "No files found for scan patterns: " + strings.Join(g.Config.ScanPatterns, ", ")}
	}

	for _, file := range files {
		astFile, err := parseFile(file)
		if err != nil {
			g.Warnings = append(g.Warnings, fmt.Sprintf("failed to parse %s: %v", file, err))
			continue
		}
		handlers := extractHandlerDocs(astFile)
		for _, tags := range handlers {
			path, method := parseRouterTag(tags.get("Router"))
			if path == "" || method == "" {
				continue
			}
			if _, ok := paths[path]; !ok {
				paths[path] = map[string]PathItem{}
			}
			if _, exists := paths[path][method]; exists {
				g.Warnings = append(g.Warnings, fmt.Sprintf("duplicate handler for %s %s, overwriting previous", strings.ToUpper(method), path))
			}
			paths[path][method] = buildPathItem(tags, reg)
		}
		schemaStructs := extractSchemaAnnotatedStructs(astFile)
		for _, structName := range schemaStructs {
			if _, ok := reg.schemas[structName]; !ok {
				typeFile := findTypeFile(structName, reg.typeDirs)
				if typeFile != "" {
					if t := findReflectTypeByName(structName); t != nil {
						reg.schemas[structName] = generateSchemaForTypeReflect(t)
					} else {
						reg.schemas[structName] = map[string]interface{}{"type": "object"}
					}
				} else {
					reg.schemas[structName] = map[string]interface{}{"type": "object"}
				}
			}
		}
	}

	info := map[string]string{"title": g.Config.Title, "version": g.Config.Version}
	if g.Config.Description != "" {
		info["description"] = g.Config.Description
	}

	components := map[string]interface{}{}
	if len(reg.schemas) > 0 {
		components["schemas"] = reg.schemas
	}

	return map[string]interface{}{
		"openapi":    "3.0.0",
		"info":       info,
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
