package oapifly

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// The parity types are declared twice: here as Go types the registry reads by reflection,
// and in paritySource as the text the annotation path reads. Both descriptions of the same
// API must come out the same.

type ParityStatus string

type ParityItem struct {
	ID    int64   `json:"id"`
	Name  string  `json:"name" example:"inbox"`
	Score float64 `json:"score,omitempty"`
}

type ParityBase struct {
	CreatedAt time.Time `json:"created_at"`
}

type ParityNode struct {
	Label    string       `json:"label"`
	Children []ParityNode `json:"children,omitempty"`
}

type ParityPage[T any] struct {
	Data  []T `json:"data"`
	Total int `json:"total"`
}

type ParityEnvelope struct {
	ParityBase
	Status   ParityStatus           `json:"status" enums:"active,disabled"`
	Owner    *ParityItem            `json:"owner,omitempty"`
	Note     *string                `json:"note"`
	Items    []ParityItem           `json:"items"`
	Counts   map[string]int         `json:"counts"`
	Tree     ParityNode             `json:"tree"`
	Page     ParityPage[ParityItem] `json:"page"`
	Kind     string                 `json:"kind" format:"hostname"`
	Hidden   string                 `json:"-"`
	Untagged bool
}

const paritySource = `package types

import "time"

type ParityStatus string

type ParityItem struct {
	ID    int64   ` + "`json:\"id\"`" + `
	Name  string  ` + "`json:\"name\" example:\"inbox\"`" + `
	Score float64 ` + "`json:\"score,omitempty\"`" + `
}

type ParityBase struct {
	CreatedAt time.Time ` + "`json:\"created_at\"`" + `
}

type ParityNode struct {
	Label    string       ` + "`json:\"label\"`" + `
	Children []ParityNode ` + "`json:\"children,omitempty\"`" + `
}

type ParityPage[T any] struct {
	Data  []T ` + "`json:\"data\"`" + `
	Total int ` + "`json:\"total\"`" + `
}

type ParityEnvelope struct {
	ParityBase
	Status   ParityStatus           ` + "`json:\"status\" enums:\"active,disabled\"`" + `
	Owner    *ParityItem            ` + "`json:\"owner,omitempty\"`" + `
	Note     *string                ` + "`json:\"note\"`" + `
	Items    []ParityItem           ` + "`json:\"items\"`" + `
	Counts   map[string]int         ` + "`json:\"counts\"`" + `
	Tree     ParityNode             ` + "`json:\"tree\"`" + `
	Page     ParityPage[ParityItem] ` + "`json:\"page\"`" + `
	Kind     string                 ` + "`json:\"kind\" format:\"hostname\"`" + `
	Hidden   string                 ` + "`json:\"-\"`" + `
	Untagged bool
}
`

const parityHandler = `package handlers

// @Summary Create an envelope
// @ID createEnvelope
// @Param body body types.ParityEnvelope true "the envelope"
// @Success 201 {object} types.ParityEnvelope "created"
// @Failure 400 {object} types.ParityItem "bad"
// @Router /parity [post]
func CreateEnvelope() {}
`

func parityEndpoint() Endpoint {
	return Endpoint{
		Method:      "POST",
		Path:        "/parity",
		OperationID: "createEnvelope",
		Summary:     "Create an envelope",
		Request:     TypeOf[ParityEnvelope](),
		Responses: []EndpointResponse{
			{Status: 201, Description: "created", Type: TypeOf[ParityEnvelope]()},
			{Status: 400, Description: "bad", Type: TypeOf[ParityItem]()},
		},
	}
}

// writeIn writes a Go file into dir, creating dir first.
func writeIn(t *testing.T, dir, name, src string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeGoFile(t, dir, name, src)
}

func asJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func operation(t *testing.T, spec map[string]interface{}, path, method string) PathItem {
	t.Helper()
	paths, ok := spec["paths"].(map[string]map[string]PathItem)
	if !ok {
		t.Fatalf("no paths in %v", spec)
	}
	item, ok := paths[path][method]
	if !ok {
		t.Fatalf("%s %s is not described: %v", method, path, paths)
	}
	return item
}

func componentsOf(spec map[string]interface{}) interface{} {
	components, _ := spec["components"].(map[string]interface{})
	return components["schemas"]
}

// The same types, described through the registry and through annotations, give the same
// document: the components, the request body and every response.
func TestRegistryMatchesAnnotations(t *testing.T) {
	dir := t.TempDir()
	writeIn(t, filepath.Join(dir, "types"), "types.go", paritySource)
	writeIn(t, filepath.Join(dir, "handlers"), "handlers.go", parityHandler)
	annotated := New(Config{
		ScanPatterns: []string{filepath.Join(dir, "handlers", "*.go")},
		TypeDirs:     []string{filepath.Join(dir, "types")},
	})
	fromComments := annotated.Generate()
	if len(annotated.Warnings) > 0 {
		t.Fatalf("the annotated description should be clean: %v", annotated.Warnings)
	}

	registered := New(Config{})
	registered.Register(parityEndpoint())
	fromCode := registered.Generate()
	if len(registered.Warnings) > 0 {
		t.Fatalf("the registered description should be clean: %v", registered.Warnings)
	}

	if got, want := asJSON(t, componentsOf(fromCode)), asJSON(t, componentsOf(fromComments)); got != want {
		t.Fatalf("components differ\nregistry:    %s\nannotations: %s", got, want)
	}

	code := operation(t, fromCode, "/parity", "post")
	comments := operation(t, fromComments, "/parity", "post")
	if got, want := asJSON(t, code.RequestBody.Content), asJSON(t, comments.RequestBody.Content); got != want {
		t.Fatalf("request bodies differ\nregistry:    %s\nannotations: %s", got, want)
	}
	if !code.RequestBody.Required {
		t.Fatalf("a request type that is the body is a required body")
	}
	if got, want := asJSON(t, code.Responses), asJSON(t, comments.Responses); got != want {
		t.Fatalf("responses differ\nregistry:    %s\nannotations: %s", got, want)
	}
	if code.Summary != comments.Summary || code.OperationID != comments.OperationID {
		t.Fatalf("titles differ: %q/%q against %q/%q", code.Summary, code.OperationID, comments.Summary, comments.OperationID)
	}
}

// A program with no Go source on disk describes its registered operations completely.
func TestRegistryNeedsNoSourceOnDisk(t *testing.T) {
	empty := t.TempDir()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(empty); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(wd) })

	g := New(Config{Title: "REST Mail", Version: "1"})
	g.Register(parityEndpoint())
	spec := g.Generate()

	if msg, failed := spec["error"]; failed {
		t.Fatalf("a registry-only generator needs no source: %v", msg)
	}
	if len(g.Warnings) > 0 {
		t.Fatalf("nothing to warn about: %v", g.Warnings)
	}
	schemas, _ := componentsOf(spec).(map[string]map[string]interface{})
	for _, name := range []string{"ParityEnvelope", "ParityItem", "ParityNode", "ParityPage-ParityItem"} {
		if _, ok := schemas[name]; !ok {
			t.Fatalf("component %s missing from %v", name, schemas)
		}
	}
	if spec["openapi"] != "3.0.0" {
		t.Fatalf("still an OpenAPI 3.0 document: %v", spec["openapi"])
	}
}

type domainPath struct {
	ID int64 `path:"id" description:"The domain's id"`
}

type domainList struct {
	Limit  *int   `query:"limit" example:"20" description:"How many to return"`
	Sort   string `query:"sort" enums:"name,created"`
	Tenant string `query:"tenant,required"`
	Trace  string `header:"X-Request-ID"`
}

type domainCreate struct {
	Name string `json:"name"`
}

type domainUpdate struct {
	ID   int64         `path:"id"`
	Body *domainCreate `body:""`
}

type domainImport struct {
	ID   int64  `path:"id"`
	File []byte `body:"application/octet-stream"`
}

// Parameters come from the request type's tagged fields, in the template's order for the
// path, and the body from the field marked as the body.
func TestRegistryParameters(t *testing.T) {
	g := New(Config{})
	g.Register(
		Endpoint{Method: "get", Path: "/domains/{id}", Request: TypeOf[domainPath](),
			Responses: []EndpointResponse{{Status: 200, Type: TypeOf[domainCreate]()}}},
		Endpoint{Method: "GET", Path: "/domains", Request: TypeOf[domainList](),
			Responses: []EndpointResponse{{Status: 200, Type: TypeOf[[]domainCreate]()}}},
		Endpoint{Method: "PUT", Path: "/domains/{id}", Request: TypeOf[domainUpdate](),
			Responses: []EndpointResponse{{Status: 204}}},
		Endpoint{Method: "POST", Path: "/domains/{id}/import", Request: TypeOf[*domainImport](),
			Responses: []EndpointResponse{{Status: 202}}},
	)
	spec := g.Generate()
	if len(g.Warnings) > 0 {
		t.Fatalf("no warnings expected: %v", g.Warnings)
	}

	get := operation(t, spec, "/domains/{id}", "get")
	if len(get.Parameters) != 1 {
		t.Fatalf("one path parameter: %+v", get.Parameters)
	}
	id := get.Parameters[0]
	if id.Name != "id" || id.In != "path" || !id.Required || id.Description != "The domain's id" ||
		asJSON(t, id.Schema) != `{"type":"integer"}` {
		t.Fatalf("path parameter: %+v", id)
	}
	if get.RequestBody != nil {
		t.Fatalf("a request type with only parameters has no body: %+v", get.RequestBody)
	}
	if desc := get.Responses["200"].Description; desc != "OK" {
		t.Fatalf("a response with no description says its status text: %q", desc)
	}

	list := operation(t, spec, "/domains", "get")
	byName := map[string]Parameter{}
	for _, p := range list.Parameters {
		byName[p.Name] = p
	}
	limit := byName["limit"]
	if limit.In != "query" || limit.Required || limit.Example != float64(20) && limit.Example != int64(20) && limit.Example != 20 {
		t.Fatalf("an optional, typed query parameter with its example: %+v", limit)
	}
	if _, nullable := limit.Schema["nullable"]; nullable {
		t.Fatalf("a pointer parameter is optional, not null: %+v", limit.Schema)
	}
	if asJSON(t, byName["sort"].Schema) != `{"enum":["name","created"],"type":"string"}` {
		t.Fatalf("enums constrain a parameter: %+v", byName["sort"].Schema)
	}
	if !byName["tenant"].Required {
		t.Fatalf(",required makes a query parameter required: %+v", byName["tenant"])
	}
	if byName["X-Request-ID"].In != "header" {
		t.Fatalf("a header parameter: %+v", byName["X-Request-ID"])
	}
	if got := asJSON(t, list.Responses["200"].Content); got != `{"application/json":{"schema":{"items":{"$ref":"#/components/schemas/domainCreate"},"type":"array"}}}` {
		t.Fatalf("a list response is an array of refs: %s", got)
	}

	put := operation(t, spec, "/domains/{id}", "put")
	if put.RequestBody == nil || put.RequestBody.Required {
		t.Fatalf("a pointer body field is an optional body: %+v", put.RequestBody)
	}
	if got := asJSON(t, put.RequestBody.Content); got != `{"application/json":{"schema":{"allOf":[{"$ref":"#/components/schemas/domainCreate"}],"nullable":true,"type":"object"}}}` {
		t.Fatalf("body schema: %s", got)
	}
	if put.Responses["204"].Content != nil {
		t.Fatalf("a response with no type has no content: %+v", put.Responses["204"])
	}

	imp := operation(t, spec, "/domains/{id}/import", "post")
	if got := asJSON(t, imp.RequestBody.Content); got != `{"application/octet-stream":{"schema":{"format":"byte","type":"string"}}}` {
		t.Fatalf("a body field names its media type: %s", got)
	}
}

type unboundRequest struct {
	ID    int64  `path:"id"`
	Name  string `json:"name"`
	Extra int64  `path:"extra"`
}

// What the registration cannot place is reported, not guessed at: a template variable with no
// field, a path field the template does not name, and fields that are neither parameter nor
// body.
func TestRegistryReportsWhatItCannotPlace(t *testing.T) {
	g := New(Config{})
	g.Register(Endpoint{Method: "PATCH", Path: "/a/{id}/{slug}", Request: TypeOf[unboundRequest]()})
	spec := g.Generate()

	want := []string{
		"PATCH /a/{id}/{slug}: main.unboundRequest fields Name are neither parameters nor the body",
		"PATCH /a/{id}/{slug}: path parameter {slug} has no field in the request type, described as a string",
		`PATCH /a/{id}/{slug}: request field for path parameter "extra" is not in the path template, not described`,
	}
	joined := strings.Join(g.Warnings, "\n")
	for _, w := range want {
		probe := strings.Replace(w, "main.", "github.com/antimatter-studios/go-oapifly.", 1)
		if !strings.Contains(joined, probe) {
			t.Fatalf("missing warning %q in:\n%s", probe, joined)
		}
	}
	params := operation(t, spec, "/a/{id}/{slug}", "patch").Parameters
	if len(params) != 2 || params[0].Name != "id" || params[1].Name != "slug" {
		t.Fatalf("both template variables are parameters, in order: %+v", params)
	}
}

// A route both annotated and registered takes its structure from the registration and, where
// the registration leaves them out, its titles from the comments.
func TestRegistryTakesTitlesFromComments(t *testing.T) {
	dir := t.TempDir()
	writeIn(t, filepath.Join(dir, "types"), "types.go", paritySource)
	writeIn(t, filepath.Join(dir, "handlers"), "handlers.go", `package handlers

// @Summary Create an envelope
// @Description From the comment.
// @ID createEnvelope
// @Tags envelopes
// @Success 200 {object} types.ParityItem "old"
// @Router /parity [post]
func CreateEnvelope() {}
`)
	g := New(Config{
		ScanPatterns: []string{filepath.Join(dir, "handlers", "*.go")},
		TypeDirs:     []string{filepath.Join(dir, "types")},
	})
	g.Register(Endpoint{
		Method:    "POST",
		Path:      "/parity",
		Request:   TypeOf[ParityItem](),
		Responses: []EndpointResponse{{Status: 201, Description: "created", Type: TypeOf[ParityEnvelope]()}},
	})
	item := operation(t, g.Generate(), "/parity", "post")

	if item.Summary != "Create an envelope" || item.Description != "From the comment." ||
		item.OperationID != "createEnvelope" || len(item.Tags) != 1 || item.Tags[0] != "envelopes" {
		t.Fatalf("titles come from the comment: %+v", item)
	}
	if _, old := item.Responses["200"]; old {
		t.Fatalf("the structure is the registration's, not the comment's: %+v", item.Responses)
	}
	if _, ok := item.Responses["201"]; !ok || item.RequestBody == nil {
		t.Fatalf("the registration's request and response are used: %+v", item)
	}
}

type looseValues struct {
	Anything interface{}     `json:"anything"`
	Raw      json.RawMessage `json:"raw"`
	Blob     []byte          `json:"blob"`
	When     *time.Time      `json:"when"`
	Dash     string          `json:"-,"`
}

type URL struct {
	Here bool `json:"here"`
}

type twoURLs struct {
	Ours   URL     `json:"ours"`
	Theirs url.URL `json:"theirs"`
}

// Where the annotation path is wrong today, the registry is right on purpose: an interface{}
// is unconstrained (#27), the well-known types are recognised (#29), []byte is the base64
// string encoding/json writes, and json:"-," names a field "-".
func TestRegistryDescribesLooseValuesHonestly(t *testing.T) {
	reg := newSchemaRegistry(nil)
	reg.reflectSchema(TypeOf[looseValues]())
	props := reg.schemas["looseValues"]["properties"].(map[string]interface{})

	want := map[string]string{
		"anything": `{}`,
		"raw":      `{}`,
		"blob":     `{"format":"byte","type":"string"}`,
		"when":     `{"format":"date-time","nullable":true,"type":"string"}`,
		"-":        `{"type":"string"}`,
	}
	for name, schema := range want {
		if got := asJSON(t, props[name]); got != schema {
			t.Fatalf("%s: got %s, want %s", name, got, schema)
		}
	}
}

// Two types with the same name from different packages are two components (#26).
func TestRegistryKeepsSameNamedTypesApart(t *testing.T) {
	reg := newSchemaRegistry(nil)
	reg.reflectSchema(TypeOf[twoURLs]())

	props := reg.schemas["twoURLs"]["properties"].(map[string]interface{})
	ours := asJSON(t, props["ours"])
	theirs := asJSON(t, props["theirs"])
	if ours == theirs {
		t.Fatalf("two different types share one component: %s", ours)
	}
	if ours != `{"$ref":"#/components/schemas/URL"}` || theirs != `{"$ref":"#/components/schemas/net.url.URL"}` {
		t.Fatalf("ours keeps its name, theirs is qualified by package: %s %s", ours, theirs)
	}
	if _, ok := reg.schemas["net.url.URL"]["properties"].(map[string]interface{})["Host"]; !ok {
		t.Fatalf("the other package's type is described from its own fields: %v", reg.schemas["net.url.URL"])
	}
	if len(reg.warnings) != 1 || !strings.Contains(reg.warnings[0], "share the name URL") {
		t.Fatalf("the clash is reported once: %v", reg.warnings)
	}
}

// Registering after a document was built makes the next one include it.
func TestRegisterAfterGenerate(t *testing.T) {
	g := New(Config{})
	g.Register(Endpoint{Method: "GET", Path: "/one", Responses: []EndpointResponse{{Status: 204}}})
	first := g.Generate()
	if len(first["paths"].(map[string]map[string]PathItem)) != 1 {
		t.Fatalf("one path first: %v", first["paths"])
	}
	g.Register(Endpoint{Method: "GET", Path: "/two", Responses: []EndpointResponse{{Status: 204}}})
	if paths := g.Generate()["paths"].(map[string]map[string]PathItem); len(paths) != 2 {
		t.Fatalf("both paths after the second registration: %v", paths)
	}
}

// Patterns that match nothing are still reported when registrations carry the document.
func TestRegistryWithPatternsThatMatchNothing(t *testing.T) {
	g := New(Config{ScanPatterns: []string{filepath.Join(t.TempDir(), "*.go")}})
	g.Register(Endpoint{Method: "GET", Path: "/one", Responses: []EndpointResponse{{Status: 204}}})
	spec := g.Generate()
	if _, failed := spec["error"]; failed {
		t.Fatalf("registered operations still make a document: %v", spec)
	}
	if len(g.Warnings) != 1 || !strings.HasPrefix(g.Warnings[0], "No files found for scan patterns") {
		t.Fatalf("the empty patterns are reported: %v", g.Warnings)
	}
}

// TypeOf names interface and pointer types as well as structs.
func TestTypeOf(t *testing.T) {
	if TypeOf[error]().Kind() != reflect.Interface {
		t.Fatalf("an interface type")
	}
	if TypeOf[*ParityItem]().Kind() != reflect.Pointer {
		t.Fatalf("a pointer type")
	}
}
