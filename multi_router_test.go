package oapifly

import (
	"testing"
)

// One handler can serve several routes, and swaggo says so by repeating @Router. Reading
// only the first meant the rest were described by nothing.

func TestSeveralRouterTagsOnOneHandler(t *testing.T) {
	paths, warnings := generateFrom(t, `package api
type C struct{}
// @Summary Proxy a request
// @Router /proxy/{guid}/{path} [get]
// @Router /proxy/{guid}/{path} [post]
// @Router /proxy/{guid}/{path} [delete]
func (c *C) Proxy() {}
`)

	item := paths["/proxy/{guid}/{path}"]
	for _, method := range []string{"get", "post", "delete"} {
		if _, ok := item[method]; !ok {
			t.Fatalf("every @Router line describes a route: %#v", item)
		}
	}
	if len(item) != 3 {
		t.Fatalf("and only those three: %#v", item)
	}
	if len(warnings) != 0 {
		t.Fatalf("repeating @Router is not a duplicate handler: %v", warnings)
	}
}

func TestSeveralRouterTagsOnDifferentPaths(t *testing.T) {
	// The same handler mounted twice - an alias, or a versioned path kept alongside the
	// one that replaced it.
	paths, _ := generateFrom(t, `package api
type C struct{}
// @Summary Read the config
// @Router /config [get]
// @Router /settings [get]
func (c *C) Config() {}
`)

	if _, ok := paths["/config"]["get"]; !ok {
		t.Fatalf("first path missing: %#v", paths)
	}
	if _, ok := paths["/settings"]["get"]; !ok {
		t.Fatalf("second path missing: %#v", paths)
	}
}

func TestSeveralRouterTagsShareTheOperation(t *testing.T) {
	paths, _ := generateFrom(t, `package api
type C struct{}
// @Summary Proxy a request
// @Description Forwards to the device
// @Param guid path string true "Device GUID"
// @Router /proxy/{guid} [get]
// @Router /proxy/{guid} [post]
func (c *C) Proxy() {}
`)

	item := paths["/proxy/{guid}"]
	if item["get"].Summary != "Proxy a request" || item["post"].Summary != "Proxy a request" {
		t.Fatalf("each route carries the operation described above it: %#v", item)
	}
	if len(item["post"].Parameters) != 1 || item["post"].Parameters[0].Name != "guid" {
		t.Fatalf("including its parameters: %#v", item["post"].Parameters)
	}
}

func TestParametersFollowEachRouterPath(t *testing.T) {
	// The path parameters come from the route, so two routes with different templates
	// cannot share one parameter list.
	paths, _ := generateFrom(t, `package api
type C struct{}
// @Summary Read a device
// @Router /device/{guid} [get]
// @Router /device/{guid}/{part} [get]
func (c *C) Device() {}
`)

	short := paths["/device/{guid}"]["get"].Parameters
	long := paths["/device/{guid}/{part}"]["get"].Parameters
	if len(short) != 1 {
		t.Fatalf("one placeholder, one parameter: %#v", short)
	}
	if len(long) != 2 {
		t.Fatalf("two placeholders, two parameters: %#v", long)
	}
}

func TestOneUnusableRouterTagDoesNotDropTheOthers(t *testing.T) {
	paths, warnings := generateFrom(t, `package api
type C struct{}
// @Summary Tunnel
// @Router /tunnel [get]
// @Router /tunnel [connect]
func (c *C) Tunnel() {}
`)

	if _, ok := paths["/tunnel"]["get"]; !ok {
		t.Fatalf("the routes it could describe still belong in the document: %#v", paths["/tunnel"])
	}
	if len(warnings) != 1 {
		t.Fatalf("and the one it could not is reported: %v", warnings)
	}
}
