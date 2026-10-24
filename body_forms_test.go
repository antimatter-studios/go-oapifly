package oapifly

import (
	"errors"
	"strings"
	"testing"
)

type formsReport struct {
	Org string `json:"organization-name" minLength:"1"`
}

// formsRequest takes a TLS-RPT report as JSON, under either JSON media type, or gzipped.
type formsRequest struct {
	Report *formsReport `body:"application/tlsrpt+json,application/json"`
	Gzip   *[]byte      `body:"application/tlsrpt+gzip"`
}

// formsOverlap gives application/json twice: the second field is no new form.
type formsOverlap struct {
	Report *formsReport `body:"application/json"`
	Again  *formsReport `body:"application/json"`
}

// formsOneField takes one body under two JSON media types.
type formsOneField struct {
	Report formsReport `body:"application/tlsrpt+json,application/json"`
}

// A body taken in several forms is described in each media type, required in one of them,
// each form as what it is when it is there (#55).
func TestABodyInSeveralFormsIsDescribedInEach(t *testing.T) {
	g := New(Config{})
	g.Register(
		Endpoint{Method: "POST", Path: "/reports", OperationID: "receiveReport", Request: TypeOf[formsRequest]()},
		Endpoint{Method: "POST", Path: "/one", OperationID: "one", Request: TypeOf[formsOneField]()},
	)
	spec := g.Generate()
	body := operation(t, spec, "/reports", "post").RequestBody
	if body == nil || !body.Required {
		t.Fatalf("the body is not required: %+v", body)
	}
	want := `{"application/json":{"schema":{"$ref":"#/components/schemas/formsReport"}},` +
		`"application/tlsrpt+gzip":{"schema":{"format":"byte","type":"string"}},` +
		`"application/tlsrpt+json":{"schema":{"$ref":"#/components/schemas/formsReport"}}}`
	if got := asJSON(t, body.Content); got != want {
		t.Errorf("content %s,\nwant %s", got, want)
	}
	one := operation(t, spec, "/one", "post").RequestBody
	if got := asJSON(t, one.Content); !one.Required || got != `{"application/json":{"schema":{"$ref":"#/components/schemas/formsReport"}},"application/tlsrpt+json":{"schema":{"$ref":"#/components/schemas/formsReport"}}}` {
		t.Errorf("one field in two media types: required %v, %s", one.Required, got)
	}
	if len(g.Warnings) != 0 {
		t.Errorf("warnings: %q", g.Warnings)
	}

	g = New(Config{})
	g.Register(Endpoint{Method: "POST", Path: "/overlap", OperationID: "overlap", Request: TypeOf[formsOverlap]()})
	g.Generate()
	if len(g.Warnings) != 1 || !strings.Contains(g.Warnings[0], "more than one body field, Again is ignored") {
		t.Errorf("a second field in a media type already taken: %q", g.Warnings)
	}
}

// contentType is a parameter lookup with only a Content-Type header, absent when ct is "".
func contentType(ct string) func(in, name string) (string, bool) {
	return func(in, name string) (string, bool) {
		if in == "header" && strings.EqualFold(name, "Content-Type") && ct != "" {
			return ct, true
		}
		return "", false
	}
}

// Binding sets the form the Content-Type names and leaves the others nil; with no
// Content-Type it is the first form. Another media type, or no body, is refused (#55).
func TestABodyInSeveralFormsIsBoundByItsContentType(t *testing.T) {
	report := []byte(`{"organization-name":"Reporter"}`)
	for _, ct := range []string{"application/tlsrpt+json", "Application/JSON; charset=utf-8", ""} {
		var in formsRequest
		if err := Bind(&in, contentType(ct), report); err != nil || in.Report == nil || in.Report.Org != "Reporter" || in.Gzip != nil {
			t.Errorf("%q: %v, %+v", ct, err, in)
		}
	}

	var in formsRequest
	gz := []byte{0x1f, 0x8b, 0x08, 0x00}
	if err := Bind(&in, contentType("application/tlsrpt+gzip"), gz); err != nil || in.Gzip == nil || string(*in.Gzip) != string(gz) || in.Report != nil {
		t.Errorf("gzip: %v, %+v", err, in)
	}

	err := Bind(&formsRequest{}, contentType("text/plain"), report)
	if !errors.Is(err, ErrUnsupportedMediaType) || !strings.Contains(err.Error(), "application/tlsrpt+json, application/json, application/tlsrpt+gzip") {
		t.Errorf("text/plain: %v", err)
	}
	if err := Bind(&formsRequest{}, contentType("application/tlsrpt+json"), nil); !errors.Is(err, ErrMalformedBody) {
		t.Errorf("no body: %v", err)
	}
	var violations Violations
	if err := Bind(&formsRequest{}, contentType("application/json"), []byte(`{"organization-name":""}`)); !errors.As(err, &violations) || violations["organization-name"] == "" {
		t.Errorf("a JSON form is checked as any JSON body is: %v", err)
	}

	// One form takes its body whatever the Content-Type, as before.
	var one formsOneField
	if err := Bind(&one, contentType("text/plain"), report); err != nil || one.Report.Org != "Reporter" {
		t.Errorf("one form: %v, %+v", err, one)
	}
}
