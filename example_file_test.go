package oapifly

import (
	"strings"
	"testing"
	"testing/fstest"
)

type exThingIn struct {
	Name string `json:"name" minLength:"1"`
}

type exThing struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type exError struct {
	Code string `json:"code"`
}

type exThingPath struct {
	ID int64 `path:"id"`
}

// exampleGenerator registers createThing and getThing, with the example file text beside
// them as openapi-example.json.
func exampleGenerator(file string, endpoints ...Endpoint) *Generator {
	g := New(Config{Examples: fstest.MapFS{"openapi-example.json": {Data: []byte(file)}}, ExamplesFile: "openapi-example.json"})
	g.Register(
		Endpoint{Method: "POST", Path: "/things", OperationID: "createThing", Request: TypeOf[exThingIn](),
			Responses: []EndpointResponse{{Status: 201, Type: TypeOf[exThing]()}, {Status: 422, Type: TypeOf[exError]()}}},
		Endpoint{Method: "GET", Path: "/things/{id}", OperationID: "getThing", Request: TypeOf[exThingPath](),
			Responses: []EndpointResponse{{Status: 200, Type: TypeOf[exThing]()}, {Status: 404, Type: TypeOf[exError]()}}},
	)
	g.Register(endpoints...)
	return g
}

const thingExamples = `{
  "createThing": {
    "created": {
      "summary": "A new thing",
      "request": {"body": {"name": "box"}},
      "response": {"status": 201, "body": {"id": 7, "name": "box"}}
    },
    "missingName": {
      "summary": "A thing with no name",
      "request": {"body": {"name": ""}, "invalid": true},
      "response": {"status": 422, "body": {"code": "validation_failed"}}
    }
  },
  "getThing": {
    "unknownId": {
      "request": {"path": {"id": 999999}},
      "response": {"status": 404, "body": {"code": "not_found"}}
    }
  }
}`

// The file's pairs go into the document under their names: each request body into its
// media type's examples, each parameter value into the parameter's, each response body into
// its response's. A documented status no pair shows is reported (#20).
func TestExamplePairsAreMergedIntoTheDocument(t *testing.T) {
	g := exampleGenerator(thingExamples)
	spec := g.Generate()

	create := operation(t, spec, "/things", "post")
	if got := asJSON(t, create.RequestBody.Content["application/json"].(map[string]interface{})["examples"]); got !=
		`{"created":{"summary":"A new thing","value":{"name":"box"}},"missingName":{"summary":"A thing with no name","value":{"name":""}}}` {
		t.Errorf("request examples %s", got)
	}
	if got := asJSON(t, create.Responses["201"].Content["application/json"].(map[string]interface{})["examples"]); got !=
		`{"created":{"summary":"A new thing","value":{"id":7,"name":"box"}}}` {
		t.Errorf("201 examples %s", got)
	}
	if got := asJSON(t, create.Responses["422"].Content["application/json"].(map[string]interface{})["examples"]); got !=
		`{"missingName":{"summary":"A thing with no name","value":{"code":"validation_failed"}}}` {
		t.Errorf("422 examples %s", got)
	}
	get := operation(t, spec, "/things/{id}", "get")
	if got := asJSON(t, get.Parameters[0].Examples); got != `{"unknownId":{"value":999999}}` {
		t.Errorf("parameter examples %s", got)
	}

	var kinds []string
	for _, w := range g.Report() {
		kinds = append(kinds, string(w.Kind)+"@"+w.OperationID+":"+w.Status)
	}
	if got := strings.Join(kinds, " "); got != "example-missing@getThing:200" {
		t.Errorf("warnings %s (%q)", got, g.Warnings)
	}
}

// A pair that names what the document does not have is reported and left out, and nothing
// else changes; a shared response is not changed for the operations that share it.
func TestAnExampleThatDoesNotFitIsReported(t *testing.T) {
	shared := Middleware{Responses: []EndpointResponse{{Status: 401, Type: TypeOf[exError]()}}}
	g := exampleGenerator(`{
  "nowhere": {"a": {"request": {}, "response": {"status": 200}}},
  "getThing": {
    "badParam": {"request": {"query": {"page": 2}}, "response": {"status": 404}},
    "badBody": {"request": {"body": {"x": 1}}, "response": {"status": 404}},
    "badStatus": {"request": {}, "response": {"status": 418}},
    "badMedia": {"request": {}, "response": {"status": 200, "mediaType": "text/plain"}}
  },
  "listThings": {"unauthorized": {"request": {}, "response": {"status": 401, "body": {"code": "unauthorized"}}}},
  "deleteThing": {"noBody": {"request": {}, "response": {"status": 204, "body": {"gone": true}}}}
}`,
		Endpoint{Method: "DELETE", Path: "/things/{id}", OperationID: "deleteThing", Request: TypeOf[exThingPath](),
			Responses: []EndpointResponse{{Status: 204}}, Middleware: []Middleware{shared}},
		Endpoint{Method: "PUT", Path: "/things/{id}", OperationID: "putThing", Request: TypeOf[exThingPath](),
			Responses: []EndpointResponse{{Status: 204}}, Middleware: []Middleware{shared}},
		Endpoint{Method: "GET", Path: "/things", OperationID: "listThings",
			Responses: []EndpointResponse{{Status: 200, Type: TypeOf[[]exThing]()}}, Middleware: []Middleware{shared}},
	)
	spec := g.Generate()

	want := map[string]string{
		"a":         "",
		"badParam":  "parameter page",
		"badBody":   "",
		"badStatus": "status 418",
		"badMedia":  "status 200",
		"noBody":    "status 204",
	}
	got := map[string]string{}
	for _, w := range g.Report() {
		if w.Kind != WarnExampleOrphaned {
			continue
		}
		subject := ""
		switch {
		case w.Parameter != "":
			subject = "parameter " + w.Parameter
		case w.Status != "":
			subject = "status " + w.Status
		}
		got[w.Example] = subject
	}
	for name, subject := range want {
		if s, ok := got[name]; !ok || s != subject {
			t.Errorf("%s: reported %v (%q), want %q", name, ok, s, subject)
		}
	}
	if _, ok := got["unauthorized"]; ok {
		t.Errorf("the 401 example was reported: %q", g.Warnings)
	}
	list := operation(t, spec, "/things", "get")
	if _, ok := list.Responses["401"].Content["application/json"].(map[string]interface{})["examples"].(map[string]interface{})["unauthorized"]; !ok {
		t.Errorf("listThings' 401 lacks its example: %s", asJSON(t, list.Responses["401"]))
	}

	del := operation(t, spec, "/things/{id}", "delete")
	put := operation(t, spec, "/things/{id}", "put")
	if _, ok := del.Responses["401"].Content["application/json"].(map[string]interface{})["examples"]; ok {
		t.Errorf("deleteThing's 401 took listThings' example")
	}
	if _, ok := put.Responses["401"].Content["application/json"].(map[string]interface{})["examples"]; ok {
		t.Errorf("putThing's 401 took listThings' example")
	}
}

// A body that does not validate against its schema is reported, and so is a request marked
// invalid whose body does validate (#20).
func TestAnExampleBodyIsValidated(t *testing.T) {
	g := exampleGenerator(`{
  "createThing": {
    "wrongType": {"request": {"body": {"name": 5}}, "response": {"status": 201, "body": {"id": 1, "name": "x"}}},
    "notReallyInvalid": {"request": {"body": {"name": "fine"}, "invalid": true}, "response": {"status": 422, "body": {"code": "x"}}},
    "badAnswer": {"request": {"body": {"name": "a"}}, "response": {"status": 201, "body": {"id": "seven", "name": "a"}}}
  }
}`)
	got := map[string]string{}
	for _, w := range g.Report() {
		if w.Kind == WarnExampleInvalid {
			got[w.Example] = w.Status + " " + w.Message
		}
	}
	if len(got) != 3 || !strings.Contains(got["wrongType"], "request body does not validate") ||
		!strings.Contains(got["notReallyInvalid"], "marked invalid, and its body validates") ||
		!strings.HasPrefix(got["badAnswer"], "201 ") || !strings.Contains(got["badAnswer"], "201 response body does not validate") {
		t.Errorf("example-invalid: %v", got)
	}
}

// A missing or broken example file is reported, and the document is generated without it.
func TestAnUnreadableExampleFileIsReported(t *testing.T) {
	for name, fsys := range map[string]fstest.MapFS{
		"missing":       {},
		"not JSON":      {"openapi-example.json": {Data: []byte("{")}},
		"unknown field": {"openapi-example.json": {Data: []byte(`{"getThing": {"a": {"request": {}, "response": {"status": 200}, "extra": 1}}}`)}},
	} {
		g := New(Config{Examples: fsys, ExamplesFile: "openapi-example.json"})
		g.Register(Endpoint{Method: "GET", Path: "/ok", OperationID: "getOK"})
		report := g.Report()
		if len(report) != 1 || report[0].Kind != WarnExamplesUnreadable || report[0].File != "openapi-example.json" {
			t.Errorf("%s: %+v", name, report)
		}
	}
}

// Sync adds a stub for every documented status no pair shows, keeps every pair it has, and
// is written the same way each time (#20).
func TestExampleStubsFillTheGaps(t *testing.T) {
	g := exampleGenerator(`{"getThing": {"found": {"request": {"path": {"id": 1}}, "response": {"status": 200}}, "status404": {"request": {}, "response": {"status": 500}}}}`)
	file, err := g.ExampleStubs()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := file["getThing"]["found"]; !ok {
		t.Error("an existing pair was dropped")
	}
	stub, ok := file["getThing"]["status404_"]
	if !ok || stub.Response.Status != 404 || !strings.HasPrefix(stub.Summary, "TODO") {
		t.Errorf("the 404 stub: %v %+v (file %v)", ok, stub, file["getThing"])
	}
	for _, status := range []int{201, 422} {
		found := false
		for _, pair := range file["createThing"] {
			found = found || pair.Response.Status == status
		}
		if !found {
			t.Errorf("no createThing stub for %d", status)
		}
	}
	a, err := MarshalExamples(file)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := MarshalExamples(file)
	if string(a) != string(b) || !strings.HasSuffix(string(a), "}\n") || !strings.Contains(string(a), "\n  \"createThing\": {") {
		t.Errorf("not written stably:\n%s", a)
	}
}

// openapi.json's examples are in openapi-example.json.
func TestExamplesFileFor(t *testing.T) {
	for doc, want := range map[string]string{
		"openapi.json":     "openapi-example.json",
		"api/openapi.yaml": "api/openapi-example.yaml",
		"description":      "description-example.json",
		"v1.2/openapi":     "v1.2/openapi-example.json",
	} {
		if got := ExamplesFileFor(doc); got != want {
			t.Errorf("%s: %s, want %s", doc, got, want)
		}
	}
}

type exThingFilter struct {
	Kind *string `query:"kind" enums:"alpha,beta"`
}

// A pair whose parameter value is outside that parameter's enum stays out of the document:
// Dredd-style testers refuse a description whose parameter example is not an enum value.
// Its status still counts as shown, so no example is reported missing.
func TestAPairOutsideAParameterEnumIsLeftOut(t *testing.T) {
	g := exampleGenerator(`{
  "listThings": {
    "alpha": {"request": {"query": {"kind": "alpha"}}, "response": {"status": 200, "body": []}},
    "unknownKind": {"request": {"query": {"kind": "gamma"}, "invalid": true}, "response": {"status": 422, "body": {"code": "validation_failed"}}}
  }
}`,
		Endpoint{Method: "GET", Path: "/things", OperationID: "listThings", Request: TypeOf[exThingFilter](),
			Responses: []EndpointResponse{{Status: 200, Type: TypeOf[[]exThing]()}, {Status: 422, Type: TypeOf[exError]()}}},
	)
	spec := g.Generate()

	list := operation(t, spec, "/things", "get")
	if got := asJSON(t, list.Parameters[parameterIndex(list.Parameters, "query", "kind")].Examples); got != `{"alpha":{"value":"alpha"}}` {
		t.Errorf("kind examples %s", got)
	}
	if examples, ok := list.Responses["422"].Content["application/json"].(map[string]interface{})["examples"]; ok {
		t.Errorf("the 422 response took the left-out pair: %s", asJSON(t, examples))
	}
	for _, w := range g.Report() {
		if w.OperationID == "listThings" {
			t.Errorf("unexpected warning: %+v", w)
		}
	}
}

// A pair's requires reaches each of its Example Objects as x-requires, and a kind or value
// the vocabulary does not list is reported (#62).
func TestAPairsRequiresReachesTheDocumentAndIsChecked(t *testing.T) {
	file := `{
  "getThing": {
    "found": {"request": {"path": {"id": 1}}, "response": {"status": 200, "body": {"id": 1, "name": "x"}}},
    "storageDown": {"requires": {"fault": "database"}, "request": {"path": {"id": 1}}, "response": {"status": 404, "body": {"code": "not_found"}}},
    "typoKind": {"requires": {"falut": "database"}, "request": {"path": {"id": 2}}, "response": {"status": 404, "body": {"code": "not_found"}}},
    "typoValue": {"requires": {"fault": "databse"}, "request": {"path": {"id": 3}}, "response": {"status": 404, "body": {"code": "not_found"}}}
  }
}`
	g := New(Config{
		Examples:            fstest.MapFS{"openapi-example.json": {Data: []byte(file)}},
		ExamplesFile:        "openapi-example.json",
		ExampleRequirements: map[string][]string{"fault": {"database"}, "principal": {"app_password"}},
	})
	g.Register(Endpoint{Method: "GET", Path: "/things/{id}", OperationID: "getThing", Request: TypeOf[exThingPath](),
		Responses: []EndpointResponse{{Status: 200, Type: TypeOf[exThing]()}, {Status: 404, Type: TypeOf[exError]()}}})
	spec := g.Generate()

	get := operation(t, spec, "/things/{id}", "get")
	examples := get.Responses["404"].Content["application/json"].(map[string]interface{})["examples"].(map[string]interface{})
	if got := asJSON(t, examples["storageDown"]); got != `{"value":{"code":"not_found"},"x-requires":{"fault":"database"}}` {
		t.Errorf("404 storageDown %s", got)
	}
	param := get.Parameters[parameterIndex(get.Parameters, "path", "id")].Examples
	if got := asJSON(t, param["storageDown"]); got != `{"value":1,"x-requires":{"fault":"database"}}` {
		t.Errorf("id storageDown %s", got)
	}
	if got := asJSON(t, param["found"]); got != `{"value":1}` {
		t.Errorf("a pair with no requires carries none: %s", got)
	}

	reported := map[string]bool{}
	for _, w := range g.Report() {
		if w.Kind == WarnExampleRequirementUnknown {
			reported[w.Example] = true
		}
	}
	if !reported["typoKind"] || !reported["typoValue"] || reported["storageDown"] || reported["found"] {
		t.Errorf("unknown requirements reported for %v, want typoKind and typoValue only", reported)
	}
}
