package oapifly

import (
	"strings"
	"testing"
)

type mediaAutoconfig struct {
	Provider string `json:"provider"`
}

type mediaSession struct {
	Account string `json:"account"`
}

type mediaID struct {
	ID uint32 `path:"id"`
}

type mediaUpload struct {
	Name string `path:"name"`
	Body string `body:"application/xml"`
}

// Responses carry their own media types and headers, and one status may be answered in more
// than one media type (#18). Every document here also validates as OpenAPI 3.1 (TestMain).
func TestResponsesInTheirOwnMediaTypes(t *testing.T) {
	text, bytes := TypeOf[string](), TypeOf[[]byte]()
	g := New(Config{Title: "t"})
	g.Register(
		Endpoint{Method: "POST", Path: "/login", Responses: []EndpointResponse{{
			Status: 200, Type: TypeOf[mediaSession](),
			Headers: map[string]ResponseHeader{"Set-Cookie": {Description: "The session, refresh and CSRF cookies", Required: true}},
		}, {
			Status: 429, Type: TypeOf[mediaSession](),
			Headers: map[string]ResponseHeader{"Retry-After": {Type: TypeOf[int](), Required: true}},
		}}},
		Endpoint{Method: "GET", Path: "/mail/config-v1.1.xml", Responses: []EndpointResponse{
			{Status: 200, Type: TypeOf[mediaAutoconfig](), MediaType: "application/xml"},
		}},
		Endpoint{Method: "GET", Path: "/.well-known/mta-sts.txt", Responses: []EndpointResponse{
			{Status: 200, Type: text, MediaType: "text/plain"},
		}},
		Endpoint{Method: "GET", Path: "/messages/{id}/raw", Request: TypeOf[mediaID](), Responses: []EndpointResponse{
			{Status: 200, MediaType: "message/rfc822"},
		}},
		Endpoint{Method: "GET", Path: "/events", Responses: []EndpointResponse{
			{Status: 200, Type: text, MediaType: "text/event-stream"},
		}},
		Endpoint{Method: "GET", Path: "/attachments/{id}", Request: TypeOf[mediaID](), Responses: []EndpointResponse{
			{Status: 200, Type: bytes, MediaType: "application/octet-stream", Headers: map[string]ResponseHeader{
				"Content-Disposition": {Required: true}, "Content-Length": {Type: TypeOf[int64](), Required: true}, "ETag": {},
			}},
			// The same status, in the attachment's stored type as well.
			{Status: 200, MediaType: "image/png"},
		}},
		Endpoint{Method: "GET", Path: "/api/docs", Responses: []EndpointResponse{
			{Status: 200, Type: text, MediaType: "text/html"},
		}},
		Endpoint{Method: "GET", Path: "/basic", Responses: []EndpointResponse{
			{Status: 401, Type: TypeOf[mediaSession](), Headers: map[string]ResponseHeader{"WWW-Authenticate": {Required: true}}},
		}},
		Endpoint{Method: "POST", Path: "/things", Responses: []EndpointResponse{
			{Status: 201, Type: TypeOf[mediaSession](), Headers: map[string]ResponseHeader{"Location": {Required: true}}},
		}},
		Endpoint{Method: "PUT", Path: "/uploads/{name}", Request: TypeOf[mediaUpload](), Responses: []EndpointResponse{{Status: 204}}},
	)
	spec := g.Generate()
	if len(g.Warnings) > 0 {
		t.Fatalf("warnings: %v", g.Warnings)
	}

	content := func(path, method, status string) string {
		t.Helper()
		return asJSON(t, operation(t, spec, path, method).Responses[status].Content)
	}
	headers := func(path, method, status string) string {
		t.Helper()
		return asJSON(t, operation(t, spec, path, method).Responses[status].Headers)
	}
	for _, c := range []struct{ name, got, want string }{
		{"Set-Cookie", headers("/login", "post", "200"), `{"Set-Cookie":{"description":"The session, refresh and CSRF cookies","required":true,"schema":{"type":"string"}}}`},
		{"Retry-After", headers("/login", "post", "429"), `{"Retry-After":{"required":true,"schema":{"type":"integer"}}}`},
		{"XML", content("/mail/config-v1.1.xml", "get", "200"), `{"application/xml":{"schema":{"$ref":"#/components/schemas/mediaAutoconfig"}}}`},
		{"text/plain", content("/.well-known/mta-sts.txt", "get", "200"), `{"text/plain":{"schema":{"type":"string"}}}`},
		{"message/rfc822", content("/messages/{id}/raw", "get", "200"), `{"message/rfc822":{}}`},
		{"event stream", content("/events", "get", "200"), `{"text/event-stream":{"schema":{"type":"string"}}}`},
		{"a download in two types", content("/attachments/{id}", "get", "200"), `{"application/octet-stream":{},"image/png":{}}`},
		{"download headers", headers("/attachments/{id}", "get", "200"), `{"Content-Disposition":{"required":true,"schema":{"type":"string"}},"Content-Length":{"required":true,"schema":{"format":"int64","type":"integer"}},"ETag":{"schema":{"type":"string"}}}`},
		{"HTML", content("/api/docs", "get", "200"), `{"text/html":{"schema":{"type":"string"}}}`},
		{"WWW-Authenticate", headers("/basic", "get", "401"), `{"WWW-Authenticate":{"required":true,"schema":{"type":"string"}}}`},
		{"Location", headers("/things", "post", "201"), `{"Location":{"required":true,"schema":{"type":"string"}}}`},
		{"no body", content("/uploads/{name}", "put", "204"), `null`},
	} {
		if c.got != c.want {
			t.Errorf("%s: %s, want %s", c.name, c.got, c.want)
		}
	}

	upload := operation(t, spec, "/uploads/{name}", "put").RequestBody
	if got := asJSON(t, upload.Content); got != `{"application/xml":{"schema":{"type":"string"}}}` {
		t.Errorf("a request body in its own media type: %s", got)
	}
}

// The same status registered twice in the same media type is still a mistake, reported.
func TestSameStatusAndMediaTypeTwiceIsReported(t *testing.T) {
	g := New(Config{Title: "t"})
	g.Register(Endpoint{Method: "GET", Path: "/a", Responses: []EndpointResponse{
		{Status: 200, Type: TypeOf[string](), MediaType: "text/plain"},
		{Status: 200, Type: TypeOf[string](), MediaType: "text/plain"},
	}})
	g.Generate()
	if !strings.Contains(strings.Join(g.Warnings, "\n"), "response 200 is registered twice") {
		t.Fatalf("warnings: %v", g.Warnings)
	}
}
