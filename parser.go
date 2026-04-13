package oapifly

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"reflect"
	"strings"
)

// detectJSONType uses reflection and json.Marshal to infer the JSON type.
func detectJSONType(t reflect.Type) string {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	v := reflect.New(t).Elem().Interface()
	data, _ := json.Marshal(v)
	var out interface{}
	json.Unmarshal(data, &out)
	switch out.(type) {
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	case []interface{}:
		return "array"
	case map[string]interface{}:
		return "object"
	default:
		return "object"
	}
}

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

// buildPathItemWithSchemas parses swaggo tags and builds a PathItem with schema references.
func buildPathItemWithSchemas(tags map[string]string, schemaTypes map[string]map[string]interface{}) PathItem {
	summary := tags["Summary"]
	description := tags["Description"]
	tagsList := []string{}
	if v, ok := tags["Tags"]; ok {
		tagsList = strings.Split(v, ",")
	}
	parameters := []Parameter{}

	if routerTag, ok := tags["Router"]; ok {
		fields := strings.Fields(routerTag)
		if len(fields) == 2 {
			path := fields[0]
			paramExamples := map[string]string{}
			for k, v := range tags {
				if k == "Param" {
					parts := strings.Fields(v)
					if len(parts) >= 5 && parts[1] == "path" {
						paramName := parts[0]
						exampleVal := ""
						for i := 5; i < len(parts); i++ {
							p := parts[i]
							if strings.HasPrefix(p, "example(") && strings.HasSuffix(p, ")") {
								exampleVal = p[len("example(") : len(p)-1]
							} else if p == "example" && i+1 < len(parts) {
								next := parts[i+1]
								if strings.HasPrefix(next, "\"") && strings.HasSuffix(next, "\"") && len(next) > 1 {
									exampleVal = next[1 : len(next)-1]
								}
							}
						}
						if exampleVal != "" {
							paramExamples[paramName] = exampleVal
						}
					}
				}
			}
			start := 0
			for start < len(path) {
				open := strings.Index(path[start:], "{")
				if open == -1 {
					break
				}
				open += start
				close := strings.Index(path[open:], "}")
				if close == -1 {
					break
				}
				close += open
				paramName := path[open+1 : close]
				exampleVal := paramName
				if ex, ok := paramExamples[paramName]; ok && ex != "" {
					exampleVal = ex
				}
				parameters = append(parameters, Parameter{
					Name:        paramName,
					In:          "path",
					Description: "Path parameter '" + paramName + "'",
					Required:    true,
					Schema:      map[string]string{"type": "string"},
					Example:     exampleVal,
				})
				start = close + 1
			}
		}
	}

	responses := map[string]Response{}

	for k, v := range tags {
		if k == "Success" || k == "Failure" {
			fields := strings.Fields(v)
			if len(fields) >= 3 {
				status := fields[0]
				openapiType := fields[1]
				refType := ""
				if len(fields) > 2 {
					refType = fields[2]
				}
				desc := ""
				if len(fields) > 3 {
					desc = strings.Join(fields[3:], " ")
				}
				content := map[string]interface{}{}
				if refType != "" {
					refName := refType
					if strings.HasPrefix(refType, "types.") {
						refName = strings.TrimPrefix(refType, "types.")
					}
					if _, ok := schemaTypes[refName]; !ok {
						typeFile := findTypeFile(refName)
						if typeFile != "" {
							if t := findReflectTypeByName(refName); t != nil {
								schemaTypes[refName] = generateSchemaForTypeReflect(t)
							} else {
								schemaTypes[refName] = map[string]interface{}{"type": "object"}
							}
						} else {
							schemaTypes[refName] = map[string]interface{}{"type": "object"}
						}
					}
					if openapiType == "{array}" {
						content["application/json"] = map[string]interface{}{
							"schema": map[string]interface{}{
								"type":  "array",
								"items": map[string]interface{}{"$ref": "#/components/schemas/" + refName},
							},
						}
					} else {
						content["application/json"] = map[string]interface{}{
							"schema": map[string]interface{}{"$ref": "#/components/schemas/" + refName},
						}
					}
				}
				responses[status] = Response{
					Description: strings.Trim(desc, "\""),
					Content:     content,
				}
			}
		}
	}

	return PathItem{
		Summary:     summary,
		Description: description,
		Tags:        tagsList,
		Parameters:  parameters,
		Responses:   responses,
	}
}

// findTypeFile tries to locate the Go file containing a struct definition.
func findTypeFile(typeName string) string {
	typesDir := "types"
	entries, err := os.ReadDir(typesDir)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		path := typesDir + "/" + entry.Name()
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
	return ""
}

// resolveFieldTypeReflect resolves a struct field type to OpenAPI type info using reflection.
func resolveFieldTypeReflect(rt reflect.Type) fieldTypeInfo {
	if rt.Kind() == reflect.Ptr {
		rt = rt.Elem()
	}
	jsonType := detectJSONType(rt)
	if rt.Kind() == reflect.Struct && jsonType == "object" {
		return fieldTypeInfo{Ref: rt.Name()}
	}
	return fieldTypeInfo{Schema: map[string]interface{}{"type": jsonType}}
}

// generateSchemaForTypeReflect generates a JSON Schema map using Go reflection.
func generateSchemaForTypeReflect(rt reflect.Type) map[string]interface{} {
	if rt.Kind() == reflect.Ptr {
		rt = rt.Elem()
	}
	props := map[string]interface{}{}
	required := []string{}
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		jsonName := field.Name
		if tag, ok := field.Tag.Lookup("json"); ok {
			parts := strings.Split(tag, ",")
			if len(parts) > 0 && parts[0] != "" && parts[0] != "-" {
				jsonName = parts[0]
			}
		}
		if jsonName == "-" || jsonName == "" {
			continue
		}
		fieldType := resolveFieldTypeReflect(field.Type)
		props[jsonName] = fieldType.Schema
		omitempty := false
		if tag, ok := field.Tag.Lookup("json"); ok {
			parts := strings.Split(tag, ",")
			for _, part := range parts[1:] {
				if part == "omitempty" {
					omitempty = true
					break
				}
			}
		}
		if !omitempty {
			required = append(required, jsonName)
		}
	}
	return map[string]interface{}{
		"type":       "object",
		"properties": props,
		"required":   required,
	}
}

// findReflectTypeByName is a stub for resolving a struct name to reflect.Type.
// TODO: Implement using a type registry for full schema resolution.
func findReflectTypeByName(name string) reflect.Type {
	return nil
}

// parseFile parses a Go source file and returns the AST.
func parseFile(path string) (*ast.File, error) {
	src, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// extractHandlerDocs extracts swaggo tags from handler methods with @Router annotations.
func extractHandlerDocs(f *ast.File) []map[string]string {
	docs := []map[string]string{}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Doc == nil {
			continue
		}
		tags := map[string]string{}
		for _, comment := range fn.Doc.List {
			line := strings.TrimSpace(strings.TrimPrefix(comment.Text, "//"))
			if strings.HasPrefix(line, "@") {
				fields := strings.Fields(line)
				if len(fields) > 1 {
					tag := strings.TrimPrefix(fields[0], "@")
					value := strings.Join(fields[1:], " ")
					tags[tag] = value
				}
			}
		}
		if _, ok := tags["Router"]; ok {
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
	path := fields[0]
	method := strings.Trim(fields[1], "[]")
	return path, strings.ToLower(method)
}
