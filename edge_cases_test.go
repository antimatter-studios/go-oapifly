package oapifly

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// The branches a well-formed annotation never reaches: malformed input that the parser has
// to refuse or pass through without inventing anything. Each is a pure function, so a
// table is enough.

func TestExtractTagValue_Malformed(t *testing.T) {
	tests := []struct {
		name, tag, key, want string
	}{
		{"key with no opening quote", `json:username`, "json", ""},
		{"key at end of tag", `json:`, "json", ""},
		{"key with no closing quote", `json:"username`, "json", ""},
		{"empty tag", ``, "json", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractTagValue(tt.tag, tt.key); got != tt.want {
				t.Errorf("extractTagValue(%q, %q) = %q, want %q", tt.tag, tt.key, got, tt.want)
			}
		})
	}
}

func TestResolveContentType_Fallbacks(t *testing.T) {
	tests := []struct {
		name, input, want string
	}{
		{"unknown word is json", "yaml", "application/json"},
		{"full mime type passes through", "application/vnd.api+json", "application/vnd.api+json"},
		{"full mime type is trimmed", "  application/pdf  ", "application/pdf"},
		{"case is normalised for known words", "JSON", "application/json"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveContentType(tt.input); got != tt.want {
				t.Errorf("resolveContentType(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseExample_Malformed(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"too few fields", `accepted request`},
		{"response without status", `accepted response {"ok":true}`},
		{"unknown side", `accepted sideways {"ok":true}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := parseExample(tt.input); ok {
				t.Errorf("parseExample(%q) accepted a malformed example", tt.input)
			}
		})
	}
}

func TestSplitExampleSummary_EdgeCases(t *testing.T) {
	tests := []struct {
		name, input, wantBody, wantSummary string
	}{
		{"no summary", `{"a":1}`, `{"a":1}`, ""},
		{"quoted summary", `{"a":1} "A thing"`, `{"a":1}`, "A thing"},
		// A trailing quote with no opening one earlier is part of the value, not a summary.
		{"lone trailing quote", `abc"`, `abc"`, ""},
		// The whole text is one quoted string: an opening quote at position 0 is the value's
		// own quote, so nothing is peeled off as a summary.
		{"entire value quoted", `"just text"`, `"just text"`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, summary := splitExampleSummary(tt.input)
			if body != tt.wantBody || summary != tt.wantSummary {
				t.Errorf("splitExampleSummary(%q) = (%q, %q), want (%q, %q)", tt.input, body, summary, tt.wantBody, tt.wantSummary)
			}
		})
	}
}

// embeddedTypeName has to name every shape an embedded field can take, and say what it
// cannot name rather than guess.
func TestEmbeddedTypeName_Shapes(t *testing.T) {
	src := `package p
type A struct {
	Plain
	*Pointer
	pkg.Qualified
}`
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "a.go", src, 0)
	if err != nil {
		t.Fatal(err)
	}
	st := f.Decls[0].(*ast.GenDecl).Specs[0].(*ast.TypeSpec).Type.(*ast.StructType)

	tests := []struct {
		field                 int
		wantName, wantDisplay string
	}{
		{0, "Plain", "Plain"},
		{1, "Pointer", "Pointer"},
		{2, "Qualified", "pkg.Qualified"},
	}
	for _, tt := range tests {
		name, display := embeddedTypeName(st.Fields.List[tt.field].Type)
		if name != tt.wantName || display != tt.wantDisplay {
			t.Errorf("field %d: embeddedTypeName = (%q, %q), want (%q, %q)", tt.field, name, display, tt.wantName, tt.wantDisplay)
		}
	}

	// The remaining shapes cannot be written as an embedded field in Go source, so they are
	// built by hand. A selector whose receiver is not a bare identifier still names the
	// type, with the display falling back to the bare name because the qualifier cannot be
	// spelled; anything that is not a named type at all reports nothing.
	deep := &ast.SelectorExpr{
		X:   &ast.SelectorExpr{X: ast.NewIdent("pkg"), Sel: ast.NewIdent("Nested")},
		Sel: ast.NewIdent("Deep"),
	}
	if name, display := embeddedTypeName(deep); name != "Deep" || display != "Deep" {
		t.Errorf("nested selector: embeddedTypeName = (%q, %q), want (\"Deep\", \"Deep\")", name, display)
	}
	slice := &ast.ArrayType{Elt: ast.NewIdent("Thing")}
	if name, display := embeddedTypeName(slice); name != "" || display != "" {
		t.Errorf("slice: embeddedTypeName = (%q, %q), want nothing", name, display)
	}
}

func TestIsStructRef_ListShapes(t *testing.T) {
	tests := []struct {
		dataType string
		want     bool
	}{
		{"[]int", false},
		{"[]string", false},
		{"array", false},
		{"[]Item", true},
	}
	for _, tt := range tests {
		t.Run(tt.dataType, func(t *testing.T) {
			if got := isStructRef(tt.dataType); got != tt.want {
				t.Errorf("isStructRef(%q) = %v, want %v", tt.dataType, got, tt.want)
			}
		})
	}
}
