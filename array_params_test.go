package oapifly

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

// A list-taking query parameter, declared swaggo-style as `[]int`, has to reach the
// description as an array with typed items and a list example. Collapsing it to a
// string with the literal text "[1,2]" as its example produced a document that no
// contract tester could send correctly and no reader could tell apart from a real
// string parameter.

func TestDataTypeSchema_ArrayOfPrimitive(t *testing.T) {
	tests := []struct {
		dataType string
		want     map[string]interface{}
	}{
		{"[]int", map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "integer"}}},
		{"[]string", map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}},
		{"[]bool", map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "boolean"}}},
		{"[]number", map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "number"}}},
		// The swaggo spelling for the same thing, kept for annotations written against swag.
		{"array", map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}}},
	}
	for _, tt := range tests {
		t.Run(tt.dataType, func(t *testing.T) {
			got := dataTypeSchema(tt.dataType)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("dataTypeSchema(%q) = %v, want %v", tt.dataType, got, tt.want)
			}
		})
	}
}

// Scalar parameters keep exactly the schema they had, so nothing already generated
// changes shape when the array support arrives.
func TestDataTypeSchema_ScalarUnchanged(t *testing.T) {
	tests := []struct {
		dataType string
		want     map[string]interface{}
	}{
		{"int", map[string]interface{}{"type": "integer"}},
		{"string", map[string]interface{}{"type": "string"}},
		{"file", map[string]interface{}{"type": "string", "format": "binary"}},
	}
	for _, tt := range tests {
		t.Run(tt.dataType, func(t *testing.T) {
			got := dataTypeSchema(tt.dataType)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("dataTypeSchema(%q) = %v, want %v", tt.dataType, got, tt.want)
			}
		})
	}
}

// An example is only useful when it has the type of the thing it exemplifies. A tester
// validating the example against the schema, or building a request from it, needs the
// integers to be integers and the list to be a list.
func TestParameterExample_TypedByDataType(t *testing.T) {
	tests := []struct {
		name     string
		dataType string
		example  string
		want     interface{}
	}{
		{"int list", "[]int", "[1,2]", []interface{}{float64(1), float64(2)}},
		{"string list", "[]string", "[a,b]", []interface{}{"a", "b"}},
		{"bool list", "[]bool", "[true,false]", []interface{}{true, false}},
		{"empty list", "[]int", "[]", []interface{}{}},
		{"scalar int", "int", "20", float64(20)},
		{"scalar bool", "bool", "true", true},
		{"scalar number", "number", "1.5", 1.5},
		{"scalar string", "string", "gdt", "gdt"},
		// A value that does not parse as the declared type is passed through as text
		// rather than dropped: a wrong example is a defect worth seeing in the output.
		{"int that is not an int", "int", "abc", "abc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parameterExample(tt.dataType, tt.example)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parameterExample(%q, %q) = %#v, want %#v", tt.dataType, tt.example, got, tt.want)
			}
		})
	}
}

// End to end through the annotation parser and the parameter builder, checked on the
// JSON that a reader of the description actually sees.
func TestBuildParameters_ArrayQueryParam(t *testing.T) {
	p, ok := parseParam(`ids query []int false "Job IDs to act on" example([1,2])`)
	if !ok {
		t.Fatal("annotation did not parse")
	}
	result := buildParameters("/api/jobs", []parsedParam{p})
	if len(result) != 1 {
		t.Fatalf("expected 1 param, got %d", len(result))
	}

	raw, err := json.Marshal(result[0])
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]interface{}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}

	schema, _ := got["schema"].(map[string]interface{})
	if schema["type"] != "array" {
		t.Errorf("schema.type = %v, want array (schema %v)", schema["type"], schema)
	}
	items, _ := schema["items"].(map[string]interface{})
	if items["type"] != "integer" {
		t.Errorf("schema.items.type = %v, want integer (schema %v)", items["type"], schema)
	}
	example, isList := got["example"].([]interface{})
	if !isList || len(example) != 2 || example[0] != float64(1) || example[1] != float64(2) {
		t.Errorf("example = %#v, want the JSON list [1,2]", got["example"])
	}
}

// A list-shaped formData parameter - several files, several tags - is an array in the
// multipart schema, the same way it is in a query string. Building the scalar schema by
// hand here left the request-body path with no idea what a `[]string` was.
func TestBuildRequestBody_FormDataArray(t *testing.T) {
	reg := newSchemaRegistry(nil)
	params := []parsedParam{
		{Name: "tags", In: "formData", DataType: "[]string", Required: false},
	}
	rb := buildRequestBody(params, reg)
	if rb == nil {
		t.Fatal("expected non-nil")
	}
	content := rb.Content["multipart/form-data"].(map[string]interface{})
	schema := content["schema"].(map[string]interface{})
	props := schema["properties"].(map[string]interface{})
	tags := props["tags"].(map[string]interface{})
	if tags["type"] != "array" {
		t.Fatalf("tags schema = %v, want an array", tags)
	}
	items, _ := tags["items"].(map[string]interface{})
	if items["type"] != "string" {
		t.Errorf("tags items = %v, want string items", items)
	}
}

// One reading of "text as a typed value" serves both struct tags and parameter examples,
// so an integer example is the same kind of number whichever annotation it came from.
func TestTypedValue_SharedBetweenTagsAndParams(t *testing.T) {
	fromParam := parameterExample("int", "3")
	fromTag := typedTagValue("3", map[string]interface{}{"type": "integer"})
	if !reflect.DeepEqual(fromParam, fromTag) {
		t.Errorf("parameter example %#v and tag value %#v disagree on the same text", fromParam, fromTag)
	}
}

// A body declared as a list of structs is an array whose items reference the struct, not a
// component named "[]Item". Classifying the slice as a struct reference and passing the
// slice spelling to the registry looked up a type that cannot exist.
func TestBuildRequestBody_BodyArrayOfStructRefsItems(t *testing.T) {
	dir := t.TempDir()
	src := "package types\n\ntype Item struct {\n\tName string `json:\"name\"`\n}\n"
	if err := os.WriteFile(dir+"/item.go", []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	reg := newSchemaRegistry([]string{dir})
	params := []parsedParam{
		{Name: "items", In: "body", DataType: "[]Item", Required: true},
	}
	rb := buildRequestBody(params, reg)
	if rb == nil {
		t.Fatal("expected non-nil")
	}
	content := rb.Content["application/json"].(map[string]interface{})
	schema := content["schema"].(map[string]interface{})
	if schema["type"] != "array" {
		t.Fatalf("schema = %v, want an array", schema)
	}
	items, _ := schema["items"].(map[string]interface{})
	if items["$ref"] != "#/components/schemas/Item" {
		t.Errorf("items = %v, want a $ref to Item", items)
	}
	if _, registered := reg.schemas["Item"]; !registered {
		t.Error("Item was not registered as a component")
	}
	if _, bogus := reg.schemas["[]Item"]; bogus {
		t.Error("a component named []Item was registered")
	}
}

// An integer example that is not an integer stays visibly wrong rather than being quietly
// read as a number beside an integer schema.
func TestTypedValue_IntegerRejectsFraction(t *testing.T) {
	if got := typedValue("1.5", "integer"); got != "1.5" {
		t.Errorf("typedValue(\"1.5\", integer) = %#v, want the text passed through", got)
	}
	if got := typedValue("2", "integer"); got != float64(2) {
		t.Errorf("typedValue(\"2\", integer) = %#v, want float64(2)", got)
	}
	if got := typedValue("1.5", "number"); got != 1.5 {
		t.Errorf("typedValue(\"1.5\", number) = %#v, want 1.5", got)
	}
}
