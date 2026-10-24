package oapifly

import (
	"strings"
	"testing"
)

type optionalXML struct {
	Address string  `query:"emailaddress"`
	Body    *string `body:"application/xml"`
}

type optionalBytes struct {
	Body *[]byte `body:"application/octet-stream"`
}

type optionalNumber struct {
	Body *int `body:"text/plain"`
}

// A body that is not JSON is optional when its field is a pointer: described with required
// false and as what it is when it is there, never as JSON null, and bound as nil when the
// request has none (#50).
func TestOptionalBodyThatIsNotJSON(t *testing.T) {
	g := New(Config{Title: "t"})
	g.Register(Endpoint{Method: "POST", Path: "/autodiscover", Request: TypeOf[optionalXML](),
		Responses: []EndpointResponse{{Status: 200, Type: TypeOf[string](), MediaType: "application/xml"}}})
	spec := g.Generate()
	if len(g.Warnings) > 0 {
		t.Fatalf("warnings: %v", g.Warnings)
	}
	body := operation(t, spec, "/autodiscover", "post").RequestBody
	if body == nil || body.Required {
		t.Fatalf("the body is %+v, want an optional one", body)
	}
	if got := asJSON(t, body.Content); got != `{"application/xml":{"schema":{"type":"string"}}}` {
		t.Errorf("an optional XML body is described as %s, want a string", got)
	}

	var none optionalXML
	if err := Bind(&none, params(map[string]string{"query:emailaddress": "eve@example.com"}), nil); err != nil || none.Body != nil || none.Address != "eve@example.com" {
		t.Fatalf("no body: %v %+v, want nil", err, none)
	}
	var some optionalXML
	if err := Bind(&some, noParams, []byte("<Autodiscover/>")); err != nil || some.Body == nil || *some.Body != "<Autodiscover/>" {
		t.Fatalf("a body: %v %+v", err, some)
	}
	var raw optionalBytes
	if err := Bind(&raw, noParams, []byte{0x1f, 0x8b}); err != nil || raw.Body == nil || string(*raw.Body) != "\x1f\x8b" {
		t.Fatalf("a []byte body: %v %+v", err, raw)
	}
	var empty optionalBytes
	if err := Bind(&empty, noParams, []byte{}); err != nil || empty.Body != nil {
		t.Fatalf("an empty []byte body: %v %+v, want nil", err, empty)
	}
	var number optionalNumber
	if err := Bind(&number, noParams, []byte("5")); err == nil || !strings.Contains(err.Error(), "string or []byte field, or a pointer to one") {
		t.Fatalf("a *int text body: %v", err)
	}
}
