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

	// cached holds the generated spec after the first call to Generate.
	// Source files don't change at runtime, so the spec is computed once.
	cached map[string]interface{}
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
// The result is cached after the first call since source files don't change at runtime.
func (g *Generator) Generate() map[string]interface{} {
	if g.cached != nil {
		return g.cached
	}

	g.Warnings = nil

	reg := newSchemaRegistry(g.Config.TypeDirs)
	paths := map[string]map[string]PathItem{}
	// Which of a path's methods were named by a handler of their own, as opposed to filled in
	// by a catch-all route.
	describedMethods := map[string]map[string]bool{}

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
			methods, usable := methodsFor(method)
			if !usable {
				g.Warnings = append(g.Warnings, fmt.Sprintf("%s %s: '%s' is not a method a Path Item Object may carry, route not described", strings.ToUpper(method), path, method))
				continue
			}
			if _, ok := paths[path]; !ok {
				paths[path] = map[string]PathItem{}
				describedMethods[path] = map[string]bool{}
			}
			item := buildPathItem(tags, reg)
			// A catch-all fills in the methods nothing else describes. A method described on
			// its own terms says more than "this route answers everything", so it wins wherever
			// the two meet, in whichever order the two handlers were read.
			catchAll := len(methods) > 1
			for _, m := range methods {
				if catchAll {
					if describedMethods[path][m] {
						continue
					}
				} else {
					if describedMethods[path][m] {
						g.Warnings = append(g.Warnings, fmt.Sprintf("duplicate handler for %s %s, overwriting previous", strings.ToUpper(m), path))
					}
					describedMethods[path][m] = true
				}
				paths[path][m] = item
			}
		}
		// A struct carrying a @Schema annotation is registered the same way a type named by
		// a response annotation is, so an alias or a named non-struct is described as what
		// it is and anything unresolvable is reported rather than quietly widened.
		for _, structName := range extractSchemaAnnotatedStructs(astFile) {
			reg.resolve(structName)
		}
	}

	// Types the registry could not describe are reported to the caller rather than left in
	// the spec as unexplained untyped objects, so a missing TypeDirs entry is visible at
	// generation time instead of surfacing later as a contract test that cannot fail.
	g.Warnings = append(g.Warnings, reg.warnings...)

	info := map[string]string{"title": g.Config.Title, "version": g.Config.Version}
	if g.Config.Description != "" {
		info["description"] = g.Config.Description
	}

	components := map[string]interface{}{}
	if len(reg.schemas) > 0 {
		components["schemas"] = reg.schemas
	}

	g.cached = map[string]interface{}{
		"openapi":    "3.0.0",
		"info":       info,
		"paths":      paths,
		"components": components,
	}

	return g.cached
}

// JSON returns the OpenAPI spec as JSON bytes.
func (g *Generator) JSON() ([]byte, error) {
	return json.Marshal(g.Generate())
}

// YAML returns the OpenAPI spec as YAML bytes.
func (g *Generator) YAML() ([]byte, error) {
	return yaml.Marshal(g.Generate())
}
