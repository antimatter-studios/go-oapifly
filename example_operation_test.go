package oapifly

import (
	"encoding/json"
	"strings"
	"testing"
)

// A related operation's pair may stay beside the create examples that explain it.
func TestExampleOperationOverride(t *testing.T) {
	file := strings.Replace(thingExamples, `"unknownId": {`, `"unknownId": {"operation": "getThing",`, 1)
	var pairs ExampleFile
	if err := json.Unmarshal([]byte(file), &pairs); err != nil {
		t.Fatal(err)
	}
	pairs["createThing"]["unknownId"] = pairs["getThing"]["unknownId"]
	delete(pairs, "getThing")
	data, err := MarshalExamples(pairs)
	if err != nil {
		t.Fatal(err)
	}
	g := exampleGenerator(string(data))
	get := operation(t, g.Generate(), "/things/{id}", "get")
	if got := asJSON(t, get.Parameters[0].Examples); got != `{"unknownId":{"value":999999}}` {
		t.Errorf("target parameter examples = %s", got)
	}
	if got := asJSON(t, get.Responses["404"]); !strings.Contains(got, `"unknownId"`) {
		t.Errorf("target response lacks the pair: %s", got)
	}
	for _, warning := range g.Report() {
		if warning.Kind != WarnExampleMissing || warning.OperationID != "getThing" || warning.Status != "200" {
			t.Errorf("unexpected warning: %+v", warning)
		}
	}
	stubs, err := g.ExampleStubs()
	if err != nil {
		t.Fatal(err)
	}
	if stubs["createThing"]["unknownId"].Operation != "getThing" {
		t.Fatal("sync moved or rewrote the original pair")
	}
	if len(stubs["getThing"]) != 1 || stubs["getThing"]["status200"].Response.Status != 200 {
		t.Errorf("sync duplicated the overridden 404: %+v", stubs)
	}
}

func TestExampleOperationOverrideReportsProblems(t *testing.T) {
	for _, test := range []struct {
		name, file, kind, id, message string
	}{
		{"unknown target", `{"createThing":{"missing":{"operation":"nowhere","request":{},"response":{"status":404}}}}`, string(WarnExampleOrphaned), "nowhere", "no operation"},
		{"invalid target body", `{"createThing":{"bad":{"operation":"getThing","request":{"path":{"id":9}},"response":{"status":404,"body":{"code":5}}}}}`, string(WarnExampleInvalid), "getThing", "response body does not validate"},
		{"colliding names", `{"createThing":{"same":{"operation":"getThing","request":{"path":{"id":9}},"response":{"status":404}}},"getThing":{"same":{"request":{"path":{"id":10}},"response":{"status":404}}}}`, string(WarnExampleOrphaned), "getThing", "target the same operation and name"},
	} {
		t.Run(test.name, func(t *testing.T) {
			g := exampleGenerator(test.file)
			found := false
			for _, warning := range g.Report() {
				if string(warning.Kind) == test.kind && warning.OperationID == test.id && strings.Contains(warning.Message, test.message) {
					found = true
				}
			}
			if !found {
				t.Fatalf("wanted %s for %s: %+v", test.message, test.id, g.Report())
			}
			if _, err := g.ExampleStubs(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
