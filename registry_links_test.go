package oapifly

import (
	"encoding/json"
	"strings"
	"testing"
)

// A registered response's links are the description's Link Objects, as an @Link annotation's
// are: a create leads to the read of what it made (#65). A link to an operation the document
// does not declare is reported, not silently kept.
func TestRegistryResponseLinks(t *testing.T) {
	g := New(Config{Title: "Links", Version: "1"})
	g.Register(
		Endpoint{Method: "POST", Path: "/domains", OperationID: "createDomain", Request: TypeOf[domainCreate](),
			Responses: []EndpointResponse{{Status: 201, Type: TypeOf[domainCreate](), Links: map[string]Link{
				"Read":    {OperationID: "getDomain", Parameters: map[string]string{"id": "$response.body#/id"}},
				"Missing": {OperationID: "noSuchOperation"},
			}}}},
		Endpoint{Method: "GET", Path: "/domains/{id}", OperationID: "getDomain", Request: TypeOf[domainPath](),
			Responses: []EndpointResponse{{Status: 200, Type: TypeOf[domainCreate]()}}},
	)
	spec := g.Generate()
	raw, err := json.Marshal(spec["paths"])
	if err != nil {
		t.Fatal(err)
	}
	var paths map[string]map[string]struct {
		Responses map[string]struct {
			Links map[string]Link `json:"links"`
		} `json:"responses"`
	}
	if err := json.Unmarshal(raw, &paths); err != nil {
		t.Fatal(err)
	}
	links := paths["/domains"]["post"].Responses["201"].Links
	read, ok := links["Read"]
	if !ok || read.OperationID != "getDomain" || read.Parameters["id"] != "$response.body#/id" {
		t.Fatalf("the create's links = %+v", links)
	}
	if got := paths["/domains/{id}"]["get"].Responses["200"].Links; len(got) != 0 {
		t.Fatalf("the read has links it was never given: %+v", got)
	}
	reported := false
	for _, w := range g.Warnings {
		if strings.Contains(w, "noSuchOperation") {
			reported = true
		}
	}
	if !reported {
		t.Fatalf("a link to an undeclared operation is not reported: %v", g.Warnings)
	}
}
