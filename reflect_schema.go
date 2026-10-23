package oapifly

import (
	"path"
	"reflect"
	"strings"
)

// reflectSchema describes a Go type by reflection, the way the annotation path describes the
// same type from its source, so a registered operation and an annotated one produce the same
// document. A named struct becomes a component and a $ref to it; everything else is inlined.
//
// It differs from the annotation path only where that path is wrong today, each on purpose:
// an interface{} field is unconstrained (#27), the well-known types are recognised wherever
// they appear (#29), two same-named types from different packages stay two components (#26),
// and []byte is the base64 string encoding/json writes.
func (r *schemaRegistry) reflectSchema(t reflect.Type) map[string]interface{} {
	if t == nil {
		return map[string]interface{}{}
	}

	if t.Kind() == reflect.Pointer {
		// A pointer is the same type, absent-able: OpenAPI 3.0 spells that nullable.
		return asNullable(r.reflectSchema(t.Elem()))
	}

	if known, ok := qualifiedJSONTypes[qualifiedName(t)]; ok {
		return copySchema(known)
	}

	switch t.Kind() {
	case reflect.Interface:
		// The concrete value is chosen at runtime, so nothing about its shape can be said.
		return map[string]interface{}{}
	case reflect.Struct:
		if t.Name() == "" {
			// An anonymous struct has no name to give a component, so it is described where
			// it stands.
			return r.reflectStructBody(t)
		}
		return r.reflectRef(t)
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			// encoding/json writes a []byte as a base64 string, not an array of numbers.
			return map[string]interface{}{"type": "string", "format": "byte"}
		}
		return map[string]interface{}{"type": "array", "items": r.reflectSchema(t.Elem())}
	case reflect.Map:
		return map[string]interface{}{
			"type":                 "object",
			"additionalProperties": r.reflectSchema(t.Elem()),
		}
	case reflect.String:
		return map[string]interface{}{"type": "string"}
	case reflect.Bool:
		return map[string]interface{}{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]interface{}{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]interface{}{"type": "number"}
	}

	r.warn("type %s has no JSON form this generator can express, described as an untyped object", t)
	return map[string]interface{}{"type": "object"}
}

// qualifiedName is a named type's name as the qualifiedJSONTypes table spells it: the last
// element of its package path, a dot, and the type's name - time.Time, uuid.UUID.
func qualifiedName(t reflect.Type) string {
	if t.Name() == "" || t.PkgPath() == "" {
		return ""
	}
	return path.Base(t.PkgPath()) + "." + t.Name()
}

// reflectRef registers a named struct as a component and refers to it.
//
// The component is keyed by the type's short name, as an annotated one is. A generic
// instantiation is named the way the annotation path names it - Page[types.Item] becomes
// Page-Item - since reflect spells it Page[full/import/path.Item]. A second, different type
// arriving at a name already taken by another package's type gets a package-qualified key
// of its own instead of silently sharing the first one's schema (#26).
func (r *schemaRegistry) reflectRef(t reflect.Type) map[string]interface{} {
	name := r.componentName(t)
	ref := map[string]interface{}{"$ref": "#/components/schemas/" + name}

	if r.resolving[name] {
		// Reached from its own definition further up the stack; the ref is still correct.
		return ref
	}
	if _, known := r.schemas[name]; known {
		return ref
	}

	r.resolving[name] = true
	schema := r.reflectStructBody(t)
	delete(r.resolving, name)

	r.schemas[name] = schema
	return ref
}

// componentName picks the component key for a named struct, and remembers which type holds it.
func (r *schemaRegistry) componentName(t reflect.Type) string {
	if r.reflectOwners == nil {
		r.reflectOwners = map[string]reflect.Type{}
	}
	name := sanitizeComponentKey(t.Name())
	if base, args, ok := parseGenericName(t.Name()); ok {
		name = genericSchemaName(base, args)
	}

	owner, taken := r.reflectOwners[name]
	if !taken || owner == t {
		r.reflectOwners[name] = t
		return name
	}

	qualified := sanitizeComponentKey(strings.ReplaceAll(t.PkgPath(), "/", ".")) + "." + name
	if other, ok := r.reflectOwners[qualified]; ok && other != t {
		r.warn("types %s and %s both reduce to component %s", typeDisplay(other), typeDisplay(t), qualified)
	} else if !ok {
		r.warn("%s and %s share the name %s; the second is described as %s",
			typeDisplay(owner), typeDisplay(t), name, qualified)
	}
	r.reflectOwners[qualified] = t
	return qualified
}

// reflectStructBody describes a struct's fields by encoding/json's rules: exported fields only,
// the json tag's name, "-" to leave a field out, and omitempty for a field that need not be
// present. An embedded struct's fields are promoted into the same object, as Go marshals them.
func (r *schemaRegistry) reflectStructBody(t reflect.Type) map[string]interface{} {
	props := map[string]interface{}{}
	var required []string

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		jsonTag, hasJSON := f.Tag.Lookup("json")
		name, opts, _ := strings.Cut(jsonTag, ",")

		if f.Anonymous && (!hasJSON || name == "") {
			embedded := f.Type
			for embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				body := r.reflectStructBody(embedded)
				if embeddedProps, ok := body["properties"].(map[string]interface{}); ok {
					for k, v := range embeddedProps {
						props[k] = v
					}
				}
				if embeddedRequired, ok := body["required"].([]string); ok {
					required = append(required, embeddedRequired...)
				}
				continue
			}
		}

		// `json:"-"` leaves a field out; `json:"-,"` names it "-".
		if !f.IsExported() || jsonTag == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}

		schema := r.reflectSchema(f.Type)
		applyTagConstraints(schema, string(f.Tag))
		props[name] = schema

		if !hasOption(opts, "omitempty") {
			required = append(required, name)
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
