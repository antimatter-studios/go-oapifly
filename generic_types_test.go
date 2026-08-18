package oapifly

import (
	"go/ast"
	"regexp"
	"strings"
	"testing"
	"time"
)

// A generic type is how one response envelope serves every payload:
//
//	type Page[T any] struct { Data []T; Meta Meta }
//
// Before this, `@Success 200 {object} types.Page[types.Item]` was described as
// `{"type": "object"}` under a component named "Page[types.Item]" - no fields, and a key
// containing brackets, which the Components Object forbids. A consumer therefore learned
// nothing about the payload and a strict reader rejected the document outright.

// componentKey is the pattern the OpenAPI specification requires of every key under
// components. Brackets are not in it.
var componentKey = regexp.MustCompile(`^[a-zA-Z0-9.\-_]+$`)

const genericSource = `package types

type Meta struct {
	Total int ` + "`json:\"total\"`" + `
}

type Item struct {
	Name string ` + "`json:\"name\"`" + `
}

type Page[T any] struct {
	Data []T  ` + "`json:\"data\"`" + `
	Meta Meta ` + "`json:\"meta\"`" + `
}

type One[T any] struct {
	Value T ` + "`json:\"value\"`" + `
}

type Pair[K any, V any] struct {
	Key   K ` + "`json:\"key\"`" + `
	Value V ` + "`json:\"value\"`" + `
}

type Wrapper[T any] struct {
	Inner T ` + "`json:\"inner\"`" + `
}

type ItemPage = Page[Item]

type Holder struct {
	Page Page[Item] ` + "`json:\"page\"`" + `
}
`

func genericRegistry(t *testing.T) *schemaRegistry {
	t.Helper()
	dir := t.TempDir()
	writeGoFile(t, dir, "types.go", genericSource)
	return newSchemaRegistry([]string{dir})
}

func TestParseGenericName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantBase string
		wantArgs []string
		wantOK   bool
	}{
		{"plain type", "Item", "", nil, false},
		{"one argument", "Page[Item]", "Page", []string{"Item"}, true},
		{"qualified argument", "types.Page[types.Item]", "types.Page", []string{"types.Item"}, true},
		{"two arguments", "Pair[string,Item]", "Pair", []string{"string", "Item"}, true},
		{"spaces are trimmed", "Pair[string, Item]", "Pair", []string{"string", "Item"}, true},
		{"list argument", "Page[[]Item]", "Page", []string{"[]Item"}, true},
		// A nested instantiation is one argument, not two: the comma inside it belongs to the
		// inner type, so the split has to count bracket depth.
		{"nested instantiation", "Page[Pair[string,Item]]", "Page", []string{"Pair[string,Item]"}, true},
		{"no closing bracket", "Page[Item", "", nil, false},
		{"no arguments", "Page[]", "", nil, false},
		{"nothing before the bracket", "[Item]", "", nil, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base, args, ok := parseGenericName(tt.input)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if base != tt.wantBase {
				t.Errorf("base = %q, want %q", base, tt.wantBase)
			}
			if len(args) != len(tt.wantArgs) {
				t.Fatalf("args = %q, want %q", args, tt.wantArgs)
			}
			for i := range args {
				if args[i] != tt.wantArgs[i] {
					t.Errorf("args[%d] = %q, want %q", i, args[i], tt.wantArgs[i])
				}
			}
		})
	}
}

// The component name has to be usable as a key, which rules out the brackets the Go
// spelling uses.
func TestGenericSchemaName_IsAValidComponentKey(t *testing.T) {
	tests := []struct {
		base string
		args []string
		want string
	}{
		{"Page", []string{"Item"}, "Page-Item"},
		{"types.Page", []string{"types.Item"}, "Page-Item"},
		{"Pair", []string{"string", "Item"}, "Pair-string-Item"},
		{"Page", []string{"[]Item"}, "Page-ItemList"},
		{"Page", []string{"Pair[string,Item]"}, "Page-Pair-string-Item"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			got := genericSchemaName(tt.base, tt.args)
			if got != tt.want {
				t.Errorf("genericSchemaName(%q, %q) = %q, want %q", tt.base, tt.args, got, tt.want)
			}
			if !componentKey.MatchString(got) {
				t.Errorf("%q is not a legal component key", got)
			}
		})
	}
}

// The whole point: the instantiated type's fields are described, with the type parameter
// replaced by the argument.
func TestResolveGenericInstantiation_SubstitutesTheArgument(t *testing.T) {
	reg := genericRegistry(t)

	name := reg.resolve("types.Page[types.Item]")
	if name != "Page-Item" {
		t.Fatalf("resolve returned %q, want Page-Item", name)
	}

	schema, ok := reg.schemas[name]
	if !ok {
		t.Fatalf("no component registered under %q; have %v", name, keysOf(reg.schemas))
	}
	if schema["type"] != "object" {
		t.Fatalf("instantiation should be an object, got %#v", schema)
	}

	data := propOf(t, schema, "data")
	if data["type"] != "array" {
		t.Fatalf("data should be an array, got %#v", data)
	}
	items, _ := data["items"].(map[string]interface{})
	if items["$ref"] != "#/components/schemas/Item" {
		t.Errorf("data items should reference Item, got %#v", items)
	}
	if got := refOf(t, schema, "meta"); got != "#/components/schemas/Meta" {
		t.Errorf("meta should reference Meta, got %q", got)
	}

	// The argument and the ordinary field type both earn their own components.
	for _, want := range []string{"Item", "Meta"} {
		if _, ok := reg.schemas[want]; !ok {
			t.Errorf("%s was not registered; have %v", want, keysOf(reg.schemas))
		}
	}
	// And every key is legal.
	for key := range reg.schemas {
		if !componentKey.MatchString(key) {
			t.Errorf("component key %q is not legal under the OpenAPI specification", key)
		}
	}
}

// A type parameter in a plain field, not inside a list.
func TestResolveGenericInstantiation_BareTypeParameterField(t *testing.T) {
	reg := genericRegistry(t)
	name := reg.resolve("One[Item]")
	schema := reg.schemas[name]
	if got := refOf(t, schema, "value"); got != "#/components/schemas/Item" {
		t.Errorf("value should reference Item, got %q (schema %#v)", got, schema)
	}
}

// A primitive argument needs no component of its own.
func TestResolveGenericInstantiation_PrimitiveArgument(t *testing.T) {
	reg := genericRegistry(t)
	name := reg.resolve("Page[string]")
	if name != "Page-string" {
		t.Fatalf("resolve returned %q, want Page-string", name)
	}
	data := propOf(t, reg.schemas[name], "data")
	items, _ := data["items"].(map[string]interface{})
	if items["type"] != "string" {
		t.Errorf("data items should be strings, got %#v", items)
	}
}

func TestResolveGenericInstantiation_TwoParameters(t *testing.T) {
	reg := genericRegistry(t)
	name := reg.resolve("Pair[string,Item]")
	schema := reg.schemas[name]
	key := propOf(t, schema, "key")
	if key["type"] != "string" {
		t.Errorf("key should be a string, got %#v", key)
	}
	if got := refOf(t, schema, "value"); got != "#/components/schemas/Item" {
		t.Errorf("value should reference Item, got %q", got)
	}
}

// An instantiation whose argument is itself an instantiation.
func TestResolveGenericInstantiation_Nested(t *testing.T) {
	reg := genericRegistry(t)
	name := reg.resolve("Page[Wrapper[Item]]")
	if name != "Page-Wrapper-Item" {
		t.Fatalf("resolve returned %q, want Page-Wrapper-Item", name)
	}
	data := propOf(t, reg.schemas[name], "data")
	items, _ := data["items"].(map[string]interface{})
	if items["$ref"] != "#/components/schemas/Wrapper-Item" {
		t.Fatalf("data items should reference the inner instantiation, got %#v", items)
	}
	inner, ok := reg.schemas["Wrapper-Item"]
	if !ok {
		t.Fatalf("the inner instantiation was not registered; have %v", keysOf(reg.schemas))
	}
	if got := refOf(t, inner, "inner"); got != "#/components/schemas/Item" {
		t.Errorf("inner should reference Item, got %q", got)
	}
}

// An alias to an instantiation is how a project gives the envelope a readable name.
func TestResolveGenericInstantiation_ThroughAnAlias(t *testing.T) {
	reg := genericRegistry(t)
	reg.resolve("ItemPage")

	alias, ok := reg.schemas["ItemPage"]
	if !ok {
		t.Fatalf("ItemPage was not registered; have %v", keysOf(reg.schemas))
	}
	if alias["$ref"] != "#/components/schemas/Page-Item" {
		t.Fatalf("ItemPage should reference the instantiation, got %#v", alias)
	}
	if _, ok := reg.schemas["Page-Item"]; !ok {
		t.Errorf("the instantiation behind the alias was not registered; have %v", keysOf(reg.schemas))
	}
}

// A field whose type is an instantiation, inside an ordinary struct.
func TestResolveGenericInstantiation_AsAStructField(t *testing.T) {
	reg := genericRegistry(t)
	reg.resolve("Holder")

	if got := refOf(t, reg.schemas["Holder"], "page"); got != "#/components/schemas/Page-Item" {
		t.Errorf("page should reference the instantiation, got %q", got)
	}
	if _, ok := reg.schemas["Page-Item"]; !ok {
		t.Errorf("the instantiation was not registered; have %v", keysOf(reg.schemas))
	}
}

// Given the wrong number of arguments there is no honest schema to produce, so it is
// reported rather than guessed at.
func TestResolveGenericInstantiation_ArityMismatchWarns(t *testing.T) {
	reg := genericRegistry(t)
	name := reg.resolve("Pair[Item]")

	schema := reg.schemas[name]
	if schema["type"] != "object" || schema["properties"] != nil {
		t.Errorf("a mismatch should fall back to an untyped object, got %#v", schema)
	}
	if len(reg.warnings) == 0 {
		t.Fatal("a mismatch must be warned about")
	}
	found := false
	for _, w := range reg.warnings {
		if strings.Contains(w, "Pair") && strings.Contains(w, "type parameter") {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning named the type and its parameters: %q", reg.warnings)
	}
}

// Instantiating something that is not generic is a mistake worth reporting too.
func TestResolveGenericInstantiation_NonGenericBaseWarns(t *testing.T) {
	reg := genericRegistry(t)
	reg.resolve("Item[string]")
	if len(reg.warnings) == 0 {
		t.Fatal("instantiating a non-generic type must be warned about")
	}
}

func keysOf(m map[string]map[string]interface{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// End to end: an annotation naming an instantiation produces a response that references a
// real component, and the document's keys stay legal.
func TestGenerate_AnnotationNamingAnInstantiation(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "types.go", genericSource)
	handlerDir := t.TempDir()
	handler := writeGoFile(t, handlerDir, "handler.go", `package main

// @Summary List items
// @Produce json
// @Success 200 {object} types.Page[types.Item]
// @Router /items [get]
func ListItems() {}
`)

	g := New(Config{Title: "t", Version: "1", ScanPatterns: []string{handler}, TypeDirs: []string{dir}})
	spec := g.Generate()

	components, _ := spec["components"].(map[string]interface{})
	schemas, _ := components["schemas"].(map[string]map[string]interface{})
	if _, ok := schemas["Page-Item"]; !ok {
		t.Fatalf("the instantiation is missing from components; have %v", keysOf(schemas))
	}
	for key := range schemas {
		if !componentKey.MatchString(key) {
			t.Errorf("component key %q is not legal under the OpenAPI specification", key)
		}
	}

	paths, _ := spec["paths"].(map[string]map[string]PathItem)
	item := paths["/items"]["get"]
	content := item.Responses["200"].Content["application/json"].(map[string]interface{})
	schema, _ := content["schema"].(map[string]interface{})
	if schema["$ref"] != "#/components/schemas/Page-Item" {
		t.Errorf("the 200 response should reference the instantiation, got %#v", schema)
	}
	for _, w := range g.Warnings {
		if strings.Contains(w, "Page") {
			t.Errorf("unexpected warning about the instantiation: %q", w)
		}
	}
}

// The shapes a generic declaration can take beyond a plain struct of its parameter, and the
// arguments a caller can name. Each is something a project writing generics will reach for.

const genericEdgeSource = `package types

import "time"

type Item struct {
	Name string ` + "`json:\"name\"`" + `
}

// A generic whose body is not a struct.
type List[T any] []T

// A generic that reaches itself - a tree.
type Node[T any] struct {
	Value    T       ` + "`json:\"value\"`" + `
	Children []Node[T] ` + "`json:\"children\"`" + `
}

type Page[T any] struct {
	Data []T ` + "`json:\"data\"`" + `
}

type Wrapper[T any] struct {
	Inner T ` + "`json:\"inner\"`" + `
}

// Fields whose types are instantiations written in Go rather than in an annotation.
type Fields struct {
	Nested    Wrapper[Page[Item]] ` + "`json:\"nested\"`" + `
	Stamped   Page[time.Time]     ` + "`json:\"stamped\"`" + `
	Listed    Page[[]Item]        ` + "`json:\"listed\"`" + `
	Optional  Page[*Item]         ` + "`json:\"optional\"`" + `
}
`

func genericEdgeRegistry(t *testing.T) *schemaRegistry {
	t.Helper()
	dir := t.TempDir()
	writeGoFile(t, dir, "types.go", genericEdgeSource)
	return newSchemaRegistry([]string{dir})
}

// `type List[T any] []T` is an array, not an object, and saying otherwise would reject the
// list the handler sends.
func TestResolveGenericInstantiation_NonStructBody(t *testing.T) {
	reg := genericEdgeRegistry(t)
	name := reg.resolve("List[Item]")
	schema := reg.schemas[name]
	if schema["type"] != "array" {
		t.Fatalf("List[Item] should be an array, got %#v", schema)
	}
	items, _ := schema["items"].(map[string]interface{})
	if items["$ref"] != "#/components/schemas/Item" {
		t.Errorf("items should reference Item, got %#v", items)
	}
}

// A tree must terminate, and its children must reference the instantiation being built.
func TestResolveGenericInstantiation_SelfReferential(t *testing.T) {
	reg := genericEdgeRegistry(t)
	done := make(chan string, 1)
	go func() { done <- reg.resolve("Node[Item]") }()
	select {
	case name := <-done:
		children := propOf(t, reg.schemas[name], "children")
		items, _ := children["items"].(map[string]interface{})
		if items["$ref"] != "#/components/schemas/Node-Item" {
			t.Errorf("children should reference the instantiation itself, got %#v", items)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("resolving a self-referential generic did not terminate")
	}
}

// An argument whose JSON form is not its Go structure keeps that form.
func TestResolveGenericInstantiation_QualifiedArgument(t *testing.T) {
	reg := genericEdgeRegistry(t)
	name := reg.resolve("Page[time.Time]")
	if name != "Page-Time" {
		t.Fatalf("resolve returned %q, want Page-Time", name)
	}
	data := propOf(t, reg.schemas[name], "data")
	items, _ := data["items"].(map[string]interface{})
	if items["type"] != "string" || items["format"] != "date-time" {
		t.Errorf("a time argument should stay an RFC 3339 string, got %#v", items)
	}
}

// A pointer argument permits null, and earns a key of its own so it cannot be confused with
// the value's instantiation.
func TestResolveGenericInstantiation_PointerArgument(t *testing.T) {
	reg := genericEdgeRegistry(t)
	name := reg.resolve("Wrapper[*Item]")
	if name != "Wrapper-ItemOrNull" {
		t.Fatalf("resolve returned %q, want Wrapper-ItemOrNull", name)
	}
	inner := propOf(t, reg.schemas[name], "inner")
	if inner["nullable"] != true {
		t.Errorf("a pointer argument should be nullable, got %#v", inner)
	}
	// And the value instantiation stays separate.
	if reg.resolve("Wrapper[Item]") == name {
		t.Error("Wrapper[Item] and Wrapper[*Item] must not share a component")
	}
}

// Instantiations written as Go field types, which reach the resolver as AST rather than text.
func TestResolveGenericInstantiation_FieldTypes(t *testing.T) {
	reg := genericEdgeRegistry(t)
	reg.resolve("Fields")
	schema := reg.schemas["Fields"]

	for property, want := range map[string]string{
		"nested":   "#/components/schemas/Wrapper-Page-Item",
		"stamped":  "#/components/schemas/Page-Time",
		"listed":   "#/components/schemas/Page-ItemList",
		"optional": "#/components/schemas/Page-ItemOrNull",
	} {
		if got := refOf(t, schema, property); got != want {
			t.Errorf("%s should reference %q, got %q", property, want, got)
		}
	}

	// The nested instantiation's own component describes the inner page.
	nested, ok := reg.schemas["Wrapper-Page-Item"]
	if !ok {
		t.Fatalf("the nested instantiation was not registered; have %v", keysOf(reg.schemas))
	}
	if got := refOf(t, nested, "inner"); got != "#/components/schemas/Page-Item" {
		t.Errorf("inner should reference Page-Item, got %q", got)
	}
	for key := range reg.schemas {
		if !componentKey.MatchString(key) {
			t.Errorf("component key %q is not legal under the OpenAPI specification", key)
		}
	}
}

// A base outside the configured TypeDirs cannot be described, and that is said rather than
// guessed at.
func TestResolveGenericInstantiation_BaseNotInTypeDirs(t *testing.T) {
	reg := newSchemaRegistry([]string{t.TempDir()})
	name := reg.resolve("Page[Item]")
	if schema := reg.schemas[name]; schema["type"] != "object" || schema["properties"] != nil {
		t.Errorf("an unfindable base should fall back to an untyped object, got %#v", schema)
	}
	found := false
	for _, w := range reg.warnings {
		if strings.Contains(w, "TypeDirs") {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning said the base could not be found: %q", reg.warnings)
	}
}

// A second reference to the same instantiation reuses the component rather than rebuilding it.
func TestResolveGenericInstantiation_SecondReferenceReusesTheComponent(t *testing.T) {
	reg := genericEdgeRegistry(t)
	first := reg.resolve("Page[Item]")
	before := len(reg.schemas)
	second := reg.resolve("Page[Item]")
	if first != second {
		t.Errorf("the same instantiation gave two names: %q and %q", first, second)
	}
	if len(reg.schemas) != before {
		t.Errorf("resolving it again registered something new: %v", keysOf(reg.schemas))
	}
}

// A type argument can be anything Go accepts, including shapes this generator has no
// spelling for. The document must stay legal and the gap must be stated.
func TestGenericSchemaName_SanitisesAnUnnameableArgument(t *testing.T) {
	got := genericSchemaName("Page", []string{"map[string]Item"})
	if !componentKey.MatchString(got) {
		t.Errorf("%q is not a legal component key", got)
	}
}

func TestParseGenericName_TrailingEmptyArgument(t *testing.T) {
	if _, _, ok := parseGenericName("Pair[string,]"); ok {
		t.Error("an argument list with a trailing comma is not an instantiation")
	}
}

func TestResolveGenericInstantiation_UnnameableFieldArgumentWarns(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "types.go", `package types

type Item struct {
	Name string `+"`json:\"name\"`"+`
}

type Page[T any] struct {
	Data []T `+"`json:\"data\"`"+`
}

type Holder struct {
	Mapped Page[map[string]Item] `+"`json:\"mapped\"`"+`
}
`)
	reg := newSchemaRegistry([]string{dir})
	reg.resolve("Holder")

	mapped := propOf(t, reg.schemas["Holder"], "mapped")
	if mapped["type"] != "object" {
		t.Errorf("an unnameable argument should leave an untyped object, got %#v", mapped)
	}
	found := false
	for _, w := range reg.warnings {
		if strings.Contains(w, "cannot name") {
			found = true
		}
	}
	if !found {
		t.Errorf("the gap was not reported: %q", reg.warnings)
	}
	for key := range reg.schemas {
		if !componentKey.MatchString(key) {
			t.Errorf("component key %q is not legal", key)
		}
	}
}

// The renderer meets legal Go it has no spelling for - a map, a func, a channel - wherever a
// type can appear. None of it may panic, and each has to degrade to "cannot name this". These
// shapes cannot be written as an instantiation in source, so they are built directly.
func TestTypeExprText_DegradesOnUnnameableShapes(t *testing.T) {
	reg := newSchemaRegistry(nil)
	unnameable := &ast.MapType{Key: ast.NewIdent("string"), Value: ast.NewIdent("Item")}

	cases := []struct {
		name string
		expr ast.Expr
	}{
		{"a bare map", unnameable},
		{"a list of maps", &ast.ArrayType{Elt: unnameable}},
		{"an instantiation of a map", &ast.IndexExpr{X: unnameable, Index: ast.NewIdent("Item")}},
		{"an instantiation by a map", &ast.IndexExpr{X: ast.NewIdent("Page"), Index: unnameable}},
		{"a multi-argument instantiation by a map", &ast.IndexListExpr{
			X:       ast.NewIdent("Pair"),
			Indices: []ast.Expr{ast.NewIdent("string"), unnameable},
		}},
		{"a pointer to a map", &ast.StarExpr{X: unnameable}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if name, ok := reg.typeExprText(c.expr); ok {
				t.Errorf("expected no name, got %q", name)
			}
		})
	}

	// A selector whose receiver is not a bare identifier still names the type.
	deep := &ast.SelectorExpr{
		X:   &ast.SelectorExpr{X: ast.NewIdent("pkg"), Sel: ast.NewIdent("Nested")},
		Sel: ast.NewIdent("Deep"),
	}
	if name, ok := reg.typeExprText(deep); !ok || name != "Deep" {
		t.Errorf("nested selector = (%q, %v), want (\"Deep\", true)", name, ok)
	}
}

// A field whose base cannot be named leaves an untyped object and says so, rather than
// referencing a component whose key would be nonsense.
func TestGenericFieldSchema_UnnameableBaseWarns(t *testing.T) {
	reg := newSchemaRegistry(nil)
	unnameable := &ast.MapType{Key: ast.NewIdent("string"), Value: ast.NewIdent("Item")}

	schema := reg.genericFieldSchema(unnameable, []ast.Expr{ast.NewIdent("Item")})
	if schema["type"] != "object" {
		t.Errorf("expected an untyped object, got %#v", schema)
	}
	found := false
	for _, w := range reg.warnings {
		if strings.Contains(w, "could not be named") {
			found = true
		}
	}
	if !found {
		t.Errorf("the gap was not reported: %q", reg.warnings)
	}
}

// A type parameter's name is scoped to its own declaration. A parameter called Item does not
// change what the type Item means anywhere else, and binding it registry-wide made an
// ordinary struct's field describe the generic's argument instead of its own type.
func TestResolveGenericInstantiation_BindingDoesNotLeakIntoOtherDeclarations(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "types.go", `package types

type Item struct {
	Name string `+"`json:\"name\"`"+`
}

type Thing struct {
	Size int `+"`json:\"size\"`"+`
}

// Meta has a field of the ordinary type Item.
type Meta struct {
	Item Item `+"`json:\"item\"`"+`
}

// The parameter is deliberately named Item too, shadowing the type inside this body only.
type Envelope[Item any] struct {
	Data []Item `+"`json:\"data\"`"+`
	Meta Meta   `+"`json:\"meta\"`"+`
}
`)
	reg := newSchemaRegistry([]string{dir})
	name := reg.resolve("Envelope[Thing]")

	// Inside the envelope, Item is the argument.
	data := propOf(t, reg.schemas[name], "data")
	items, _ := data["items"].(map[string]interface{})
	if items["$ref"] != "#/components/schemas/Thing" {
		t.Errorf("data items should reference the argument Thing, got %#v", items)
	}

	// Inside Meta, which is its own declaration, Item is the type Item.
	meta, ok := reg.schemas["Meta"]
	if !ok {
		t.Fatalf("Meta was not registered; have %v", keysOf(reg.schemas))
	}
	if got := refOf(t, meta, "item"); got != "#/components/schemas/Item" {
		t.Errorf("Meta.item should reference Item, got %q - the binding leaked out of the generic", got)
	}
}

// Two different instantiations can reduce to the same component name, because the name drops
// package qualifiers. The first one then describes both, and that is said rather than left to
// be discovered in the document.
func TestResolveGenericInstantiation_CollidingNamesWarn(t *testing.T) {
	dir := t.TempDir()
	writeGoFile(t, dir, "types.go", `package types

type Item struct {
	Name string `+"`json:\"name\"`"+`
}

type Page[T any] struct {
	Data []T `+"`json:\"data\"`"+`
}
`)
	reg := newSchemaRegistry([]string{dir})

	first := reg.resolve("Page[Item]")
	second := reg.resolve("Page[other.Item]")
	if first != second {
		t.Fatalf("the two instantiations should reduce to one name, got %q and %q", first, second)
	}
	found := false
	for _, w := range reg.warnings {
		if strings.Contains(w, "same component") {
			found = true
		}
	}
	if !found {
		t.Errorf("the collision was not reported: %q", reg.warnings)
	}
}

// Naming a generic type without its arguments cannot be described - the parameters have
// nothing to stand for. The message has to say that, rather than report the parameter as a
// type nobody can find, which is what a reader would then go looking for.
func TestResolveBareGenericName_SaysItNeedsArguments(t *testing.T) {
	reg := genericRegistry(t)
	name := reg.resolve("Page")

	if schema := reg.schemas[name]; schema["properties"] != nil {
		t.Errorf("a bare generic name should not be described, got %#v", schema)
	}
	if len(reg.warnings) == 0 {
		t.Fatal("naming a generic without arguments must be warned about")
	}
	for _, w := range reg.warnings {
		if strings.Contains(w, "type T is not in any configured TypeDirs") {
			t.Errorf("the warning blames the type parameter instead of the missing arguments: %q", w)
		}
	}
	found := false
	for _, w := range reg.warnings {
		if strings.Contains(w, "Page") && strings.Contains(w, "type parameter") {
			found = true
		}
	}
	if !found {
		t.Errorf("no warning explained that arguments are needed: %q", reg.warnings)
	}
}
