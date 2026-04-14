package oapifly

import (
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"testing"
)

// ---------------------------------------------------------------------------
// tagSet
// ---------------------------------------------------------------------------

func TestTagSet(t *testing.T) {
	ts := newTagSet()

	if ts.has("Router") {
		t.Error("empty tagSet should not have Router")
	}
	if ts.get("Router") != "" {
		t.Error("get on missing key should return empty string")
	}
	if ts.getAll("Router") != nil {
		t.Error("getAll on missing key should return nil")
	}

	ts.add("Router", "/api/users [GET]")
	ts.add("Param", "id path string true \"ID\"")
	ts.add("Param", "name query string false \"Name\"")

	if !ts.has("Router") {
		t.Error("should have Router")
	}
	if ts.get("Router") != "/api/users [GET]" {
		t.Errorf("get Router = %q", ts.get("Router"))
	}
	if len(ts.getAll("Param")) != 2 {
		t.Errorf("expected 2 Param values, got %d", len(ts.getAll("Param")))
	}
}

func TestTagSet_SingleWordAnnotation(t *testing.T) {
	ts := newTagSet()
	ts.add("Deprecated", "")

	if !ts.has("Deprecated") {
		t.Error("should have Deprecated even with empty value")
	}
	if ts.get("Deprecated") != "" {
		t.Errorf("get Deprecated = %q, want empty", ts.get("Deprecated"))
	}
}

// ---------------------------------------------------------------------------
// parseRouterTag
// ---------------------------------------------------------------------------

func TestParseRouterTag(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantPath   string
		wantMethod string
	}{
		{"get route", "/api/users [GET]", "/api/users", "get"},
		{"post route", "/api/users [POST]", "/api/users", "post"},
		{"delete route", "/api/users/{id} [DELETE]", "/api/users/{id}", "delete"},
		{"put route", "/api/users/{id} [PUT]", "/api/users/{id}", "put"},
		{"patch route", "/api/users/{id} [PATCH]", "/api/users/{id}", "patch"},
		{"empty string", "", "", ""},
		{"single field", "/api/users", "", ""},
		{"three fields", "/api/users [GET] extra", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path, method := parseRouterTag(tt.input)
			if path != tt.wantPath {
				t.Errorf("path = %q, want %q", path, tt.wantPath)
			}
			if method != tt.wantMethod {
				t.Errorf("method = %q, want %q", method, tt.wantMethod)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// goKindToJSONType
// ---------------------------------------------------------------------------

func TestGoKindToJSONType(t *testing.T) {
	ch := make(chan int)
	tests := []struct {
		name string
		typ  reflect.Type
		want string
	}{
		{"string", reflect.TypeOf(""), "string"},
		{"int", reflect.TypeOf(0), "integer"},
		{"int8", reflect.TypeOf(int8(0)), "integer"},
		{"int16", reflect.TypeOf(int16(0)), "integer"},
		{"int32", reflect.TypeOf(int32(0)), "integer"},
		{"int64", reflect.TypeOf(int64(0)), "integer"},
		{"uint", reflect.TypeOf(uint(0)), "integer"},
		{"uint8", reflect.TypeOf(uint8(0)), "integer"},
		{"uint16", reflect.TypeOf(uint16(0)), "integer"},
		{"uint32", reflect.TypeOf(uint32(0)), "integer"},
		{"uint64", reflect.TypeOf(uint64(0)), "integer"},
		{"float32", reflect.TypeOf(float32(0)), "number"},
		{"float64", reflect.TypeOf(0.0), "number"},
		{"bool", reflect.TypeOf(false), "boolean"},
		{"slice", reflect.TypeOf([]int{}), "array"},
		{"string slice", reflect.TypeOf([]string{}), "array"},
		{"array", reflect.TypeOf([3]int{}), "array"},
		{"struct", reflect.TypeOf(struct{ X int }{}), "object"},
		{"map", reflect.TypeOf(map[string]int{}), "object"},
		{"ptr to string", reflect.TypeOf(new(string)), "string"},
		{"ptr to struct", reflect.TypeOf(&struct{ X int }{}), "object"},
		{"channel (default)", reflect.TypeOf(ch), "object"},
		{"func (default)", reflect.TypeOf(func() {}), "object"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := goKindToJSONType(tt.typ)
			if got != tt.want {
				t.Errorf("goKindToJSONType(%v) = %q, want %q", tt.typ, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// goKindToOpenAPIFormat
// ---------------------------------------------------------------------------

func TestGoKindToOpenAPIFormat(t *testing.T) {
	tests := []struct {
		name string
		typ  reflect.Type
		want string
	}{
		{"int", reflect.TypeOf(0), "int64"},
		{"int32", reflect.TypeOf(int32(0)), "int32"},
		{"int64", reflect.TypeOf(int64(0)), "int64"},
		{"float32", reflect.TypeOf(float32(0)), "float"},
		{"float64", reflect.TypeOf(0.0), "double"},
		{"string (none)", reflect.TypeOf(""), ""},
		{"bool (none)", reflect.TypeOf(false), ""},
		{"uint (none)", reflect.TypeOf(uint(0)), ""},
		{"int8 (none)", reflect.TypeOf(int8(0)), ""},
		{"ptr to int", reflect.TypeOf(new(int)), "int64"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := goKindToOpenAPIFormat(tt.typ)
			if got != tt.want {
				t.Errorf("goKindToOpenAPIFormat(%v) = %q, want %q", tt.typ, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// resolveJSONFieldName
// ---------------------------------------------------------------------------

func TestResolveJSONFieldName(t *testing.T) {
	type S struct {
		Plain    string
		Named    string `json:"custom_name"`
		OmitE    string `json:"omit_me,omitempty"`
		Skipped  string `json:"-"`
		EmptyTag string `json:",omitempty"`
	}

	rt := reflect.TypeOf(S{})

	tests := []struct {
		fieldName     string
		wantName      string
		wantOmitempty bool
		wantSkip      bool
	}{
		{"Plain", "Plain", false, false},
		{"Named", "custom_name", false, false},
		{"OmitE", "omit_me", true, false},
		{"Skipped", "", false, true},
		{"EmptyTag", "EmptyTag", true, false},
	}

	for _, tt := range tests {
		t.Run(tt.fieldName, func(t *testing.T) {
			field, _ := rt.FieldByName(tt.fieldName)
			name, omit, skip := resolveJSONFieldName(field)
			if name != tt.wantName {
				t.Errorf("name = %q, want %q", name, tt.wantName)
			}
			if omit != tt.wantOmitempty {
				t.Errorf("omitempty = %v, want %v", omit, tt.wantOmitempty)
			}
			if skip != tt.wantSkip {
				t.Errorf("skip = %v, want %v", skip, tt.wantSkip)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// resolveFieldTypeReflect
// ---------------------------------------------------------------------------

func TestResolveFieldTypeReflect(t *testing.T) {
	type Inner struct {
		Value string `json:"value"`
	}

	tests := []struct {
		name    string
		typ     reflect.Type
		wantRef string
	}{
		{"string field", reflect.TypeOf(""), ""},
		{"int field", reflect.TypeOf(0), ""},
		{"struct field", reflect.TypeOf(Inner{}), "Inner"},
		{"ptr to struct", reflect.TypeOf(&Inner{}), "Inner"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info := resolveFieldTypeReflect(tt.typ)
			if info.Ref != tt.wantRef {
				t.Errorf("Ref = %q, want %q", info.Ref, tt.wantRef)
			}
			if tt.wantRef == "" && info.Schema == nil {
				t.Error("expected non-nil Schema for non-ref type")
			}
		})
	}
}

func TestResolveFieldTypeReflect_SliceOfStrings(t *testing.T) {
	info := resolveFieldTypeReflect(reflect.TypeOf([]string{}))
	if info.Ref != "" {
		t.Errorf("Ref should be empty for slice, got %q", info.Ref)
	}
	schema := info.Schema
	if schema["type"] != "array" {
		t.Errorf("type = %v, want array", schema["type"])
	}
	items, ok := schema["items"].(map[string]interface{})
	if !ok {
		t.Fatal("items missing or wrong type")
	}
	if items["type"] != "string" {
		t.Errorf("items.type = %v, want string", items["type"])
	}
}

func TestResolveFieldTypeReflect_SliceOfInts(t *testing.T) {
	info := resolveFieldTypeReflect(reflect.TypeOf([]int{}))
	schema := info.Schema
	items := schema["items"].(map[string]interface{})
	if items["type"] != "integer" {
		t.Errorf("items.type = %v, want integer", items["type"])
	}
	if items["format"] != "int64" {
		t.Errorf("items.format = %v, want int64", items["format"])
	}
}

func TestResolveFieldTypeReflect_IntWithFormat(t *testing.T) {
	info := resolveFieldTypeReflect(reflect.TypeOf(0))
	if info.Schema["type"] != "integer" {
		t.Errorf("type = %v, want integer", info.Schema["type"])
	}
	if info.Schema["format"] != "int64" {
		t.Errorf("format = %v, want int64", info.Schema["format"])
	}
}

func TestResolveFieldTypeReflect_Float64WithFormat(t *testing.T) {
	info := resolveFieldTypeReflect(reflect.TypeOf(0.0))
	if info.Schema["type"] != "number" {
		t.Errorf("type = %v, want number", info.Schema["type"])
	}
	if info.Schema["format"] != "double" {
		t.Errorf("format = %v, want double", info.Schema["format"])
	}
}

func TestResolveFieldTypeReflect_StringNoFormat(t *testing.T) {
	info := resolveFieldTypeReflect(reflect.TypeOf(""))
	if _, ok := info.Schema["format"]; ok {
		t.Errorf("string should have no format, got %v", info.Schema["format"])
	}
}

// ---------------------------------------------------------------------------
// generateSchemaForTypeReflect
// ---------------------------------------------------------------------------

type TestSchema struct {
	ID    int    `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email,omitempty"`
}

func TestGenerateSchemaForTypeReflect(t *testing.T) {
	schema := generateSchemaForTypeReflect(reflect.TypeOf(TestSchema{}))

	if schema["type"] != "object" {
		t.Errorf("type = %v, want object", schema["type"])
	}

	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("properties is not a map")
	}
	for _, field := range []string{"id", "name", "email"} {
		if _, ok := props[field]; !ok {
			t.Errorf("missing property %q", field)
		}
	}

	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("required is not a string slice")
	}
	requiredSet := map[string]bool{}
	for _, r := range required {
		requiredSet[r] = true
	}
	if !requiredSet["id"] || !requiredSet["name"] {
		t.Errorf("id and name should be required, got %v", required)
	}
	if requiredSet["email"] {
		t.Error("email should not be required (omitempty)")
	}
}

func TestGenerateSchemaForTypeReflect_Pointer(t *testing.T) {
	schema := generateSchemaForTypeReflect(reflect.TypeOf(&TestSchema{}))
	if schema["type"] != "object" {
		t.Errorf("should unwrap pointer, got type = %v", schema["type"])
	}
}

func TestGenerateSchemaForTypeReflect_SkipsIgnoredFields(t *testing.T) {
	type WithIgnored struct {
		Visible string `json:"visible"`
		Hidden  string `json:"-"`
	}

	schema := generateSchemaForTypeReflect(reflect.TypeOf(WithIgnored{}))
	props := schema["properties"].(map[string]interface{})
	if _, ok := props["Hidden"]; ok {
		t.Error("field with json:\"-\" should be skipped")
	}
	if _, ok := props["visible"]; !ok {
		t.Error("visible field should be present")
	}
}

func TestGenerateSchemaForTypeReflect_AllFieldsOmitempty(t *testing.T) {
	type AllOptional struct {
		A string `json:"a,omitempty"`
		B int    `json:"b,omitempty"`
	}

	schema := generateSchemaForTypeReflect(reflect.TypeOf(AllOptional{}))
	required := schema["required"].([]string)
	if len(required) != 0 {
		t.Errorf("all omitempty fields, required should be empty, got %v", required)
	}
}

func TestGenerateSchemaForTypeReflect_NoJSONTags(t *testing.T) {
	type NoTags struct {
		Foo string
		Bar int
	}

	schema := generateSchemaForTypeReflect(reflect.TypeOf(NoTags{}))
	props := schema["properties"].(map[string]interface{})
	if _, ok := props["Foo"]; !ok {
		t.Error("untagged field should use Go field name")
	}
	required := schema["required"].([]string)
	if len(required) != 2 {
		t.Errorf("untagged fields should all be required, got %v", required)
	}
}

// ---------------------------------------------------------------------------
// parseParam / parseAllParams
// ---------------------------------------------------------------------------

func TestParseParam(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantOK  bool
		wantIn  string
		wantReq bool
		wantEx  string
		wantDes string
	}{
		{
			"path param with example",
			`id path string true "User ID" example(abc123)`,
			true, "path", true, "abc123", "User ID",
		},
		{
			"query param optional",
			`page query int false "Page number"`,
			true, "query", false, "", "Page number",
		},
		{
			"header param",
			`Authorization header string true "Bearer token"`,
			true, "header", true, "", "Bearer token",
		},
		{
			"body param struct",
			`user body User true "User payload"`,
			true, "body", true, "", "User payload",
		},
		{
			"formData param",
			`file formData file true "Upload file"`,
			true, "formData", true, "", "Upload file",
		},
		{
			"too few fields",
			"id path string",
			false, "", false, "", "",
		},
		{
			"minimal (no description)",
			"id path string true",
			true, "path", true, "", "",
		},
		{
			"quoted example format",
			`id path string true "ID" example "abc"`,
			true, "path", true, "abc", "ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, ok := parseParam(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if p.In != tt.wantIn {
				t.Errorf("In = %q, want %q", p.In, tt.wantIn)
			}
			if p.Required != tt.wantReq {
				t.Errorf("Required = %v, want %v", p.Required, tt.wantReq)
			}
			if p.Example != tt.wantEx {
				t.Errorf("Example = %q, want %q", p.Example, tt.wantEx)
			}
			if p.Description != tt.wantDes {
				t.Errorf("Description = %q, want %q", p.Description, tt.wantDes)
			}
		})
	}
}

func TestParseAllParams(t *testing.T) {
	tags := []string{
		`id path string true "ID"`,
		`page query int false "Page"`,
		"bad",
		`user body User true "Payload"`,
	}
	params := parseAllParams(tags)
	if len(params) != 3 {
		t.Fatalf("expected 3 parsed params, got %d", len(params))
	}
	if params[0].In != "path" {
		t.Errorf("params[0].In = %q", params[0].In)
	}
	if params[1].In != "query" {
		t.Errorf("params[1].In = %q", params[1].In)
	}
	if params[2].In != "body" {
		t.Errorf("params[2].In = %q", params[2].In)
	}
}

func TestParseAllParams_Empty(t *testing.T) {
	params := parseAllParams(nil)
	if len(params) != 0 {
		t.Errorf("expected empty, got %v", params)
	}
}

// ---------------------------------------------------------------------------
// dataType mapping
// ---------------------------------------------------------------------------

func TestDataTypeToOpenAPIType(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"string", "string"},
		{"int", "integer"},
		{"integer", "integer"},
		{"number", "number"},
		{"float", "number"},
		{"float64", "number"},
		{"bool", "boolean"},
		{"boolean", "boolean"},
		{"file", "string"},
		{"SomeStruct", "string"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := dataTypeToOpenAPIType(tt.input)
			if got != tt.want {
				t.Errorf("dataTypeToOpenAPIType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestDataTypeSchema(t *testing.T) {
	s := dataTypeSchema("int")
	if s["type"] != "integer" {
		t.Errorf("type = %q", s["type"])
	}

	s = dataTypeSchema("file")
	if s["type"] != "string" || s["format"] != "binary" {
		t.Errorf("file schema = %v", s)
	}

	s = dataTypeSchema("string")
	if _, ok := s["format"]; ok {
		t.Error("string should have no format")
	}
}

func TestIsStructRef(t *testing.T) {
	primitives := []string{"string", "int", "integer", "number", "float", "float64", "bool", "boolean", "file"}
	for _, p := range primitives {
		if isStructRef(p) {
			t.Errorf("isStructRef(%q) should be false", p)
		}
	}
	structs := []string{"User", "ErrorResponse", "types.Item"}
	for _, s := range structs {
		if !isStructRef(s) {
			t.Errorf("isStructRef(%q) should be true", s)
		}
	}
}

// ---------------------------------------------------------------------------
// buildParameters
// ---------------------------------------------------------------------------

func TestBuildParameters_PathOnly(t *testing.T) {
	params := []parsedParam{
		{Name: "id", In: "path", DataType: "int", Required: true, Description: "User ID", Example: "123"},
	}
	result := buildParameters("/api/users/{id}", params)

	if len(result) != 1 {
		t.Fatalf("expected 1 param, got %d", len(result))
	}
	if result[0].Schema["type"] != "integer" {
		t.Errorf("schema type = %q, want integer", result[0].Schema["type"])
	}
	if result[0].Description != "User ID" {
		t.Errorf("description = %q", result[0].Description)
	}
	if result[0].Example != "123" {
		t.Errorf("example = %v", result[0].Example)
	}
}

func TestBuildParameters_PathQueryHeader(t *testing.T) {
	params := []parsedParam{
		{Name: "id", In: "path", DataType: "string", Required: true},
		{Name: "page", In: "query", DataType: "int", Required: false, Description: "Page number"},
		{Name: "limit", In: "query", DataType: "int", Required: false, Description: "Limit", Example: "20"},
		{Name: "Authorization", In: "header", DataType: "string", Required: true, Description: "Token"},
	}
	result := buildParameters("/api/users/{id}", params)

	if len(result) != 4 {
		t.Fatalf("expected 4 params, got %d", len(result))
	}

	// Path param first
	if result[0].In != "path" || result[0].Name != "id" {
		t.Errorf("result[0] = %+v", result[0])
	}
	// Query params
	if result[1].In != "query" || result[1].Name != "page" {
		t.Errorf("result[1] = %+v", result[1])
	}
	if result[1].Schema["type"] != "integer" {
		t.Errorf("page schema = %v", result[1].Schema)
	}
	if result[2].Example != "20" {
		t.Errorf("limit example = %v", result[2].Example)
	}
	// Header param
	if result[3].In != "header" || result[3].Name != "Authorization" {
		t.Errorf("result[3] = %+v", result[3])
	}
}

func TestBuildParameters_NoPlaceholders(t *testing.T) {
	params := []parsedParam{
		{Name: "q", In: "query", DataType: "string", Required: false},
	}
	result := buildParameters("/api/search", params)

	if len(result) != 1 {
		t.Fatalf("expected 1 param, got %d", len(result))
	}
	if result[0].In != "query" {
		t.Errorf("expected query param, got %q", result[0].In)
	}
}

func TestBuildParameters_NoParams(t *testing.T) {
	result := buildParameters("/api/items", nil)
	if len(result) != 0 {
		t.Errorf("expected 0 params, got %d", len(result))
	}
}

func TestBuildParameters_SkipsBodyAndFormData(t *testing.T) {
	params := []parsedParam{
		{Name: "user", In: "body", DataType: "User", Required: true},
		{Name: "file", In: "formData", DataType: "file", Required: true},
		{Name: "q", In: "query", DataType: "string"},
	}
	result := buildParameters("/api/items", params)

	if len(result) != 1 {
		t.Fatalf("expected 1 param (query only), got %d", len(result))
	}
	if result[0].Name != "q" {
		t.Errorf("expected query param, got %+v", result[0])
	}
}

func TestBuildParameters_PathFallbackDefaults(t *testing.T) {
	// Path param with no @Param metadata — should get defaults
	result := buildParameters("/api/{id}", nil)
	if len(result) != 1 {
		t.Fatalf("expected 1, got %d", len(result))
	}
	if result[0].Schema["type"] != "string" {
		t.Errorf("default schema type = %q", result[0].Schema["type"])
	}
	if result[0].Example != "id" {
		t.Errorf("default example = %v, want param name", result[0].Example)
	}
}

// ---------------------------------------------------------------------------
// buildRequestBody
// ---------------------------------------------------------------------------

func TestBuildRequestBody_BodyStructRef(t *testing.T) {
	reg := newSchemaRegistry(nil)
	params := []parsedParam{
		{Name: "user", In: "body", DataType: "User", Required: true, Description: "User data"},
	}
	rb := buildRequestBody(params, reg)

	if rb == nil {
		t.Fatal("expected non-nil RequestBody")
	}
	if rb.Description != "User data" {
		t.Errorf("description = %q", rb.Description)
	}
	if !rb.Required {
		t.Error("should be required")
	}

	content := rb.Content["application/json"].(map[string]interface{})
	schema := content["schema"].(map[string]interface{})
	if schema["$ref"] != "#/components/schemas/User" {
		t.Errorf("$ref = %v", schema["$ref"])
	}
	if _, ok := reg.schemas["User"]; !ok {
		t.Error("User should be registered in schema registry")
	}
}

func TestBuildRequestBody_BodyPrimitive(t *testing.T) {
	reg := newSchemaRegistry(nil)
	params := []parsedParam{
		{Name: "data", In: "body", DataType: "string", Required: true},
	}
	rb := buildRequestBody(params, reg)

	if rb == nil {
		t.Fatal("expected non-nil")
	}
	content := rb.Content["application/json"].(map[string]interface{})
	schema := content["schema"].(map[string]interface{})
	if schema["type"] != "string" {
		t.Errorf("type = %v", schema["type"])
	}
}

func TestBuildRequestBody_FormData(t *testing.T) {
	reg := newSchemaRegistry(nil)
	params := []parsedParam{
		{Name: "file", In: "formData", DataType: "file", Required: true, Description: "Upload"},
		{Name: "name", In: "formData", DataType: "string", Required: false},
	}
	rb := buildRequestBody(params, reg)

	if rb == nil {
		t.Fatal("expected non-nil")
	}
	if rb.Description != "Upload" {
		t.Errorf("description = %q", rb.Description)
	}

	content := rb.Content["multipart/form-data"].(map[string]interface{})
	schema := content["schema"].(map[string]interface{})
	props := schema["properties"].(map[string]interface{})
	fileProp := props["file"].(map[string]interface{})
	if fileProp["format"] != "binary" {
		t.Errorf("file format = %v", fileProp["format"])
	}

	required := schema["required"].([]string)
	if len(required) != 1 || required[0] != "file" {
		t.Errorf("required = %v", required)
	}
}

func TestBuildRequestBody_NilWhenNoBodyOrForm(t *testing.T) {
	reg := newSchemaRegistry(nil)
	params := []parsedParam{
		{Name: "id", In: "path", DataType: "string"},
		{Name: "q", In: "query", DataType: "string"},
	}
	rb := buildRequestBody(params, reg)
	if rb != nil {
		t.Error("should be nil when no body/formData params")
	}
}

func TestBuildRequestBody_Empty(t *testing.T) {
	reg := newSchemaRegistry(nil)
	rb := buildRequestBody(nil, reg)
	if rb != nil {
		t.Error("should be nil for empty params")
	}
}

// ---------------------------------------------------------------------------
// schemaRegistry
// ---------------------------------------------------------------------------

func TestSchemaRegistry_Resolve(t *testing.T) {
	reg := newSchemaRegistry(nil)
	name := reg.resolve("Foo")
	if name != "Foo" {
		t.Errorf("name = %q", name)
	}
	if _, ok := reg.schemas["Foo"]; !ok {
		t.Error("Foo should be registered")
	}
}

func TestSchemaRegistry_StripTypesPrefix(t *testing.T) {
	reg := newSchemaRegistry(nil)
	name := reg.resolve("types.Bar")
	if name != "Bar" {
		t.Errorf("name = %q", name)
	}
	if _, ok := reg.schemas["Bar"]; !ok {
		t.Error("Bar should be registered")
	}
}

func TestSchemaRegistry_AlreadyRegistered(t *testing.T) {
	reg := newSchemaRegistry(nil)
	reg.schemas["Existing"] = map[string]interface{}{"type": "object", "custom": true}

	name := reg.resolve("Existing")
	if name != "Existing" {
		t.Errorf("name = %q", name)
	}
	if reg.schemas["Existing"]["custom"] != true {
		t.Error("existing schema should not be modified")
	}
}

// ---------------------------------------------------------------------------
// buildResponse
// ---------------------------------------------------------------------------

func TestBuildResponse_Object(t *testing.T) {
	reg := newSchemaRegistry(nil)
	status, resp := buildResponse(`200 {object} User "OK"`, "", reg)

	if status != "200" {
		t.Errorf("status = %q", status)
	}
	if resp.Description != "OK" {
		t.Errorf("description = %q", resp.Description)
	}
	content := resp.Content["application/json"].(map[string]interface{})
	schema := content["schema"].(map[string]interface{})
	if schema["$ref"] != "#/components/schemas/User" {
		t.Errorf("$ref = %v", schema["$ref"])
	}
}

func TestBuildResponse_Array(t *testing.T) {
	reg := newSchemaRegistry(nil)
	_, resp := buildResponse(`200 {array} Item "OK"`, "", reg)

	content := resp.Content["application/json"].(map[string]interface{})
	schema := content["schema"].(map[string]interface{})
	if schema["type"] != "array" {
		t.Errorf("type = %v, want array", schema["type"])
	}
}

func TestBuildResponse_TooFewFields(t *testing.T) {
	reg := newSchemaRegistry(nil)
	status, _ := buildResponse("200 {object}", "", reg)
	if status != "" {
		t.Errorf("expected empty status for malformed input, got %q", status)
	}
}

func TestBuildResponse_StripTypesPrefix(t *testing.T) {
	reg := newSchemaRegistry(nil)
	_, resp := buildResponse(`200 {object} types.User "OK"`, "", reg)

	content := resp.Content["application/json"].(map[string]interface{})
	schema := content["schema"].(map[string]interface{})
	if schema["$ref"] != "#/components/schemas/User" {
		t.Errorf("$ref = %v", schema["$ref"])
	}
}

func TestBuildResponse_NoDescription(t *testing.T) {
	reg := newSchemaRegistry(nil)
	status, resp := buildResponse(`204 {object} Empty`, "", reg)
	if status != "204" {
		t.Errorf("status = %q", status)
	}
	if resp.Description != "" {
		t.Errorf("description should be empty, got %q", resp.Description)
	}
}

func TestBuildResponse_ProduceCSV(t *testing.T) {
	reg := newSchemaRegistry(nil)
	_, resp := buildResponse(`200 {object} Data "OK"`, "text/csv", reg)

	if _, ok := resp.Content["text/csv"]; !ok {
		t.Errorf("expected text/csv content type, got keys: %v", resp.Content)
	}
	if _, ok := resp.Content["application/json"]; ok {
		t.Error("should not have application/json when @Produce is text/csv")
	}
}

func TestResolveContentType(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"", "application/json"},
		{"json", "application/json"},
		{"application/json", "application/json"},
		{"text/csv", "text/csv"},
		{"csv", "text/csv"},
		{"text/plain", "text/plain"},
		{"plain", "text/plain"},
		{"xml", "application/xml"},
		{"text/html", "text/html"},
		{"application/octet-stream", "application/octet-stream"},
		{"custom/type", "custom/type"},
	}
	for _, tt := range tests {
		got := resolveContentType(tt.input)
		if got != tt.want {
			t.Errorf("resolveContentType(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// extractHandlerDocs
// ---------------------------------------------------------------------------

const handlerSource = `package controllers

type Controller struct{}

// @Summary List users
// @Description Returns all users
// @Tags users
// @Router /api/users [GET]
func (c *Controller) ListUsers() {}

// @Summary Get user
// @Description Returns a single user
// @Tags users
// @Param id path string true "User ID" example(abc123)
// @Success 200 {object} User "OK"
// @Failure 404 {object} Error "Not found"
// @Router /api/users/{id} [GET]
func (c *Controller) GetUser() {}

// No annotations
func (c *Controller) HelperMethod() {}

// @Summary Standalone handler
// @Router /api/standalone [GET]
func StandaloneFunc() {}
`

func TestExtractHandlerDocs(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", handlerSource, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	docs := extractHandlerDocs(f)
	if len(docs) != 3 {
		t.Fatalf("expected 3 handler docs (including standalone), got %d", len(docs))
	}

	if docs[0].get("Summary") != "List users" {
		t.Errorf("first summary = %q", docs[0].get("Summary"))
	}
	if docs[2].get("Summary") != "Standalone handler" {
		t.Errorf("standalone summary = %q", docs[2].get("Summary"))
	}
}

func TestExtractHandlerDocs_MultipleParams(t *testing.T) {
	src := `package api

type C struct{}

// @Summary Multi param
// @Param orgId path string true "Org ID"
// @Param userId path string true "User ID"
// @Param page query int false "Page"
// @Router /api/orgs/{orgId}/users/{userId} [GET]
func (c *C) Get() {}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	docs := extractHandlerDocs(f)
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	params := docs[0].getAll("Param")
	if len(params) != 3 {
		t.Errorf("expected 3 Param values, got %d", len(params))
	}
}

func TestExtractHandlerDocs_NoDoc(t *testing.T) {
	src := `package api
type C struct{}
func (c *C) NoDoc() {}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	docs := extractHandlerDocs(f)
	if len(docs) != 0 {
		t.Errorf("expected 0 docs, got %d", len(docs))
	}
}

func TestExtractHandlerDocs_DeprecatedAnnotation(t *testing.T) {
	src := `package api

type C struct{}

// @Deprecated
// @Summary Old endpoint
// @Router /api/old [GET]
func (c *C) Old() {}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	docs := extractHandlerDocs(f)
	if len(docs) != 1 {
		t.Fatalf("expected 1 doc, got %d", len(docs))
	}
	if !docs[0].has("Deprecated") {
		t.Error("@Deprecated should be stored in tagSet")
	}
}

// ---------------------------------------------------------------------------
// extractSchemaAnnotatedStructs
// ---------------------------------------------------------------------------

const schemaSource = `package types

// @schema
type User struct {
	ID   int
	Name string
}

// Not annotated
type Internal struct {
	Foo string
}

// @schema
type Error struct {
	Message string
}
`

func TestExtractSchemaAnnotatedStructs(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "types.go", schemaSource, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	structs := extractSchemaAnnotatedStructs(f)
	if len(structs) != 2 {
		t.Fatalf("expected 2 schema structs, got %d: %v", len(structs), structs)
	}

	want := map[string]bool{"User": true, "Error": true}
	for _, s := range structs {
		if !want[s] {
			t.Errorf("unexpected struct %q", s)
		}
	}
}

func TestExtractSchemaAnnotatedStructs_NoAnnotations(t *testing.T) {
	src := `package types
type Plain struct { ID int }
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	structs := extractSchemaAnnotatedStructs(f)
	if len(structs) != 0 {
		t.Errorf("expected 0, got %v", structs)
	}
}

func TestExtractSchemaAnnotatedStructs_FuncDeclIgnored(t *testing.T) {
	src := `package types
// @schema
func NotAType() {}
`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "test.go", src, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}

	structs := extractSchemaAnnotatedStructs(f)
	if len(structs) != 0 {
		t.Errorf("func decls should be ignored, got %v", structs)
	}
}

// ---------------------------------------------------------------------------
// buildPathItem
// ---------------------------------------------------------------------------

func TestBuildPathItem_Basic(t *testing.T) {
	tags := newTagSet()
	tags.add("Summary", "List items")
	tags.add("Description", "Returns all items")
	tags.add("Tags", "items,admin")
	tags.add("Router", "/api/items [GET]")

	reg := newSchemaRegistry(nil)
	item := buildPathItem(tags, reg)

	if item.Summary != "List items" {
		t.Errorf("Summary = %q", item.Summary)
	}
	if item.Description != "Returns all items" {
		t.Errorf("Description = %q", item.Description)
	}
	if len(item.Tags) != 2 || item.Tags[0] != "items" || item.Tags[1] != "admin" {
		t.Errorf("Tags = %v", item.Tags)
	}
}

func TestBuildPathItem_TagsWhitespaceTrimming(t *testing.T) {
	tags := newTagSet()
	tags.add("Tags", "users , admin , public")
	tags.add("Router", "/api/test [GET]")

	reg := newSchemaRegistry(nil)
	item := buildPathItem(tags, reg)

	if len(item.Tags) != 3 {
		t.Fatalf("expected 3 tags, got %d", len(item.Tags))
	}
	for i, want := range []string{"users", "admin", "public"} {
		if item.Tags[i] != want {
			t.Errorf("Tags[%d] = %q, want %q (whitespace should be trimmed)", i, item.Tags[i], want)
		}
	}
}

func TestBuildPathItem_NoTags(t *testing.T) {
	tags := newTagSet()
	tags.add("Router", "/api/items [GET]")

	reg := newSchemaRegistry(nil)
	item := buildPathItem(tags, reg)

	if item.Tags != nil {
		t.Errorf("Tags should be nil when no Tags annotation, got %v", item.Tags)
	}
}

func TestBuildPathItem_Deprecated(t *testing.T) {
	tags := newTagSet()
	tags.add("Deprecated", "")
	tags.add("Summary", "Old endpoint")
	tags.add("Router", "/api/old [GET]")

	reg := newSchemaRegistry(nil)
	item := buildPathItem(tags, reg)

	if !item.Deprecated {
		t.Error("should be deprecated")
	}
}

func TestBuildPathItem_OperationID(t *testing.T) {
	tags := newTagSet()
	tags.add("ID", "listUsers")
	tags.add("Router", "/api/users [GET]")

	reg := newSchemaRegistry(nil)
	item := buildPathItem(tags, reg)

	if item.OperationID != "listUsers" {
		t.Errorf("OperationID = %q", item.OperationID)
	}
}

func TestBuildPathItem_WithQueryParams(t *testing.T) {
	tags := newTagSet()
	tags.add("Router", "/api/users [GET]")
	tags.add("Param", `page query int false "Page number" example(1)`)
	tags.add("Param", `limit query int false "Page size"`)

	reg := newSchemaRegistry(nil)
	item := buildPathItem(tags, reg)

	if len(item.Parameters) != 2 {
		t.Fatalf("expected 2 params, got %d", len(item.Parameters))
	}
	if item.Parameters[0].In != "query" || item.Parameters[0].Name != "page" {
		t.Errorf("param[0] = %+v", item.Parameters[0])
	}
	if item.Parameters[0].Schema["type"] != "integer" {
		t.Errorf("page schema type = %q", item.Parameters[0].Schema["type"])
	}
}

func TestBuildPathItem_WithBodyParam(t *testing.T) {
	tags := newTagSet()
	tags.add("Router", "/api/users [POST]")
	tags.add("Param", `user body User true "User data"`)

	reg := newSchemaRegistry(nil)
	item := buildPathItem(tags, reg)

	if item.RequestBody == nil {
		t.Fatal("RequestBody should not be nil")
	}
	if item.RequestBody.Description != "User data" {
		t.Errorf("RequestBody.Description = %q", item.RequestBody.Description)
	}
	if len(item.Parameters) != 0 {
		t.Errorf("body params should not appear in Parameters, got %d", len(item.Parameters))
	}
}

func TestBuildPathItem_MultipleResponses(t *testing.T) {
	tags := newTagSet()
	tags.add("Router", "/api/items [GET]")
	tags.add("Success", `200 {array} Item "OK"`)
	tags.add("Failure", `500 {object} Error "Server error"`)

	reg := newSchemaRegistry(nil)
	item := buildPathItem(tags, reg)

	if _, ok := item.Responses["200"]; !ok {
		t.Error("missing 200 response")
	}
	if _, ok := item.Responses["500"]; !ok {
		t.Error("missing 500 response")
	}
}

func TestBuildPathItem_MultipleSuccessCodes(t *testing.T) {
	tags := newTagSet()
	tags.add("Router", "/api/items [POST]")
	tags.add("Success", `200 {object} Item "OK"`)
	tags.add("Success", `201 {object} Item "Created"`)

	reg := newSchemaRegistry(nil)
	item := buildPathItem(tags, reg)

	if len(item.Responses) != 2 {
		t.Errorf("expected 2 responses, got %d", len(item.Responses))
	}
	if item.Responses["201"].Description != "Created" {
		t.Errorf("201 description = %q", item.Responses["201"].Description)
	}
}

func TestBuildPathItem_FullEndpoint(t *testing.T) {
	tags := newTagSet()
	tags.add("Summary", "Create user")
	tags.add("Description", "Creates a new user")
	tags.add("ID", "createUser")
	tags.add("Tags", "users")
	tags.add("Param", `user body User true "User data"`)
	tags.add("Success", `201 {object} User "Created"`)
	tags.add("Failure", `400 {object} Error "Validation error"`)
	tags.add("Router", "/api/users [POST]")

	reg := newSchemaRegistry(nil)
	item := buildPathItem(tags, reg)

	if item.Summary != "Create user" {
		t.Errorf("Summary = %q", item.Summary)
	}
	if item.OperationID != "createUser" {
		t.Errorf("OperationID = %q", item.OperationID)
	}
	if item.RequestBody == nil {
		t.Error("RequestBody should not be nil")
	}
	if len(item.Responses) != 2 {
		t.Errorf("expected 2 responses, got %d", len(item.Responses))
	}
	if _, ok := reg.schemas["User"]; !ok {
		t.Error("User schema should be registered")
	}
}

// ---------------------------------------------------------------------------
// stripPackagePrefix
// ---------------------------------------------------------------------------

func TestStripPackagePrefix(t *testing.T) {
	tests := []struct {
		input, want string
	}{
		{"restclient.LoginRequest", "LoginRequest"},
		{"types.User", "User"},
		{"LoginRequest", "LoginRequest"},
		{"deeply.nested.Type", "Type"},
		{"", ""},
	}
	for _, tt := range tests {
		got := stripPackagePrefix(tt.input)
		if got != tt.want {
			t.Errorf("stripPackagePrefix(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// AST-based schema generation
// ---------------------------------------------------------------------------

func TestGenerateSchemaForTypeAST(t *testing.T) {
	// Write a temp Go file with a struct
	dir := t.TempDir()
	src := `package testpkg

type LoginRequest struct {
	Username string ` + "`json:\"username\"`" + `
	Password string ` + "`json:\"password\"`" + `
}
`
	path := dir + "/types.go"
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	schema := generateSchemaForTypeAST("LoginRequest", path)
	if schema == nil {
		t.Fatal("expected non-nil schema")
	}
	if schema["type"] != "object" {
		t.Errorf("type = %v", schema["type"])
	}

	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("properties should be a map")
	}
	if _, ok := props["username"]; !ok {
		t.Error("missing username property")
	}
	if _, ok := props["password"]; !ok {
		t.Error("missing password property")
	}

	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatal("required should be a string slice")
	}
	if len(required) != 2 {
		t.Errorf("expected 2 required fields, got %d", len(required))
	}
}

func TestGenerateSchemaForTypeAST_Omitempty(t *testing.T) {
	dir := t.TempDir()
	src := `package testpkg

type Response struct {
	Success bool   ` + "`json:\"success\"`" + `
	Message string ` + "`json:\"message,omitempty\"`" + `
	Name    string ` + "`json:\"name,omitempty\"`" + `
}
`
	path := dir + "/types.go"
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	schema := generateSchemaForTypeAST("Response", path)
	if schema == nil {
		t.Fatal("expected non-nil schema")
	}

	required := schema["required"].([]string)
	if len(required) != 1 || required[0] != "success" {
		t.Errorf("required = %v, want [success]", required)
	}
}

func TestGenerateSchemaForTypeAST_SkipJsonDash(t *testing.T) {
	dir := t.TempDir()
	src := `package testpkg

type Filtered struct {
	Visible  string ` + "`json:\"visible\"`" + `
	Internal string ` + "`json:\"-\"`" + `
}
`
	path := dir + "/types.go"
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	schema := generateSchemaForTypeAST("Filtered", path)
	props := schema["properties"].(map[string]interface{})
	if _, ok := props["visible"]; !ok {
		t.Error("missing visible")
	}
	if _, ok := props["Internal"]; ok {
		t.Error("Internal should be skipped (json:\"-\")")
	}
}

func TestGenerateSchemaForTypeAST_FieldTypes(t *testing.T) {
	dir := t.TempDir()
	src := `package testpkg

type AllTypes struct {
	Name    string   ` + "`json:\"name\"`" + `
	Age     int      ` + "`json:\"age\"`" + `
	Active  bool     ` + "`json:\"active\"`" + `
	Score   float64  ` + "`json:\"score\"`" + `
	Tags    []string ` + "`json:\"tags\"`" + `
	Ptr     *string  ` + "`json:\"ptr,omitempty\"`" + `
}
`
	path := dir + "/types.go"
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	schema := generateSchemaForTypeAST("AllTypes", path)
	props := schema["properties"].(map[string]interface{})

	nameSchema := props["name"].(map[string]interface{})
	if nameSchema["type"] != "string" {
		t.Errorf("name type = %v", nameSchema["type"])
	}

	ageSchema := props["age"].(map[string]interface{})
	if ageSchema["type"] != "integer" {
		t.Errorf("age type = %v", ageSchema["type"])
	}

	activeSchema := props["active"].(map[string]interface{})
	if activeSchema["type"] != "boolean" {
		t.Errorf("active type = %v", activeSchema["type"])
	}

	scoreSchema := props["score"].(map[string]interface{})
	if scoreSchema["type"] != "number" {
		t.Errorf("score type = %v", scoreSchema["type"])
	}

	tagsSchema := props["tags"].(map[string]interface{})
	if tagsSchema["type"] != "array" {
		t.Errorf("tags type = %v", tagsSchema["type"])
	}

	ptrSchema := props["ptr"].(map[string]interface{})
	if ptrSchema["type"] != "string" {
		t.Errorf("ptr type = %v (should deref pointer)", ptrSchema["type"])
	}
}

func TestGenerateSchemaForTypeAST_NotFound(t *testing.T) {
	dir := t.TempDir()
	src := `package testpkg
type Other struct {}
`
	path := dir + "/types.go"
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	schema := generateSchemaForTypeAST("NonExistent", path)
	if schema != nil {
		t.Errorf("expected nil for non-existent type, got %v", schema)
	}
}

func TestResolvePackagePrefixedType(t *testing.T) {
	// Create a temp dir with a type file
	dir := t.TempDir()
	src := `package restclient

type LoginRequest struct {
	Username string ` + "`json:\"username\"`" + `
	Password string ` + "`json:\"password\"`" + `
}
`
	path := dir + "/auth_type.go"
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}

	reg := newSchemaRegistry([]string{dir})
	name := reg.resolve("restclient.LoginRequest")

	if name != "restclient.LoginRequest" {
		t.Errorf("name = %q, want restclient.LoginRequest", name)
	}

	schema, ok := reg.schemas["restclient.LoginRequest"]
	if !ok {
		t.Fatal("schema not registered")
	}

	props, ok := schema["properties"].(map[string]interface{})
	if !ok {
		t.Fatal("properties should be a map")
	}
	if _, ok := props["username"]; !ok {
		t.Error("missing username property")
	}
	if _, ok := props["password"]; !ok {
		t.Error("missing password property")
	}
}

// ---------------------------------------------------------------------------
// extractTagValue
// ---------------------------------------------------------------------------

func TestExtractTagValue(t *testing.T) {
	tests := []struct {
		tag, key, want string
	}{
		{`json:"username"`, "json", "username"},
		{`json:"name,omitempty"`, "json", "name,omitempty"},
		{`json:"username" xml:"user"`, "json", "username"},
		{`json:"username" xml:"user"`, "xml", "user"},
		{`xml:"user"`, "json", ""},
		{`json:"-"`, "json", "-"},
	}
	for _, tt := range tests {
		got := extractTagValue(tt.tag, tt.key)
		if got != tt.want {
			t.Errorf("extractTagValue(%q, %q) = %q, want %q", tt.tag, tt.key, got, tt.want)
		}
	}
}
