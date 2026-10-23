package oapifly

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type bindOwner struct {
	Email string `json:"email" format:"email"`
}

type bindMember struct {
	Name string `json:"name" minLength:"1"`
}

type bindCreate struct {
	Name     string            `json:"name" minLength:"3" maxLength:"10" pattern:"^[a-z]+$"`
	Kind     string            `json:"kind" enums:"alias,mailbox"`
	Quota    int               `json:"quota" minimum:"1" maximum:"100"`
	Ratio    float64           `json:"ratio,omitempty" minimum:"0.5"`
	Priority int               `json:"priority,omitempty" enums:"1,2,3"`
	Nickname *string           `json:"nickname" minLength:"2"`
	Owner    *bindOwner        `json:"owner,omitempty"`
	Members  []bindMember      `json:"members" minItems:"1" maxItems:"3"`
	Labels   map[string]string `json:"labels,omitempty"`
	Raw      json.RawMessage   `json:"raw,omitempty"`
	When     time.Time         `json:"when,omitempty"`
	Data     []byte            `json:"data,omitempty"`
	Settings *bindSettings     `json:"settings,omitempty"`
}

type bindSettings struct {
	_     struct{} `oapi:"open"`
	Theme string   `json:"theme"`
}

const bindValid = `{"name":"abc","kind":"alias","quota":5,"nickname":null,"members":[{"name":"m"}]}`

func bindBody(t *testing.T, b Binder, body string) (bindCreate, error) {
	t.Helper()
	var v bindCreate
	err := b.Bind(&v, noParams, []byte(body))
	return v, err
}

func noParams(string, string) (string, bool) { return "", false }

func violations(t *testing.T, err error) Violations {
	t.Helper()
	var v Violations
	if !errors.As(err, &v) {
		t.Fatalf("want Violations, got %v", err)
	}
	return v
}

// with is bindValid with fields replaced, added or (for a nil value) removed.
func with(t *testing.T, changes map[string]interface{}) string {
	t.Helper()
	var obj map[string]interface{}
	if err := json.Unmarshal([]byte(bindValid), &obj); err != nil {
		t.Fatal(err)
	}
	for k, v := range changes {
		if v == nil {
			delete(obj, k)
			continue
		}
		obj[k] = v
	}
	out, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

func TestBindAcceptsAValidBody(t *testing.T) {
	v, err := bindBody(t, Binder{}, bindValid)
	if err != nil {
		t.Fatal(err)
	}
	if v.Name != "abc" || v.Quota != 5 || len(v.Members) != 1 || v.Nickname != nil {
		t.Fatalf("decoded: %+v", v)
	}
}

// Each rule the type declares is enforced, and named by the field's JSON path.
func TestBindEnforcesEachRule(t *testing.T) {
	cases := []struct {
		name    string
		changes map[string]interface{}
		field   string
		problem string
	}{
		{"required", map[string]interface{}{"name": nil}, "name", "required"},
		{"a required pointer may be null, not left out", map[string]interface{}{"nickname": nil}, "nickname", "required"},
		{"null where the field is not nullable", map[string]interface{}{"members": json.RawMessage("null")}, "members", "must not be null"},
		{"wrong type", map[string]interface{}{"quota": "five"}, "quota", "must be an integer"},
		{"not an integer", map[string]interface{}{"quota": 1.5}, "quota", "must be an integer"},
		{"minimum", map[string]interface{}{"quota": 0}, "quota", "must be at least 1"},
		{"maximum", map[string]interface{}{"quota": 101}, "quota", "must be at most 100"},
		{"a fractional minimum", map[string]interface{}{"ratio": 0.25}, "ratio", "must be at least 0.5"},
		{"minLength", map[string]interface{}{"name": "ab"}, "name", "must be at least 3 characters"},
		{"maxLength", map[string]interface{}{"name": "abcdefghijk"}, "name", "must be at most 10 characters"},
		{"length counts characters, not bytes", map[string]interface{}{"nickname": "é"}, "nickname", "must be at least 2 characters"},
		{"pattern", map[string]interface{}{"name": "ABC"}, "name", "must match ^[a-z]+$"},
		{"string enum", map[string]interface{}{"kind": "group"}, "kind", "must be one of alias, mailbox"},
		{"number enum", map[string]interface{}{"priority": 4}, "priority", "must be one of 1, 2, 3"},
		{"format", map[string]interface{}{"owner": map[string]interface{}{"email": "nope"}}, "owner.email", "must be a valid email"},
		{"minItems", map[string]interface{}{"members": []interface{}{}}, "members", "must have at least 1 items"},
		{"maxItems", map[string]interface{}{"members": []interface{}{
			map[string]string{"name": "a"}, map[string]string{"name": "b"},
			map[string]string{"name": "c"}, map[string]string{"name": "d"}}}, "members", "must have at most 3 items"},
		{"inside a list", map[string]interface{}{"members": []interface{}{map[string]string{"name": ""}}}, "members[0].name", "must be at least 1 characters"},
		{"a closed struct", map[string]interface{}{"extra": 1}, "extra", "not allowed"},
		{"a closed nested struct", map[string]interface{}{"owner": map[string]string{"email": "a@b.c", "x": "y"}}, "owner.x", "not allowed"},
		{"a known type reads itself", map[string]interface{}{"when": "yesterday"}, "when", "must be a date-time"},
		{"bytes are base64", map[string]interface{}{"data": "%%%"}, "data", "must be a base64 string"},
		{"map values are typed", map[string]interface{}{"labels": map[string]int{"a": 1}}, "labels.a", "must be a string"},
	}
	for _, c := range cases {
		_, err := bindBody(t, Binder{}, with(t, c.changes))
		got := violations(t, err)
		if got[c.field] != c.problem || len(got) != 1 {
			t.Errorf("%s: %v, want %s: %s", c.name, got, c.field, c.problem)
		}
	}
}

// What the type leaves open is open: an optional field left out, an open struct, a raw value.
func TestBindAllowsWhatTheTypeAllows(t *testing.T) {
	bodies := []string{
		with(t, map[string]interface{}{"settings": map[string]string{"theme": "dark", "font": "mono"}}),
		with(t, map[string]interface{}{"raw": map[string]interface{}{"anything": []int{1}}}),
		with(t, map[string]interface{}{"when": "2026-10-23T10:00:00Z", "data": "aGk=", "labels": map[string]string{"a": "b"}}),
		with(t, map[string]interface{}{"owner": map[string]string{"email": "a@example.com"}, "priority": 2}),
	}
	for _, body := range bodies {
		if _, err := bindBody(t, Binder{}, body); err != nil {
			t.Errorf("%s: %v", body, err)
		}
	}
}

// Every rule broken is reported, not just the first.
func TestBindReportsEveryViolation(t *testing.T) {
	_, err := bindBody(t, Binder{}, `{"name":"A","kind":"x","quota":0,"nickname":null,"members":[],"extra":1}`)
	got := violations(t, err)
	for _, field := range []string{"name", "kind", "quota", "members", "extra"} {
		if got[field] == "" {
			t.Errorf("%s is not reported: %v", field, got)
		}
	}
}

func TestBindOpenStructs(t *testing.T) {
	if _, err := bindBody(t, Binder{OpenStructs: true}, with(t, map[string]interface{}{"extra": 1})); err != nil {
		t.Fatalf("an open binder ignores an extra field: %v", err)
	}
}

func TestBindMalformedBody(t *testing.T) {
	for _, body := range []string{`{"name":`, ``, `  `} {
		if _, err := bindBody(t, Binder{}, body); !errors.Is(err, ErrMalformedBody) {
			t.Errorf("%q: %v", body, err)
		}
	}
	if _, err := bindBody(t, Binder{}, `[1]`); violations(t, err)["body"] != "must be an object" {
		t.Errorf("a body of the wrong shape is a violation: %v", err)
	}
}

type bindRename struct {
	ID     uint64     `path:"id"`
	Limit  *int       `query:"limit" minimum:"1" maximum:"50"`
	Sort   string     `query:"sort,required" enums:"name,size"`
	Trace  string     `header:"X-Trace" pattern:"^t[0-9]+$"`
	Body   bindMember `body:""`
	ignore string
}

type bindOptionalBody struct {
	ID   uint64      `path:"id"`
	Body *bindMember `body:""`
}

type bindUpload struct {
	Name string `path:"name"`
	Body []byte `body:"text/plain"`
}

func params(values map[string]string) func(in, name string) (string, bool) {
	return func(in, name string) (string, bool) {
		v, ok := values[in+":"+name]
		return v, ok
	}
}

func TestBindParameters(t *testing.T) {
	var v bindRename
	err := Bind(&v, params(map[string]string{"path:id": "7", "query:limit": "10", "query:sort": "name", "header:X-Trace": "t1"}),
		[]byte(`{"name":"n"}`))
	if err != nil {
		t.Fatal(err)
	}
	if v.ID != 7 || v.Limit == nil || *v.Limit != 10 || v.Sort != "name" || v.Trace != "t1" || v.Body.Name != "n" {
		t.Fatalf("bound: %+v", v)
	}

	cases := []struct {
		values map[string]string
		want   Violations
	}{
		{map[string]string{"query:sort": "name"}, Violations{"id": "required"}},
		{map[string]string{"path:id": "7"}, Violations{"sort": "required"}},
		{map[string]string{"path:id": "x", "query:sort": "name"}, Violations{"id": "must be a non-negative integer"}},
		{map[string]string{"path:id": "7", "query:sort": "name", "query:limit": "0"}, Violations{"limit": "must be at least 1"}},
		{map[string]string{"path:id": "7", "query:sort": "date"}, Violations{"sort": "must be one of name, size"}},
		{map[string]string{"path:id": "7", "query:sort": "name", "header:X-Trace": "x"}, Violations{"X-Trace": "must match ^t[0-9]+$"}},
	}
	for _, c := range cases {
		var v bindRename
		got := violations(t, Bind(&v, params(c.values), []byte(`{"name":"n"}`)))
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v: %v, want %v", c.values, got, c.want)
		}
	}

	// A parameter and the body are reported together.
	var both bindRename
	got := violations(t, Bind(&both, params(map[string]string{"query:sort": "name"}), []byte(`{"name":""}`)))
	if got["id"] == "" || got["name"] == "" {
		t.Errorf("both are reported: %v", got)
	}
}

func TestBindBodyKinds(t *testing.T) {
	var optional bindOptionalBody
	if err := Bind(&optional, params(map[string]string{"path:id": "1"}), nil); err != nil || optional.Body != nil {
		t.Fatalf("an optional body may be left out: %v %+v", err, optional)
	}
	var upload bindUpload
	if err := Bind(&upload, params(map[string]string{"path:name": "a.txt"}), []byte("not json")); err != nil || string(upload.Body) != "not json" {
		t.Fatalf("a body that is not JSON is handed over: %v %q", err, upload.Body)
	}
	var list []bindMember
	if err := Bind(&list, noParams, []byte(`[{"name":"a"},{"name":""}]`)); violations(t, err)["[1].name"] == "" {
		t.Fatalf("a list body is checked item by item: %v", err)
	}
}

// The tags Bind enforces are the tags the document describes, on both description paths.
func TestConstraintsAreDescribedAsEnforced(t *testing.T) {
	want := map[string]map[string]interface{}{
		"name":     {"minLength": float64(3), "maxLength": float64(10), "pattern": "^[a-z]+$"},
		"kind":     {"enum": []interface{}{"alias", "mailbox"}},
		"quota":    {"minimum": float64(1), "maximum": float64(100)},
		"ratio":    {"minimum": 0.5},
		"priority": {"enum": []interface{}{float64(1), float64(2), float64(3)}},
		"members":  {"minItems": float64(1), "maxItems": float64(3)},
	}
	check := func(path string, props map[string]interface{}) {
		for field, keywords := range want {
			schema, _ := props[field].(map[string]interface{})
			for key, value := range keywords {
				if !reflect.DeepEqual(schema[key], value) {
					t.Errorf("%s: %s.%s is %#v, want %#v", path, field, key, schema[key], value)
				}
			}
		}
	}

	reg := newSchemaRegistry(nil)
	reg.reflectSchema(TypeOf[bindCreate]())
	props, _ := reg.schemas["bindCreate"]["properties"].(map[string]interface{})
	check("reflection", props)

	dir := t.TempDir()
	writeGoFile(t, dir, "create.go", "package types\n\ntype Create struct {\n"+
		"\tName string `json:\"name\" minLength:\"3\" maxLength:\"10\" pattern:\"^[a-z]+$\"`\n"+
		"\tKind string `json:\"kind\" enums:\"alias,mailbox\"`\n"+
		"\tQuota int `json:\"quota\" minimum:\"1\" maximum:\"100\"`\n"+
		"\tRatio float64 `json:\"ratio,omitempty\" minimum:\"0.5\"`\n"+
		"\tPriority int `json:\"priority,omitempty\" enums:\"1,2,3\"`\n"+
		"\tMembers []string `json:\"members\" minItems:\"1\" maxItems:\"3\"`\n}\n")
	annotated := newSchemaRegistry([]string{dir})
	name := annotated.resolve("types.Create")
	props, _ = annotated.schemas[name]["properties"].(map[string]interface{})
	check("annotations", props)

	// A bound that is not a number of the field's kind is left off, and Bind does not
	// enforce it either.
	type loose struct {
		Count int    `json:"count" minimum:"1.5" maximum:"many"`
		Name  string `json:"name" minLength:"-1"`
	}
	reg = newSchemaRegistry(nil)
	reg.reflectSchema(TypeOf[loose]())
	looseProps, _ := reg.schemas["loose"]["properties"].(map[string]interface{})
	if s := looseProps["count"].(map[string]interface{}); s["minimum"] != nil || s["maximum"] != nil {
		t.Errorf("unusable bounds are left off: %v", s)
	}
	if s := looseProps["name"].(map[string]interface{}); s["minLength"] != nil {
		t.Errorf("a negative length is left off: %v", s)
	}
	var l loose
	if err := Bind(&l, noParams, []byte(`{"count":0,"name":""}`)); err != nil {
		t.Errorf("what is not described is not enforced: %v", err)
	}
}

// Struct tags are read the way Go reads them: exact keys, escapes undone.
func TestTagsAreReadAsGoReadsThem(t *testing.T) {
	schema := map[string]interface{}{"type": "string"}
	applyTagConstraints(schema, `xformat:"nope" pattern:"^\\d+$"`)
	if schema["format"] != nil {
		t.Errorf("xformat is not format: %v", schema)
	}
	if schema["pattern"] != `^\d+$` {
		t.Errorf("the pattern is unescaped: %v", schema["pattern"])
	}
	if !strings.Contains(Violations{"a": "b"}.Error(), "a: b") {
		t.Error("Violations name their fields")
	}
}
