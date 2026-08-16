package oapifly

import (
	"strings"
	"testing"
)

// A type named by an annotation - @Success 200 {object} pkg.Thing - is resolved by
// schemaRegistry.resolve. These cover what it makes of a declaration that is not a struct,
// and what it says when it cannot find the type at all.

func TestResolveFollowsAlias(t *testing.T) {
	dto := t.TempDir()
	writeGoFile(t, dto, "auth.go", `package dto

type LoginRequest struct {
	Username string `+"`json:\"username\"`"+`
	Password string `+"`json:\"password\"`"+`
}
`)

	// The alias sits in a directory searched before the one declaring the struct, which is
	// how a compatibility shim shadows the type it points at.
	shim := t.TempDir()
	writeGoFile(t, shim, "aliases.go", `package restclient

import "example.com/dto"

type (
	LoginRequest = dto.LoginRequest
)
`)

	reg := newSchemaRegistry([]string{shim, dto})
	name := reg.resolve("restclient.LoginRequest")

	schema := reg.schemas[name]
	props, _ := schema["properties"].(map[string]interface{})
	if len(props) != 2 {
		t.Fatalf("alias was not followed to the struct it points at: %#v", schema)
	}
	if len(reg.warnings) != 0 {
		t.Fatalf("resolving through an alias should not warn: %v", reg.warnings)
	}
}

func TestResolveNamedStringType(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "device_type.go", `package types

type DeviceType string
`)

	reg := newSchemaRegistry([]string{dir})
	schema := reg.schemas[reg.resolve("types.DeviceType")]

	if schema["type"] != "string" {
		t.Fatalf("a named string type should stay a string, got %#v", schema)
	}
}

func TestResolveNamedIntType(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "status.go", `package types

type JobStatus int
`)

	reg := newSchemaRegistry([]string{dir})
	schema := reg.schemas[reg.resolve("types.JobStatus")]

	if schema["type"] != "integer" {
		t.Fatalf("a named int type should stay an integer, got %#v", schema)
	}
}

func TestResolveWarnsForUnknownType(t *testing.T) {
	reg := newSchemaRegistry([]string{t.TempDir()})
	schema := reg.schemas[reg.resolve("types.NoSuchResponse")]

	if schema["type"] != "object" {
		t.Fatalf("an unresolvable type still needs a usable schema, got %#v", schema)
	}
	if len(reg.warnings) != 1 {
		t.Fatalf("an unresolvable type must be reported, got %v", reg.warnings)
	}
	if !strings.Contains(reg.warnings[0], "types.NoSuchResponse") {
		t.Fatalf("the warning should name the type: %q", reg.warnings[0])
	}
}

func TestResolveWarnsOnlyOncePerType(t *testing.T) {
	reg := newSchemaRegistry([]string{t.TempDir()})
	reg.resolve("types.NoSuchResponse")
	reg.resolve("types.NoSuchResponse")

	if len(reg.warnings) != 1 {
		t.Fatalf("a type referenced twice should be reported once, got %v", reg.warnings)
	}
}

func TestResolveDoesNotWarnForOpenAPIPrimitives(t *testing.T) {
	// A multipart upload is declared as `file`, and `string` and friends reach here from
	// @Param declarations. None of them is a Go type that could have been found.
	reg := newSchemaRegistry([]string{t.TempDir()})
	for _, primitive := range []string{"file", "string", "integer", "number", "boolean", "object", "array"} {
		reg.resolve(primitive)
	}

	if len(reg.warnings) != 0 {
		t.Fatalf("OpenAPI primitives are not missing Go types: %v", reg.warnings)
	}
}
