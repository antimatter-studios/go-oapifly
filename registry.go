package oapifly

import (
	"fmt"
	"net/http"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// Endpoint is one operation registered in code rather than described by annotations in a
// source file. Its structure - parameters, request body, responses - comes from the Go types
// it names, read by reflection, so a document can be produced from a binary that has no
// source on disk (#15).
//
// Titles and descriptions are the comments' job. A registration may carry its own, and when it
// leaves them empty an annotated handler for the same route still supplies them.
type Endpoint struct {
	// Method is the HTTP method, in any case.
	Method string
	// Path is the OpenAPI path template, for example /api/v1/domains/{id}.
	Path string

	OperationID string
	Summary     string
	Description string
	Tags        []string
	Deprecated  bool

	// Request is the type the operation binds, or nil when it takes nothing. A field tagged
	// `path:"name"`, `query:"name"` or `header:"name"` is a parameter; the field tagged
	// `body:""` is the request body. A struct with no parameter fields is the body itself.
	// A pointer to a struct is read as the struct.
	//
	// A query or header parameter is optional unless its tag says `,required`. A
	// `description:"..."` tag describes a parameter, and the constraint tags (enums, format,
	// example, minimum, maximum, minLength, maxLength, pattern, minItems, maxItems) constrain
	// it the same way they constrain a body field. `body:"<media type>"` names a body that is
	// not application/json. Bind reads a request into this type and enforces what is
	// described.
	Request reflect.Type

	// Responses are the answers the operation can give, one per status.
	Responses []EndpointResponse
}

// EndpointResponse is one response of a registered operation.
type EndpointResponse struct {
	Status int
	// Description defaults to the status text, since a Response Object must have one.
	Description string
	// Type is the response body's type, or nil for a response with no body.
	Type reflect.Type
	// MediaType defaults to application/json when Type is set.
	MediaType string
}

// TypeOf returns the type of T, for an Endpoint's Request and a response's Type:
//
//	oapifly.Endpoint{Request: oapifly.TypeOf[CreateDomainRequest]()}
func TypeOf[T any]() reflect.Type {
	return reflect.TypeOf((*T)(nil)).Elem()
}

// Register adds an operation described by its Go types. Registering after Generate has been
// called makes the next call build the document again.
func (g *Generator) Register(endpoints ...Endpoint) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.endpoints = append(g.endpoints, endpoints...)
	g.cached = nil
}

// addEndpoints puts the registered operations into paths. A route an annotated handler also
// describes takes its structure from the registration - the code - and only the titles,
// description, tags and operationId the registration leaves empty from the annotation.
func addEndpoints(endpoints []Endpoint, paths map[string]map[string]PathItem, reg *schemaRegistry) {
	seen := map[string]bool{}
	for _, e := range endpoints {
		method := strings.ToLower(strings.TrimSpace(e.Method))
		if method == "" || e.Path == "" {
			reg.warn("registered endpoint %q %q has no method or path, not described", e.Method, e.Path)
			continue
		}
		if _, ok := methodsFor(method); !ok || method == "any" {
			reg.warn("%s %s: '%s' is not a method a Path Item Object may carry, route not described", strings.ToUpper(method), e.Path, e.Method)
			continue
		}
		key := method + " " + e.Path
		registeredBefore := seen[key]
		if registeredBefore {
			reg.warn("%s %s is registered twice, the later registration is used", strings.ToUpper(method), e.Path)
		}
		seen[key] = true

		item := buildEndpointItem(e, reg)
		if _, ok := paths[e.Path]; !ok {
			paths[e.Path] = map[string]PathItem{}
		}
		// Only an annotated description fills gaps; an earlier registration of the same route
		// is simply replaced.
		if annotated, ok := paths[e.Path][method]; ok && !registeredBefore {
			item = fillFromAnnotation(item, annotated)
		}
		paths[e.Path][method] = item
	}
}

// fillFromAnnotation copies what comments are for - titles, descriptions, tags and the
// operationId - from an annotated description of the same route, where the registration left
// them empty.
func fillFromAnnotation(item, annotated PathItem) PathItem {
	if item.Summary == "" {
		item.Summary = annotated.Summary
	}
	if item.Description == "" {
		item.Description = annotated.Description
	}
	if item.OperationID == "" {
		item.OperationID = annotated.OperationID
	}
	if len(item.Tags) == 0 {
		item.Tags = annotated.Tags
	}
	if !item.Deprecated {
		item.Deprecated = annotated.Deprecated
	}
	return item
}

func buildEndpointItem(e Endpoint, reg *schemaRegistry) PathItem {
	route := strings.ToUpper(e.Method) + " " + e.Path
	parameters, requestBody := endpointRequest(route, e.Path, e.Request, reg)

	responses := map[string]Response{}
	for _, r := range e.Responses {
		status := strconv.Itoa(r.Status)
		if _, dup := responses[status]; dup {
			reg.warn("%s: response %s is registered twice, the later one is used", route, status)
		}
		desc := r.Description
		if desc == "" {
			desc = http.StatusText(r.Status)
		}
		resp := Response{Description: desc}
		if r.Type != nil {
			media := r.MediaType
			if media == "" {
				media = "application/json"
			}
			resp.Content = map[string]interface{}{
				media: map[string]interface{}{"schema": reg.reflectSchema(r.Type)},
			}
		}
		responses[status] = resp
	}

	return PathItem{
		Summary:     e.Summary,
		Description: e.Description,
		OperationID: e.OperationID,
		Tags:        e.Tags,
		Deprecated:  e.Deprecated,
		Parameters:  parameters,
		RequestBody: requestBody,
		Responses:   responses,
	}
}

// endpointRequest reads a request type into its parameters and body. The path template is the
// authority on which path parameters exist: a template variable with no field is still a
// required string parameter, as it is for an annotated route, and is reported; a path field
// the template does not name is reported too.
func endpointRequest(route, path string, t reflect.Type, reg *schemaRegistry) ([]Parameter, *RequestBody) {
	byName := map[string]Parameter{}
	var queryAndHeader []Parameter
	var body *RequestBody
	var unbound []string

	if t != nil {
		for t.Kind() == reflect.Pointer {
			t = t.Elem()
		}
		hasParams := false
		if t.Kind() == reflect.Struct {
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				if f.Tag.Get("path") != "" || f.Tag.Get("query") != "" || f.Tag.Get("header") != "" {
					hasParams = true
				}
			}
		}

		switch {
		case !hasParams && !hasBodyField(t):
			// A request type with no parameters is the body itself.
			body = jsonBody(reg.reflectSchema(t), "application/json")
		default:
			for i := 0; i < t.NumField(); i++ {
				f := t.Field(i)
				switch {
				case f.Tag.Get("path") != "":
					p := fieldParameter(f, "path", reg)
					byName[p.Name] = p
				case f.Tag.Get("query") != "":
					queryAndHeader = append(queryAndHeader, fieldParameter(f, "query", reg))
				case f.Tag.Get("header") != "":
					queryAndHeader = append(queryAndHeader, fieldParameter(f, "header", reg))
				case hasTag(f, "body"):
					if body != nil {
						reg.warn("%s: %s has more than one body field, %s is ignored", route, typeDisplay(t), f.Name)
						continue
					}
					media := f.Tag.Get("body")
					if media == "" {
						media = "application/json"
					}
					required := f.Type.Kind() != reflect.Pointer
					body = jsonBody(reg.reflectSchema(f.Type), media)
					body.Required = required
				default:
					if f.IsExported() && f.Tag.Get("json") != "-" {
						unbound = append(unbound, f.Name)
					}
				}
			}
		}
	}

	if len(unbound) > 0 {
		reg.warn("%s: %s fields %s are neither parameters nor the body; tag them path, query or header, or mark a body field",
			route, typeDisplay(t), strings.Join(unbound, ", "))
	}

	// Path parameters in the order the template names them.
	var params []Parameter
	named := map[string]bool{}
	for _, name := range templateVariables(path) {
		named[name] = true
		if p, ok := byName[name]; ok {
			params = append(params, p)
			continue
		}
		reg.warn("%s: path parameter {%s} has no field in the request type, described as a string", route, name)
		params = append(params, Parameter{
			Name:        name,
			In:          "path",
			Description: "Path parameter '" + name + "'",
			Required:    true,
			Schema:      map[string]interface{}{"type": "string"},
			Example:     name,
		})
	}
	var extra []string
	for name := range byName {
		if !named[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	for _, name := range extra {
		reg.warn("%s: request field for path parameter %q is not in the path template, not described", route, name)
	}

	return append(params, queryAndHeader...), body
}

func jsonBody(schema map[string]interface{}, media string) *RequestBody {
	return &RequestBody{
		Required: true,
		Content:  map[string]interface{}{media: map[string]interface{}{"schema": schema}},
	}
}

func hasTag(f reflect.StructField, key string) bool {
	_, ok := f.Tag.Lookup(key)
	return ok
}

func hasBodyField(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	for i := 0; i < t.NumField(); i++ {
		if hasTag(t.Field(i), "body") {
			return true
		}
	}
	return false
}

// fieldParameter describes one parameter field. A parameter is a value, not an absent-able
// one: a pointer field means the parameter may be left out, so it is described as the type
// it points at.
func fieldParameter(f reflect.StructField, in string, reg *schemaRegistry) Parameter {
	name, opts, _ := strings.Cut(f.Tag.Get(in), ",")
	ft := f.Type
	for ft.Kind() == reflect.Pointer {
		ft = ft.Elem()
	}
	schema := reg.reflectSchema(ft)
	applyTagConstraints(schema, string(f.Tag))

	p := Parameter{
		Name:        name,
		In:          in,
		Description: f.Tag.Get("description"),
		Required:    in == "path" || hasOption(opts, "required"),
		Schema:      schema,
	}
	if example, ok := schema["example"]; ok {
		p.Example = example
		delete(schema, "example")
	}
	return p
}

func hasOption(opts, want string) bool {
	for _, o := range strings.Split(opts, ",") {
		if strings.TrimSpace(o) == want {
			return true
		}
	}
	return false
}

// templateVariables lists the {name} variables of a path template, in order.
func templateVariables(path string) []string {
	var names []string
	for {
		open := strings.Index(path, "{")
		if open < 0 {
			return names
		}
		close := strings.Index(path[open:], "}")
		if close < 0 {
			return names
		}
		names = append(names, path[open+1:open+close])
		path = path[open+close+1:]
	}
}

func typeDisplay(t reflect.Type) string {
	if t == nil {
		return "<nil>"
	}
	if t.Name() == "" {
		return t.String()
	}
	return fmt.Sprintf("%s.%s", t.PkgPath(), t.Name())
}
