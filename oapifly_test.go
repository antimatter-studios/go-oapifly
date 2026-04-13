package oapifly

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

// ---------------------------------------------------------------------------
// New
// ---------------------------------------------------------------------------

func TestNew_DefaultVersion(t *testing.T) {
	g := New(Config{Title: "Test API"})
	if g.Config.Version != "dev" {
		t.Errorf("default version = %q, want %q", g.Config.Version, "dev")
	}
}

func TestNew_ExplicitVersion(t *testing.T) {
	g := New(Config{Title: "Test API", Version: "2.0.0"})
	if g.Config.Version != "2.0.0" {
		t.Errorf("version = %q, want %q", g.Config.Version, "2.0.0")
	}
}

func TestNew_DefaultTypeDirs(t *testing.T) {
	g := New(Config{Title: "Test"})
	if len(g.Config.TypeDirs) != 1 || g.Config.TypeDirs[0] != "types" {
		t.Errorf("TypeDirs = %v, want [types]", g.Config.TypeDirs)
	}
}

func TestNew_CustomTypeDirs(t *testing.T) {
	g := New(Config{Title: "Test", TypeDirs: []string{"pkg/models", "internal/domain"}})
	if len(g.Config.TypeDirs) != 2 {
		t.Errorf("TypeDirs = %v", g.Config.TypeDirs)
	}
}

// ---------------------------------------------------------------------------
// resolveFiles
// ---------------------------------------------------------------------------

func TestResolveFiles_MultiplePatterns(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "a.go", "package a")
	writeTestFile(t, dir, "b.txt", "hello")

	files := resolveFiles([]string{
		filepath.Join(dir, "*.go"),
		filepath.Join(dir, "*.txt"),
	})
	if len(files) != 2 {
		t.Errorf("expected 2 files, got %d: %v", len(files), files)
	}
}

func TestResolveFiles_InvalidGlob(t *testing.T) {
	files := resolveFiles([]string{"[invalid"})
	if len(files) != 0 {
		t.Errorf("invalid glob should return no files, got %v", files)
	}
}

func TestResolveFiles_NoMatches(t *testing.T) {
	files := resolveFiles([]string{"/nonexistent/path/*.go"})
	if len(files) != 0 {
		t.Errorf("expected empty, got %v", files)
	}
}

func TestResolveFiles_Empty(t *testing.T) {
	files := resolveFiles(nil)
	if len(files) != 0 {
		t.Errorf("expected empty, got %v", files)
	}
}

// ---------------------------------------------------------------------------
// Generate
// ---------------------------------------------------------------------------

func TestGenerate_NoFilesFound(t *testing.T) {
	g := New(Config{
		Title:        "Test",
		ScanPatterns: []string{"nonexistent/**/*.go"},
	})
	spec := g.Generate()
	if _, ok := spec["error"]; !ok {
		t.Error("expected error key when no files match")
	}
}

func TestGenerate_WithAnnotatedFile(t *testing.T) {
	dir := t.TempDir()

	src := `package handlers

type Controller struct{}

// @Summary List items
// @Description Get all items
// @Tags items
// @Success 200 {object} Item "OK"
// @Router /api/items [GET]
func (c *Controller) List() {}

// @Summary Create item
// @Tags items
// @Router /api/items [POST]
func (c *Controller) Create() {}
`
	writeTestFile(t, dir, "handlers.go", src)

	g := New(Config{
		Title:        "Test API",
		Version:      "1.0.0",
		ScanPatterns: []string{filepath.Join(dir, "*.go")},
	})
	spec := g.Generate()

	if spec["openapi"] != "3.0.0" {
		t.Errorf("openapi = %v", spec["openapi"])
	}

	info, ok := spec["info"].(map[string]string)
	if !ok {
		t.Fatal("info is not map[string]string")
	}
	if info["title"] != "Test API" || info["version"] != "1.0.0" {
		t.Errorf("info = %v", info)
	}

	paths := spec["paths"].(map[string]map[string]PathItem)
	apiItems := paths["/api/items"]
	if _, ok := apiItems["get"]; !ok {
		t.Error("missing GET method")
	}
	if _, ok := apiItems["post"]; !ok {
		t.Error("missing POST method")
	}
	if apiItems["get"].Summary != "List items" {
		t.Errorf("GET summary = %q", apiItems["get"].Summary)
	}
}

func TestGenerate_SkipsInvalidFiles(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "bad.go", "this is not valid go code !!!")
	writeTestFile(t, dir, "good.go", `package handlers
type C struct{}
// @Summary OK
// @Router /health [GET]
func (c *C) Health() {}
`)

	g := New(Config{
		Title:        "Test",
		ScanPatterns: []string{filepath.Join(dir, "*.go")},
	})
	spec := g.Generate()

	paths := spec["paths"].(map[string]map[string]PathItem)
	if _, ok := paths["/health"]; !ok {
		t.Error("valid file should still be processed despite bad file")
	}
}

func TestGenerate_WarningsOnParseFailure(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "bad.go", "this is not valid go !!!")
	writeTestFile(t, dir, "good.go", `package api
type C struct{}
// @Summary OK
// @Router /ok [GET]
func (c *C) OK() {}
`)

	g := New(Config{
		Title:        "Test",
		ScanPatterns: []string{filepath.Join(dir, "*.go")},
	})
	g.Generate()

	if len(g.Warnings) != 1 {
		t.Errorf("expected 1 warning, got %d: %v", len(g.Warnings), g.Warnings)
	}
}

func TestGenerate_WarningsResetOnEachCall(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "bad.go", "not go code !!!")
	writeTestFile(t, dir, "good.go", `package api
type C struct{}
// @Summary OK
// @Router /ok [GET]
func (c *C) OK() {}
`)

	g := New(Config{
		Title:        "Test",
		ScanPatterns: []string{filepath.Join(dir, "*.go")},
	})

	g.Generate()
	if len(g.Warnings) != 1 {
		t.Fatalf("first call: expected 1 warning, got %d", len(g.Warnings))
	}

	g.Generate()
	if len(g.Warnings) != 1 {
		t.Errorf("warnings should reset per call, got %d", len(g.Warnings))
	}
}

func TestGenerate_EmptyComponents(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "api.go", `package api
type C struct{}
// @Summary No schemas
// @Router /simple [GET]
func (c *C) Simple() {}
`)

	g := New(Config{
		Title:        "Test",
		ScanPatterns: []string{filepath.Join(dir, "*.go")},
	})
	spec := g.Generate()

	components := spec["components"].(map[string]interface{})
	if _, ok := components["schemas"]; ok {
		t.Error("schemas should be absent when no schema types exist")
	}
}

func TestGenerate_MethodWithoutRouter(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "api.go", `package api
type C struct{}
// @Summary No router tag
func (c *C) NoRoute() {}
// @Summary With router
// @Router /ok [GET]
func (c *C) WithRoute() {}
`)

	g := New(Config{
		Title:        "Test",
		ScanPatterns: []string{filepath.Join(dir, "*.go")},
	})
	spec := g.Generate()

	paths := spec["paths"].(map[string]map[string]PathItem)
	if len(paths) != 1 {
		t.Errorf("expected 1 path, got %d", len(paths))
	}
}

func TestGenerate_StandaloneFunction(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "api.go", `package api
// @Summary Health check
// @Router /health [GET]
func HealthCheck() {}
`)

	g := New(Config{
		Title:        "Test",
		ScanPatterns: []string{filepath.Join(dir, "*.go")},
	})
	spec := g.Generate()

	paths := spec["paths"].(map[string]map[string]PathItem)
	if _, ok := paths["/health"]; !ok {
		t.Error("standalone functions should be picked up")
	}
	if paths["/health"]["get"].Summary != "Health check" {
		t.Errorf("summary = %q", paths["/health"]["get"].Summary)
	}
}

func TestGenerate_SchemaAnnotatedStructs(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "api.go", `package api
type C struct{}
// @schema
type Item struct {
	ID   int
	Name string
}
// @Summary List
// @Router /items [GET]
func (c *C) List() {}
`)

	g := New(Config{
		Title:        "Test",
		ScanPatterns: []string{filepath.Join(dir, "*.go")},
	})
	spec := g.Generate()

	components := spec["components"].(map[string]interface{})
	schemas := components["schemas"].(map[string]map[string]interface{})
	if _, ok := schemas["Item"]; !ok {
		t.Fatal("Item schema should be registered")
	}
}

func TestGenerate_MultiplePaths(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "api.go", `package api
type C struct{}
// @Summary A
// @Router /a [GET]
func (c *C) A() {}
// @Summary B
// @Router /b [POST]
func (c *C) B() {}
// @Summary C
// @Router /a [POST]
func (c *C) C() {}
`)

	g := New(Config{
		Title:        "Test",
		ScanPatterns: []string{filepath.Join(dir, "*.go")},
	})
	spec := g.Generate()

	paths := spec["paths"].(map[string]map[string]PathItem)
	if len(paths) != 2 {
		t.Errorf("expected 2 paths, got %d", len(paths))
	}
	if len(paths["/a"]) != 2 {
		t.Errorf("expected 2 methods on /a, got %d", len(paths["/a"]))
	}
}

func TestGenerate_MultipleFilesAcrossPatterns(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	writeTestFile(t, dir1, "a.go", `package a
type C struct{}
// @Summary From dir1
// @Router /dir1 [GET]
func (c *C) A() {}
`)
	writeTestFile(t, dir2, "b.go", `package b
type C struct{}
// @Summary From dir2
// @Router /dir2 [GET]
func (c *C) B() {}
`)

	g := New(Config{
		Title: "Test",
		ScanPatterns: []string{
			filepath.Join(dir1, "*.go"),
			filepath.Join(dir2, "*.go"),
		},
	})
	spec := g.Generate()

	paths := spec["paths"].(map[string]map[string]PathItem)
	if _, ok := paths["/dir1"]; !ok {
		t.Error("missing /dir1")
	}
	if _, ok := paths["/dir2"]; !ok {
		t.Error("missing /dir2")
	}
}

func TestGenerate_Description(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "api.go", `package api
type C struct{}
// @Summary OK
// @Router /ok [GET]
func (c *C) OK() {}
`)

	g := New(Config{
		Title:        "Test",
		Description:  "My awesome API",
		ScanPatterns: []string{filepath.Join(dir, "*.go")},
	})
	spec := g.Generate()

	info := spec["info"].(map[string]string)
	if info["description"] != "My awesome API" {
		t.Errorf("description = %q", info["description"])
	}
}

func TestGenerate_NoDescriptionOmitted(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "api.go", `package api
type C struct{}
// @Summary OK
// @Router /ok [GET]
func (c *C) OK() {}
`)

	g := New(Config{
		Title:        "Test",
		ScanPatterns: []string{filepath.Join(dir, "*.go")},
	})
	spec := g.Generate()

	info := spec["info"].(map[string]string)
	if _, ok := info["description"]; ok {
		t.Error("description should be omitted when empty")
	}
}

func TestGenerate_DeprecatedEndpoint(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "api.go", `package api
type C struct{}
// @Deprecated
// @Summary Old endpoint
// @Router /old [GET]
func (c *C) Old() {}
`)

	g := New(Config{
		Title:        "Test",
		ScanPatterns: []string{filepath.Join(dir, "*.go")},
	})
	spec := g.Generate()

	paths := spec["paths"].(map[string]map[string]PathItem)
	if !paths["/old"]["get"].Deprecated {
		t.Error("endpoint should be deprecated")
	}
}

func TestGenerate_QueryAndHeaderParams(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "api.go", `package api
type C struct{}
// @Summary Search
// @Param q query string true "Search query"
// @Param page query int false "Page number"
// @Param Authorization header string true "Bearer token"
// @Router /api/search [GET]
func (c *C) Search() {}
`)

	g := New(Config{
		Title:        "Test",
		ScanPatterns: []string{filepath.Join(dir, "*.go")},
	})
	spec := g.Generate()

	paths := spec["paths"].(map[string]map[string]PathItem)
	params := paths["/api/search"]["get"].Parameters
	if len(params) != 3 {
		t.Fatalf("expected 3 params, got %d", len(params))
	}

	byName := map[string]Parameter{}
	for _, p := range params {
		byName[p.Name] = p
	}
	if byName["q"].In != "query" || !byName["q"].Required {
		t.Errorf("q param = %+v", byName["q"])
	}
	if byName["page"].Schema["type"] != "integer" {
		t.Errorf("page schema = %v", byName["page"].Schema)
	}
	if byName["Authorization"].In != "header" {
		t.Errorf("Authorization param = %+v", byName["Authorization"])
	}
}

func TestGenerate_RequestBody(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "api.go", `package api
type C struct{}
// @Summary Create user
// @Param user body User true "User data"
// @Success 201 {object} User "Created"
// @Router /api/users [POST]
func (c *C) CreateUser() {}
`)

	g := New(Config{
		Title:        "Test",
		ScanPatterns: []string{filepath.Join(dir, "*.go")},
	})
	spec := g.Generate()

	paths := spec["paths"].(map[string]map[string]PathItem)
	item := paths["/api/users"]["post"]

	if item.RequestBody == nil {
		t.Fatal("RequestBody should not be nil")
	}
	if item.RequestBody.Description != "User data" {
		t.Errorf("RequestBody.Description = %q", item.RequestBody.Description)
	}
	if len(item.Parameters) != 0 {
		t.Errorf("body params should not appear in Parameters, got %d", len(item.Parameters))
	}
}

func TestGenerate_DuplicateHandlerWarning(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "api.go", `package api
type C struct{}
// @Summary First
// @Router /api/items [GET]
func (c *C) First() {}
// @Summary Second
// @Router /api/items [GET]
func (c *C) Second() {}
`)

	g := New(Config{
		Title:        "Test",
		ScanPatterns: []string{filepath.Join(dir, "*.go")},
	})
	spec := g.Generate()

	// Second should win
	paths := spec["paths"].(map[string]map[string]PathItem)
	if paths["/api/items"]["get"].Summary != "Second" {
		t.Errorf("expected second handler to win, got %q", paths["/api/items"]["get"].Summary)
	}

	// Should have a warning about the duplicate
	found := false
	for _, w := range g.Warnings {
		if strings.Contains(w, "duplicate") && strings.Contains(w, "/api/items") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected duplicate handler warning, got %v", g.Warnings)
	}
}

// ---------------------------------------------------------------------------
// JSON / YAML
// ---------------------------------------------------------------------------

func TestJSON(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "api.go", `package api
type C struct{}
// @Summary Ping
// @Router /ping [GET]
func (c *C) Ping() {}
`)

	g := New(Config{
		Title:        "JSON Test",
		Version:      "0.1.0",
		ScanPatterns: []string{filepath.Join(dir, "*.go")},
	})

	data, err := g.JSON()
	if err != nil {
		t.Fatalf("JSON() error: %v", err)
	}

	var spec map[string]interface{}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if spec["openapi"] != "3.0.0" {
		t.Errorf("openapi = %v", spec["openapi"])
	}
}

func TestYAML(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "api.go", `package api
type C struct{}
// @Summary Ping
// @Router /ping [GET]
func (c *C) Ping() {}
`)

	g := New(Config{
		Title:        "YAML Test",
		Version:      "0.1.0",
		ScanPatterns: []string{filepath.Join(dir, "*.go")},
	})

	data, err := g.YAML()
	if err != nil {
		t.Fatalf("YAML() error: %v", err)
	}
	if len(data) == 0 {
		t.Error("YAML output is empty")
	}
}

func TestJSON_ValidStructure(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "api.go", `package api
type C struct{}
// @Summary Get item
// @Tags items
// @Param id path string true "Item ID" example(abc)
// @Param q query string false "Filter"
// @Success 200 {object} Item "OK"
// @Router /api/items/{id} [GET]
func (c *C) GetItem() {}
`)

	g := New(Config{
		Title:        "Roundtrip",
		Version:      "1.0.0",
		ScanPatterns: []string{filepath.Join(dir, "*.go")},
	})

	data, err := g.JSON()
	if err != nil {
		t.Fatal(err)
	}

	var spec map[string]interface{}
	if err := json.Unmarshal(data, &spec); err != nil {
		t.Fatal(err)
	}

	paths := spec["paths"].(map[string]interface{})
	if paths["/api/items/{id}"] == nil {
		t.Error("path /api/items/{id} should exist in JSON output")
	}
}
