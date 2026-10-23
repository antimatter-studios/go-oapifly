package oapifly

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// A struct's schema is closed: a property it does not declare is refused (#30). These tests
// check what the schemas accept, not only how they are spelt, with a small validator for the
// keywords this package writes for objects.

const closedSource = `package types

import "elsewhere"

type Closed struct {
	ID int ` + "`json:\"id\"`" + `
}

type Opened struct {
	_  struct{} ` + "`oapi:\"open\"`" + `
	ID int      ` + "`json:\"id\"`" + `
}

type Base struct {
	ID int ` + "`json:\"id\"`" + `
}

type Entry struct {
	Base
	Size int ` + "`json:\"size\"`" + `
}

type OpenBase struct {
	_  struct{} ` + "`oapi:\"open\"`" + `
	ID int      ` + "`json:\"id\"`" + `
}

type OpenEntry struct {
	OpenBase
	Size int ` + "`json:\"size\"`" + `
}

type Holder struct {
	Owner *Closed ` + "`json:\"owner\"`" + `
}

type Unreadable struct {
	elsewhere.Thing
	Size int ` + "`json:\"size\"`" + `
}
`

type closedSchemaClosed struct {
	ID int `json:"id"`
}

type closedSchemaOpened struct {
	_  struct{} `oapi:"open"`
	ID int      `json:"id"`
}

type closedSchemaBase struct {
	ID int `json:"id"`
}

type closedSchemaEntry struct {
	closedSchemaBase
	Size int `json:"size"`
}

type closedSchemaOpenBase struct {
	_  struct{} `oapi:"open"`
	ID int      `json:"id"`
}

type closedSchemaOpenEntry struct {
	closedSchemaOpenBase
	Size int `json:"size"`
}

type closedSchemaHolder struct {
	Owner *closedSchemaClosed `json:"owner"`
}

// describer describes one of the shapes above by name, through one of the two paths.
type describer func(t *testing.T, name string) (map[string]interface{}, map[string]map[string]interface{})

func annotatedPath(open bool) describer {
	return func(t *testing.T, name string) (map[string]interface{}, map[string]map[string]interface{}) {
		t.Helper()
		dir := t.TempDir()
		writeGoFile(t, dir, "closed.go", closedSource)
		reg := newSchemaRegistry([]string{dir})
		reg.openStructs = open
		return map[string]interface{}{"$ref": "#/components/schemas/" + reg.resolve("types."+name)}, reg.schemas
	}
}

func reflectedPath(open bool) describer {
	types := map[string]reflect.Type{
		"Closed":    TypeOf[closedSchemaClosed](),
		"Opened":    TypeOf[closedSchemaOpened](),
		"Entry":     TypeOf[closedSchemaEntry](),
		"OpenEntry": TypeOf[closedSchemaOpenEntry](),
		"Holder":    TypeOf[closedSchemaHolder](),
	}
	return func(t *testing.T, name string) (map[string]interface{}, map[string]map[string]interface{}) {
		t.Helper()
		reg := newSchemaRegistry(nil)
		reg.openStructs = open
		return reg.reflectSchema(types[name]), reg.schemas
	}
}

func TestStructSchemasAreClosed(t *testing.T) {
	cases := []struct {
		shape, body string
		want        bool
	}{
		{"Closed", `{"id":1}`, true},
		{"Closed", `{"id":1,"extra":2}`, false},
		{"Opened", `{"id":1,"extra":2}`, true},
		// An embedded struct's fields are the object's own, not extra.
		{"Entry", `{"id":1,"size":2}`, true},
		{"Entry", `{"id":1,"size":2,"extra":3}`, false},
		// A struct that embeds an open one is open.
		{"OpenEntry", `{"id":1,"size":2,"extra":3}`, true},
		// A nullable reference is an allOf around the closed component: still satisfiable,
		// and still refusing what the component does not declare.
		{"Holder", `{"owner":{"id":1}}`, true},
		{"Holder", `{"owner":null}`, true},
		{"Holder", `{"owner":{"id":1,"extra":2}}`, false},
		{"Holder", `{"owner":{"id":1},"extra":2}`, false},
	}
	paths := map[string]describer{"annotations": annotatedPath(false), "reflection": reflectedPath(false)}
	for pathName, describe := range paths {
		for _, c := range cases {
			schema, components := describe(t, c.shape)
			if got := accepts(t, components, schema, decoded(t, c.body)); got != c.want {
				t.Errorf("%s: %s accepts %s: %v, want %v\n%v", pathName, c.shape, c.body, got, c.want, components)
			}
		}
	}
}

// Config.OpenStructs keeps the description open, as it was before structs were closed.
func TestOpenStructsLeavesSchemasOpen(t *testing.T) {
	paths := map[string]describer{"annotations": annotatedPath(true), "reflection": reflectedPath(true)}
	for pathName, describe := range paths {
		schema, components := describe(t, "Entry")
		if !accepts(t, components, schema, decoded(t, `{"id":1,"size":2,"extra":3}`)) {
			t.Errorf("%s: an open struct accepts an extra field: %v", pathName, components)
		}
	}

	g := New(Config{Title: "t", OpenStructs: true})
	g.Register(Endpoint{Method: "POST", Path: "/x", Request: TypeOf[closedSchemaClosed](),
		Responses: []EndpointResponse{{Status: 200, Type: TypeOf[closedSchemaClosed]()}}})
	doc, err := json.Marshal(componentsOf(g.Generate()))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(doc), "additionalProperties") {
		t.Fatalf("OpenStructs reaches the generator: %s", doc)
	}
}

// An embedded type the scanner cannot read leaves the struct open: closing it would refuse
// the fields the schema could not name.
func TestUnreadableEmbedLeavesStructOpen(t *testing.T) {
	schema, components := annotatedPath(false)(t, "Unreadable")
	if !accepts(t, components, schema, decoded(t, `{"size":1,"from_the_embed":2}`)) {
		t.Fatalf("a struct with an unreadable embed is left open: %v", components)
	}
}

func decoded(t *testing.T, body string) interface{} {
	t.Helper()
	var v interface{}
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

// accepts is a small JSON Schema check, enough for the keywords this package writes for
// objects: $ref, allOf, nullable, type, properties, required and additionalProperties.
func accepts(t *testing.T, components map[string]map[string]interface{}, schema map[string]interface{}, value interface{}) bool {
	t.Helper()
	if ref, ok := schema["$ref"].(string); ok {
		target, found := components[strings.TrimPrefix(ref, "#/components/schemas/")]
		if !found {
			t.Fatalf("dangling $ref %s", ref)
		}
		return accepts(t, components, target, value)
	}
	if parts, ok := schema["allOf"].([]interface{}); ok {
		for _, part := range parts {
			if !accepts(t, components, part.(map[string]interface{}), value) {
				return false
			}
		}
	}
	for _, key := range []string{"oneOf", "anyOf"} {
		parts, ok := schema[key].([]interface{})
		if !ok {
			continue
		}
		held := 0
		for _, part := range parts {
			if accepts(t, components, part.(map[string]interface{}), value) {
				held++
			}
		}
		if held == 0 || (key == "oneOf" && held > 1) {
			return false
		}
	}
	types := []interface{}{schema["type"]}
	if list, ok := schema["type"].([]interface{}); ok {
		types = list
	}
	if value == nil {
		for _, each := range types {
			if each == "null" {
				return true
			}
		}
		return schema["type"] == nil
	}
	for _, each := range types {
		if each != "null" && acceptsType(t, components, schema, each, value) {
			return true
		}
	}
	return false
}

// acceptsType checks value against one of a schema's types.
func acceptsType(t *testing.T, components map[string]map[string]interface{}, schema map[string]interface{}, typ, value interface{}) bool {
	t.Helper()
	switch typ {
	case "object":
		obj, ok := value.(map[string]interface{})
		if !ok {
			return false
		}
		required, _ := schema["required"].([]string)
		for _, name := range required {
			if _, ok := obj[name]; !ok {
				return false
			}
		}
		props, _ := schema["properties"].(map[string]interface{})
		for name, v := range obj {
			if prop, ok := props[name]; ok {
				if !accepts(t, components, prop.(map[string]interface{}), v) {
					return false
				}
				continue
			}
			switch extra := schema["additionalProperties"].(type) {
			case bool:
				if !extra {
					return false
				}
			case map[string]interface{}:
				if !accepts(t, components, extra, v) {
					return false
				}
			}
		}
	case "integer", "number":
		_, ok := value.(float64)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	}
	return true
}
