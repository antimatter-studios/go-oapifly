package oapifly

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// A type whose JSON form is not its Go structure is described the same way when an annotation
// names it directly as when it is a field's type, with no warning (#29).
func TestAnnotationsNameKnownTypes(t *testing.T) {
	names := make([]string, 0, len(qualifiedJSONTypes))
	for name := range qualifiedJSONTypes {
		names = append(names, name)
	}
	sort.Strings(names)

	var src strings.Builder
	src.WriteString("package handlers\n\ntype Controller struct{}\n")
	for i, name := range names {
		fmt.Fprintf(&src, `
// @Param body body %s true "the body"
// @Success 200 {object} %s "OK"
// @Failure 400 {object} %s "refused"
// @Router /known/%d [POST]
func (c *Controller) Known%d() {}
`, name, name, name, i, i)
	}
	dir := t.TempDir()
	writeTestFile(t, dir, "handlers.go", src.String())

	g := New(Config{Title: "t", ScanPatterns: []string{filepath.Join(dir, "*.go")}, TypeDirs: []string{t.TempDir()}})
	spec := g.Generate()
	if len(g.Warnings) > 0 {
		t.Fatalf("a known type named by an annotation is not looked for in TypeDirs: %v", g.Warnings)
	}
	components, _ := componentsOf(spec).(map[string]map[string]interface{})
	for i, name := range names {
		want := asJSON(t, qualifiedJSONTypes[name])
		if got := asJSON(t, components[name]); got != want {
			t.Errorf("%s is described as %s, want %s", name, got, want)
		}
		item := operation(t, spec, fmt.Sprintf("/known/%d", i), "post")
		ref := `{"$ref":"#/components/schemas/` + name + `"}`
		if got := asJSON(t, item.RequestBody.Content["application/json"].(map[string]interface{})["schema"]); got != ref {
			t.Errorf("%s as a body: %s", name, got)
		}
		for _, status := range []string{"200", "400"} {
			if got := asJSON(t, item.Responses[status].Content["application/json"].(map[string]interface{})["schema"]); got != ref {
				t.Errorf("%s as a %s response: %s", name, status, got)
			}
		}
	}
}

const anyFieldsSource = `package types

type Shape interface {
	Area() float64
}

type Holder struct {
	Empty    interface{} ` + "`json:\"empty\"`" + `
	Spaced   interface {} ` + "`json:\"spaced\"`" + `
	Any      any ` + "`json:\"any\"`" + `
	Methods  interface{ Name() string } ` + "`json:\"methods\"`" + `
	Named    Shape ` + "`json:\"named\"`" + `
}
`

// An interface field holds whatever is chosen at run time, so it is any value at all, never
// only an object; one whose interface has methods is reported too (#27).
func TestInterfaceFieldsAreAnyValue(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "holder.go", anyFieldsSource)
	reg := newSchemaRegistry([]string{dir})
	name := reg.resolve("types.Holder")
	props := reg.schemas[name]["properties"].(map[string]interface{})

	for _, field := range []string{"empty", "spaced", "any", "methods", "named"} {
		schema := props[field].(map[string]interface{})
		schema = resolvedLocally(reg, schema)
		for _, body := range []string{`"text"`, `3`, `[1,"a"]`, `{"k":"v"}`, `null`} {
			var value interface{}
			if err := json.Unmarshal([]byte(body), &value); err != nil {
				t.Fatal(err)
			}
			if !accepts(t, reg.schemas, schema, value) {
				t.Errorf("%s refuses %s: %v", field, body, schema)
			}
		}
	}
	warned := strings.Join(reg.warnings, "\n")
	if strings.Count(warned, "with methods") != 2 {
		t.Errorf("each interface with methods is reported once: %v", reg.warnings)
	}
}

// resolvedLocally follows a $ref into the registry's components.
func resolvedLocally(reg *schemaRegistry, schema map[string]interface{}) map[string]interface{} {
	if ref, ok := schema["$ref"].(string); ok {
		return reg.schemas[strings.TrimPrefix(ref, "#/components/schemas/")]
	}
	return schema
}
