package oapifly

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

type wordedName struct {
	Name string `json:"name" minLength:"1" maxLength:"10" pattern:"\\S" pattern2:"^[^\\x00-\\x1f\\x7f]*$" minLengthMessage:"required" maxLengthMessage:"must be at most 10 characters" pattern2Message:"must not contain control characters" message:"is not a name"`
}

type wordedAll struct {
	Count   int      `json:"count" minimum:"1" maximum:"5" minimumMessage:"too few" maximumMessage:"too many"`
	Kind    string   `json:"kind" enums:"a,b" enumsMessage:"must be a or b"`
	Mail    string   `json:"mail" format:"email" formatMessage:"must be an address"`
	Tags    []string `json:"tags" minItems:"1" maxItems:"2" minItemsMessage:"needs a tag" maxItemsMessage:"two tags at most"`
	Note    string   `json:"note" nullMessage:"may be empty, not null" message:"unused"`
	Raw     []byte   `json:"raw" type:"array" typeMessage:"must be a list" message:"unused"`
	Plain   string   `json:"plain" minLength:"2" message:"one wording for all"`
	Default string   `json:"default" maxLength:"1"`
}

// Each constraint of a field can say what is wrong in its own words, a null its own, and the
// message tag still words the rest (#58).
func TestEachConstraintHasItsOwnMessage(t *testing.T) {
	cases := []struct {
		body  string
		field string
		want  string
	}{
		{`{"name":""}`, "name", "required"},
		{`{"name":"   "}`, "name", "is not a name"},
		{`{"name":"abcdefghijk"}`, "name", "must be at most 10 characters"},
		{`{"name":"a\u0007b"}`, "name", "must not contain control characters"},
	}
	for _, c := range cases {
		err := Bind(&wordedName{}, contentType(""), []byte(c.body))
		var v Violations
		if !errors.As(err, &v) || v[c.field] != c.want {
			t.Errorf("%s: %v, want %s: %q", c.body, err, c.field, c.want)
		}
	}
	if err := Bind(&wordedName{}, contentType(""), []byte(`{"name":"fine"}`)); err != nil {
		t.Errorf("a good name: %v", err)
	}

	base := `"count":3,"kind":"a","mail":"x@example.com","tags":["t"],"note":"","raw":[],"plain":"ok","default":"d"`
	for change, want := range map[string]map[string]string{
		`"count":0`:            {"count": "too few"},
		`"count":9`:            {"count": "too many"},
		`"kind":"c"`:           {"kind": "must be a or b"},
		`"mail":"nope"`:        {"mail": "must be an address"},
		`"tags":[]`:            {"tags": "needs a tag"},
		`"tags":["a","b","c"]`: {"tags": "two tags at most"},
		`"note":null`:          {"note": "may be empty, not null"},
		`"raw":{}`:             {"raw": "must be a list"},
		`"plain":"x"`:          {"plain": "one wording for all"},
		`"default":"dd"`:       {"default": "must be at most 1 character"},
	} {
		key := strings.SplitN(change, ":", 2)[0]
		body := "{" + strings.Replace(base, key+":"+valueOf(base, key), change, 1) + "}"
		err := Bind(&wordedAll{}, contentType(""), []byte(body))
		var v Violations
		if !errors.As(err, &v) || !reflect.DeepEqual(map[string]string(v), want) {
			t.Errorf("%s: %v, want %v", change, err, want)
		}
	}
}

// valueOf is the JSON text of key's value in a flat object's members.
func valueOf(members, key string) string {
	rest := members[strings.Index(members, key+":")+len(key)+1:]
	depth := 0
	for i, r := range rest {
		switch r {
		case '[', '{':
			depth++
		case ']', '}':
			depth--
		case ',':
			if depth == 0 {
				return rest[:i]
			}
		}
	}
	return rest
}

// A further pattern is described as a branch of allOf, since a schema has one pattern.
func TestFurtherPatternsAreDescribed(t *testing.T) {
	reg := newSchemaRegistry(nil)
	props := reg.reflectStructBody(reflect.TypeOf(wordedName{}))["properties"].(map[string]interface{})
	want := `{"allOf":[{"pattern":"^[^\\x00-\\x1f\\x7f]*$"}],"maxLength":10,"minLength":1,"pattern":"\\S","type":"string"}`
	if got := asJSON(t, props["name"]); got != want {
		t.Errorf("name is %s, want %s", got, want)
	}
}
