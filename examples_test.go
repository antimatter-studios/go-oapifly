package oapifly

import (
	"encoding/json"
	"strings"
	"testing"
)

// The Examples Object lets one operation describe several exchanges - a request that is
// accepted and one that is rejected - instead of a single specimen body. A consumer pairs
// them by name, so a name on the request side is only useful when the response side carries
// the same one.

func exampleTags(lines ...string) tagSet {
	tags := newTagSet()
	for _, line := range lines {
		parts := strings.SplitN(line, " ", 2)
		tags.add(parts[0], parts[1])
	}
	return tags
}

func TestNamedExamplesOnRequestAndResponse(t *testing.T) {
	reg := newSchemaRegistry(nil)
	item := buildPathItem("/x", exampleTags(
		`Router /login [post]`,
		`Param credentials body object true "Credentials"`,
		`Success 200 {object} object`,
		`Failure 401 {object} object`,
		`Example accepted request {"username": "gooduser"}`,
		`Example accepted response 200 {"token": "abc"}`,
		`Example rejected request {"username": "baduser"}`,
		`Example rejected response 401 {"error": "unknown user"}`,
	), reg)

	requestExamples := examplesOf(t, item.RequestBody.Content, "application/json")
	if _, ok := requestExamples["accepted"]; !ok {
		t.Fatalf("the request example was not named: %#v", requestExamples)
	}
	if _, ok := requestExamples["rejected"]; !ok {
		t.Fatalf("both request examples belong in the document: %#v", requestExamples)
	}

	// Same names on the response side, or a consumer pairing by name matches a rejected
	// request against every response, including the successful one.
	okExamples := examplesOf(t, item.Responses["200"].Content, "application/json")
	if _, ok := okExamples["accepted"]; !ok {
		t.Fatalf("the 200 should carry the accepted example: %#v", okExamples)
	}
	if _, ok := okExamples["rejected"]; ok {
		t.Fatalf("the 200 must not carry the rejected example: %#v", okExamples)
	}

	failExamples := examplesOf(t, item.Responses["401"].Content, "application/json")
	if _, ok := failExamples["rejected"]; !ok {
		t.Fatalf("the 401 should carry the rejected example: %#v", failExamples)
	}
}

func TestExampleValueIsParsedAsJSON(t *testing.T) {
	reg := newSchemaRegistry(nil)
	item := buildPathItem("/x", exampleTags(
		`Router /login [post]`,
		`Param credentials body object true "Credentials"`,
		`Success 200 {object} object`,
		`Example accepted request {"username": "gooduser", "attempts": 2}`,
	), reg)

	value := examplesOf(t, item.RequestBody.Content, "application/json")["accepted"].Value
	decoded, ok := value.(map[string]interface{})
	if !ok {
		t.Fatalf("a JSON example should be structured, not a string: %#v", value)
	}
	if decoded["username"] != "gooduser" {
		t.Fatalf("value not carried through: %#v", decoded)
	}
	if decoded["attempts"].(float64) != 2 {
		t.Fatalf("numbers should stay numbers: %#v", decoded)
	}
}

func TestExampleValueThatIsNotJSONStaysAString(t *testing.T) {
	reg := newSchemaRegistry(nil)
	item := buildPathItem("/x", exampleTags(
		`Router /thing [post]`,
		`Param body body object true "Body"`,
		`Success 200 {object} object`,
		`Example plain request just text`,
	), reg)

	value := examplesOf(t, item.RequestBody.Content, "application/json")["plain"].Value
	if value != "just text" {
		t.Fatalf("a non-JSON example is the text itself: %#v", value)
	}
}

func TestExampleSummaryIsOptional(t *testing.T) {
	reg := newSchemaRegistry(nil)
	item := buildPathItem("/x", exampleTags(
		`Router /login [post]`,
		`Param credentials body object true "Credentials"`,
		`Success 200 {object} object`,
		`Example accepted request {"username": "gooduser"} "A user that exists"`,
	), reg)

	example := examplesOf(t, item.RequestBody.Content, "application/json")["accepted"]
	if example.Summary != "A user that exists" {
		t.Fatalf("the trailing quoted text is the summary: %#v", example)
	}
	decoded, ok := example.Value.(map[string]interface{})
	if !ok || decoded["username"] != "gooduser" {
		t.Fatalf("the summary must not eat the value: %#v", example.Value)
	}
}

func TestOperationWithoutExamplesIsUnchanged(t *testing.T) {
	reg := newSchemaRegistry(nil)
	item := buildPathItem("/x", exampleTags(
		`Router /thing [get]`,
		`Success 200 {object} object`,
	), reg)

	content := item.Responses["200"].Content
	media, _ := content["application/json"].(map[string]interface{})
	if _, ok := media["examples"]; ok {
		t.Fatalf("no examples declared means no examples key: %#v", media)
	}
}

func TestExamplesSurviveIntoTheDocument(t *testing.T) {
	reg := newSchemaRegistry(nil)
	item := buildPathItem("/x", exampleTags(
		`Router /login [post]`,
		`Param credentials body object true "Credentials"`,
		`Success 200 {object} object`,
		`Example accepted request {"username": "gooduser"}`,
		`Example accepted response 200 {"token": "abc"}`,
	), reg)

	encoded, err := json.Marshal(item)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"examples"`) {
		t.Fatalf("examples must serialise into the document: %s", encoded)
	}
	if strings.Contains(string(encoded), `"examples":null`) {
		t.Fatalf("an operation without examples should omit the key: %s", encoded)
	}
}

func examplesOf(t *testing.T, content map[string]interface{}, mediaType string) map[string]Example {
	t.Helper()
	media, ok := content[mediaType].(map[string]interface{})
	if !ok {
		t.Fatalf("no %s content: %#v", mediaType, content)
	}
	examples, ok := media["examples"].(map[string]Example)
	if !ok {
		t.Fatalf("no examples in %s: %#v", mediaType, media)
	}
	return examples
}

func TestBodylessResponsesAreNamedOnlyWhenTheyCanBeConfused(t *testing.T) {
	statuses := func(codes ...string) map[string]map[string]PathItem {
		responses := map[string]Response{}
		for _, code := range codes {
			responses[code] = Response{Description: code}
		}
		return map[string]map[string]PathItem{"/x": {"get": {OperationID: "getX", Responses: responses}}}
	}
	pair := func(status int) ExamplePair { return ExamplePair{Response: ExampleResponse{Status: status}} }
	for _, test := range []struct {
		name  string
		codes []string
		pairs map[string]ExamplePair
		want  map[string]string
	}{
		{"several, one pair each", []string{"304", "412"},
			map[string]ExamplePair{"not_modified": pair(304), "precondition_failed": pair(412)},
			map[string]string{"304": "not_modified", "412": "precondition_failed"}},
		{"the only bodyless response", []string{"204"},
			map[string]ExamplePair{"deleted": pair(204)}, map[string]string{"204": ""}},
		{"a status two pairs show", []string{"304", "412"},
			map[string]ExamplePair{"a": pair(304), "b": pair(304), "c": pair(412)},
			map[string]string{"304": "", "412": "c"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			paths := statuses(test.codes...)
			reg := newSchemaRegistry(nil)
			mergeExamples(paths, ExampleFile{"getX": test.pairs}, nil, nil, reg)
			for code, want := range test.want {
				if got := paths["/x"]["get"].Responses[code].Example; got != want {
					t.Errorf("%s named %q, want %q", code, got, want)
				}
			}
		})
	}
}
