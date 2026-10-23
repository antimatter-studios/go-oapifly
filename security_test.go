package oapifly

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

var securitySchemes = map[string]SecurityScheme{
	"SessionCookie": {Type: "apiKey", In: "cookie", Name: "restmail_session"},
	"CSRFHeader":    {Type: "apiKey", In: "header", Name: "X-CSRF-Token"},
	"BearerAuth":    {Type: "http", Scheme: "bearer"},
	"AppPassword":   {Type: "http", Scheme: "basic"},
	"GatewayCert":   {Type: "mutualTLS", Description: "The gateway's client certificate."},
}

type securityThing struct {
	ID int `json:"id"`
}

func securityDocument(t *testing.T, config Config, endpoints ...Endpoint) (map[string]interface{}, *Generator) {
	t.Helper()
	if config.SecuritySchemes == nil {
		config.SecuritySchemes = securitySchemes
	}
	g := New(config)
	g.Register(endpoints...)
	return g.Generate(), g
}

func endpointAt(method, path string, security []SecurityRequirement) Endpoint {
	return Endpoint{Method: method, Path: path, Security: security,
		Responses: []EndpointResponse{{Status: 200, Type: TypeOf[securityThing]()}}}
}

func securityJSON(t *testing.T, item PathItem) string {
	t.Helper()
	if item.Security == nil {
		return "inherited"
	}
	return asJSON(t, *item.Security)
}

func TestOperationSecurity(t *testing.T) {
	spec, g := securityDocument(t, Config{Title: "t"},
		endpointAt("GET", "/health", Public),
		endpointAt("GET", "/me", []SecurityRequirement{{"BearerAuth": nil}}),
		endpointAt("GET", "/domains", []SecurityRequirement{
			{"SessionCookie": nil, "CSRFHeader": nil},
			{"BearerAuth": {"domains:read"}},
		}),
		endpointAt("POST", "/domains", []SecurityRequirement{{"BearerAuth": {"domains:write", "domains:read"}}}),
		endpointAt("GET", "/inherits", nil),
	)
	if len(g.Warnings) > 0 {
		t.Fatalf("every scheme is defined: %v", g.Warnings)
	}
	cases := []struct{ path, method, want string }{
		// Public: an empty list, which overrides any document default.
		{"/health", "get", `[]`},
		// One scheme, no roles: an empty role list, never null.
		{"/me", "get", `[{"BearerAuth":[]}]`},
		// Alternatives, one of them two schemes together, the other with a role.
		{"/domains", "get", `[{"CSRFHeader":[],"SessionCookie":[]},{"BearerAuth":["domains:read"]}]`},
		{"/domains", "post", `[{"BearerAuth":["domains:write","domains:read"]}]`},
		// Nothing stated: no key, so the document's default applies.
		{"/inherits", "get", "inherited"},
	}
	for _, c := range cases {
		if got := securityJSON(t, operation(t, spec, c.path, c.method)); got != c.want {
			t.Errorf("%s %s: %s, want %s", c.method, c.path, got, c.want)
		}
	}

	components := spec["components"].(map[string]interface{})
	schemes, err := json.Marshal(components["securitySchemes"])
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"SessionCookie":{"type":"apiKey","name":"restmail_session","in":"cookie"}`,
		`"BearerAuth":{"type":"http","scheme":"bearer"}`,
		`"AppPassword":{"type":"http","scheme":"basic"}`,
		`"GatewayCert":{"type":"mutualTLS","description":"The gateway's client certificate."}`,
	} {
		if !strings.Contains(string(schemes), want) {
			t.Errorf("securitySchemes lacks %s: %s", want, schemes)
		}
	}
	if _, ok := spec["security"]; ok {
		t.Errorf("no document default was configured: %v", spec["security"])
	}
}

// The document's default applies to every operation that states nothing, and an operation's
// own requirement, or Public, overrides it.
func TestDocumentSecurityDefault(t *testing.T) {
	spec, _ := securityDocument(t, Config{Title: "t", Security: []SecurityRequirement{{"BearerAuth": nil}, {"SessionCookie": nil}}},
		endpointAt("GET", "/default", nil),
		endpointAt("GET", "/override", []SecurityRequirement{{"AppPassword": {"mail:send"}}}),
		endpointAt("GET", "/public", Public),
	)
	if got := asJSON(t, spec["security"]); got != `[{"BearerAuth":[]},{"SessionCookie":[]}]` {
		t.Fatalf("document security: %s", got)
	}
	for path, want := range map[string]string{
		"/default":  "inherited",
		"/override": `[{"AppPassword":["mail:send"]}]`,
		"/public":   `[]`,
	} {
		if got := securityJSON(t, operation(t, spec, path, "get")); got != want {
			t.Errorf("%s: %s, want %s", path, got, want)
		}
	}
}

// A requirement naming a scheme the document does not define is reported, wherever it is.
func TestUndefinedSecuritySchemeIsReported(t *testing.T) {
	_, g := securityDocument(t, Config{Title: "t", Security: []SecurityRequirement{{"Missing": nil}}},
		endpointAt("GET", "/a", []SecurityRequirement{{"BearerAuth": nil, "Nowhere": {"x"}}}),
	)
	joined := strings.Join(g.Warnings, "\n")
	for _, want := range []string{
		`Config.Security: security scheme "Missing" is not defined`,
		`GET /a: security scheme "Nowhere" is not defined`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing warning %q in %v", want, g.Warnings)
		}
	}
	if strings.Contains(joined, `"BearerAuth"`) {
		t.Errorf("a defined scheme is not reported: %v", g.Warnings)
	}
}

// swaggo's @Security: each line, and each || within one, is an alternative; && joins schemes
// that are all needed; roles go in brackets.
func TestAnnotatedSecurity(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "handlers.go", `package handlers

type Controller struct{}

// @Security BearerAuth[domains:read, domains:write]
// @Router /domains [GET]
func (c *Controller) List() {}

// @Security SessionCookie && CSRFHeader || BearerAuth
// @Security AppPassword
// @Router /send [POST]
func (c *Controller) Send() {}

// @Router /open [GET]
func (c *Controller) Open() {}

// @Security BearerAuth[unclosed
// @Router /broken [GET]
func (c *Controller) Broken() {}
`)
	g := New(Config{Title: "t", ScanPatterns: []string{filepath.Join(dir, "*.go")}, SecuritySchemes: securitySchemes})
	spec := g.Generate()
	for _, c := range []struct{ path, method, want string }{
		{"/domains", "get", `[{"BearerAuth":["domains:read","domains:write"]}]`},
		{"/send", "post", `[{"CSRFHeader":[],"SessionCookie":[]},{"BearerAuth":[]},{"AppPassword":[]}]`},
		{"/open", "get", "inherited"},
		{"/broken", "get", "inherited"},
	} {
		if got := securityJSON(t, operation(t, spec, c.path, c.method)); got != c.want {
			t.Errorf("%s %s: %s, want %s", c.method, c.path, got, c.want)
		}
	}
	if !strings.Contains(strings.Join(g.Warnings, "\n"), "does not close its roles") {
		t.Errorf("a malformed @Security line is reported: %v", g.Warnings)
	}
}

// A registration's security is the one used for its route; an annotation fills in only
// when the registration states none.
func TestRegisteredSecurityWinsOverAnnotation(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "handlers.go", `package handlers

type Controller struct{}

// @Security AppPassword
// @Router /a [GET]
func (c *Controller) A() {}

// @Security AppPassword
// @Router /b [GET]
func (c *Controller) B() {}
`)
	g := New(Config{Title: "t", ScanPatterns: []string{filepath.Join(dir, "*.go")}, SecuritySchemes: securitySchemes})
	g.Register(endpointAt("GET", "/a", []SecurityRequirement{{"BearerAuth": {"x:read"}}}), endpointAt("GET", "/b", nil))
	spec := g.Generate()
	if got := securityJSON(t, operation(t, spec, "/a", "get")); got != `[{"BearerAuth":["x:read"]}]` {
		t.Errorf("the registration's security is used: %s", got)
	}
	if got := securityJSON(t, operation(t, spec, "/b", "get")); got != `[{"AppPassword":[]}]` {
		t.Errorf("the annotation fills a registration with none: %s", got)
	}
}
