package oapifly

import (
	"path/filepath"
	"strings"
	"testing"
)

// A Path Item Object's keys are a fixed set of HTTP methods. Whatever the @Router tag says
// ends up as one of those keys, so a tag naming something else produces a document no
// consumer will read.

func generateFrom(t *testing.T, source string) (map[string]map[string]PathItem, []string) {
	t.Helper()
	dir := t.TempDir()
	writeTestFile(t, dir, "api.go", source)

	g := New(Config{
		Title:        "Test",
		ScanPatterns: []string{filepath.Join(dir, "*.go")},
	})
	spec := g.Generate()

	return spec["paths"].(map[string]map[string]PathItem), g.Warnings
}

func TestRouterMethodIsLowercased(t *testing.T) {
	paths, warnings := generateFrom(t, `package api
type C struct{}
// @Summary Fetch
// @Router /thing [GET]
func (c *C) Get() {}
`)

	if _, ok := paths["/thing"]["get"]; !ok {
		t.Fatalf("the key is the lowercased method: %#v", paths["/thing"])
	}
	if len(warnings) != 0 {
		t.Fatalf("a real method should not warn: %v", warnings)
	}
}

func TestRouterAnyExpandsToEveryMethod(t *testing.T) {
	// A catch-all route - fiber's App.All, gin's router.Any - serves every method, and
	// `any` is what those frameworks call it. OpenAPI has no such key, so it has to be
	// written out as the methods it stands for.
	paths, warnings := generateFrom(t, `package api
type C struct{}
// @Summary Proxy anything
// @Router /proxy/{guid} [any]
func (c *C) Any() {}
`)

	item := paths["/proxy/{guid}"]
	for _, method := range []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"} {
		if _, ok := item[method]; !ok {
			t.Fatalf("a catch-all route serves %s too: %#v", method, item)
		}
	}
	if _, ok := item["any"]; ok {
		t.Fatalf("`any` is not a key a Path Item Object may carry: %#v", item)
	}
	if len(warnings) != 0 {
		t.Fatalf("a catch-all route is describable, so it should not warn: %v", warnings)
	}
}

func TestRouterAnyCarriesTheSameOperationToEachMethod(t *testing.T) {
	paths, _ := generateFrom(t, `package api
type C struct{}
// @Summary Proxy anything
// @Description Forwards the request to the device
// @Router /proxy/{guid} [any]
func (c *C) Any() {}
`)

	item := paths["/proxy/{guid}"]
	if item["get"].Summary != "Proxy anything" || item["delete"].Summary != "Proxy anything" {
		t.Fatalf("every expanded method describes the same operation: %#v", item)
	}
}

func TestRouterMethodThatIsNotAMethodWarns(t *testing.T) {
	// Anything else is a typo or a framework verb with no OpenAPI equivalent. Emitting it
	// produced an invalid document in silence: dredd skipped the whole path item, so the
	// endpoint looked documented and was tested by nothing.
	paths, warnings := generateFrom(t, `package api
type C struct{}
// @Summary Connect
// @Router /tunnel [connect]
func (c *C) Connect() {}
`)

	if _, ok := paths["/tunnel"]; ok {
		t.Fatalf("an unusable method must not reach the document: %#v", paths["/tunnel"])
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "connect") {
		t.Fatalf("the warning should name the method it could not use: %v", warnings)
	}
}

func TestRouterMethodWarningNamesThePath(t *testing.T) {
	_, warnings := generateFrom(t, `package api
type C struct{}
// @Summary Connect
// @Router /tunnel [connect]
func (c *C) Connect() {}
`)

	if len(warnings) != 1 || !strings.Contains(warnings[0], "/tunnel") {
		t.Fatalf("the warning should say which route it dropped: %v", warnings)
	}
}

func TestRouterAnyDoesNotOverwriteAnExplicitOperation(t *testing.T) {
	// A route can be registered as a catch-all and then have one method described in its
	// own right. The specific description is the more useful one.
	paths, _ := generateFrom(t, `package api
type C struct{}
// @Summary Proxy anything
// @Router /proxy/{guid} [any]
func (c *C) Any() {}

// @Summary Read the device
// @Router /proxy/{guid} [get]
func (c *C) Get() {}
`)

	if got := paths["/proxy/{guid}"]["get"].Summary; got != "Read the device" {
		t.Fatalf("the method described on its own terms wins, got %q", got)
	}
	if got := paths["/proxy/{guid}"]["post"].Summary; got != "Proxy anything" {
		t.Fatalf("the others still come from the catch-all, got %q", got)
	}
}
