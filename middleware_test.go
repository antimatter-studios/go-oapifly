package oapifly

import (
	"path/filepath"
	"strings"
	"testing"
)

type middlewareError struct {
	Code string `json:"code"`
}

var (
	errorBody = TypeOf[middlewareError]()

	authenticated = Middleware{
		Security: []SecurityRequirement{{"SessionCookie": nil, "CSRFHeader": nil}, {"BearerAuth": nil}},
		Responses: []EndpointResponse{
			{Status: 401, Description: "No session", Type: errorBody},
			{Status: 503, Description: "The session check failed", Type: errorBody},
		},
	}
	rateLimited = Middleware{Responses: []EndpointResponse{{Status: 429, Type: errorBody, Headers: map[string]ResponseHeader{
		"Retry-After": {Description: "Seconds to wait", Required: true, Type: TypeOf[int]()},
	}}}}
)

func require(roles ...string) Middleware {
	return Middleware{Roles: roles, Responses: []EndpointResponse{{Status: 403, Description: "Missing capability", Type: errorBody}}}
}

// Each middleware describes itself once, and every operation it wraps answers what it can
// answer and requires what it enforces.
func TestMiddlewareDescribesWhatItAdds(t *testing.T) {
	spec, g := securityDocument(t, Config{Title: "t"},
		Endpoint{Method: "POST", Path: "/domains", Middleware: []Middleware{rateLimited, authenticated, require("domains:write")},
			Responses: []EndpointResponse{{Status: 201, Type: TypeOf[securityThing]()}}},
		Endpoint{Method: "GET", Path: "/domains", Middleware: []Middleware{authenticated, require("domains:read")},
			Responses: []EndpointResponse{
				{Status: 200, Type: TypeOf[securityThing]()},
				// The operation's own 403 is kept over the middleware's.
				{Status: 403, Description: "Not your domain", Type: errorBody},
			}},
	)
	if len(g.Warnings) > 0 {
		t.Fatalf("warnings: %v", g.Warnings)
	}

	post := operation(t, spec, "/domains", "post")
	for _, status := range []string{"201", "401", "403", "429", "503"} {
		if _, ok := post.Responses[status]; !ok {
			t.Errorf("POST /domains answers %s: %v", status, post.Responses)
		}
	}
	if got := asJSON(t, post.Responses["429"].Headers); got != `{"Retry-After":{"description":"Seconds to wait","required":true,"schema":{"type":"integer"}}}` {
		t.Errorf("a 429 says when to retry: %s", got)
	}
	if got := securityJSON(t, post); got != `[{"CSRFHeader":["domains:write"],"SessionCookie":["domains:write"]},{"BearerAuth":["domains:write"]}]` {
		t.Errorf("the capability check's role is on every scheme of every alternative: %s", got)
	}

	get := operation(t, spec, "/domains", "get")
	if get.Responses["403"].Description != "Not your domain" {
		t.Errorf("the operation's own response is kept: %+v", get.Responses["403"])
	}
	if got := securityJSON(t, get); got != `[{"CSRFHeader":["domains:read"],"SessionCookie":["domains:read"]},{"BearerAuth":["domains:read"]}]` {
		t.Errorf("GET security: %s", got)
	}
}

// An operation's own security wins over its middleware's, a capability check without an
// authenticating middleware adds its roles to the document's default, and a role on an
// operation that needs no credential is reported.
func TestMiddlewareSecurityPrecedence(t *testing.T) {
	spec, g := securityDocument(t, Config{Title: "t", Security: []SecurityRequirement{{"BearerAuth": nil}}},
		Endpoint{Method: "GET", Path: "/own", Security: []SecurityRequirement{{"AppPassword": nil}},
			Middleware: []Middleware{authenticated, require("mail:read")}},
		Endpoint{Method: "GET", Path: "/default", Middleware: []Middleware{require("audit:read")}},
		Endpoint{Method: "GET", Path: "/plain", Middleware: []Middleware{rateLimited}},
		Endpoint{Method: "GET", Path: "/public", Security: Public, Middleware: []Middleware{require("x:y")}},
	)
	for path, want := range map[string]string{
		"/own":     `[{"AppPassword":["mail:read"]}]`,
		"/default": `[{"BearerAuth":["audit:read"]}]`,
		"/plain":   "inherited",
		"/public":  `[]`,
	} {
		if got := securityJSON(t, operation(t, spec, path, "get")); got != want {
			t.Errorf("%s: %s, want %s", path, got, want)
		}
	}
	if !strings.Contains(strings.Join(g.Warnings, "\n"), "GET /public: its middleware requires x:y") {
		t.Errorf("a role with no credential to hold it is reported: %v", g.Warnings)
	}
}

// A middleware's header joins an operation's own response for the same status.
func TestMiddlewareHeaderJoinsOwnResponse(t *testing.T) {
	spec, _ := securityDocument(t, Config{Title: "t"},
		Endpoint{Method: "GET", Path: "/a", Middleware: []Middleware{rateLimited},
			Responses: []EndpointResponse{{Status: 429, Description: "Slow down", Type: errorBody}}},
	)
	resp := operation(t, spec, "/a", "get").Responses["429"]
	if resp.Description != "Slow down" || resp.Headers["Retry-After"].Schema == nil {
		t.Fatalf("the operation's 429 with the middleware's header: %+v", resp)
	}
}

// swaggo's @Header puts a header on the responses it names.
func TestAnnotatedHeaders(t *testing.T) {
	dir := t.TempDir()
	writeTestFile(t, dir, "handlers.go", `package handlers

type Controller struct{}

// @Success 201 {object} Thing "Created"
// @Failure 429 {object} Thing "Slow down"
// @Header 201 {string} Location "Where the new thing is"
// @Header 429 {integer} Retry-After "Seconds to wait"
// @Header all {string} X-Request-ID "The request's ID"
// @Header 404 {string} X-Nowhere "No such response"
// @Router /things [POST]
func (c *Controller) Create() {}
`)
	g := New(Config{Title: "t", ScanPatterns: []string{filepath.Join(dir, "*.go")}})
	spec := g.Generate()
	item := operation(t, spec, "/things", "post")
	if got := asJSON(t, item.Responses["201"].Headers); got != `{"Location":{"description":"Where the new thing is","schema":{"type":"string"}},"X-Request-ID":{"description":"The request's ID","schema":{"type":"string"}}}` {
		t.Errorf("201 headers: %s", got)
	}
	if got := asJSON(t, item.Responses["429"].Headers["Retry-After"]); got != `{"description":"Seconds to wait","schema":{"type":"integer"}}` {
		t.Errorf("429 Retry-After: %s", got)
	}
	if !strings.Contains(strings.Join(g.Warnings, "\n"), "@Header X-Nowhere names response 404") {
		t.Errorf("a header on a response the operation does not describe is reported: %v", g.Warnings)
	}
}
