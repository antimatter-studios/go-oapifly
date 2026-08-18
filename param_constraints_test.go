package oapifly

import (
	"reflect"
	"strings"
	"testing"
)

// A Go type says what shape a value has, not which values are allowed. A page number is an
// integer and so is minus one; an id is an integer and so is zero. Where a handler enforces a
// bound the description has to state it, or a consumer reading the description sends a value
// the handler refuses and cannot see why - and a boundary-probing tester reports the handler
// and the description disagreeing, which is exactly what they are doing.

func TestParseParam_Constraints(t *testing.T) {
	tests := []struct {
		name        string
		input       string
		wantMinimum string
		wantMaximum string
		wantEnums   []string
	}{
		{
			"minimum only",
			`page query int false "Page number" minimum(1)`,
			"1", "", nil,
		},
		{
			"minimum and maximum",
			`limit query int false "Page size" minimum(1) maximum(100)`,
			"1", "100", nil,
		},
		{
			"enums",
			`type query string false "Key type" enums(authorized,device)`,
			"", "", []string{"authorized", "device"},
		},
		{
			"alongside an example",
			`id path int true "Key ID" minimum(1) example(7)`,
			"1", "", nil,
		},
		{
			"none",
			`filter query string false "Filter"`,
			"", "", nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := parseParam(tt.input)
			if !ok {
				t.Fatal("annotation did not parse")
			}
			if p.Minimum != tt.wantMinimum {
				t.Errorf("Minimum = %q, want %q", p.Minimum, tt.wantMinimum)
			}
			if p.Maximum != tt.wantMaximum {
				t.Errorf("Maximum = %q, want %q", p.Maximum, tt.wantMaximum)
			}
			if !reflect.DeepEqual(p.Enums, tt.wantEnums) {
				t.Errorf("Enums = %q, want %q", p.Enums, tt.wantEnums)
			}
		})
	}
}

// The example still parses when a constraint sits beside it.
func TestParseParam_ExampleSurvivesConstraints(t *testing.T) {
	p, ok := parseParam(`id path int true "Key ID" minimum(1) example(7)`)
	if !ok {
		t.Fatal("annotation did not parse")
	}
	if p.Example != "7" {
		t.Errorf("Example = %q, want 7", p.Example)
	}
}

// The bounds reach the parameter's schema, typed as the schema is: a bound written as text
// beside an integer would describe a parameter that rejects every value it accepts.
func TestBuildParameters_CarriesConstraints(t *testing.T) {
	params := []parsedParam{
		{Name: "limit", In: "query", DataType: "int", Description: "Page size", Minimum: "1", Maximum: "100"},
		{Name: "type", In: "query", DataType: "string", Description: "Key type", Enums: []string{"authorized", "device"}},
	}
	result := buildParameters("/api/v1/things", params, newSchemaRegistry(nil))
	if len(result) != 2 {
		t.Fatalf("expected 2 parameters, got %d", len(result))
	}

	limit := result[0].Schema
	if limit["minimum"] != float64(1) {
		t.Errorf("minimum = %#v, want the number 1", limit["minimum"])
	}
	if limit["maximum"] != float64(100) {
		t.Errorf("maximum = %#v, want the number 100", limit["maximum"])
	}

	enums, ok := result[1].Schema["enum"].([]interface{})
	if !ok || len(enums) != 2 || enums[0] != "authorized" || enums[1] != "device" {
		t.Errorf("enum = %#v, want the two key types", result[1].Schema["enum"])
	}
}

// A path parameter carries them too - an id of zero names no record.
func TestBuildParameters_PathParameterConstraints(t *testing.T) {
	params := []parsedParam{
		{Name: "id", In: "path", DataType: "int", Required: true, Minimum: "1"},
	}
	result := buildParameters("/api/v1/things/{id}", params, newSchemaRegistry(nil))
	if len(result) != 1 {
		t.Fatalf("expected 1 parameter, got %d", len(result))
	}
	if result[0].Schema["minimum"] != float64(1) {
		t.Errorf("minimum = %#v, want the number 1", result[0].Schema["minimum"])
	}
}

// A constraint that is not a number beside a numeric parameter is a mistake in the
// annotation, and describing the parameter as rejecting everything would be worse than
// leaving the bound off and saying so.
func TestBuildParameters_NonNumericBoundIsDropped(t *testing.T) {
	params := []parsedParam{
		{Name: "page", In: "query", DataType: "int", Minimum: "one"},
	}
	result := buildParameters("/api/v1/things", params, newSchemaRegistry(nil))
	if _, present := result[0].Schema["minimum"]; present {
		t.Errorf("a bound that is not a number should be left off, got %#v", result[0].Schema)
	}
}

// The annotation path's type map was less complete than the AST path's: an int64 parameter was
// described as a string, so a consumer sent "1" where the handler wanted a number, and any
// bound declared on it was dropped for not being a value of the schema's type.
func TestDataTypeToOpenAPIType_NumericAliases(t *testing.T) {
	for _, dataType := range []string{"int8", "int16", "int32", "int64", "uint", "uint8", "uint16", "uint32", "uint64"} {
		t.Run(dataType, func(t *testing.T) {
			if got := dataTypeToOpenAPIType(dataType); got != "integer" {
				t.Errorf("dataTypeToOpenAPIType(%q) = %q, want integer", dataType, got)
			}
		})
	}
	if got := dataTypeToOpenAPIType("float32"); got != "number" {
		t.Errorf("dataTypeToOpenAPIType(float32) = %q, want number", got)
	}
}

func TestBuildParameters_BoundsOnANumericAlias(t *testing.T) {
	params := []parsedParam{
		{Name: "id", In: "path", DataType: "int64", Required: true, Minimum: "1"},
	}
	result := buildParameters("/api/v1/things/{id}", params, newSchemaRegistry(nil))
	if result[0].Schema["type"] != "integer" {
		t.Fatalf("an int64 parameter should be an integer, got %#v", result[0].Schema)
	}
	if result[0].Schema["minimum"] != float64(1) {
		t.Errorf("minimum = %#v, want the number 1", result[0].Schema["minimum"])
	}
}

// An enum entry that is not a value of the parameter's type leaves the whole enum off.
//
// Keeping the entries that did parse would describe the parameter as forbidding whichever
// value the mistyped entry meant - if enums(1,two,3) was a typo for 2, a schema of {1,3}
// rejects the 2 the handler accepts. An unconstrained integer is less precise and never wrong.
func TestBuildParameters_EnumWithAnUntypeableEntryIsDropped(t *testing.T) {
	params := []parsedParam{
		{Name: "priority", In: "query", DataType: "int", Enums: []string{"1", "two", "3"}},
	}
	result := buildParameters("/api/v1/things", params, newSchemaRegistry(nil))
	if enum, present := result[0].Schema["enum"]; present {
		t.Errorf("the enum should have been left off entirely, got %#v", enum)
	}
}

func TestBuildParameters_NumericEnumIsTyped(t *testing.T) {
	params := []parsedParam{
		{Name: "priority", In: "query", DataType: "int", Enums: []string{"1", "2", "3"}},
	}
	result := buildParameters("/api/v1/things", params, newSchemaRegistry(nil))
	enum, ok := result[0].Schema["enum"].([]interface{})
	if !ok || len(enum) != 3 {
		t.Fatalf("enum = %#v, want three numbers", result[0].Schema["enum"])
	}
	for i, want := range []float64{1, 2, 3} {
		if enum[i] != want {
			t.Errorf("enum[%d] = %#v, want %v", i, enum[i], want)
		}
	}
}

// A bound the annotation declared and this generator could not use is reported, not silently
// discarded: a description weaker than its author believes it to be is the failure mode every
// other warning in this package exists to prevent.
func TestApplyParamConstraints_ReportsWhatItDropped(t *testing.T) {
	tests := []struct {
		name  string
		param parsedParam
		says  string
	}{
		{
			"a bound that is not a number",
			parsedParam{Name: "page", In: "query", DataType: "int", Minimum: "one"},
			"minimum(one)",
		},
		{
			// An unsigned bound past the signed range cannot be held exactly by a JSON number,
			// so it is left off rather than rounded into a bound the handler does not enforce.
			"an unsigned bound past the signed range",
			parsedParam{Name: "id", In: "query", DataType: "uint64", Maximum: "18446744073709551615"},
			"maximum(18446744073709551615)",
		},
		{
			"an enum entry of the wrong type",
			parsedParam{Name: "priority", In: "query", DataType: "int", Enums: []string{"1", "two"}},
			"enum value \"two\"",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := newSchemaRegistry(nil)
			buildParameters("/api/v1/things", []parsedParam{tt.param}, reg)

			found := false
			for _, w := range reg.warnings {
				if strings.Contains(w, tt.says) && strings.Contains(w, tt.param.Name) {
					found = true
				}
			}
			if !found {
				t.Errorf("the dropped constraint was not reported: %q", reg.warnings)
			}
		})
	}
}

// A bound it could use is not warned about.
func TestApplyParamConstraints_SilentWhenItCanUseTheBound(t *testing.T) {
	reg := newSchemaRegistry(nil)
	buildParameters("/api/v1/things", []parsedParam{
		{Name: "limit", In: "query", DataType: "int", Minimum: "1", Maximum: "100"},
		{Name: "type", In: "query", DataType: "string", Enums: []string{"authorized", "device"}},
	}, reg)
	if len(reg.warnings) != 0 {
		t.Errorf("expected no warnings, got %q", reg.warnings)
	}
}
