package oapifly

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
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

	// warnings records types that could not be resolved. A schema that says
	// nothing about a field is honest; one that guesses is not, and without a
	// warning the caller has no way to learn its spec is incomplete.
	warnings []string

	// resolving guards against a type that reaches itself - a tree node with
	// child nodes of its own type would otherwise recurse until the stack ends.
	resolving map[string]bool

	// typeArgs binds the type parameters of the generic declaration currently being
	// described to the arguments it was instantiated with. Empty everywhere else.
	typeArgs map[string]typeArg

	// genericOrigins records which instantiation produced each generic component name, so a
	// second instantiation reducing to the same name is reported rather than silently
	// answered by the first one's schema. It doubles as the recursion guard for generics.
	genericOrigins map[string]string
}

func newSchemaRegistry(typeDirs []string) *schemaRegistry {
	return &schemaRegistry{
		schemas:        map[string]map[string]interface{}{},
		typeDirs:       typeDirs,
		resolving:      map[string]bool{},
		genericOrigins: map[string]string{},
	}
}

func (r *schemaRegistry) warn(format string, args ...interface{}) {
	r.warnings = append(r.warnings, fmt.Sprintf(format, args...))
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
	// A generic instantiation names no declaration of its own - the file declares the base
	// and the arguments arrive beside it - so it is resolved by binding rather than lookup.
	if base, args, ok := parseGenericName(refType); ok {
		return r.resolveGeneric(base, args, refType)
	}

	refName := strings.TrimPrefix(refType, "types.")
	if _, known := r.schemas[refName]; known {
		return refName
	}

	if schema, ok := openAPIPrimitiveSchema(refName); ok {
		r.schemas[refName] = schema
		return refName
	}

	shortName := stripPackagePrefix(refName)
	typeFile := findTypeFile(shortName, r.typeDirs)
	if typeFile == "" {
		r.warn("type %s named by an annotation is not in any configured TypeDirs, described as an untyped object", refType)
		r.schemas[refName] = map[string]interface{}{"type": "object"}
		return refName
	}

	// The same resolver the field path uses, so a declaration that is not a struct - a named
	// string, an alias to another package's type - is described as what it is rather than
	// abandoned. Describing `type DeviceType string` as an object rejects the plain string
	// the handler sends.
	schema, _ := r.inOwnScope(shortName, typeFile)
	if schema == nil {
		r.warn("type %s in %s has no schema this generator can express, described as an untyped object", refType, typeFile)
		schema = map[string]interface{}{"type": "object"}
	}

	r.schemas[refName] = schema
	return refName
}

// openAPIPrimitiveSchema reports the schema for a token that names an OpenAPI type rather
// than a Go one. A multipart upload is declared as `file`, and the primitives arrive from
// @Param declarations; none of them is a missing Go type worth warning about.
func openAPIPrimitiveSchema(name string) (map[string]interface{}, bool) {
	switch name {
	case "file":
		return map[string]interface{}{"type": "string", "format": "binary"}, true
	case "string", "boolean", "integer", "number", "object", "array":
		return map[string]interface{}{"type": name}, true
	}
	return nil, false
}

// ---------------------------------------------------------------------------
// @Param parsing
// ---------------------------------------------------------------------------

// parsedParam is the structured result of parsing a single @Param annotation.
//
// The bounds are kept as the text they were written as and typed when they join a schema, the
// same way a struct tag's constraint is: an annotation can only carry text, and a bound beside
// an integer has to become a number or it would describe a parameter that rejects every value
// it accepts.
type parsedParam struct {
	Name        string
	In          string // path, query, header, body, formData
	DataType    string
	Required    bool
	Description string
	Example     string
	Minimum     string
	Maximum     string
	Enums       []string
}

// parseParam parses a single @Param tag value into structured data.
// Format: name location dataType required "description" [attribute(value)...]
//
// The attributes are swaggo's: example, minimum, maximum and enums. A Go type says what shape
// a value has, not which values are allowed, so where a handler enforces a bound these are how
// the description states it.
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

	// Extract the attributes
	for i := 4; i < len(parts); i++ {
		part := parts[i]
		if name, value, ok := parseAttribute(part); ok {
			switch name {
			case "example":
				p.Example = value
			case "minimum":
				p.Minimum = value
			case "maximum":
				p.Maximum = value
			case "enums":
				for _, entry := range strings.Split(value, ",") {
					if entry = strings.TrimSpace(entry); entry != "" {
						p.Enums = append(p.Enums, entry)
					}
				}
			}
			continue
		}
		// swaggo also writes an example as `example "value"`.
		if part == "example" && i+1 < len(parts) {
			next := parts[i+1]
			if strings.HasPrefix(next, "\"") && strings.HasSuffix(next, "\"") && len(next) > 1 {
				p.Example = next[1 : len(next)-1]
			}
		}
	}

	return p, true
}

// parseAttribute reads one `name(value)` attribute.
func parseAttribute(part string) (name, value string, ok bool) {
	open := strings.Index(part, "(")
	if open <= 0 || !strings.HasSuffix(part, ")") {
		return "", "", false
	}
	return part[:open], part[open+1 : len(part)-1], true
}

// applyParamConstraints puts the bounds an annotation declared into a parameter's schema,
// typed as the schema is. A bound that is not a value of that type is left off rather than
// written as text: describing a parameter as accepting only the string "one" would reject
// every number it really takes, and the annotation is what needs fixing.
func applyParamConstraints(schema map[string]interface{}, p parsedParam) {
	openapiType, _ := schema["type"].(string)

	for key, raw := range map[string]string{"minimum": p.Minimum, "maximum": p.Maximum} {
		if raw == "" {
			continue
		}
		if value := typedValue(raw, openapiType); value != raw {
			schema[key] = value
		}
	}

	if len(p.Enums) > 0 {
		values := make([]interface{}, 0, len(p.Enums))
		for _, entry := range p.Enums {
			values = append(values, typedValue(entry, openapiType))
		}
		schema["enum"] = values
	}
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

// arrayItemType reports the element type of a list-shaped @Param data type, and whether
// it was one. Both spellings are accepted: Go's `[]int`, and swaggo's bare `array`, whose
// items are strings because that is all the annotation can say.
func arrayItemType(dataType string) (string, bool) {
	switch {
	case strings.HasPrefix(dataType, "[]"):
		return dataType[len("[]"):], true
	case strings.EqualFold(dataType, "array"):
		return "string", true
	}
	return "", false
}

// dataTypeSchema builds a Parameter.Schema map for a swaggo data type. A list-shaped
// type becomes an array schema with typed items; anything else is the scalar it names.
func dataTypeSchema(dataType string) map[string]interface{} {
	if item, ok := arrayItemType(dataType); ok {
		return map[string]interface{}{"type": "array", "items": dataTypeSchema(item)}
	}
	schema := map[string]interface{}{"type": dataTypeToOpenAPIType(dataType)}
	if f := dataTypeToFormat(dataType); f != "" {
		schema["format"] = f
	}
	return schema
}

// parameterExample turns the text of an example(...) attribute into a value of the
// declared type: integers, numbers and booleans parse, a list-shaped type splits its
// bracketed comma-separated text and types each element. Text that does not parse as
// the declared type is passed through unchanged rather than dropped, so a wrong example
// shows up in the output where it can be fixed.
func parameterExample(dataType, example string) interface{} {
	if item, ok := arrayItemType(dataType); ok {
		body := strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(example), "["), "]")
		values := []interface{}{}
		if strings.TrimSpace(body) == "" {
			return values
		}
		for _, part := range strings.Split(body, ",") {
			values = append(values, parameterExample(item, strings.TrimSpace(part)))
		}
		return values
	}
	return typedValue(example, dataTypeToOpenAPIType(dataType))
}

// typedValue reads annotation text as a value of the OpenAPI type it is meant to be.
// Annotations are always written as text, so an integer's "3" has to become a number on
// the way in - a quoted example on an integer field describes a value the field rejects.
// Integers are read as float64 like numbers, which is the one numeric type JSON has, so
// a value is the same whether it came from an annotation or from decoding a document.
// Text that does not parse as the type is returned unchanged rather than dropped, so a
// wrong value shows up in the output where it can be fixed.
func typedValue(raw, openapiType string) interface{} {
	switch openapiType {
	case "integer":
		// Parsed as an integer and returned as float64: an integer example must BE an
		// integer, so "1.5" stays text, but the value is a JSON number like any other.
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return float64(n)
		}
	case "number":
		if n, err := strconv.ParseFloat(raw, 64); err == nil {
			return n
		}
	case "boolean":
		if b, err := strconv.ParseBool(raw); err == nil {
			return b
		}
	}
	return raw
}

// isStructRef returns true if the data type refers to a struct (not a primitive or a
// list of primitives).
func isStructRef(dataType string) bool {
	if item, ok := arrayItemType(dataType); ok {
		return isStructRef(item)
	}
	switch strings.ToLower(dataType) {
	case "string", "int", "integer", "number", "float", "float64",
		"bool", "boolean", "file":
		return false
	default:
		return true
	}
}

// ---------------------------------------------------------------------------
// AST-based schema generation
// ---------------------------------------------------------------------------

// generateSchemaForTypeAST reports the schema of the named type when it is declared as a
// struct in filePath, and nil for anything else - a named string, an alias, an interface,
// or a name the file does not declare.
func generateSchemaForTypeAST(typeName, filePath string, reg *schemaRegistry) map[string]interface{} {
	schema, isStruct := schemaForNamedTypeAST(typeName, filePath, reg)
	if !isStruct {
		return nil
	}
	return schema
}

// schemaForNamedTypeAST resolves a named type declaration to a schema, reporting whether it
// was a struct. Non-struct declarations matter as much as structs: `type DeviceType string`
// and `type LogAction string` appear all over a response, and describing them as objects
// would reject the plain strings the handlers actually send. isStruct tells the caller
// whether the result deserves its own entry in components or should be inlined.
func schemaForNamedTypeAST(typeName, filePath string, reg *schemaRegistry) (schema map[string]interface{}, isStruct bool) {
	f, err := parseFile(filePath)
	if err != nil {
		return nil, false
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
			// A generic declaration reached by its bare name has nothing for its parameters to
			// stand for. Describing it anyway resolved each parameter as if it were a type, and
			// the reader was sent looking for a type called T; an instantiation reaches its body
			// through resolveGeneric instead, with the arguments bound.
			if params := typeParamNames(typeSpec); len(params) > 0 && len(reg.typeArgs) == 0 {
				reg.warn("%s declares type parameter(s) %s, so it cannot be described without arguments; name an instantiation such as %s[...]",
					typeName, strings.Join(params, ", "), typeName)
				return nil, false
			}
			if structType, ok := typeSpec.Type.(*ast.StructType); ok {
				return buildSchemaFromStructAST(structType, reg), true
			}
			// An interface constrains nothing that can be expressed here - the concrete
			// value is chosen at runtime - so it stays an unconstrained schema rather than
			// claiming a shape.
			if _, ok := typeSpec.Type.(*ast.InterfaceType); ok {
				return map[string]interface{}{}, false
			}
			return resolveFieldTypeAST(typeSpec.Type, reg), false
		}
	}
	return nil, false
}

// embeddedFields returns the properties an embedded type contributes to the struct that
// embeds it. A type outside the configured TypeDirs cannot be read, and its fields would
// disappear from the schema without a word, so that is reported rather than passed over.
func (r *schemaRegistry) embeddedFields(expr ast.Expr) (map[string]interface{}, []string) {
	name, display := embeddedTypeName(expr)
	if name == "" {
		return nil, nil
	}

	if r.resolving[name] {
		// Reached from its own definition further up the stack; the fields are already there.
		return nil, nil
	}

	typeFile := findTypeFile(name, r.typeDirs)
	if typeFile == "" {
		r.warn("embedded type %s is not in any configured TypeDirs, its fields are missing from the schema", display)
		return nil, nil
	}

	r.resolving[name] = true
	schema, isStruct := r.inOwnScope(name, typeFile)
	delete(r.resolving, name)

	if !isStruct || schema == nil {
		r.warn("embedded type %s in %s is not a struct, its fields are missing from the schema", display, typeFile)
		return nil, nil
	}

	props, _ := schema["properties"].(map[string]interface{})
	required, _ := schema["required"].([]string)
	return props, required
}

// embeddedTypeName reports the type name of an embedded field, and how to name it in a
// warning. An embedded field is a bare type, a pointer to one, or a qualified one.
func embeddedTypeName(expr ast.Expr) (name, display string) {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name, t.Name
	case *ast.StarExpr:
		return embeddedTypeName(t.X)
	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		if !ok {
			return t.Sel.Name, t.Sel.Name
		}
		return t.Sel.Name, pkg.Name + "." + t.Sel.Name
	}
	return "", ""
}

// buildSchemaFromStructAST builds an OpenAPI schema from an AST struct type.
func buildSchemaFromStructAST(st *ast.StructType, reg *schemaRegistry) map[string]interface{} {
	props := map[string]interface{}{}
	var required []string

	for _, field := range st.Fields.List {
		if len(field.Names) == 0 {
			// Go promotes an embedded struct's fields into the same JSON object, so they are
			// merged here. Skipping them described a type built entirely from embeds - a
			// gorm.Model and a payload struct, say - as an object with no fields at all.
			embeddedProps, embeddedRequired := reg.embeddedFields(field.Type)
			for name, schema := range embeddedProps {
				props[name] = schema
			}
			required = append(required, embeddedRequired...)
			continue
		}

		jsonName, omitempty, skip := resolveJSONFieldNameAST(field)
		if skip || jsonName == "" {
			continue
		}

		schema := resolveFieldTypeAST(field.Type, reg)
		applyFieldConstraints(schema, field)
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

// applyFieldConstraints reads the constraint tags swaggo defines - enums, format, example -
// off a struct field and puts them in its schema.
//
// A Go type says what shape a value has, not which values are allowed. Without these, a
// schema describes `type: string` where the handler accepts two words, so a document
// permits bodies the handler goes on to reject and a consumer generating a request from
// the description gets a 400 it cannot explain.
//
// The values are typed against the schema they are joining: `enums:"1,2,3"` on an integer
// field has to produce numbers, because quoting them would describe a field that rejects
// every value it actually accepts.
func applyFieldConstraints(schema map[string]interface{}, field *ast.Field) {
	if field.Tag == nil {
		return
	}
	tag := strings.Trim(field.Tag.Value, "`")

	if raw := extractTagValue(tag, "enums"); raw != "" {
		var values []interface{}
		for _, part := range strings.Split(raw, ",") {
			values = append(values, typedTagValue(strings.TrimSpace(part), schema))
		}
		if len(values) > 0 {
			schema["enum"] = values
		}
	}

	// A format that comes with the type - date-time for time.Time, uuid for uuid.UUID -
	// describes what Go actually marshals, so it wins over a tag saying otherwise.
	if raw := extractTagValue(tag, "format"); raw != "" {
		if _, already := schema["format"]; !already {
			schema["format"] = raw
		}
	}

	if raw := extractTagValue(tag, "example"); raw != "" {
		schema["example"] = typedTagValue(raw, schema)
	}
}

// typedTagValue reads a tag's text as the type the schema declares.
func typedTagValue(raw string, schema map[string]interface{}) interface{} {
	openapiType, _ := schema["type"].(string)
	return typedValue(raw, openapiType)
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

// qualifiedJSONTypes are the standard-library and well-known types whose JSON form is
// not their Go structure. time.Time is a struct of unexported fields but marshals to an
// RFC 3339 string; uuid.UUID is a byte array that marshals to a string. Describing them
// structurally would be wrong, so each is named here with the schema it actually produces.
//
// Anything NOT in this table is resolved like any other named type, because the previous
// blanket "package-qualified means string" rule silently described every foreign struct -
// a CPU stats object, a slice of devices - as a string, and nothing in the generated spec
// showed that a guess had been made.
var qualifiedJSONTypes = map[string]map[string]interface{}{
	"time.Time":       {"type": "string", "format": "date-time"},
	"time.Duration":   {"type": "integer", "format": "int64"},
	"uuid.UUID":       {"type": "string", "format": "uuid"},
	"gorm.DeletedAt":  {"type": "string", "format": "date-time", "nullable": true},
	"sql.NullString":  {"type": "string", "nullable": true},
	"sql.NullTime":    {"type": "string", "format": "date-time", "nullable": true},
	"sql.NullInt64":   {"type": "integer", "format": "int64", "nullable": true},
	"sql.NullBool":    {"type": "boolean", "nullable": true},
	"sql.NullFloat64": {"type": "number", "format": "double", "nullable": true},
	// json.RawMessage is whatever the producer put in it, so the honest schema constrains
	// nothing at all rather than claiming a type.
	"json.RawMessage": {},
}

// resolveFieldTypeAST maps an AST type expression to an OpenAPI schema map. Named types
// are resolved through reg so a nested struct becomes a real $ref instead of a bare
// object - the reflection path has always emitted refs, and an AST path that did not was
// the same generator producing two different specs for the same input.
func resolveFieldTypeAST(expr ast.Expr, reg *schemaRegistry) map[string]interface{} {
	switch t := expr.(type) {
	case *ast.Ident:
		// A bound type parameter is whatever it was instantiated with, checked before the
		// primitives so a parameter can be named anything at all.
		if arg, bound := reg.typeArgs[t.Name]; bound {
			return copySchema(arg.schema)
		}
		if primitive := goIdentToOpenAPIType(t.Name); primitive != "object" {
			return map[string]interface{}{"type": primitive}
		}
		// A non-primitive identifier is a type declared in this package.
		return reg.refOrObject(t.Name, t.Name)
	case *ast.IndexExpr:
		// A generic instantiation with one argument: Page[Item].
		return reg.genericFieldSchema(t.X, []ast.Expr{t.Index})
	case *ast.IndexListExpr:
		// ... and with several: Pair[string, Item].
		return reg.genericFieldSchema(t.X, t.Indices)
	case *ast.StarExpr:
		// A pointer is the same type, absent-able: OpenAPI 3.0 spells that nullable.
		return asNullable(resolveFieldTypeAST(t.X, reg))
	case *ast.ArrayType:
		items := resolveFieldTypeAST(t.Elt, reg)
		return map[string]interface{}{"type": "array", "items": items}
	case *ast.SelectorExpr:
		pkg, ok := t.X.(*ast.Ident)
		if !ok {
			return map[string]interface{}{"type": "object"}
		}
		qualified := pkg.Name + "." + t.Sel.Name
		if known, ok := qualifiedJSONTypes[qualified]; ok {
			return copySchema(known)
		}
		return reg.refOrObject(t.Sel.Name, qualified)
	case *ast.MapType:
		// The value type is as describable as any other, and a consumer reading
		// map[string]SambaUser deserves to know what the values are.
		return map[string]interface{}{
			"type":                 "object",
			"additionalProperties": resolveFieldTypeAST(t.Value, reg),
		}
	default:
		return map[string]interface{}{"type": "object"}
	}
}

// asNullable marks a schema as permitting null.
//
// A reference needs different treatment from an inline schema. In OpenAPI 3.0 and in JSON
// Schema, any keyword sitting beside $ref is ignored, so `{"$ref": x, "nullable": true}`
// describes a field as non-nullable however it is read - and a consumer validating a nil
// pointer, which serialises to null, rejects a body the API legitimately returns. Wrapping
// the reference in allOf gives the keyword something to apply to, which is the spelling the
// OpenAPI 3.0 specification recommends for exactly this case.
func asNullable(schema map[string]interface{}) map[string]interface{} {
	if len(schema) == 0 {
		// An unconstrained schema already permits null; saying so would narrow nothing.
		return schema
	}

	if ref, ok := schema["$ref"]; ok {
		// type is stated in the same object on purpose. OpenAPI 3.0 defines nullable as
		// adding null to the type declared alongside it, and only when a type is declared
		// there - so allOf plus nullable on its own is as inert as the sibling keyword it
		// replaced. A reference is only ever registered for a struct, so object is accurate,
		// and allOf keeps the referenced shape rather than duplicating it inline.
		return map[string]interface{}{
			"type":     "object",
			"nullable": true,
			"allOf":    []interface{}{map[string]interface{}{"$ref": ref}},
		}
	}

	schema["nullable"] = true
	return schema
}

// copySchema returns a copy so a caller adding nullable to a pointer field cannot mutate
// the shared entry in qualifiedJSONTypes and change every other field of that type.
func copySchema(src map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(src))
	for k, v := range src {
		out[k] = v
	}
	return out
}

// refOrObject registers typeName if it can be found in the configured TypeDirs and
// returns a $ref to it. When it cannot be found the schema falls back to a plain object
// and the failure is recorded: an unconstrained object accepts whatever the handler
// returns, so the spec stays usable, and the warning says which type is missing and which
// directory would have to be scanned to describe it.
func (r *schemaRegistry) refOrObject(typeName, displayName string) map[string]interface{} {
	if r.resolving[typeName] {
		// Already being built further up the stack; the ref is still correct.
		return map[string]interface{}{"$ref": "#/components/schemas/" + typeName}
	}

	if _, known := r.schemas[typeName]; known {
		return map[string]interface{}{"$ref": "#/components/schemas/" + typeName}
	}

	typeFile := findTypeFile(typeName, r.typeDirs)
	if typeFile == "" {
		r.warn("type %s is not in any configured TypeDirs, described as an untyped object", displayName)
		return map[string]interface{}{"type": "object"}
	}

	r.resolving[typeName] = true
	schema, isStruct := r.inOwnScope(typeName, typeFile)
	delete(r.resolving, typeName)

	if schema == nil {
		r.warn("type %s in %s has no schema this generator can express, described as an untyped object", displayName, typeFile)
		return map[string]interface{}{"type": "object"}
	}

	// Only a struct earns a named entry in components. A declaration like
	// `type DeviceType string` is a string everywhere it appears, so it is inlined -
	// registering it as an object would describe a plain string as an object, which is the
	// same class of lie this change exists to remove.
	if !isStruct {
		return schema
	}

	r.schemas[typeName] = schema
	return map[string]interface{}{"$ref": "#/components/schemas/" + typeName}
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

// pathItemMethods are the only keys a Path Item Object may carry an operation under.
var pathItemMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// methodsFor turns the verb from a @Router tag into the methods it describes.
//
// A route can be registered for every method at once - fiber's App.All, gin's router.Any,
// echo's Any - and `any` is the name those frameworks give it. OpenAPI has no such key, so
// it is written out as the methods it stands for. Anything else is a verb OpenAPI cannot
// express (CONNECT) or a typo, and the caller drops the route rather than emit a key no
// consumer will read.
func methodsFor(method string) ([]string, bool) {
	if method == "any" {
		return pathItemMethods, true
	}
	for _, known := range pathItemMethods {
		if method == known {
			return []string{method}, true
		}
	}
	return nil, false
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
		schema := map[string]interface{}{"type": "string"}
		var example interface{} = name

		if meta, ok := pathMeta[name]; ok {
			if meta.Description != "" {
				desc = meta.Description
			}
			schema = dataTypeSchema(meta.DataType)
			applyParamConstraints(schema, meta)
			if meta.Example != "" {
				example = parameterExample(meta.DataType, meta.Example)
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
		schema := dataTypeSchema(p.DataType)
		applyParamConstraints(schema, p)
		param := Parameter{
			Name:        p.Name,
			In:          p.In,
			Description: p.Description,
			Required:    p.Required,
			Schema:      schema,
		}
		if p.Example != "" {
			param.Example = parameterExample(p.DataType, p.Example)
		}
		result = append(result, param)
	}

	return result
}

// bodySchema describes a request body's data type: a struct becomes a reference to its
// registered component, a list of structs an array of such references, and anything else
// the scalar or list-of-scalars schema the data type names. The slice spelling is peeled
// off BEFORE the registry sees the name - handing it "[]Item" would register a component
// by that name and describe the array as a single object.
func bodySchema(dataType string, reg *schemaRegistry) map[string]interface{} {
	if item, ok := arrayItemType(dataType); ok {
		return map[string]interface{}{"type": "array", "items": bodySchema(item, reg)}
	}
	if isStructRef(dataType) {
		refName := reg.resolve(dataType)
		return map[string]interface{}{"$ref": "#/components/schemas/" + refName}
	}
	return dataTypeSchema(dataType)
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
		schema := bodySchema(p.DataType, reg)
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
		props[p.Name] = dataTypeSchema(p.DataType)
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

// resolveContentType maps a swaggo @Produce/@Accept value to an OpenAPI MIME type.
// Falls back to application/json if empty or unrecognized.
func resolveContentType(produce string) string {
	switch strings.TrimSpace(strings.ToLower(produce)) {
	case "json", "application/json", "":
		return "application/json"
	case "text/csv", "csv":
		return "text/csv"
	case "text/plain", "plain":
		return "text/plain"
	case "xml", "application/xml":
		return "application/xml"
	case "html", "text/html":
		return "text/html"
	case "multipart/form-data":
		return "multipart/form-data"
	case "application/octet-stream", "octet-stream":
		return "application/octet-stream"
	default:
		// If it looks like a full MIME type, use as-is
		if strings.Contains(produce, "/") {
			return strings.TrimSpace(produce)
		}
		return "application/json"
	}
}

// buildResponse parses a @Success or @Failure tag value and returns the
// status code and Response. Registers any referenced schema types.
// The produce parameter is the resolved content type from @Produce.
func buildResponse(value string, produce string, reg *schemaRegistry) (string, Response) {
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

	contentType := resolveContentType(produce)

	content := map[string]interface{}{}
	if refType != "" {
		refName := reg.resolve(refType)
		ref := map[string]interface{}{"$ref": "#/components/schemas/" + refName}
		if openapiType == "{array}" {
			content[contentType] = map[string]interface{}{
				"schema": map[string]interface{}{"type": "array", "items": ref},
			}
		} else {
			content[contentType] = map[string]interface{}{"schema": ref}
		}
	}
	return status, Response{Description: desc, Content: content}
}

// ---------------------------------------------------------------------------
// @Example parsing
// ---------------------------------------------------------------------------

// parsedExample is one @Example annotation:
//
//	@Example accepted request  {"username": "gooduser"}  "A user that exists"
//	@Example accepted response 200 {"token": "abc"}
//
// The name is what ties a request to the response it produces. A consumer reading the
// document pairs them by that name, so a name used on one side and not the other pairs with
// everything on the other side - which is why both sides are declared together.
type parsedExample struct {
	Name    string
	Side    string // "request" or "response"
	Status  string // response only
	Summary string
	Value   interface{}
}

// parseExample reads one @Example tag value, reporting whether it was well formed.
func parseExample(value string) (parsedExample, bool) {
	fields := strings.Fields(value)
	if len(fields) < 3 {
		return parsedExample{}, false
	}

	example := parsedExample{Name: fields[0], Side: strings.ToLower(fields[1])}
	rest := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), fields[0]))
	rest = strings.TrimSpace(strings.TrimPrefix(rest, fields[1]))

	switch example.Side {
	case "request":
	case "response":
		if len(fields) < 4 {
			return parsedExample{}, false
		}
		example.Status = fields[2]
		rest = strings.TrimSpace(strings.TrimPrefix(rest, fields[2]))
	default:
		return parsedExample{}, false
	}

	body, summary := splitExampleSummary(rest)
	example.Summary = summary
	example.Value = decodeExampleValue(body)
	return example, true
}

// splitExampleSummary separates the value from an optional quoted summary after it. The
// value is JSON often enough that a quote inside it cannot be assumed to open the summary,
// so only a quoted run at the very end counts.
func splitExampleSummary(rest string) (body, summary string) {
	rest = strings.TrimSpace(rest)
	if !strings.HasSuffix(rest, `"`) {
		return rest, ""
	}
	opening := strings.LastIndex(rest[:len(rest)-1], `"`)
	if opening <= 0 {
		return rest, ""
	}
	return strings.TrimSpace(rest[:opening]), rest[opening+1 : len(rest)-1]
}

// decodeExampleValue reads the value as JSON, keeping the structure a consumer needs to send
// it as a body. Text that is not JSON is the value itself - a plain string example is legal.
func decodeExampleValue(body string) interface{} {
	var decoded interface{}
	if err := json.Unmarshal([]byte(body), &decoded); err == nil {
		return decoded
	}
	return body
}

// attachExamples files each parsed example under the media type it belongs to. Content is
// built before this runs, so an example for a status with no declared response, or for an
// operation with no body, is dropped rather than inventing content to hold it.
func attachExamples(examples []parsedExample, requestBody *RequestBody, responses map[string]Response) {
	for _, example := range examples {
		switch example.Side {
		case "request":
			if requestBody != nil {
				addExample(requestBody.Content, example)
			}
		case "response":
			if response, ok := responses[example.Status]; ok {
				addExample(response.Content, example)
			}
		}
	}
}

// addExample puts one example into every media type of a content map. An operation declares
// a single media type in practice, and spreading it is better than guessing which one was
// meant.
func addExample(content map[string]interface{}, example parsedExample) {
	for mediaType, raw := range content {
		media, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		existing, ok := media["examples"].(map[string]Example)
		if !ok {
			existing = map[string]Example{}
			media["examples"] = existing
		}
		existing[example.Name] = Example{Summary: example.Summary, Value: example.Value}
		content[mediaType] = media
	}
}

// buildPathItem parses swaggo tags and builds a PathItem with schema references.
func buildPathItem(routerPath string, tags tagSet, reg *schemaRegistry) PathItem {
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
	if routerPath != "" {
		parameters = buildParameters(routerPath, params)
	}

	requestBody := buildRequestBody(params, reg)

	produce := tags.get("Produce")

	responses := map[string]Response{}
	for _, v := range tags.getAll("Success") {
		if status, resp := buildResponse(v, produce, reg); status != "" {
			responses[status] = resp
		}
	}
	for _, v := range tags.getAll("Failure") {
		if status, resp := buildResponse(v, produce, reg); status != "" {
			responses[status] = resp
		}
	}

	var examples []parsedExample
	for _, v := range tags.getAll("Example") {
		if example, ok := parseExample(v); ok {
			examples = append(examples, example)
		}
	}
	attachExamples(examples, requestBody, responses)

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
// A compatibility shim declaring `Thing = other.Thing` is a signpost, not a declaration.
// Taking it because its directory came first describes the type it points at as an object
// accepting anything, so an alias is only used when nothing declares the type outright.
func findTypeFile(typeName string, dirs []string) string {
	var aliasPath string
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
					if typeSpec.Name.Name != typeName {
						continue
					}
					if typeSpec.Assign.IsValid() {
						if aliasPath == "" {
							aliasPath = path
						}
						continue
					}
					return path
				}
			}
		}
	}
	return aliasPath
}
