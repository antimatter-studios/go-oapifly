package oapifly

import (
	"os"
	"strings"
	"testing"
	"time"
)

// writeGoFile puts src in dir under name and returns the full path.
func writeGoFile(t *testing.T, dir, name, src string) string {
	t.Helper()
	path := dir + "/" + name
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func refOf(t *testing.T, schema map[string]interface{}, property string) string {
	t.Helper()
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("schema has no properties: %#v", schema)
	}
	field, ok := props[property].(map[string]interface{})
	if !ok {
		t.Fatalf("property %q missing from %#v", property, props)
	}
	ref, _ := field["$ref"].(string)
	return ref
}

func propOf(t *testing.T, schema map[string]interface{}, property string) map[string]interface{} {
	t.Helper()
	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("schema has no properties: %#v", schema)
	}
	field, ok := props[property].(map[string]interface{})
	if !ok {
		t.Fatalf("property %q missing from %#v", property, props)
	}
	return field
}

// A struct field whose type is declared in the same package used to be described as a bare
// object, which accepts any shape at all. It has to become a reference to a real schema, or
// a contract test can never catch a wrong nested field.
func TestSiblingStructBecomesRef(t *testing.T) {
	dir := t.TempDir()
	path := writeGoFile(t, dir, "types.go", `package types

type Stats struct {
	Usage float64 `+"`json:\"usage\"`"+`
}

type Report struct {
	CPU Stats `+"`json:\"cpu\"`"+`
}
`)

	reg := newSchemaRegistry([]string{dir})
	schema := generateSchemaForTypeAST("Report", path, reg)

	if got := refOf(t, schema, "cpu"); got != "#/components/schemas/Stats" {
		t.Errorf("cpu should reference Stats, got %q", got)
	}
	nested, ok := reg.schemas["Stats"]
	if !ok {
		t.Fatal("Stats was not registered as a schema")
	}
	if _, ok := nested["properties"].(map[string]interface{})["usage"]; !ok {
		t.Errorf("Stats schema lost its usage property: %#v", nested)
	}
}

// The bug that produced this change: a package-qualified type was described as a string, so
// the spec claimed an object was a string and dredd reported the handler at fault.
func TestQualifiedStructResolvesRatherThanBecomingString(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "system.go", `package system

type CPUStats struct {
	Usage float64 `+"`json:\"usage\"`"+`
}
`)
	path := writeGoFile(t, dir, "response.go", `package restclient

type DeviceInfoResponse struct {
	CPU system.CPUStats `+"`json:\"cpu\"`"+`
}
`)

	reg := newSchemaRegistry([]string{dir})
	schema := generateSchemaForTypeAST("DeviceInfoResponse", path, reg)

	cpu := propOf(t, schema, "cpu")
	if cpu["type"] == "string" {
		t.Fatal("qualified struct was described as a string, the defect this guards")
	}
	if got, _ := cpu["$ref"].(string); got != "#/components/schemas/CPUStats" {
		t.Errorf("cpu should reference CPUStats, got %#v", cpu)
	}
}

// A slice of a named type has to carry the reference into items, since that is the shape
// every paginated `data` field in a response takes.
func TestSliceOfNamedTypeRefsItems(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "device.go", `package system

type Device struct {
	Hostname string `+"`json:\"hostname\"`"+`
}
`)
	path := writeGoFile(t, dir, "list.go", `package restclient

type DeviceListResponse struct {
	Data []system.Device `+"`json:\"data\"`"+`
}
`)

	reg := newSchemaRegistry([]string{dir})
	schema := generateSchemaForTypeAST("DeviceListResponse", path, reg)

	data := propOf(t, schema, "data")
	if data["type"] != "array" {
		t.Fatalf("data should be an array, got %#v", data)
	}
	items, ok := data["items"].(map[string]interface{})
	if !ok {
		t.Fatalf("data has no items: %#v", data)
	}
	if got, _ := items["$ref"].(string); got != "#/components/schemas/Device" {
		t.Errorf("items should reference Device, got %#v", items)
	}
}

// An unresolvable type must fall back to an unconstrained object AND say so. Silence is
// what let the string guess survive: the spec looked complete and was not.
func TestUnresolvableTypeWarnsAndFallsBackToObject(t *testing.T) {
	dir := t.TempDir()
	path := writeGoFile(t, dir, "response.go", `package restclient

type Wrapper struct {
	Payload elsewhere.Thing `+"`json:\"payload\"`"+`
}
`)

	reg := newSchemaRegistry([]string{dir})
	schema := generateSchemaForTypeAST("Wrapper", path, reg)

	payload := propOf(t, schema, "payload")
	if payload["type"] != "object" {
		t.Errorf("unresolvable type should fall back to object, got %#v", payload)
	}
	if len(reg.warnings) == 0 {
		t.Fatal("an unresolvable type must produce a warning")
	}
	found := false
	for _, w := range reg.warnings {
		if strings.Contains(w, "elsewhere.Thing") {
			found = true
		}
	}
	if !found {
		t.Errorf("warning should name the type, got %v", reg.warnings)
	}
}

// Types whose JSON form is not their Go structure keep their hand-written schema: describing
// time.Time structurally would be wrong, and a timestamp really is a string.
func TestKnownQualifiedTypesKeepTheirJSONShape(t *testing.T) {
	dir := t.TempDir()
	path := writeGoFile(t, dir, "row.go", `package restclient

type Row struct {
	CreatedAt time.Time       `+"`json:\"created_at\"`"+`
	DeletedAt gorm.DeletedAt  `+"`json:\"deleted_at\"`"+`
	GUID      uuid.UUID       `+"`json:\"guid\"`"+`
	Extra     json.RawMessage `+"`json:\"extra\"`"+`
}
`)

	reg := newSchemaRegistry([]string{dir})
	schema := generateSchemaForTypeAST("Row", path, reg)

	created := propOf(t, schema, "created_at")
	if created["type"] != "string" || created["format"] != "date-time" {
		t.Errorf("time.Time should be a date-time string, got %#v", created)
	}

	// deleted_at is null on every row that has not been soft-deleted, so the schema has to
	// permit null or every list response fails validation.
	deleted := propOf(t, schema, "deleted_at")
	if deleted["nullable"] != true {
		t.Errorf("gorm.DeletedAt must be nullable, got %#v", deleted)
	}

	guid := propOf(t, schema, "guid")
	if guid["format"] != "uuid" {
		t.Errorf("uuid.UUID should carry the uuid format, got %#v", guid)
	}

	// Raw JSON is whatever the producer wrote; constraining it would reject valid bodies.
	if extra := propOf(t, schema, "extra"); len(extra) != 0 {
		t.Errorf("json.RawMessage should be unconstrained, got %#v", extra)
	}

	if len(reg.warnings) != 0 {
		t.Errorf("known types must not warn, got %v", reg.warnings)
	}
}

// A pointer field is absent-able, which OpenAPI 3.0 spells nullable.
func TestPointerFieldIsNullable(t *testing.T) {
	dir := t.TempDir()
	path := writeGoFile(t, dir, "opt.go", `package restclient

type Optional struct {
	Note *string `+"`json:\"note\"`"+`
}
`)

	reg := newSchemaRegistry([]string{dir})
	schema := generateSchemaForTypeAST("Optional", path, reg)

	note := propOf(t, schema, "note")
	if note["type"] != "string" || note["nullable"] != true {
		t.Errorf("pointer should be a nullable string, got %#v", note)
	}
}

// Marking a shared entry nullable through a pointer must not leak into other fields of the
// same type - the table in qualifiedJSONTypes is package-level state.
func TestNullableDoesNotLeakAcrossFields(t *testing.T) {
	dir := t.TempDir()
	path := writeGoFile(t, dir, "times.go", `package restclient

type Times struct {
	Maybe *time.Time `+"`json:\"maybe\"`"+`
	Always time.Time `+"`json:\"always\"`"+`
}
`)

	reg := newSchemaRegistry([]string{dir})
	schema := generateSchemaForTypeAST("Times", path, reg)

	if propOf(t, schema, "maybe")["nullable"] != true {
		t.Error("pointer timestamp should be nullable")
	}
	if _, leaked := propOf(t, schema, "always")["nullable"]; leaked {
		t.Error("non-pointer timestamp must not have been made nullable")
	}
}

// A type that reaches itself must terminate. Without the guard this recurses until the
// stack ends, which would take the whole hub down at startup rather than fail a test.
func TestSelfReferencingTypeTerminates(t *testing.T) {
	dir := t.TempDir()
	path := writeGoFile(t, dir, "node.go", `package restclient

type Node struct {
	Name     string  `+"`json:\"name\"`"+`
	Children []Node  `+"`json:\"children\"`"+`
}
`)

	reg := newSchemaRegistry([]string{dir})
	done := make(chan map[string]interface{}, 1)
	go func() { done <- generateSchemaForTypeAST("Node", path, reg) }()

	select {
	case schema := <-done:
		data := propOf(t, schema, "children")
		items, _ := data["items"].(map[string]interface{})
		if got, _ := items["$ref"].(string); got != "#/components/schemas/Node" {
			t.Errorf("children should reference Node, got %#v", items)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("self-referencing type did not terminate")
	}
}
