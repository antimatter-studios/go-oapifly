package oapifly

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Every document a test generates is checked against the OpenAPI 3.1 schema, with its
// schemas held to the 3.1 dialect (the 2022-10-07 schema-base), so a test of any feature is
// also a test that the feature writes valid OpenAPI 3.1 (#16).

type generatedDocument struct {
	test string
	json []byte
	err  error
}

var (
	generatedMu        sync.Mutex
	generatedDocuments []generatedDocument
)

func TestMain(m *testing.M) {
	documentGenerated = func(doc map[string]interface{}) {
		data, err := json.Marshal(doc)
		generatedMu.Lock()
		defer generatedMu.Unlock()
		generatedDocuments = append(generatedDocuments, generatedDocument{test: callingTest(), json: data, err: err})
	}
	code := m.Run()
	if problems := invalidDocuments(generatedDocuments); len(problems) > 0 {
		fmt.Fprintf(os.Stderr, "%d of %d generated documents are not valid OpenAPI 3.1:\n", len(problems), len(generatedDocuments))
		for _, problem := range problems {
			fmt.Fprintln(os.Stderr, problem)
		}
		code = 1
	}
	os.Exit(code)
}

// callingTest names the test that called Generate, for the report.
func callingTest() string {
	pcs := make([]uintptr, 64)
	frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
	for {
		frame, more := frames.Next()
		if i := strings.LastIndex(frame.Function, ".Test"); i >= 0 && !strings.Contains(frame.Function, ".TestMain") {
			return frame.Function[i+1:]
		}
		if !more {
			return "unknown test"
		}
	}
}

func openAPI31Schema() (*jsonschema.Schema, error) {
	compiler := jsonschema.NewCompiler()
	files, err := filepath.Glob(filepath.Join("testdata", "oas-3.1", "*.json"))
	if err != nil || len(files) == 0 {
		return nil, fmt.Errorf("the OpenAPI 3.1 schemas are missing from testdata/oas-3.1: %v", err)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return nil, err
		}
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		id, _ := doc.(map[string]interface{})["$id"].(string)
		if err := compiler.AddResource(id, doc); err != nil {
			return nil, err
		}
	}
	return compiler.Compile("https://spec.openapis.org/oas/3.1/schema-base/2022-10-07")
}

func invalidDocuments(docs []generatedDocument) []string {
	schema, err := openAPI31Schema()
	if err != nil {
		return []string{err.Error()}
	}
	var problems []string
	for _, doc := range docs {
		if doc.err != nil {
			problems = append(problems, fmt.Sprintf("%s: the document does not encode: %v", doc.test, doc.err))
			continue
		}
		if err := documentProblem(schema, doc.json); err != nil {
			problems = append(problems, fmt.Sprintf("%s: %v\n%s", doc.test, err, doc.json))
		}
	}
	return problems
}

// documentProblem checks one document against the 3.1 schema. JSON Schema ignores keywords
// it does not know, so the schema accepts 3.0's nullable, which no 3.1 reader applies; that
// is looked for separately.
func documentProblem(schema *jsonschema.Schema, data []byte) error {
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return err
	}
	if err := schema.Validate(value); err != nil {
		return err
	}
	if path := findKey(value, "nullable", "#"); path != "" {
		return fmt.Errorf("%s uses nullable, which OpenAPI 3.1 does not have", path)
	}
	return nil
}

func findKey(value interface{}, key, path string) string {
	switch v := value.(type) {
	case map[string]interface{}:
		if _, ok := v[key]; ok {
			return path
		}
		for k, child := range v {
			if found := findKey(child, key, path+"/"+k); found != "" {
				return found
			}
		}
	case []interface{}:
		for i, child := range v {
			if found := findKey(child, key, fmt.Sprintf("%s/%d", path, i)); found != "" {
				return found
			}
		}
	}
	return ""
}

// The check itself refuses what 3.1 does not allow, so a passing suite means something.
func TestOpenAPI31SchemaRefusesA30Spelling(t *testing.T) {
	schema, err := openAPI31Schema()
	if err != nil {
		t.Fatal(err)
	}
	valid := `{"openapi":"3.1.0","info":{"title":"t","version":"1"},"paths":{},` +
		`"components":{"schemas":{"A":{"type":["string","null"]}}}}`
	for _, c := range []struct {
		doc  string
		want bool
	}{
		{valid, true},
		{strings.Replace(valid, `"type":["string","null"]`, `"type":"string","nullable":true`, 1), false},
		{strings.Replace(valid, `"type":["string","null"]`, `"type":"integer","minimum":1,"exclusiveMinimum":true`, 1), false},
		{strings.Replace(valid, `"type":["string","null"]`, `"type":"strin"`, 1), false},
		{strings.Replace(valid, `"3.1.0"`, `"3.0.0"`, 1), false},
	} {
		if got := documentProblem(schema, []byte(c.doc)) == nil; got != c.want {
			t.Errorf("%s: valid %v, want %v", c.doc, got, c.want)
		}
	}
}
