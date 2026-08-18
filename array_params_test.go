package oapifly

import (
	"encoding/json"
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
		{"int list", "[]int", "[1,2]", []interface{}{int64(1), int64(2)}},
		{"string list", "[]string", "[a,b]", []interface{}{"a", "b"}},
		{"bool list", "[]bool", "[true,false]", []interface{}{true, false}},
		{"empty list", "[]int", "[]", []interface{}{}},
		{"scalar int", "int", "20", int64(20)},
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
