package oapifly

import (
	"encoding/json"
	"reflect"
	"testing"
)

// bulkSelection takes nonempty ids or a filter (#68): a rule each field's tags cannot
// state alone.
type bulkSelection struct {
	IDs    []uint      `json:"ids,omitempty" maxItems:"1000"`
	Filter *bulkFilter `json:"filter,omitempty"`
	Note   string      `json:"note,omitempty"`
}

type bulkFilter struct {
	Status string `json:"status,omitempty"`
}

func (bulkSelection) AnyOf() AnyOfRule {
	return AnyOfRule{
		Alternatives: []Alternative{
			{Required: []string{"ids"}, Fields: map[string]string{"ids": `minItems:"1"`}},
			{Required: []string{"filter"}},
		},
		Field:   "ids",
		Message: "send at least one id, or a filter",
	}
}

func TestAnAnyOfRuleIsDescribed(t *testing.T) {
	reg := newSchemaRegistry(nil)
	schema := reg.reflectStructBody(reflect.TypeOf(bulkSelection{}))
	got, _ := json.Marshal(schema["anyOf"])
	want := `[{"properties":{"ids":{"minItems":1}},"required":["ids"]},{"required":["filter"]}]`
	if string(got) != want {
		t.Fatalf("anyOf = %s, want %s", got, want)
	}
}

func TestAnAnyOfRuleIsEnforced(t *testing.T) {
	for _, tc := range []struct {
		body string
		ok   bool
	}{
		{`{"ids":[1,2]}`, true},
		{`{"filter":{}}`, true},
		{`{"filter":{"status":"deferred"},"ids":[]}`, true},
		{`{}`, false},
		{`{"ids":[]}`, false},
		{`{"note":"x"}`, false},
		{`{"filter":null}`, false},
	} {
		t.Run(tc.body, func(t *testing.T) {
			var v bulkSelection
			err := Bind(&v, noParams, []byte(tc.body))
			if tc.ok {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			got := violations(t, err)
			if got["ids"] != "send at least one id, or a filter" || len(got) != 1 {
				t.Fatalf("violations = %v", got)
			}
		})
	}
}
