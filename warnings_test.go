package oapifly

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

type warnBody struct {
	Name string `json:"name"`
}

type warnTwoBodies struct {
	A warnBody `body:""`
	B warnBody `body:""`
}

type warnUnplaced struct {
	ID   string `path:"id"`
	Name string `json:"name"`
}

type warnUnrouted struct {
	ID string `path:"id"`
}

type warnStated struct {
	Bad json.RawMessage `json:"bad" type:"list"`
}

// annotatedGenerator writes handlers, a whole Go file, beside a types directory
// holding types (file name to source), and returns a generator over them and
// the handlers file's path.
func annotatedGenerator(t *testing.T, handlers string, types map[string]string) (*Generator, string) {
	t.Helper()
	dir := t.TempDir()
	file := filepath.Join(dir, "handlers.go")
	if err := os.WriteFile(file, []byte(handlers), 0o600); err != nil {
		t.Fatal(err)
	}
	typeDir := filepath.Join(dir, "types")
	if err := os.MkdirAll(typeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, src := range types {
		if err := os.WriteFile(filepath.Join(typeDir, name), []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return New(Config{ScanPatterns: []string{filepath.Join(dir, "*.go")}, TypeDirs: []string{typeDir}}), file
}

// lineOf is the 1-based line of src that starts with prefix.
func lineOf(t *testing.T, src, prefix string) float64 {
	t.Helper()
	for i, line := range strings.Split(src, "\n") {
		if strings.HasPrefix(line, prefix) {
			return float64(i + 1)
		}
	}
	t.Fatalf("no line starts with %q", prefix)
	return 0
}

// warningCase is a generator that emits a warning of kind, and the fields of
// its JSON beyond kind, severity and message.
type warningCase struct {
	kind  WarningKind
	name  string
	setup func(t *testing.T) (*Generator, map[string]interface{})
}

// registered is a case whose generator has only the endpoints.
func registered(want map[string]interface{}, endpoints ...Endpoint) func(t *testing.T) (*Generator, map[string]interface{}) {
	return func(*testing.T) (*Generator, map[string]interface{}) {
		g := New(Config{})
		g.Register(endpoints...)
		return g, want
	}
}

// annotatedCase is a case over an annotated handler, the operation getThing on
// GET /things declared by the function Thing, whose position the warning
// carries.
func annotatedCase(handler string, types map[string]string, want map[string]interface{}) func(t *testing.T) (*Generator, map[string]interface{}) {
	return func(t *testing.T) (*Generator, map[string]interface{}) {
		src := "package handlers\n\n" + handler
		g, file := annotatedGenerator(t, src, types)
		full := map[string]interface{}{"operationId": "getThing", "method": "GET", "path": "/things",
			"file": file, "line": lineOf(t, src, "func Thing")}
		for k, v := range want {
			full[k] = v
		}
		// A warning about a type read from its own file names that file.
		if f, ok := want["file"].(string); ok && strings.HasPrefix(f, "types/") {
			full["file"], full["line"] = filepath.Join(filepath.Dir(file), f), nil
		}
		return g, full
	}
}

const thingDoc = "// Thing gets a thing.\n// @ID getThing\n// @Router /things [get]\n"

var warningCases = []warningCase{
	{WarnScanMatchedNothing, "", func(t *testing.T) (*Generator, map[string]interface{}) {
		g := New(Config{ScanPatterns: []string{filepath.Join(t.TempDir(), "none-*.go")}})
		g.Register(Endpoint{Method: "GET", Path: "/ok"})
		return g, map[string]interface{}{}
	}},
	{WarnSourceUnparsable, "", func(t *testing.T) (*Generator, map[string]interface{}) {
		g, file := annotatedGenerator(t, "package handlers\n\nfunc (", nil)
		return g, map[string]interface{}{"file": file}
	}},
	{WarnMethodNotDescribable, "annotated", annotatedCase(
		"// Thing gets a thing.\n// @ID getThing\n// @Router /things [connect]\nfunc Thing() {}\n", nil,
		map[string]interface{}{"method": "CONNECT"})},
	{WarnMethodNotDescribable, "registered", registered(map[string]interface{}{"operationId": "connectThing", "method": "CONNECT", "path": "/things"},
		Endpoint{Method: "CONNECT", Path: "/things", OperationID: "connectThing"})},
	{WarnRouteDescribedTwice, "annotated", annotatedCase(
		thingDoc+"func Other() {}\n\n"+thingDoc+"func Thing() {}\n", nil, map[string]interface{}{})},
	{WarnRouteDescribedTwice, "registered", registered(map[string]interface{}{"operationId": "getThing", "method": "GET", "path": "/things", "tags": []interface{}{"things"}},
		Endpoint{Method: "GET", Path: "/things", OperationID: "getThing"},
		Endpoint{Method: "GET", Path: "/things", OperationID: "getThing", Tags: []string{"things"}})},
	{WarnRegistrationIncomplete, "", registered(map[string]interface{}{"operationId": "nowhere"},
		Endpoint{OperationID: "nowhere"})},
	{WarnResponseRegisteredTwice, "", registered(map[string]interface{}{"operationId": "getThing", "status": "200"},
		Endpoint{Method: "GET", Path: "/things", OperationID: "getThing", Responses: []EndpointResponse{
			{Status: 200, Type: TypeOf[warnBody]()}, {Status: 200, Type: TypeOf[warnBody]()}}})},
	{WarnBodyFieldAmbiguous, "", registered(map[string]interface{}{"operationId": "putThing", "field": "B"},
		Endpoint{Method: "PUT", Path: "/things", OperationID: "putThing", Request: TypeOf[warnTwoBodies]()})},
	{WarnFieldUnplaced, "", registered(map[string]interface{}{"operationId": "patchThing", "field": "Name"},
		Endpoint{Method: "PATCH", Path: "/things/{id}", OperationID: "patchThing", Request: TypeOf[warnUnplaced]()})},
	{WarnPathParameterUntyped, "", registered(map[string]interface{}{"operationId": "getThing", "parameter": "id"},
		Endpoint{Method: "GET", Path: "/things/{id}", OperationID: "getThing"})},
	{WarnPathParameterUnrouted, "", registered(map[string]interface{}{"operationId": "getThing", "parameter": "id"},
		Endpoint{Method: "GET", Path: "/things", OperationID: "getThing", Request: TypeOf[warnUnrouted]()})},
	{WarnSecuritySchemeUndefined, "", registered(map[string]interface{}{"operationId": "getThing", "method": "GET", "path": "/things", "scheme": "Nope"},
		Endpoint{Method: "GET", Path: "/things", OperationID: "getThing", Security: []SecurityRequirement{{"Nope": nil}}})},
	{WarnMiddlewareCredentialMissing, "", registered(map[string]interface{}{"operationId": "getThing"},
		Endpoint{Method: "GET", Path: "/things", OperationID: "getThing", Security: Public, Middleware: []Middleware{{Roles: []string{"admin"}}}})},
	{WarnAnnotationUnreadable, "@Header", annotatedCase(thingDoc+"// @Header nonsense\nfunc Thing() {}\n", nil, map[string]interface{}{})},
	{WarnAnnotationUnreadable, "@Link", annotatedCase(thingDoc+"// @Success 200 {string} string \"OK\"\n// @Link nonsense\nfunc Thing() {}\n", nil, map[string]interface{}{})},
	{WarnHeaderResponseUndescribed, "", annotatedCase(
		thingDoc+"// @Success 200 {string} string \"OK\"\n// @Header 404 {string} X-Nowhere \"No such response\"\nfunc Thing() {}\n", nil,
		map[string]interface{}{"header": "X-Nowhere", "status": "404"})},
	{WarnLinkResponseUndeclared, "", annotatedCase(
		thingDoc+"// @Success 200 {string} string \"OK\"\n// @Link 201 Read getThing\nfunc Thing() {}\n", nil,
		map[string]interface{}{"link": "Read", "status": "201"})},
	{WarnLinkDeclaredTwice, "", annotatedCase(
		thingDoc+"// @Success 200 {string} string \"OK\"\n// @Link 200 Next getThing\n// @Link 200 Next getThing\nfunc Thing() {}\n", nil,
		map[string]interface{}{"link": "Next", "status": "200"})},
	{WarnLinkTargetUnknown, "", annotatedCase(
		thingDoc+"// @Success 200 {string} string \"OK\"\n// @Link 200 Next missingOperation\nfunc Thing() {}\n", nil,
		map[string]interface{}{"link": "Next", "status": "200", "file": nil, "line": nil})},
	{WarnLinkTargetAmbiguous, "", annotatedCase(
		"// Other also claims getThing.\n// @ID getThing\n// @Router /others [get]\nfunc Other() {}\n\n"+
			thingDoc+"// @Success 200 {string} string \"OK\"\n// @Link 200 Next getThing\nfunc Thing() {}\n", nil,
		map[string]interface{}{"link": "Next", "status": "200", "file": nil, "line": nil})},
	{WarnUnresolvedType, "", annotatedCase(thingDoc+"// @Success 200 {object} types.Missing \"OK\"\nfunc Thing() {}\n", nil,
		map[string]interface{}{"type": "types.Missing"})},
	{WarnTypeAmbiguous, "", func(t *testing.T) (*Generator, map[string]interface{}) {
		pipelineDir, modelsDir := twoAttachments(t)
		dir := t.TempDir()
		file := filepath.Join(dir, "handlers.go")
		src := "package handlers\n\n" + thingDoc + "// @Success 200 {object} Attachment \"OK\"\nfunc Thing() {}\n"
		if err := os.WriteFile(file, []byte(src), 0o600); err != nil {
			t.Fatal(err)
		}
		g := New(Config{ScanPatterns: []string{file}, TypeDirs: []string{pipelineDir, modelsDir}})
		return g, map[string]interface{}{"operationId": "getThing", "type": "Attachment"}
	}},
	{WarnTypeUnexpressible, "", registered(map[string]interface{}{"operationId": "getThing", "type": "chan int"},
		Endpoint{Method: "GET", Path: "/things", OperationID: "getThing", Responses: []EndpointResponse{{Status: 200, Type: TypeOf[chan int]()}}})},
	{WarnTypeUnnameable, "", annotatedCase(thingDoc+"// @Success 200 {object} types.Holder \"OK\"\nfunc Thing() {}\n",
		map[string]string{"holder.go": "package types\n\ntype Page[T any] struct {\n\tData []T `json:\"data\"`\n}\n\ntype Holder struct {\n\tMapped Page[map[string]int] `json:\"mapped\"`\n}\n"},
		map[string]interface{}{"type": "Page"})},
	{WarnTypeArgumentsMismatch, "", annotatedCase(thingDoc+"// @Success 200 {object} types.Plain[int] \"OK\"\nfunc Thing() {}\n",
		map[string]string{"plain.go": "package types\n\ntype Plain struct {\n\tName string `json:\"name\"`\n}\n"},
		map[string]interface{}{"type": "types.Plain[int]", "file": "types/plain.go"})},
	{WarnTypeChosenAtRuntime, "", annotatedCase(thingDoc+"// @Success 200 {object} types.Shape \"OK\"\nfunc Thing() {}\n",
		map[string]string{"shape.go": "package types\n\ntype Shape interface {\n\tArea() float64\n}\n"},
		map[string]interface{}{"type": "Shape", "file": "types/shape.go"})},
	{WarnEmbeddedNotStruct, "", annotatedCase(thingDoc+"// @Success 200 {object} types.Holder \"OK\"\nfunc Thing() {}\n",
		map[string]string{"holder.go": "package types\n\ntype Name string\n\ntype Holder struct {\n\tName\n\tSize int `json:\"size\"`\n}\n"},
		map[string]interface{}{"type": "Name", "file": "types/holder.go"})},
	{WarnComponentNameCollision, "", registered(map[string]interface{}{"operationId": "getURLs"},
		Endpoint{Method: "GET", Path: "/urls", OperationID: "getURLs", Responses: []EndpointResponse{{Status: 200, Type: TypeOf[twoURLs]()}}})},
	{WarnConstraintInvalid, "", annotatedCase(thingDoc+"// @Param page query int false \"Page\" minimum(first)\nfunc Thing() {}\n", nil,
		map[string]interface{}{"parameter": "page"})},
	{WarnStatedTypeInvalid, "", registered(map[string]interface{}{"operationId": "getThing", "field": "Bad"},
		Endpoint{Method: "GET", Path: "/things", OperationID: "getThing", Responses: []EndpointResponse{{Status: 200, Type: TypeOf[warnStated]()}}})},
	{WarnAnyOfUnknownField, "", registered(map[string]interface{}{"operationId": "createThing", "field": "missing"},
		Endpoint{Method: "POST", Path: "/things", OperationID: "createThing", Request: TypeOf[warnAnyOf](), Responses: []EndpointResponse{{Status: 201}}})},
	{WarnExamplesUnreadable, "", func(*testing.T) (*Generator, map[string]interface{}) {
		g := New(Config{Examples: fstest.MapFS{}, ExamplesFile: "openapi-example.json"})
		g.Register(Endpoint{Method: "GET", Path: "/things", OperationID: "getThing"})
		return g, map[string]interface{}{"file": "openapi-example.json", "operationId": nil}
	}},
	{WarnExampleOrphaned, "", func(*testing.T) (*Generator, map[string]interface{}) {
		return exampleGenerator(`{"getThing": {"teapot": {"request": {}, "response": {"status": 418}}}}`),
			map[string]interface{}{"operationId": "getThing", "method": "GET", "path": "/things/{id}", "example": "teapot", "status": "418"}
	}},
	{WarnExampleMissing, "", func(*testing.T) (*Generator, map[string]interface{}) {
		return exampleGenerator(`{}`), map[string]interface{}{"operationId": "getThing", "method": "GET", "path": "/things/{id}", "status": "404"}
	}},
	{WarnExampleInvalid, "", func(*testing.T) (*Generator, map[string]interface{}) {
		return exampleGenerator(`{"createThing": {"wrongType": {"request": {"body": {"name": 5}}, "response": {"status": 201}}}}`),
			map[string]interface{}{"operationId": "createThing", "method": "POST", "path": "/things", "example": "wrongType"}
	}},
	{WarnExampleRequirementUnknown, "", func(*testing.T) (*Generator, map[string]interface{}) {
		return exampleGenerator(`{"getThing": {"storageDown": {"requires": {"fault": "database"}, "request": {"path": {"id": 1}}, "response": {"status": 404}}}}`),
			map[string]interface{}{"operationId": "getThing", "method": "GET", "path": "/things/{id}", "example": "storageDown"}
	}},
}

// Each kind of warning is reported with the operation, and what within it,
// it concerns, in JSON (#31). A nil wanted value is a field the warning must
// not have.
func TestEachWarningKindIsReported(t *testing.T) {
	for _, c := range warningCases {
		name := string(c.kind)
		if c.name != "" {
			name += "/" + c.name
		}
		t.Run(name, func(t *testing.T) {
			g, want := c.setup(t)
			var found []map[string]interface{}
			for _, w := range g.Report() {
				if w.Kind == "" {
					t.Errorf("a warning has no kind: %q", w.Message)
				}
				if w.Kind != c.kind {
					continue
				}
				b, err := json.Marshal(w)
				if err != nil {
					t.Fatal(err)
				}
				var m map[string]interface{}
				if err := json.Unmarshal(b, &m); err != nil {
					t.Fatal(err)
				}
				found = append(found, m)
			}
			for _, m := range found {
				if m["severity"] == SeverityWarning && m["message"] != "" && hasFields(m, want) {
					return
				}
			}
			b, _ := json.MarshalIndent(found, "", "  ")
			t.Errorf("no %s warning has %v; the %s warnings are:\n%s\nall warnings: %q", c.kind, want, c.kind, b, g.Warnings)
		})
	}
}

// hasFields reports whether m holds every wanted value, and none of those
// wanted as nil.
func hasFields(m, want map[string]interface{}) bool {
	for k, v := range want {
		got, ok := m[k]
		if v == nil {
			if ok {
				return false
			}
			continue
		}
		gb, _ := json.Marshal(got)
		wb, _ := json.Marshal(v)
		if !ok || string(gb) != string(wb) {
			return false
		}
	}
	return true
}

// Every WarningKind has a case above, and every warning the generator records
// names its kind, so nothing it can emit goes without one.
func TestEveryWarningKindIsTested(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "warnings.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	tested := map[WarningKind]bool{}
	for _, c := range warningCases {
		tested[c.kind] = true
	}
	declared := 0
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			if id, ok := vs.Type.(*ast.Ident); !ok || id.Name != "WarningKind" {
				continue
			}
			for _, v := range vs.Values {
				kind := WarningKind(strings.Trim(v.(*ast.BasicLit).Value, `"`))
				declared++
				if !tested[kind] {
					t.Errorf("no case emits %s", kind)
				}
			}
		}
	}
	if declared != len(tested) {
		t.Errorf("%d kinds are declared and %d tested", declared, len(tested))
	}

	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		src, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		ast.Inspect(src, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "warn" || len(call.Args) == 0 {
				return true
			}
			if lit, ok := call.Args[0].(*ast.CompositeLit); ok && !hasKey(lit, "Kind") {
				t.Errorf("%s: a warning without a kind", fset.Position(call.Pos()))
			}
			return true
		})
	}
}

// hasKey reports whether a composite literal sets the field key.
func hasKey(lit *ast.CompositeLit, key string) bool {
	for _, elt := range lit.Elts {
		if kv, ok := elt.(*ast.KeyValueExpr); ok {
			if id, ok := kv.Key.(*ast.Ident); ok && id.Name == key {
				return true
			}
		}
	}
	return false
}

// The report is in a stable order, and the same input gives the same bytes;
// Warnings still holds each warning's message (#31).
func TestReportJSONIsStable(t *testing.T) {
	build := func() *Generator {
		g := New(Config{})
		g.Register(
			Endpoint{Method: "GET", Path: "/b", OperationID: "getB", Security: []SecurityRequirement{{"Nope": nil}, {"Never": nil}}},
			Endpoint{Method: "GET", Path: "/a/{id}", OperationID: "getA"},
			Endpoint{Method: "GET", Path: "/a/{id}", OperationID: "getA"},
		)
		return g
	}
	first, err := build().ReportJSON()
	if err != nil {
		t.Fatal(err)
	}
	second, err := build().ReportJSON()
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("two runs differ:\n%s\n%s", first, second)
	}
	want := `{
  "warnings": [
    {
      "kind": "path-parameter-untyped",
      "severity": "warning",
      "operationId": "getA",
      "method": "GET",
      "path": "/a/{id}",
      "parameter": "id",
      "message": "GET /a/{id}: path parameter {id} has no field in the request type, described as a string"
    },
    {
      "kind": "path-parameter-untyped",
      "severity": "warning",
      "operationId": "getA",
      "method": "GET",
      "path": "/a/{id}",
      "parameter": "id",
      "message": "GET /a/{id}: path parameter {id} has no field in the request type, described as a string"
    },
    {
      "kind": "route-described-twice",
      "severity": "warning",
      "operationId": "getA",
      "method": "GET",
      "path": "/a/{id}",
      "message": "GET /a/{id} is registered twice, the later registration is used"
    },
    {
      "kind": "security-scheme-undefined",
      "severity": "warning",
      "operationId": "getB",
      "method": "GET",
      "path": "/b",
      "scheme": "Never",
      "message": "GET /b: security scheme \"Never\" is not defined in Config.SecuritySchemes"
    },
    {
      "kind": "security-scheme-undefined",
      "severity": "warning",
      "operationId": "getB",
      "method": "GET",
      "path": "/b",
      "scheme": "Nope",
      "message": "GET /b: security scheme \"Nope\" is not defined in Config.SecuritySchemes"
    }
  ]
}
`
	if string(first) != want {
		t.Errorf("report:\n%s\nwant:\n%s", first, want)
	}

	g := build()
	var messages []string
	for _, w := range g.Report() {
		messages = append(messages, w.Message)
	}
	got := append([]string(nil), g.Warnings...)
	sort.Strings(got)
	sort.Strings(messages)
	if strings.Join(got, "\n") != strings.Join(messages, "\n") {
		t.Errorf("Warnings %q, report messages %q", got, messages)
	}
}

// Strict mode turns exactly the warnings within its scope into errors: by
// operationId, tag or path prefix at a segment boundary, or all of them, those
// about no operation included (#31).
func TestStrictModeIsScoped(t *testing.T) {
	endpoints := []Endpoint{
		{Method: "GET", Path: "/api/v1/domains/{id}", OperationID: "getDomain", Tags: []string{"Domains"}},
		{Method: "GET", Path: "/api/v1/domains-x/{id}", OperationID: "getDomainX", Tags: []string{"Other"}},
		{Method: "GET", Path: "/api/v1/mail/{id}", OperationID: "getMail", Tags: []string{"Mail"}},
	}
	check := func(scope StrictScope, scan []string) []string {
		g := New(Config{Strict: scope, ScanPatterns: scan})
		g.Register(endpoints...)
		err := g.Check()
		if err == nil {
			return nil
		}
		strict, ok := err.(*StrictError)
		if !ok {
			t.Fatalf("Check returned %T", err)
		}
		var ids []string
		for _, w := range strict.Warnings {
			if w.Severity != SeverityError {
				t.Errorf("a strict warning has severity %q", w.Severity)
			}
			ids = append(ids, string(w.Kind)+"@"+w.OperationID)
		}
		return ids
	}
	cases := []struct {
		name  string
		scope StrictScope
		want  string
	}{
		{"off", StrictScope{}, ""},
		{"operation", StrictScope{Operations: []string{"getMail"}}, "path-parameter-untyped@getMail"},
		{"tag", StrictScope{Tags: []string{"Domains"}}, "path-parameter-untyped@getDomain"},
		{"path prefix", StrictScope{PathPrefixes: []string{"/api/v1/domains/"}}, "path-parameter-untyped@getDomain"},
		{"all", StrictScope{All: true}, "path-parameter-untyped@getDomainX path-parameter-untyped@getDomain path-parameter-untyped@getMail"},
	}
	for _, c := range cases {
		if got := strings.Join(check(c.scope, nil), " "); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
	missing := []string{filepath.Join(t.TempDir(), "none-*.go")}
	if got := check(StrictScope{Tags: []string{"Domains"}}, missing); len(got) != 1 {
		t.Errorf("a warning about no operation is outside a tag scope: %q", got)
	}
	if got := check(StrictScope{All: true}, missing); len(got) != 4 || got[0] != "scan-matched-nothing@" {
		t.Errorf("All takes a warning about no operation too: %q", got)
	}
	g := New(Config{Strict: StrictScope{Operations: []string{"getMail"}}})
	g.Register(endpoints...)
	if err := g.Check(); err == nil || !strings.Contains(err.Error(), "path-parameter-untyped: GET /api/v1/mail/{id}") {
		t.Errorf("the error names the warning: %v", err)
	}
	for _, w := range g.Report() {
		if (w.OperationID == "getMail") != (w.Severity == SeverityError) {
			t.Errorf("%s has severity %s", w.OperationID, w.Severity)
		}
	}
}

// warnAnyOf's rule constrains a property it does not declare.
type warnAnyOf struct {
	Name string `json:"name,omitempty"`
}

func (warnAnyOf) AnyOf() AnyOfRule {
	return AnyOfRule{Alternatives: []Alternative{{Fields: map[string]string{"missing": `minLength:"1"`}}}}
}
