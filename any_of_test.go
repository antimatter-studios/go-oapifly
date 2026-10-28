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

// vacationReply is off, or on with a subject and a body. A refusal names each field the
// reply lacks, as the explained alternative's Message says.
type vacationReply struct {
	Enabled *bool  `json:"enabled"`
	Subject string `json:"subject,omitempty"`
	Body    string `json:"body,omitempty"`
}

func (vacationReply) AnyOf() AnyOfRule {
	return AnyOfRule{
		Alternatives: []Alternative{
			{Fields: map[string]string{"enabled": `enums:"false"`}},
			{
				Required: []string{"subject", "body"},
				Fields:   map[string]string{"subject": `minLength:"1"`, "body": `minLength:"1"`},
				Message:  "required when enabled",
			},
		},
	}
}

func TestAnExplainedAlternativeIsDescribed(t *testing.T) {
	reg := newSchemaRegistry(nil)
	schema := reg.reflectStructBody(reflect.TypeOf(vacationReply{}))
	got, _ := json.Marshal(schema["anyOf"])
	want := `[{"properties":{"enabled":{"enum":[false]}}},{"properties":{"body":{"minLength":1},"subject":{"minLength":1}},"required":["subject","body"]}]`
	if string(got) != want {
		t.Fatalf("anyOf = %s, want %s", got, want)
	}
}

func TestAnExplainedAlternativeNamesEachUnmetField(t *testing.T) {
	for _, tc := range []struct {
		body string
		want map[string]string
	}{
		{`{"enabled":false}`, nil},
		{`{"enabled":true,"subject":"Away","body":"Back soon."}`, nil},
		{`{"enabled":true}`, map[string]string{"subject": "required when enabled", "body": "required when enabled"}},
		{`{"enabled":true,"subject":"Away"}`, map[string]string{"body": "required when enabled"}},
		{`{"enabled":true,"subject":"","body":"Back soon."}`, map[string]string{"subject": "required when enabled"}},
	} {
		t.Run(tc.body, func(t *testing.T) {
			var v vacationReply
			err := Bind(&v, noParams, []byte(tc.body))
			if tc.want == nil {
				if err != nil {
					t.Fatalf("refused: %v", err)
				}
				return
			}
			if got := violations(t, err); !reflect.DeepEqual(map[string]string(got), tc.want) {
				t.Fatalf("violations = %v, want %v", got, tc.want)
			}
		})
	}
}

// relayTransport is direct, or a relay with a host, port and mode, each worded as its own
// field is (#70).
type relayTransport struct {
	Transport string  `json:"transport" enums:"direct,relay"`
	Host      string  `json:"host,omitempty"`
	Port      *int    `json:"port,omitempty" minimum:"1"`
	TLSMode   *string `json:"tls_mode,omitempty" enums:"starttls,implicit"`
}

func (relayTransport) AnyOf() AnyOfRule {
	return AnyOfRule{
		Alternatives: []Alternative{
			{Fields: map[string]string{"transport": `enums:"direct"`}},
			{
				Required: []string{"host", "port", "tls_mode"},
				Fields:   map[string]string{"transport": `enums:"relay"`, "host": `minLength:"1"`},
				Messages: map[string]string{"port": "Must be between 1 and 65535", "tls_mode": "Must be one of: starttls, implicit"},
			},
		},
	}
}

func TestAnExplainedAlternativeWordsEachField(t *testing.T) {
	var v relayTransport
	if err := Bind(&v, noParams, []byte(`{"transport":"direct"}`)); err != nil {
		t.Fatalf("direct refused: %v", err)
	}
	err := Bind(&v, noParams, []byte(`{"transport":"relay","host":""}`))
	want := map[string]string{"host": "required", "port": "Must be between 1 and 65535", "tls_mode": "Must be one of: starttls, implicit"}
	if got := violations(t, err); !reflect.DeepEqual(map[string]string(got), want) {
		t.Fatalf("violations = %v, want %v", got, want)
	}
}
