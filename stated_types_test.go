package oapifly

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// rawJSON is a named []byte that writes JSON of its own, as REST Mail's models.JSONB does.
type rawJSON []byte

func (r rawJSON) MarshalJSON() ([]byte, error) { return r, nil }

func (r *rawJSON) UnmarshalJSON(b []byte) error {
	*r = append((*r)[:0], b...)
	return nil
}

type statedMessage struct {
	To      json.RawMessage  `json:"to" type:"array" items:"string" minItems:"1"`
	Headers rawJSON          `json:"headers" type:"object"`
	Note    *json.RawMessage `json:"note,omitempty" type:"string" format:"email"`
	Bad     json.RawMessage  `json:"bad,omitempty" type:"list"`
}

// A field states its JSON type when its Go type cannot say it: the stated type replaces the
// Go type's description, nullable for a pointer, refined by the other tags; a type that is
// not a JSON Schema one is reported and the Go type kept (#46).
func TestAFieldStatesItsJSONType(t *testing.T) {
	reg := newSchemaRegistry(nil)
	props := reg.reflectStructBody(reflect.TypeOf(statedMessage{}))["properties"].(map[string]interface{})
	want := map[string]string{
		"to":      `{"items":{"type":"string"},"minItems":1,"type":"array"}`,
		"headers": `{"type":"object"}`,
		"note":    `{"format":"email","type":["string","null"]}`,
		"bad":     `{}`,
	}
	for name, schema := range want {
		if got := asJSON(t, props[name]); got != schema {
			t.Errorf("%s is described as %s, want %s", name, got, schema)
		}
	}
	if !strings.Contains(strings.Join(reg.warnings, "\n"), "type list is not one of") {
		t.Errorf("the unknown type is not reported: %v", reg.warnings)
	}
}

// The annotation scanner reads the same tags from source and describes the field the same way.
func TestAFieldStatesItsJSONTypeFromSource(t *testing.T) {
	dir := t.TempDir()
	path := writeGoFile(t, dir, "types.go", "package types\n\nimport \"encoding/json\"\n\n"+
		"type Message struct {\n"+
		"\tTo json.RawMessage `json:\"to\" type:\"array\" items:\"string\"`\n"+
		"\tNote *json.RawMessage `json:\"note,omitempty\" type:\"string\"`\n"+
		"}\n")
	reg := newSchemaRegistry([]string{dir})
	schema := generateSchemaForTypeAST("Message", path, reg)
	if got := asJSON(t, propOf(t, schema, "to")); got != `{"items":{"type":"string"},"type":"array"}` {
		t.Errorf("to is described as %s", got)
	}
	if got := asJSON(t, propOf(t, schema, "note")); got != `{"type":["string","null"]}` {
		t.Errorf("note is described as %s", got)
	}
}

// Bind checks a stated field's value against the stated type, not its Go type: the type, each
// item's type, the item count, and null only where the field is a pointer (#46).
func TestBindChecksAStatedType(t *testing.T) {
	var ok statedMessage
	if err := Bind(&ok, noParams, []byte(`{"to":["a@example.test"],"headers":{"x":["1"]},"note":null}`)); err != nil {
		t.Fatalf("a body of the stated types: %v", err)
	}
	if string(ok.To) != `["a@example.test"]` || string(ok.Headers) != `{"x":["1"]}` {
		t.Errorf("the raw values were not kept: %s %s", ok.To, ok.Headers)
	}
	for body, want := range map[string]map[string]string{
		`{"to":"a@example.test","headers":{}}`: {"to": "must be an array"},
		`{"to":["a",2],"headers":{}}`:          {"to[1]": "must be a string"},
		`{"to":[],"headers":{}}`:               {"to": "must have at least 1 item"},
		`{"to":["a"],"headers":[]}`:            {"headers": "must be an object"},
		`{"to":["a"],"headers":null}`:          {"headers": "must not be null"},
		`{"to":["a"],"headers":{},"note":5}`:   {"note": "must be a string"},
		`{"to":["a"],"headers":{},"note":"x"}`: {"note": "must be a valid email"},
	} {
		var v statedMessage
		got := violations(t, Bind(&v, noParams, []byte(body)))
		for field, message := range want {
			if got[field] != message {
				t.Errorf("%s: %s is %q, want %q (all: %v)", body, field, got[field], message, got)
			}
		}
	}
}
