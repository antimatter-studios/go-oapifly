package oapifly

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// A link is how a description says that one operation's response leads to another: create a
// device, and the id in the reply is what the read and the delete need. Without them a
// contract tester can only send transactions that stand alone, so nothing checks that a
// resource a create reported actually exists, or that a delete really removed it.
//
//	@Link 201 Read readDevice deviceGuid=$response.body#/guid
//
// The expressions are OpenAPI runtime expressions and are passed through untouched: what they
// mean is the consumer's business, and a generator that parsed them would only be able to get
// them wrong.

func TestParseLink(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantOK     bool
		wantStatus string
		wantName   string
		wantTarget string
		wantParams map[string]string
	}{
		{
			"one parameter",
			"201 Read readDevice deviceGuid=$response.body#/guid",
			true, "201", "Read", "readDevice",
			map[string]string{"deviceGuid": "$response.body#/guid"},
		},
		{
			"several parameters",
			"200 Detail readJobLog id=$response.body#/id page=$request.query.page",
			true, "200", "Detail", "readJobLog",
			map[string]string{"id": "$response.body#/id", "page": "$request.query.page"},
		},
		{
			// A target that needs nothing from this response is still a chain worth naming.
			"no parameters",
			"200 List listDevices",
			true, "200", "List", "listDevices", nil,
		},
		{"too few fields", "201 Read", false, "", "", "", nil},
		{"empty", "", false, "", "", "", nil},
		// A parameter has to name what it fills; without the '=' the annotation says nothing.
		{"parameter without an expression", "201 Read readDevice deviceGuid", false, "", "", "", nil},
		{"status that is not a status", "created Read readDevice x=$statusCode", false, "", "", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			link, ok := parseLink(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if link.Status != tt.wantStatus || link.Name != tt.wantName || link.OperationID != tt.wantTarget {
				t.Errorf("got (%q, %q, %q), want (%q, %q, %q)",
					link.Status, link.Name, link.OperationID, tt.wantStatus, tt.wantName, tt.wantTarget)
			}
			if len(tt.wantParams) == 0 {
				if len(link.Parameters) != 0 {
					t.Errorf("parameters = %v, want none", link.Parameters)
				}
				return
			}
			if !reflect.DeepEqual(link.Parameters, tt.wantParams) {
				t.Errorf("parameters = %v, want %v", link.Parameters, tt.wantParams)
			}
		})
	}
}

// The expression is the consumer's language, not this generator's, so it arrives at the
// document exactly as it was written.
func TestParseLink_ExpressionPassesThroughUntouched(t *testing.T) {
	for _, expr := range []string{
		"$response.body#/guid",
		"$request.path.id",
		"$response.header.Location",
		"$statusCode",
		"$response.body#/data/0/id",
	} {
		link, ok := parseLink("201 Next target x=" + expr)
		if !ok {
			t.Fatalf("%q did not parse", expr)
		}
		if link.Parameters["x"] != expr {
			t.Errorf("expression = %q, want %q", link.Parameters["x"], expr)
		}
	}
}

func linkedPathItem(t *testing.T, tagValues ...string) (PathItem, *schemaRegistry) {
	t.Helper()
	tags := newTagSet()
	tags.add("Summary", "Add device")
	tags.add("ID", "addDevice")
	tags.add("Produce", "json")
	tags.add("Success", "201 {object} string")
	tags.add("Failure", "400 {object} string")
	for _, v := range tagValues {
		tags.add("Link", v)
	}
	reg := newSchemaRegistry(nil)
	return buildPathItem("/api/v1/device", tags, reg), reg
}

func TestBuildPathItem_AttachesTheLinkToItsResponse(t *testing.T) {
	item, reg := linkedPathItem(t, "201 Read readDevice deviceGuid=$response.body#/guid")

	created, ok := item.Responses["201"]
	if !ok {
		t.Fatal("the 201 response is missing")
	}
	link, ok := created.Links["Read"]
	if !ok {
		t.Fatalf("no link named Read on the 201; links = %v", created.Links)
	}
	if link.OperationID != "readDevice" {
		t.Errorf("operationId = %q, want readDevice", link.OperationID)
	}
	if link.Parameters["deviceGuid"] != "$response.body#/guid" {
		t.Errorf("parameters = %v", link.Parameters)
	}
	// The link belongs to the response it was declared on, and to no other.
	if other := item.Responses["400"]; len(other.Links) != 0 {
		t.Errorf("the 400 should carry no links, got %v", other.Links)
	}
	if len(reg.warnings) != 0 {
		t.Errorf("a well-formed link should warn about nothing: %q", reg.warnings)
	}
}

func TestBuildPathItem_SeveralLinksOnOneResponse(t *testing.T) {
	item, _ := linkedPathItem(t,
		"201 Read readDevice deviceGuid=$response.body#/guid",
		"201 Remove removeDevice deviceGuid=$response.body#/guid",
	)
	links := item.Responses["201"].Links
	if len(links) != 2 {
		t.Fatalf("expected both links, got %v", links)
	}
	for _, name := range []string{"Read", "Remove"} {
		if _, ok := links[name]; !ok {
			t.Errorf("link %q is missing", name)
		}
	}
}

// A link hung on a status the operation does not declare has nothing to attach to, and
// silently dropping it would leave the author believing a chain exists.
func TestBuildPathItem_LinkOnAnUndeclaredStatusWarns(t *testing.T) {
	item, reg := linkedPathItem(t, "204 Read readDevice deviceGuid=$response.body#/guid")

	if _, ok := item.Responses["204"]; ok {
		t.Error("a link must not invent the response it hangs off")
	}
	found := false
	for _, w := range reg.warnings {
		if strings.Contains(w, "204") && strings.Contains(w, "Read") {
			found = true
		}
	}
	if !found {
		t.Errorf("the dropped link was not reported: %q", reg.warnings)
	}
}

func TestBuildPathItem_MalformedLinkWarns(t *testing.T) {
	_, reg := linkedPathItem(t, "201 Read")
	if len(reg.warnings) == 0 {
		t.Error("a malformed link must be reported rather than ignored")
	}
}

// An absent links key and an empty one are different documents; only the first is right when
// nothing was declared.
func TestResponse_OmitsLinksWhenThereAreNone(t *testing.T) {
	item, _ := linkedPathItem(t)
	raw, err := json.Marshal(item.Responses["201"])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "links") {
		t.Errorf("a response with no links must not carry the key: %s", raw)
	}
}

// A link names its target by operationId, and one that names an id no operation declares is a
// chain that cannot be followed. It is only knowable once the whole document is built.
func TestGenerate_LinkToAnUnknownOperationWarns(t *testing.T) {
	dir := t.TempDir()
	handler := writeGoFile(t, dir, "handler.go", `package main

// @Summary Add device
// @ID addDevice
// @Produce json
// @Success 201 {object} string
// @Link 201 Read readDevice deviceGuid=$response.body#/guid
// @Router /api/v1/device [post]
func AddDevice() {}
`)
	g := New(Config{Title: "t", Version: "1", ScanPatterns: []string{handler}, TypeDirs: []string{dir}})
	g.Generate()

	found := false
	for _, w := range g.Warnings {
		if strings.Contains(w, "readDevice") {
			found = true
		}
	}
	if !found {
		t.Errorf("a link to an operation nothing declares was not reported: %q", g.Warnings)
	}
}

func TestGenerate_LinkToADeclaredOperationIsAccepted(t *testing.T) {
	dir := t.TempDir()
	handler := writeGoFile(t, dir, "handler.go", `package main

// @Summary Add device
// @ID addDevice
// @Produce json
// @Success 201 {object} string
// @Link 201 Read readDevice deviceGuid=$response.body#/guid
// @Router /api/v1/device [post]
func AddDevice() {}

// @Summary Read device
// @ID readDevice
// @Produce json
// @Param deviceGuid path string true "Device GUID"
// @Success 200 {object} string
// @Router /api/v1/device/guid/{deviceGuid} [get]
func ReadDevice() {}
`)
	g := New(Config{Title: "t", Version: "1", ScanPatterns: []string{handler}, TypeDirs: []string{dir}})
	spec := g.Generate()

	for _, w := range g.Warnings {
		if strings.Contains(w, "readDevice") || strings.Contains(w, "link") {
			t.Errorf("a link to a declared operation should warn about nothing: %q", w)
		}
	}

	paths, _ := spec["paths"].(map[string]map[string]PathItem)
	links := paths["/api/v1/device"]["post"].Responses["201"].Links
	if links["Read"].OperationID != "readDevice" {
		t.Errorf("the link did not survive into the document: %v", links)
	}
}
