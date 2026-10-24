package oapifly

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
)

// A field can state its own JSON type with a tag, when its Go type cannot say it (#46):
// `type:"array" items:"string"` on a json.RawMessage holding a list of addresses, or on a
// named []byte that writes JSON of its own. The stated type replaces the one the Go type
// would be described as, on both description paths, and Bind checks the value against it.
// The other constraint tags (enums, format, minItems, ...) still refine it.

// statedTypes are the JSON Schema types a field may state.
var statedTypes = map[string]bool{"string": true, "integer": true, "number": true, "boolean": true, "array": true, "object": true}

// statedSchema is the schema a field's type tag states, and whether it states one. A type
// that is not one of statedTypes, or items on anything but an array, is a problem, described
// rather than guessed at.
func statedSchema(tag string) (schema map[string]interface{}, stated bool, problem string) {
	typ := strings.TrimSpace(extractTagValue(tag, "type"))
	items := strings.TrimSpace(extractTagValue(tag, "items"))
	if typ == "" {
		if items != "" {
			return nil, false, "items is stated without type:\"array\""
		}
		return nil, false, ""
	}
	if !statedTypes[typ] {
		return nil, false, "type " + typ + " is not one of string, integer, number, boolean, array or object"
	}
	schema = map[string]interface{}{"type": typ}
	if items != "" {
		if typ != "array" {
			return nil, false, "items is stated on type " + typ + ", not array"
		}
		if !statedTypes[items] || items == "array" {
			return nil, false, "items " + items + " is not one of string, integer, number, boolean or object"
		}
		schema["items"] = map[string]interface{}{"type": items}
	}
	return schema, true, ""
}

// checkStated reports where raw is not the JSON type the field's tag states, and an array's
// item count against minItems and maxItems, as for any list.
func checkStated(raw json.RawMessage, tag reflect.StructTag, path string, out Violations) {
	typ, items := strings.TrimSpace(tag.Get("type")), strings.TrimSpace(tag.Get("items"))
	if !isJSONType(raw, typ) {
		// A value of the wrong type says which it must be, unless the field words that
		// itself (typeMessage, #58); the message tag is for the rules a value breaks.
		out[at(path)] = "must be " + article(typ)
		if message := tag.Get("typeMessage"); message != "" {
			out[at(path)] = message
		}
		return
	}
	if typ != "array" {
		// A scalar is held to the other tags as any field of its type is: enums, format,
		// pattern, lengths and bounds.
		var scalar reflect.Value
		switch typ {
		case "string":
			scalar = reflect.New(reflect.TypeOf("")).Elem()
		case "integer":
			scalar = reflect.New(reflect.TypeOf(int64(0))).Elem()
		case "number":
			scalar = reflect.New(reflect.TypeOf(float64(0))).Elem()
		default:
			return
		}
		_ = json.Unmarshal(raw, scalar.Addr().Interface())
		if problem := valueProblem(scalar, tag); problem != "" {
			out[at(path)] = problem
		}
		return
	}
	var list []json.RawMessage
	_ = json.Unmarshal(raw, &list)
	if problem := worded(itemsProblem(len(list), tag))(tag); problem != "" {
		out[at(path)] = problem
	}
	if items == "" {
		return
	}
	for i, item := range list {
		if !isJSONType(item, items) {
			out[fmt.Sprintf("%s[%d]", path, i)] = "must be " + article(items)
		}
	}
}

// isJSONType reports whether raw is a JSON value of type typ.
func isJSONType(raw json.RawMessage, typ string) bool {
	var v interface{}
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	switch typ {
	case "string":
		_, ok := v.(string)
		return ok
	case "number":
		_, ok := v.(float64)
		return ok
	case "integer":
		n, ok := v.(float64)
		return ok && n == float64(int64(n))
	case "boolean":
		_, ok := v.(bool)
		return ok
	case "array":
		_, ok := v.([]interface{})
		return ok
	case "object":
		_, ok := v.(map[string]interface{})
		return ok
	}
	return false
}

func article(typ string) string {
	switch typ {
	case "integer", "array", "object":
		return "an " + typ
	}
	return "a " + typ
}
