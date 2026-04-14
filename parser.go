package oapifly

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strings"
)

// ---------------------------------------------------------------------------
// tagSet — multi-value annotation storage
// ---------------------------------------------------------------------------

type tagSet struct {
	entries map[string][]string
}

func newTagSet() tagSet {
	return tagSet{entries: map[string][]string{}}
}

func (t tagSet) add(key, value string) {
	t.entries[key] = append(t.entries[key], value)
}

func (t tagSet) get(key string) string {
	if vals, ok := t.entries[key]; ok && len(vals) > 0 {
		return vals[0]
	}
	return ""
}

func (t tagSet) getAll(key string) []string {
	return t.entries[key]
}

func (t tagSet) has(key string) bool {
	vals, ok := t.entries[key]
	return ok && len(vals) > 0
}

// ---------------------------------------------------------------------------
// schemaRegistry — tracks discovered schemas and resolves type references
// ---------------------------------------------------------------------------

type schemaRegistry struct {
	schemas  map[string]map[string]interface{}
	typeDirs []string
}

func newSchemaRegistry(typeDirs []string) *schemaRegistry {
	return &schemaRegistry{
		schemas:  map[string]map[string]interface{}{},
		typeDirs: typeDirs,
	}
}

// stripPackagePrefix returns the short type name by removing any package prefix.
// e.g. "restclient.LoginRequest" → "LoginRequest", "LoginRequest" → "LoginRequest"
func stripPackagePrefix(refType string) string {
	if idx := strings.LastIndex(refType, "."); idx >= 0 {
		return refType[idx+1:]
	}
	return refType
}

// resolve registers the schema if unknown and returns the reference name.
// Handles package-qualified type names (e.g. "restclient.LoginRequest") by
// stripping the package prefix for file/AST lookup while preserving the full
// name as the schema key.
func (r *schemaRegistry) resolve(refType string) string {
	refName := strings.TrimPrefix(refType, "types.")
	if _, ok := r.schemas[refName]; !ok {
		shortName := stripPackagePrefix(refName)
		typeFile := findTypeFile(shortName, r.typeDirs)
		if typeFile != "" {
			schema := generateSchemaForTypeAST(shortName, typeFile)
			if schema != nil {
				r.schemas[refName] = schema
			} else {
				r.schemas[refName] = map[string]interface{}{"type": "object"}
			}
		} else {
			r.schemas[refName] = map[string]interface{}{"type": "object"}
		}
	}
	return refName
}

// ---------------------------------------------------------------------------
// @Param parsing
// ---------------------------------------------------------------------------

// parsedParam is the structured result of parsing a single @Param annotation.
type parsedParam struct {
	Name        string
	In          string // path, query, header, body, formData
	DataType    string
	Required    bool
	Description string
	Example     string
}

// parseParam parses a single @Param tag value into structured data.
// Format: name location dataType required "description" [example(...)]
func parseParam(value string) (parsedParam, bool) {
	parts := strings.Fields(value)
	if len(parts) < 4 {
		return parsedParam{}, false
	}

	p := parsedParam{
		Name:     parts[0],
		In:       parts[1],
		DataType: parts[2],
		Required: parts[3] == "true",
	}

	// Extract description from quoted text
	rest := strings.Join(parts[4:], " ")
	if start := strings.Index(rest, "\""); start >= 0 {
		if end := strings.Index(rest[start+1:], "\""); end >= 0 {
			p.Description = rest[start+1 : start+1+end]
		}
	}

	// Extract example from attributes
	for i := 4; i < len(parts); i++ {
		part := parts[i]
		if strings.HasPrefix(part, "example(") && strings.HasSuffix(part, ")") {
			p.Example = part[len("example(") : len(part)-1]
		} else if part == "example" && i+1 < len(parts) {
			next := parts[i+1]
			if strings.HasPrefix(next, "\"") && strings.HasSuffix(next, "\"") && len(next) > 1 {
				p.Example = next[1 : len(next)-1]
			}
		}
	}

	return p, true
}

// parseAllParams parses all @Param tag values.
func parseAllParams(paramTags []string) []parsedParam {
	var params []parsedParam
	for _, tag := range paramTags {
		if p, ok := parseParam(tag); ok {
			params = append(params, p)
		}
	}
	return params
}

// ---------------------------------------------------------------------------
// Swaggo-to-OpenAPI type mapping
// ---------------------------------------------------------------------------

// dataTypeToOpenAPIType converts a swaggo data type to an OpenAPI type string.
func dataTypeToOpenAPIType(dataType string) string {
	switch strings.ToLower(dataType) {
	case "int", "integer":
		return "integer"
	case "number", "float", "float64":
		return "number"
	case "bool", "boolean":
		return "boolean"
	case "file":
		return "string"
	default:
		return "string"
	}
}

// dataTypeToFormat returns the OpenAPI format for special types (e.g. "binary" for file).
func dataTypeToFormat(dataType string) string {
	if strings.ToLower(dataType) == "file" {
		return "binary"
	}
	return ""
}

// dataTypeSchema builds a Parameter.Schema map for a swaggo data type.
func dataTypeSchema(dataType string) map[string]string {
	schema := map[string]string{"type": dataTypeToOpenAPIType(dataType)}
	if f := dataTypeToFormat(dataType); f != "" {
		schema["format"] = f
	}
	return schema
}

// isStructRef returns true if the data type refers to a struct (not a primitive).
func isStructRef(dataType string) bool {
	switch strings.ToLower(dataType) {
	case "string", "int", "integer", "number", "float", "float64",
		"bool", "boolean", "file":
		return false
	default:
		return true
	}
}

// ---------------------------------------------------------------------------
// Reflection-based type mapping (for runtime schema generation)
// ---------------------------------------------------------------------------

// goKindToJSONType maps a Go reflect.Type to its JSON/OpenAPI type string.
// Distinguishes integer from number per the OpenAPI spec.
func goKindToJSONType(t reflect.Type) string {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "integer"
	case reflect.Float32, reflect.Float64:
		return "number"
	case reflect.Slice, reflect.Array:
		return "array"
	case reflect.Map, reflect.Struct:
		return "object"
	default:
		return "object"
	}
}

// goKindToOpenAPIFormat returns the OpenAPI format hint for a Go type
// (e.g. "int32", "int64", "float", "double"). Returns "" if no format applies.
func goKindToOpenAPIFormat(t reflect.Type) string {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.Int32:
		return "int32"
	case reflect.Int, reflect.Int64:
		return "int64"
	case reflect.Float32:
		return "float"
	case reflect.Float64:
		return "double"
	default:
		return ""
	}
}

// resolveJSONFieldName extracts the JSON field name and options from a struct
// field's json tag. Returns skip=true for fields tagged with json:"-".
func resolveJSONFieldName(field reflect.StructField) (name string, omitempty bool, skip bool) {
	name = field.Name
	tag, ok := field.Tag.Lookup("json")
	if !ok {
		return name, false, false
	}
	parts := strings.Split(tag, ",")
	if parts[0] == "-" {
		return "", false, true
	}
	if parts[0] != "" {
		name = parts[0]
	}
	for _, part := range parts[1:] {
		if part == "omitempty" {
			omitempty = true
			break
		}
	}
	return name, omitempty, false
}

// resolveFieldTypeReflect resolves a struct field type to OpenAPI type info.
func resolveFieldTypeReflect(rt reflect.Type) fieldTypeInfo {
	if rt.Kind() == reflect.Ptr {
		rt = rt.Elem()
	}
	jsonType := goKindToJSONType(rt)

	// Struct → return as schema $ref
	if rt.Kind() == reflect.Struct && jsonType == "object" {
		return fieldTypeInfo{Ref: rt.Name()}
	}

	// Slice/Array → include items with element type
	if rt.Kind() == reflect.Slice || rt.Kind() == reflect.Array {
		elemType := rt.Elem()
		if elemType.Kind() == reflect.Ptr {
			elemType = elemType.Elem()
		}
		items := map[string]interface{}{"type": goKindToJSONType(elemType)}
		if f := goKindToOpenAPIFormat(elemType); f != "" {
			items["format"] = f
		}
		return fieldTypeInfo{Schema: map[string]interface{}{
			"type":  "array",
			"items": items,
		}}
	}

	// Primitive → type + optional format
	schema := map[string]interface{}{"type": jsonType}
	if f := goKindToOpenAPIFormat(rt); f != "" {
		schema["format"] = f
	}
	return fieldTypeInfo{Schema: schema}
}

// generateSchemaForTypeReflect generates a JSON Schema map using Go reflection.
func generateSchemaForTypeReflect(rt reflect.Type) map[string]interface{} {
	if rt.Kind() == reflect.Ptr {
		rt = rt.Elem()
	}
	props := map[string]interface{}{}
	required := []string{}
	for i := 0; i < rt.NumField(); i++ {
		name, omitempty, skip := resolveJSONFieldName(rt.Field(i))
		if skip || name == "" {
			continue
		}
		fieldType := resolveFieldTypeReflect(rt.Field(i).Type)
		props[name] = fieldType.Schema
		if !omitempty {
			required = append(required, name)
		}
	}
	return map[string]interface{}{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}

// ---------------------------------------------------------------------------
// AST-based schema generation
// ---------------------------------------------------------------------------

// generateSchemaForTypeAST parses a Go source file and generates an OpenAPI
// schema for the named struct type using its AST. This replaces the broken
// reflection-based approach since findReflectTypeByName cannot resolve
// arbitrary types at runtime.
func generateSchemaForTypeAST(typeName, filePath string) map[string]interface{} {
	f, err := parseFile(filePath)
	if err != nil {
		return nil
	}
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok || typeSpec.Name.Name != typeName {
				continue
			}
			structType, ok := typeSpec.Type.(*ast.StructType)
			if !ok {
				continue
			}
			return buildSchemaFromStructAST(structType)
		}
	}
	return nil
}

// buildSchemaFromStructAST builds an OpenAPI schema from an AST struct type.
func buildSchemaFromStructAST(st *ast.StructType) map[string]interface{} {
	props := map[string]interface{}{}
	var required []string

	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			continue // skip embedded fields
		}

		jsonName, omitempty, skip := resolveJSONFieldNameAST(field)
		if skip || jsonName == "" {
			continue
		}

		schema := resolveFieldTypeAST(field.Type)
		props[jsonName] = schema

		if !omitempty {
			required = append(required, jsonName)
		}
	}

	result := map[string]interface{}{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		result["required"] = required
	}
	return result
}

// resolveJSONFieldNameAST extracts JSON field name from an AST struct field's tag.
func resolveJSONFieldNameAST(field *ast.Field) (name string, omitempty bool, skip bool) {
	if len(field.Names) == 0 {
		return "", false, true
	}
	name = field.Names[0].Name

	if field.Tag == nil {
		return name, false, false
	}

	// Tag value includes backticks, strip them
	tagValue := strings.Trim(field.Tag.Value, "`")
	jsonTag := extractTagValue(tagValue, "json")
	if jsonTag == "" {
		return name, false, false
	}

	parts := strings.Split(jsonTag, ",")
	if parts[0] == "-" {
		return "", false, true
	}
	if parts[0] != "" {
		name = parts[0]
	}
	for _, part := range parts[1:] {
		if part == "omitempty" {
			omitempty = true
			break
		}
	}
	return name, omitempty, false
}

// extractTagValue extracts a specific key's value from a Go struct tag string.
// e.g. extractTagValue(`json:"username" xml:"user"`, "json") → "username"
func extractTagValue(tag, key string) string {
	lookup := key + ":"
	idx := strings.Index(tag, lookup)
	if idx < 0 {
		return ""
	}
	rest := tag[idx+len(lookup):]
	if len(rest) == 0 || rest[0] != '"' {
		return ""
	}
	rest = rest[1:]
	end := strings.Index(rest, "\"")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

// resolveFieldTypeAST maps an AST type expression to an OpenAPI schema map.
func resolveFieldTypeAST(expr ast.Expr) map[string]interface{} {
	switch t := expr.(type) {
	case *ast.Ident:
		return map[string]interface{}{"type": goIdentToOpenAPIType(t.Name)}
	case *ast.StarExpr:
		return resolveFieldTypeAST(t.X)
	case *ast.ArrayType:
		items := resolveFieldTypeAST(t.Elt)
		return map[string]interface{}{"type": "array", "items": items}
	case *ast.SelectorExpr:
		// Package-qualified type like time.Time — treat as string
		return map[string]interface{}{"type": "string"}
	case *ast.MapType:
		return map[string]interface{}{"type": "object"}
	default:
		return map[string]interface{}{"type": "object"}
	}
}

// goIdentToOpenAPIType maps Go type identifiers to OpenAPI types.
func goIdentToOpenAPIType(ident string) string {
	switch ident {
	case "string":
		return "string"
	case "bool":
		return "boolean"
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64":
		return "integer"
	case "float32", "float64":
		return "number"
	default:
		return "object"
	}
}

// ---------------------------------------------------------------------------
// AST parsing
// ---------------------------------------------------------------------------

// extractSchemaAnnotatedStructs finds all struct names with a @schema annotation.
func extractSchemaAnnotatedStructs(f *ast.File) []string {
	var structs []string
	for _, decl := range f.Decls {
		genDecl, ok := decl.(*ast.GenDecl)
		if !ok || genDecl.Tok != token.TYPE {
			continue
		}
		for _, spec := range genDecl.Specs {
			typeSpec, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			if genDecl.Doc != nil {
				for _, comment := range genDecl.Doc.List {
					c := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
					if strings.HasPrefix(c, "@schema") {
						structs = append(structs, typeSpec.Name.Name)
						break
					}
				}
			}
		}
	}
	return structs
}

// parseFile parses a Go source file and returns the AST.
func parseFile(path string) (*ast.File, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	return parser.ParseFile(fset, path, src, parser.ParseComments)
}

// extractHandlerDocs extracts swaggo tags from functions with @Router annotations.
// Supports both receiver methods and standalone handler functions.
func extractHandlerDocs(f *ast.File) []tagSet {
	var docs []tagSet
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Doc == nil {
			continue
		}
		tags := newTagSet()
		for _, comment := range fn.Doc.List {
			line := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
			if strings.HasPrefix(line, "@") {
				fields := strings.Fields(line)
				key := strings.TrimPrefix(fields[0], "@")
				if len(fields) > 1 {
					tags.add(key, strings.Join(fields[1:], " "))
				} else {
					// Single-word annotations like @Deprecated
					tags.add(key, "")
				}
			}
		}
		if tags.has("Router") {
			docs = append(docs, tags)
		}
	}
	return docs
}

// parseRouterTag parses a @Router tag value into path and HTTP method.
func parseRouterTag(router string) (string, string) {
	fields := strings.Fields(router)
	if len(fields) != 2 {
		return "", ""
	}
	return fields[0], strings.ToLower(strings.Trim(fields[1], "[]"))
}

// ---------------------------------------------------------------------------
// OpenAPI building
// ---------------------------------------------------------------------------

// buildParameters builds the OpenAPI parameters list from parsed @Param data
// and URL path template placeholders.
func buildParameters(routerPath string, params []parsedParam) []Parameter {
	var result []Parameter

	// Index path param metadata for merging with URL template
	pathMeta := map[string]parsedParam{}
	for _, p := range params {
		if p.In == "path" {
			pathMeta[p.Name] = p
		}
	}

	// Extract path params from URL template, merging with @Param metadata
	start := 0
	for start < len(routerPath) {
		open := strings.Index(routerPath[start:], "{")
		if open == -1 {
			break
		}
		open += start
		close := strings.Index(routerPath[open:], "}")
		if close == -1 {
			break
		}
		close += open
		name := routerPath[open+1 : close]

		desc := "Path parameter '" + name + "'"
		schema := map[string]string{"type": "string"}
		var example interface{} = name

		if meta, ok := pathMeta[name]; ok {
			if meta.Description != "" {
				desc = meta.Description
			}
			schema = dataTypeSchema(meta.DataType)
			if meta.Example != "" {
				example = meta.Example
			}
		}

		result = append(result, Parameter{
			Name:        name,
			In:          "path",
			Description: desc,
			Required:    true,
			Schema:      schema,
			Example:     example,
		})
		start = close + 1
	}

	// Add query and header params
	for _, p := range params {
		if p.In != "query" && p.In != "header" {
			continue
		}
		param := Parameter{
			Name:        p.Name,
			In:          p.In,
			Description: p.Description,
			Required:    p.Required,
			Schema:      dataTypeSchema(p.DataType),
		}
		if p.Example != "" {
			param.Example = p.Example
		}
		result = append(result, param)
	}

	return result
}

// buildRequestBody builds an OpenAPI request body from body/formData @Param entries.
// Returns nil if no body or formData params are present.
func buildRequestBody(params []parsedParam, reg *schemaRegistry) *RequestBody {
	var bodyParams []parsedParam
	var formParams []parsedParam

	for _, p := range params {
		switch p.In {
		case "body":
			bodyParams = append(bodyParams, p)
		case "formData":
			formParams = append(formParams, p)
		}
	}

	if len(bodyParams) == 0 && len(formParams) == 0 {
		return nil
	}

	if len(bodyParams) > 0 {
		p := bodyParams[0]
		var schema map[string]interface{}
		if isStructRef(p.DataType) {
			refName := reg.resolve(p.DataType)
			schema = map[string]interface{}{"$ref": "#/components/schemas/" + refName}
		} else {
			schema = map[string]interface{}{"type": dataTypeToOpenAPIType(p.DataType)}
			if f := dataTypeToFormat(p.DataType); f != "" {
				schema["format"] = f
			}
		}
		return &RequestBody{
			Description: p.Description,
			Required:    p.Required,
			Content: map[string]interface{}{
				"application/json": map[string]interface{}{"schema": schema},
			},
		}
	}

	// formData params → multipart/form-data
	props := map[string]interface{}{}
	var required []string
	for _, p := range formParams {
		s := map[string]interface{}{"type": dataTypeToOpenAPIType(p.DataType)}
		if f := dataTypeToFormat(p.DataType); f != "" {
			s["format"] = f
		}
		props[p.Name] = s
		if p.Required {
			required = append(required, p.Name)
		}
	}
	schema := map[string]interface{}{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	desc := ""
	if formParams[0].Description != "" {
		desc = formParams[0].Description
	}
	return &RequestBody{
		Description: desc,
		Required:    len(required) > 0,
		Content: map[string]interface{}{
			"multipart/form-data": map[string]interface{}{"schema": schema},
		},
	}
}

// buildResponse parses a @Success or @Failure tag value and returns the
// status code and Response. Registers any referenced schema types.
func buildResponse(value string, reg *schemaRegistry) (string, Response) {
	fields := strings.Fields(value)
	if len(fields) < 3 {
		return "", Response{}
	}
	status := fields[0]
	openapiType := fields[1]
	refType := fields[2]
	desc := ""
	if len(fields) > 3 {
		desc = strings.Trim(strings.Join(fields[3:], " "), "\"")
	}

	content := map[string]interface{}{}
	if refType != "" {
		refName := reg.resolve(refType)
		ref := map[string]interface{}{"$ref": "#/components/schemas/" + refName}
		if openapiType == "{array}" {
			content["application/json"] = map[string]interface{}{
				"schema": map[string]interface{}{"type": "array", "items": ref},
			}
		} else {
			content["application/json"] = map[string]interface{}{"schema": ref}
		}
	}
	return status, Response{Description: desc, Content: content}
}

// buildPathItem parses swaggo tags and builds a PathItem with schema references.
func buildPathItem(tags tagSet, reg *schemaRegistry) PathItem {
	var tagsList []string
	if v := tags.get("Tags"); v != "" {
		parts := strings.Split(v, ",")
		for i, t := range parts {
			parts[i] = strings.TrimSpace(t)
		}
		tagsList = parts
	}

	params := parseAllParams(tags.getAll("Param"))

	var parameters []Parameter
	if routerTag := tags.get("Router"); routerTag != "" {
		fields := strings.Fields(routerTag)
		if len(fields) == 2 {
			parameters = buildParameters(fields[0], params)
		}
	}

	requestBody := buildRequestBody(params, reg)

	responses := map[string]Response{}
	for _, v := range tags.getAll("Success") {
		if status, resp := buildResponse(v, reg); status != "" {
			responses[status] = resp
		}
	}
	for _, v := range tags.getAll("Failure") {
		if status, resp := buildResponse(v, reg); status != "" {
			responses[status] = resp
		}
	}

	return PathItem{
		Summary:     tags.get("Summary"),
		Description: tags.get("Description"),
		OperationID: tags.get("ID"),
		Tags:        tagsList,
		Deprecated:  tags.has("Deprecated"),
		Parameters:  parameters,
		RequestBody: requestBody,
		Responses:   responses,
	}
}

// ---------------------------------------------------------------------------
// Type file discovery
// ---------------------------------------------------------------------------

// findTypeFile searches dirs for a Go file containing a type with the given name.
func findTypeFile(typeName string, dirs []string) string {
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
				continue
			}
			path := dir + "/" + entry.Name()
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if err != nil {
				continue
			}
			for _, decl := range file.Decls {
				genDecl, ok := decl.(*ast.GenDecl)
				if !ok || genDecl.Tok != token.TYPE {
					continue
				}
				for _, spec := range genDecl.Specs {
					typeSpec, ok := spec.(*ast.TypeSpec)
					if !ok {
						continue
					}
					if typeSpec.Name.Name == typeName {
						return path
					}
				}
			}
		}
	}
	return ""
}

// findReflectTypeByName is a stub for resolving a struct name to reflect.Type.
// TODO: Implement using a type registry for full schema resolution.
func findReflectTypeByName(name string) reflect.Type {
	return nil
}
