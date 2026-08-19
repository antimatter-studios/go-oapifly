package oapifly

import (
	"strings"
	"testing"
)

// `interface{}` in an annotation is a statement, not an omission: a proxy handler answers with
// whatever the far end said, and the description has no business claiming otherwise. Treating it
// as a type that could not be found had two costs. It was reported as a warning, so a document
// with a deliberate any-value in it was never warning-free and the warnings stopped being read -
// which is how a genuinely undescribed type can hide. And the name went into the document as a
// component key, where `interface{}` is not legal: OpenAPI allows only [a-zA-Z0-9.-_] there, so
// every reference to it dangled.

func TestResolve_EmptyInterfaceIsDescribedAsAnyValue(t *testing.T) {
	for _, spelling := range []string{"interface{}", "interface {}", "any"} {
		t.Run(spelling, func(t *testing.T) {
			reg := newSchemaRegistry([]string{t.TempDir()})

			name := reg.resolve(spelling)

			if name != anyValueSchemaName {
				t.Errorf("resolve(%q) = %q, want %q", spelling, name, anyValueSchemaName)
			}
			if !componentKey.MatchString(name) {
				t.Errorf("%q is not a legal component key", name)
			}
			schema, ok := reg.schemas[anyValueSchemaName]
			if !ok {
				t.Fatalf("no %s component was registered; have %v", anyValueSchemaName, keysOf(reg.schemas))
			}
			// No constraints at all, rather than "object": the far end may answer with an array
			// or a bare string, and a document saying "object" would be describing a response
			// this API never promised.
			if len(schema) != 0 {
				t.Errorf("schema = %v, want no constraints", schema)
			}
			if len(reg.warnings) != 0 {
				t.Errorf("a deliberate any-value was warned about: %v", reg.warnings)
			}
		})
	}
}

// A type that genuinely cannot be found is still reported - the point is to tell the two apart,
// not to stop warning.
func TestResolve_MissingTypeIsStillWarnedAbout(t *testing.T) {
	reg := newSchemaRegistry([]string{t.TempDir()})

	reg.resolve("types.NotHere")

	if len(reg.warnings) != 1 {
		t.Fatalf("warnings = %v, want one", reg.warnings)
	}
	if !strings.Contains(reg.warnings[0], "NotHere") {
		t.Errorf("the warning does not name the type: %q", reg.warnings[0])
	}
}

// End to end: a proxy-style response leaves a legal document that constrains nothing, and the
// generator reports nothing to fix.
func TestGenerate_ProxyResponseIsAnUnconstrainedComponent(t *testing.T) {
	handlerDir := t.TempDir()
	handler := writeGoFile(t, handlerDir, "handler.go", `package main

// @Summary Proxy a request
// @Produce json
// @Success 200 {object} interface{} "Whatever the device answered, proxied unchanged"
// @Router /proxy/{path} [get]
func Proxy() {}
`)

	g := New(Config{Title: "t", Version: "1", ScanPatterns: []string{handler}, TypeDirs: []string{t.TempDir()}})
	spec := g.Generate()

	if len(g.Warnings) != 0 {
		t.Errorf("warnings = %v, want none", g.Warnings)
	}

	components, _ := spec["components"].(map[string]interface{})
	schemas, _ := components["schemas"].(map[string]map[string]interface{})
	if _, ok := schemas[anyValueSchemaName]; !ok {
		t.Fatalf("the any-value component is missing; have %v", keysOf(schemas))
	}
	for key := range schemas {
		if !componentKey.MatchString(key) {
			t.Errorf("component key %q is not legal in an OpenAPI document", key)
		}
	}

	paths, _ := spec["paths"].(map[string]map[string]PathItem)
	content, _ := paths["/proxy/{path}"]["get"].Responses["200"].Content["application/json"].(map[string]interface{})
	schema, _ := content["schema"].(map[string]interface{})
	if want := "#/components/schemas/" + anyValueSchemaName; schema["$ref"] != want {
		t.Errorf("the response should reference the any-value component, got %#v", schema)
	}
}
