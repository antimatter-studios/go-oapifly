package oapifly

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"reflect"
	"strconv"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ExampleFile is a document's example pairs (#20): by operationId, then by example name, the
// exchanges an operation is shown and tested with. It is kept beside the document as
// <document>-example.json (ExamplesFileFor), so a reader finds a document's examples without
// being told where they are. A contract tester sends each named request and expects the
// response of the same name.
type ExampleFile map[string]map[string]ExamplePair

// ExamplePair is one exchange: a request and the response it provokes.
type ExamplePair struct {
	// Operation sends this pair to another operationId while keeping it in its
	// original group in the companion file. Empty means the group's operationId.
	Operation   string `json:"operation,omitempty"`
	Summary     string `json:"summary,omitempty"`
	Description string `json:"description,omitempty"`
	// Requires is what proving the pair needs, by kind: a fault injected, a principal
	// signed in, an oversized body sent (#62). Empty means nothing. The document carries
	// it on each of the pair's Example Objects as x-requires, for a tester to choose what
	// it can prove; Config.ExampleRequirements is its vocabulary.
	Requires map[string]string `json:"requires,omitempty"`
	Request  ExampleRequest    `json:"request"`
	Response ExampleResponse   `json:"response"`
}

// ExampleRequest is the request half of a pair: its parameter values by where they are and
// their name, and its body.
type ExampleRequest struct {
	Path   map[string]interface{} `json:"path,omitempty"`
	Query  map[string]interface{} `json:"query,omitempty"`
	Header map[string]interface{} `json:"header,omitempty"`
	// Body is the request body, absent for a request with none.
	Body json.RawMessage `json:"body,omitempty"`
	// MediaType names which of the operation's request media types Body is in; the first,
	// in order, when it is empty.
	MediaType string `json:"mediaType,omitempty"`
	// Invalid marks a request meant to break its schema, the request of a 400 or 422 pair:
	// it is expected not to validate, and every other request to.
	Invalid bool `json:"invalid,omitempty"`
}

// ExampleResponse is the response half of a pair.
type ExampleResponse struct {
	Status int `json:"status"`
	// Body is the response body, absent for a response with none or one left to its schema.
	Body json.RawMessage `json:"body,omitempty"`
	// MediaType names which of the response's media types Body is in; the first, in order,
	// when it is empty.
	MediaType string `json:"mediaType,omitempty"`
}

// ExamplesFileFor is the name of the example file that goes with a document: openapi.json's
// is openapi-example.json.
func ExamplesFileFor(document string) string {
	dot := strings.LastIndex(document, ".")
	if dot <= strings.LastIndex(document, "/") {
		return document + "-example.json"
	}
	return document[:dot] + "-example" + document[dot:]
}

// ReadExamples reads an example file; for one that does not exist, the error wraps
// fs.ErrNotExist.
func ReadExamples(fsys fs.FS, name string) (ExampleFile, error) {
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		return nil, err
	}
	var file ExampleFile
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return file, nil
}

// MarshalExamples writes an example file as it is kept: indented, keys in order, ending in a
// newline, so a file that changes nothing gives the same bytes.
func MarshalExamples(file ExampleFile) ([]byte, error) {
	if file == nil {
		file = ExampleFile{}
	}
	b, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// located is an operation found in the document by its operationId.
type located struct {
	path, method string
}

// operationsByID indexes the document's operations by operationId; an id two operations
// share is left out, since an example under it could mean either.
func operationsByID(paths map[string]map[string]PathItem) map[string]located {
	byID := map[string]located{}
	shared := map[string]bool{}
	for _, path := range sortedPaths(paths) {
		for _, method := range pathItemMethods {
			item, ok := paths[path][method]
			if !ok || item.OperationID == "" {
				continue
			}
			if _, taken := byID[item.OperationID]; taken {
				shared[item.OperationID] = true
			}
			byID[item.OperationID] = located{path, method}
		}
	}
	for id := range shared {
		delete(byID, id)
	}
	return byID
}

// mergeExamples puts the file's pairs into the document: each request body into its request
// media type's examples, each parameter value into its parameter's, and each response body
// into its response media type's, all under the pair's name. What it cannot place is
// reported, and so is every documented status of an operation that no pair shows, and every
// body that does not validate as it should.
func mergeExamples(paths map[string]map[string]PathItem, file ExampleFile, components map[string]map[string]interface{}, requirements map[string][]string, reg *schemaRegistry) {
	file = examplesByOperation(file, reg)
	byID := operationsByID(paths)
	shown := map[string]map[string]bool{} // operationId -> statuses a pair shows
	for _, id := range sortedKeys(file) {
		at, ok := byID[id]
		if !ok {
			for _, name := range sortedKeys(file[id]) {
				reg.warn(Warning{Kind: WarnExampleOrphaned, OperationID: id, Example: name},
					"example %s of %s: no operation in the document has that operationId", name, id)
			}
			continue
		}
		// The parameters, request body and responses can be shared with other operations
		// (a middleware's 401, say), so the operation gets its own before examples go in.
		item := ownCopy(paths[at.path][at.method])
		about := Warning{OperationID: id, Method: strings.ToUpper(at.method), Path: at.path, Tags: item.Tags}
		for _, name := range sortedKeys(file[id]) {
			pair := file[id][name]
			w := about
			w.Example = name
			checkRequirements(pair, requirements, w, reg)
			if placeExample(&item, name, pair, w, reg) {
				if shown[id] == nil {
					shown[id] = map[string]bool{}
				}
				shown[id][strconv.Itoa(pair.Response.Status)] = true
			}
		}
		paths[at.path][at.method] = item
	}

	validator := newExampleValidator(paths, components)
	for _, path := range sortedPaths(paths) {
		for _, method := range pathItemMethods {
			item, ok := paths[path][method]
			if !ok {
				continue
			}
			about := Warning{OperationID: item.OperationID, Method: strings.ToUpper(method), Path: path, Tags: item.Tags}
			for _, status := range sortedKeys(item.Responses) {
				if status == "default" || shown[item.OperationID][status] {
					continue
				}
				w := about
				w.Kind, w.Status = WarnExampleMissing, status
				reg.warn(w, "%s %s: no example shows its %s response", strings.ToUpper(method), path, status)
			}
			if pairs, ok := file[item.OperationID]; ok && byID[item.OperationID] == (located{path, method}) {
				validator.check(path, method, item, pairs, about, reg)
			}
		}
	}
}

// examplesByOperation resolves a pair's optional operation without rewriting the
// companion file. Two groups may not define the same name for the same operation:
// neither request can be paired unambiguously with a response under that name.
func examplesByOperation(file ExampleFile, reg *schemaRegistry) ExampleFile {
	out := ExampleFile{}
	origins := map[string]map[string]string{}
	for _, group := range sortedKeys(file) {
		for _, name := range sortedKeys(file[group]) {
			pair := file[group][name]
			id := pair.Operation
			if id == "" {
				id = group
			}
			if out[id] == nil {
				out[id] = map[string]ExamplePair{}
				origins[id] = map[string]string{}
			}
			if previous, exists := origins[id][name]; exists {
				reg.warn(Warning{Kind: WarnExampleOrphaned, OperationID: id, Example: name},
					"example %s of %s: groups %s and %s target the same operation and name; the first is used", name, id, previous, group)
				continue
			}
			out[id][name] = pair
			origins[id][name] = group
		}
	}
	return out
}

// ownCopy is item with its own parameters, request body and responses, so changing them
// changes no other operation.
func ownCopy(item PathItem) PathItem {
	item.Parameters = append([]Parameter(nil), item.Parameters...)
	for i := range item.Parameters {
		if item.Parameters[i].Examples != nil {
			item.Parameters[i].Examples = copyMap(item.Parameters[i].Examples)
		}
	}
	if item.RequestBody != nil {
		body := *item.RequestBody
		item.RequestBody = &body
	}
	responses := make(map[string]Response, len(item.Responses))
	for status, r := range item.Responses {
		responses[status] = r
	}
	item.Responses = responses
	return item
}

// placeExample puts one pair into an operation, reporting what does not fit; it reports
// whether the pair's response is one the operation documents.
func placeExample(item *PathItem, name string, pair ExamplePair, about Warning, reg *schemaRegistry) bool {
	orphan := func(w Warning, format string, args ...interface{}) {
		w.Kind = WarnExampleOrphaned
		reg.warn(w, "example %s of %s: "+format, append([]interface{}{name, item.OperationID}, args...)...)
	}
	summary := map[string]interface{}{}
	if pair.Summary != "" {
		summary["summary"] = pair.Summary
	}
	if pair.Description != "" {
		summary["description"] = pair.Description
	}
	if len(pair.Requires) > 0 {
		requires := make(map[string]interface{}, len(pair.Requires))
		for kind, value := range pair.Requires {
			requires[kind] = value
		}
		summary["x-requires"] = requires
	}

	if outsideParameterEnum(item.Parameters, pair) {
		// A Parameter Object's examples must be enum values, or Dredd-style testers refuse
		// the whole description. The pair stays in the file for the application's own tests
		// and still counts as showing its status.
		return true
	}

	for _, in := range []string{"path", "query", "header"} {
		values := map[string]map[string]interface{}{"path": pair.Request.Path, "query": pair.Request.Query, "header": pair.Request.Header}[in]
		for _, param := range sortedKeys(values) {
			i := parameterIndex(item.Parameters, in, param)
			if i < 0 {
				w := about
				w.Parameter = param
				orphan(w, "the operation has no %s parameter %q", in, param)
				continue
			}
			p := &item.Parameters[i]
			if p.Examples == nil {
				p.Examples = map[string]interface{}{}
				// example and examples are mutually exclusive in a Parameter Object.
				if p.Example != nil {
					p.Examples["example"] = map[string]interface{}{"value": p.Example}
					p.Example = nil
				}
			}
			p.Examples[name] = withValue(summary, values[param])
		}
	}

	if len(pair.Request.Body) > 0 {
		media := ""
		if item.RequestBody != nil {
			media = pickMedia(item.RequestBody.Content, pair.Request.MediaType)
		}
		if media == "" {
			orphan(about, "the operation takes no request body in %s", orNamed(pair.Request.MediaType, "any media type"))
		} else {
			item.RequestBody.Content = withNamedExample(item.RequestBody.Content, media, name, withValue(summary, jsonValue(pair.Request.Body)))
		}
	}

	status := strconv.Itoa(pair.Response.Status)
	response, documented := item.Responses[status]
	if !documented {
		w := about
		w.Status = status
		orphan(w, "the operation does not document a %s response", status)
		return false
	}
	if len(response.Content) == 0 {
		if len(pair.Response.Body) > 0 {
			w := about
			w.Status = status
			orphan(w, "its %s response has no body", status)
		}
		return true
	}
	media := pickMedia(response.Content, pair.Response.MediaType)
	if media == "" {
		w := about
		w.Status = status
		orphan(w, "its %s response is not in %s", status, pair.Response.MediaType)
		return true
	}
	example := summary
	if len(pair.Response.Body) > 0 {
		example = withValue(summary, jsonValue(pair.Response.Body))
	}
	response.Content = withNamedExample(response.Content, media, name, copyMap(example))
	item.Responses[status] = response
	return true
}

// checkRequirements reports each requirement of a pair that the vocabulary does not list:
// an unknown kind, or a value its kind does not allow.
func checkRequirements(pair ExamplePair, requirements map[string][]string, about Warning, reg *schemaRegistry) {
	for _, kind := range sortedKeys(pair.Requires) {
		value := pair.Requires[kind]
		allowed, known := requirements[kind]
		w := about
		w.Kind = WarnExampleRequirementUnknown
		switch {
		case !known:
			reg.warn(w, "example %s of %s: requires %q, which is not a known kind of requirement", about.Example, about.OperationID, kind)
		case !containsString(allowed, value):
			reg.warn(w, "example %s of %s: requires %s %q, which is not one of %s", about.Example, about.OperationID, kind, value, strings.Join(allowed, ", "))
		}
	}
}

func containsString(list []string, s string) bool {
	for _, item := range list {
		if item == s {
			return true
		}
	}
	return false
}

// outsideParameterEnum reports whether one of the pair's parameter values is not among
// the enum values its parameter allows.
func outsideParameterEnum(params []Parameter, pair ExamplePair) bool {
	for in, values := range map[string]map[string]interface{}{"path": pair.Request.Path, "query": pair.Request.Query, "header": pair.Request.Header} {
		for param, value := range values {
			i := parameterIndex(params, in, param)
			if i < 0 {
				continue
			}
			enum := reflect.ValueOf(params[i].Schema["enum"])
			if enum.Kind() != reflect.Slice {
				continue
			}
			got, _ := json.Marshal(value)
			allowed := false
			for j := 0; j < enum.Len() && !allowed; j++ {
				option, _ := json.Marshal(enum.Index(j).Interface())
				allowed = bytes.Equal(got, option)
			}
			if !allowed {
				return true
			}
		}
	}
	return false
}

// parameterIndex is the index of the parameter in and named name, -1 when there is none.
func parameterIndex(params []Parameter, in, name string) int {
	for i, p := range params {
		if p.In == in && (p.Name == name || (in == "header" && strings.EqualFold(p.Name, name))) {
			return i
		}
	}
	return -1
}

// pickMedia is the media type of content a pair names, or the first in order when it names
// none; "" when content has no such media type.
func pickMedia(content map[string]interface{}, named string) string {
	if named != "" {
		if _, ok := content[named]; ok {
			return named
		}
		return ""
	}
	keys := sortedKeys(content)
	if len(keys) == 0 {
		return ""
	}
	return keys[0]
}

// withNamedExample is content with an Example Object under a name in a media type's
// examples. content is copied, not changed: another operation may share it.
func withNamedExample(content map[string]interface{}, media, name string, example map[string]interface{}) map[string]interface{} {
	out := copyMap(content)
	object, _ := content[media].(map[string]interface{})
	object = copyMap(object)
	examples := map[string]interface{}{}
	switch existing := object["examples"].(type) {
	case map[string]interface{}:
		examples = copyMap(existing)
	case map[string]Example:
		// Named by annotations: kept beside the file's.
		for k, v := range existing {
			examples[k] = v
		}
	}
	examples[name] = example
	object["examples"] = examples
	out[media] = object
	return out
}

// withValue is an Example Object: the summary and description, and the value.
func withValue(summary map[string]interface{}, value interface{}) map[string]interface{} {
	out := copyMap(summary)
	out["value"] = value
	return out
}

func copyMap(m map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// jsonValue is a raw JSON value as data.
func jsonValue(raw json.RawMessage) interface{} {
	var v interface{}
	if err := json.Unmarshal(raw, &v); err != nil {
		return string(raw)
	}
	return v
}

func orNamed(s, otherwise string) string {
	if s == "" {
		return otherwise
	}
	return s
}

// exampleValidator checks example bodies against the schemas of the document they are in.
type exampleValidator struct {
	compiler *jsonschema.Compiler
	err      error
}

// exampleDocument is the URL the document is loaded under, to compile its schemas from.
const exampleDocument = "https://go-oapifly.invalid/document.json"

func newExampleValidator(paths map[string]map[string]PathItem, components map[string]map[string]interface{}) *exampleValidator {
	data, err := json.Marshal(map[string]interface{}{"paths": paths, "components": map[string]interface{}{"schemas": components}})
	if err != nil {
		return &exampleValidator{err: err}
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return &exampleValidator{err: err}
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource(exampleDocument, doc); err != nil {
		return &exampleValidator{err: err}
	}
	return &exampleValidator{compiler: c}
}

// pointer is a JSON Pointer to a location in the document.
func pointer(tokens ...string) string {
	escaped := make([]string, len(tokens))
	for i, t := range tokens {
		escaped[i] = strings.ReplaceAll(strings.ReplaceAll(t, "~", "~0"), "/", "~1")
	}
	return exampleDocument + "#/" + strings.Join(escaped, "/")
}

// validates reports whether value is valid against the schema at location, and whether
// the schema could be compiled at all.
func (v *exampleValidator) validates(location string, value json.RawMessage) (valid, checked bool, detail string) {
	if v.err != nil {
		return false, false, v.err.Error()
	}
	schema, err := v.compiler.Compile(location)
	if err != nil {
		return false, false, err.Error()
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(value))
	if err != nil {
		return false, true, err.Error()
	}
	if err := schema.Validate(instance); err != nil {
		return false, true, firstLine(err.Error())
	}
	return true, true, ""
}

// check validates an operation's example bodies: a request marked invalid must not
// validate, and every other JSON body must.
func (v *exampleValidator) check(path, method string, item PathItem, pairs map[string]ExamplePair, about Warning, reg *schemaRegistry) {
	for _, name := range sortedKeys(pairs) {
		pair := pairs[name]
		w := about
		w.Kind, w.Example = WarnExampleInvalid, name
		if len(pair.Request.Body) > 0 && item.RequestBody != nil {
			if media := pickMedia(item.RequestBody.Content, pair.Request.MediaType); media != "" && isJSONMedia(media) {
				valid, checked, detail := v.validates(pointer("paths", path, method, "requestBody", "content", media, "schema"), pair.Request.Body)
				switch {
				case !checked:
					reg.warn(w, "example %s of %s: its request body could not be checked: %s", name, item.OperationID, detail)
				case valid && pair.Request.Invalid:
					reg.warn(w, "example %s of %s: its request is marked invalid, and its body validates", name, item.OperationID)
				case !valid && !pair.Request.Invalid:
					reg.warn(w, "example %s of %s: its request body does not validate: %s", name, item.OperationID, detail)
				}
			}
		}
		status := strconv.Itoa(pair.Response.Status)
		if response, ok := item.Responses[status]; ok && len(pair.Response.Body) > 0 {
			if media := pickMedia(response.Content, pair.Response.MediaType); media != "" && isJSONMedia(media) {
				valid, checked, detail := v.validates(pointer("paths", path, method, "responses", status, "content", media, "schema"), pair.Response.Body)
				w.Status = status
				switch {
				case !checked:
					reg.warn(w, "example %s of %s: its %s response body could not be checked: %s", name, item.OperationID, status, detail)
				case !valid:
					reg.warn(w, "example %s of %s: its %s response body does not validate: %s", name, item.OperationID, status, detail)
				}
			}
		}
	}
}

// firstLine is the first line of a validation error, which says where and what.
func firstLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	out := strings.TrimSpace(lines[0])
	if len(lines) > 1 {
		out += ": " + strings.TrimSpace(lines[len(lines)-1])
	}
	return out
}

// ExampleStubs is the example file with a stub added for every documented status of every
// operation that no pair shows: a pair named after the status, its response that status and
// its request empty, for a person or an agent to fill in. Nothing is removed: an example
// whose operation or status is gone is reported (example-orphaned), never deleted.
func (g *Generator) ExampleStubs() (ExampleFile, error) {
	file := ExampleFile{}
	if g.Config.Examples != nil {
		existing, err := ReadExamples(g.Config.Examples, g.Config.ExamplesFile)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		for id, pairs := range existing {
			file[id] = map[string]ExamplePair{}
			for name, pair := range pairs {
				file[id][name] = pair
			}
		}
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.generate()
	paths, _ := g.cached["paths"].(map[string]map[string]PathItem)
	resolved := examplesByOperation(file, &schemaRegistry{})
	for _, path := range sortedPaths(paths) {
		for _, method := range pathItemMethods {
			item, ok := paths[path][method]
			if !ok || item.OperationID == "" {
				continue
			}
			shown := map[int]bool{}
			for _, pair := range resolved[item.OperationID] {
				shown[pair.Response.Status] = true
			}
			for _, status := range sortedKeys(item.Responses) {
				code, err := strconv.Atoi(status)
				if err != nil || shown[code] {
					continue
				}
				if file[item.OperationID] == nil {
					file[item.OperationID] = map[string]ExamplePair{}
				}
				name := "status" + status
				for _, taken := file[item.OperationID][name]; taken; _, taken = file[item.OperationID][name] {
					name += "_"
				}
				file[item.OperationID][name] = ExamplePair{
					Summary:  "TODO: a request that is answered " + status,
					Response: ExampleResponse{Status: code},
				}
			}
		}
	}
	return file, nil
}
