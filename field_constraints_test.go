package oapifly

import (
	"testing"
)

// A field's Go type says what shape a value has, not which values are allowed. The rest is
// in the struct tag, in the vocabulary swaggo uses, and without it the schema permits bodies
// the handler goes on to reject.

func TestEnumsTagBecomesEnum(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "device.go", `package types

type AddDeviceRequest struct {
	DeviceType string `+"`json:\"device_type\" enums:\"hub,dongle\"`"+`
}
`)

	reg := newSchemaRegistry([]string{dir})
	prop := propOf(t, reg.schemas[reg.resolve("types.AddDeviceRequest")], "device_type")

	values, ok := prop["enum"].([]interface{})
	if !ok || len(values) != 2 {
		t.Fatalf("the allowed values belong in the schema: %#v", prop)
	}
	if values[0] != "hub" || values[1] != "dongle" {
		t.Fatalf("in the order they were written: %#v", values)
	}
	if prop["type"] != "string" {
		t.Fatalf("an enum does not replace the type: %#v", prop)
	}
}

func TestEnumsTagOnAnIntegerKeepsNumbers(t *testing.T) {
	// Quoting the values would make a document that rejects every number the field accepts.
	dir := t.TempDir()
	writeGoFile(t, dir, "job.go", `package types

type Job struct {
	Priority int `+"`json:\"priority\" enums:\"1,2,3\"`"+`
}
`)

	reg := newSchemaRegistry([]string{dir})
	prop := propOf(t, reg.schemas[reg.resolve("types.Job")], "priority")

	values, _ := prop["enum"].([]interface{})
	if len(values) != 3 {
		t.Fatalf("three allowed values: %#v", prop)
	}
	if _, isString := values[0].(string); isString {
		t.Fatalf("an integer field's values are numbers, not strings: %#v", values)
	}
	if values[0].(float64) != 1 || values[2].(float64) != 3 {
		t.Fatalf("values not carried through: %#v", values)
	}
}

func TestFormatTagBecomesFormat(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "device.go", `package types

type AddDeviceRequest struct {
	Endpoint string `+"`json:\"inpace_api_endpoint\" format:\"uri\"`"+`
}
`)

	reg := newSchemaRegistry([]string{dir})
	prop := propOf(t, reg.schemas[reg.resolve("types.AddDeviceRequest")], "inpace_api_endpoint")

	if prop["format"] != "uri" {
		t.Fatalf("the format belongs in the schema: %#v", prop)
	}
	if prop["type"] != "string" {
		t.Fatalf("a format does not replace the type: %#v", prop)
	}
}

func TestFormatTagDoesNotOverrideAKnownType(t *testing.T) {
	// time.Time already carries date-time. A tag saying otherwise is describing the same
	// field twice, and the type mapping is the one that matches what Go marshals.
	dir := t.TempDir()
	writeGoFile(t, dir, "stamp.go", `package types

import "time"

type Stamp struct {
	At time.Time `+"`json:\"at\"`"+`
}
`)

	reg := newSchemaRegistry([]string{dir})
	prop := propOf(t, reg.schemas[reg.resolve("types.Stamp")], "at")

	if prop["format"] != "date-time" {
		t.Fatalf("time.Time is date-time: %#v", prop)
	}
}

func TestExampleTagBecomesExample(t *testing.T) {
	// A consumer generating a request body sends the example. One the handler rejects is
	// worse than none, so this is the field that makes a generated body usable.
	dir := t.TempDir()
	writeGoFile(t, dir, "device.go", `package types

type AddDeviceRequest struct {
	Endpoint string `+"`json:\"inpace_api_endpoint\" example:\"http://10.0.0.5/api/v1\"`"+`
}
`)

	reg := newSchemaRegistry([]string{dir})
	prop := propOf(t, reg.schemas[reg.resolve("types.AddDeviceRequest")], "inpace_api_endpoint")

	if prop["example"] != "http://10.0.0.5/api/v1" {
		t.Fatalf("the example belongs in the schema: %#v", prop)
	}
}

func TestExampleTagOnANumberIsANumber(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "job.go", `package types

type Job struct {
	Priority int `+"`json:\"priority\" example:\"3\"`"+`
}
`)

	reg := newSchemaRegistry([]string{dir})
	prop := propOf(t, reg.schemas[reg.resolve("types.Job")], "priority")

	if _, isString := prop["example"].(string); isString {
		t.Fatalf("a quoted example on an integer field is still a number: %#v", prop)
	}
	if prop["example"].(float64) != 3 {
		t.Fatalf("example not carried through: %#v", prop)
	}
}

func TestFieldWithNoConstraintTagsIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "plain.go", `package types

type Plain struct {
	Name string `+"`json:\"name\"`"+`
}
`)

	reg := newSchemaRegistry([]string{dir})
	prop := propOf(t, reg.schemas[reg.resolve("types.Plain")], "name")

	for _, key := range []string{"enum", "format", "example"} {
		if _, present := prop[key]; present {
			t.Fatalf("nothing was declared, so %q should be absent: %#v", key, prop)
		}
	}
}

func TestConstraintTagsCombine(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "device.go", `package types

type AddDeviceRequest struct {
	DeviceType string `+"`json:\"device_type\" enums:\"hub,dongle\" example:\"dongle\"`"+`
}
`)

	reg := newSchemaRegistry([]string{dir})
	prop := propOf(t, reg.schemas[reg.resolve("types.AddDeviceRequest")], "device_type")

	if prop["example"] != "dongle" {
		t.Fatalf("example missing: %#v", prop)
	}
	if values, _ := prop["enum"].([]interface{}); len(values) != 2 {
		t.Fatalf("enum missing: %#v", prop)
	}
}
