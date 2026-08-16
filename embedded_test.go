package oapifly

import (
	"strings"
	"testing"
)

// Go promotes an embedded struct's fields into the JSON object, so a schema that skips them
// describes an object with none of the fields it actually sends.

func TestEmbeddedStructFieldsArePromoted(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "entry.go", `package types

type Base struct {
	ID   int    `+"`json:\"id\"`"+`
	Name string `+"`json:\"name\"`"+`
}

type Entry struct {
	Base
	Size int `+"`json:\"size\"`"+`
}
`)

	reg := newSchemaRegistry([]string{dir})
	schema := reg.schemas[reg.resolve("types.Entry")]

	props, _ := schema["properties"].(map[string]interface{})
	for _, name := range []string{"id", "name", "size"} {
		if _, ok := props[name]; !ok {
			t.Fatalf("embedded field %q was not promoted: %#v", name, props)
		}
	}
	if len(reg.warnings) != 0 {
		t.Fatalf("a resolvable embedded type should not warn: %v", reg.warnings)
	}
}

func TestEmbeddedStructKeepsRequired(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "entry.go", `package types

type Base struct {
	ID       int    `+"`json:\"id\"`"+`
	Optional string `+"`json:\"optional,omitempty\"`"+`
}

type Entry struct {
	Base
}
`)

	reg := newSchemaRegistry([]string{dir})
	schema := reg.schemas[reg.resolve("types.Entry")]

	required, _ := schema["required"].([]string)
	joined := strings.Join(required, ",")
	if !strings.Contains(joined, "id") {
		t.Fatalf("a promoted field without omitempty stays required, got %v", required)
	}
	if strings.Contains(joined, "optional") {
		t.Fatalf("a promoted field with omitempty is not required, got %v", required)
	}
}

func TestEmbeddedTypeOutOfScopeWarns(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "entry.go", `package types

import "gorm.io/gorm"

type Entry struct {
	gorm.Model
	Size int `+"`json:\"size\"`"+`
}
`)

	reg := newSchemaRegistry([]string{dir})
	schema := reg.schemas[reg.resolve("types.Entry")]

	props, _ := schema["properties"].(map[string]interface{})
	if _, ok := props["size"]; !ok {
		t.Fatalf("the fields it can describe still belong in the schema: %#v", props)
	}
	if len(reg.warnings) != 1 || !strings.Contains(reg.warnings[0], "gorm.Model") {
		t.Fatalf("an embedded type it cannot reach must be reported: %v", reg.warnings)
	}
}

func TestMapValueTypeIsDescribed(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "samba.go", `package types

type SambaUser struct {
	Name string `+"`json:\"name\"`"+`
}

type SambaConfig struct {
	Users  map[string]SambaUser `+"`json:\"users\"`"+`
	Labels map[string]string    `+"`json:\"labels\"`"+`
}
`)

	reg := newSchemaRegistry([]string{dir})
	schema := reg.schemas[reg.resolve("types.SambaConfig")]

	users := propOf(t, schema, "users")
	additional, ok := users["additionalProperties"].(map[string]interface{})
	if !ok {
		t.Fatalf("a map's value type belongs in additionalProperties: %#v", users)
	}
	if ref, _ := additional["$ref"].(string); !strings.HasSuffix(ref, "/SambaUser") {
		t.Fatalf("the value type should be referenced, got %#v", additional)
	}

	labels := propOf(t, schema, "labels")
	scalar, ok := labels["additionalProperties"].(map[string]interface{})
	if !ok || scalar["type"] != "string" {
		t.Fatalf("a map of strings should say so, got %#v", labels)
	}
}
