package oapifly

import (
	"encoding/json"
	"reflect"
	"sort"
)

// AnyOf is implemented by a request type whose body must meet one of several rules that its
// fields' tags cannot state one field at a time (#68): nonempty ids or a filter, say. The
// rule is written into the schema as anyOf and enforced when the body is bound, as the tags
// are.
type AnyOf interface {
	AnyOf() AnyOfRule
}

// AnyOfRule is the alternatives a body may meet, and how a body meeting none is refused: at
// Field, saying Message. Without a Field it is reported at the body; without a Message it
// says "must meet one of its alternatives".
type AnyOfRule struct {
	Alternatives []Alternative
	Field        string
	Message      string
}

// Alternative is one way to meet an AnyOfRule: the properties it needs present and not null,
// and constraints on properties in the tag vocabulary (`minItems:"1"`), which hold of a
// property only when it is present.
type Alternative struct {
	Required []string
	Fields   map[string]string
}

// anyOfRule is the rule t states, if it states one.
func anyOfRule(t reflect.Type) (AnyOfRule, bool) {
	if t.Kind() != reflect.Struct {
		return AnyOfRule{}, false
	}
	rule, ok := reflect.New(t).Interface().(AnyOf)
	if !ok {
		return AnyOfRule{}, false
	}
	return rule.AnyOf(), true
}

// anyOfSchema writes a struct's rule into its schema: each alternative a subschema of its
// required properties and the constraints its tags give them.
func (r *schemaRegistry) anyOfSchema(t reflect.Type, schema map[string]interface{}) {
	rule, ok := anyOfRule(t)
	if !ok || len(rule.Alternatives) == 0 {
		return
	}
	fields := structFieldsOf(t)
	var alternatives []interface{}
	for _, alt := range rule.Alternatives {
		sub := map[string]interface{}{}
		props := map[string]interface{}{}
		names := make([]string, 0, len(alt.Fields))
		for name := range alt.Fields {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			f, declared := fields.byName[name]
			if !declared {
				r.warn(Warning{Kind: WarnAnyOfUnknownField, Type: typeDisplay(t), Field: name},
					"%s anyOf names %s, which it does not declare", typeDisplay(t), name)
				continue
			}
			constraint := map[string]interface{}{"type": r.reflectSchema(f.typ)["type"]}
			applyTagConstraints(constraint, alt.Fields[name])
			delete(constraint, "type")
			props[name] = constraint
		}
		if len(props) > 0 {
			sub["properties"] = props
		}
		if len(alt.Required) > 0 {
			sub["required"] = append([]string(nil), alt.Required...)
		}
		alternatives = append(alternatives, sub)
	}
	schema["anyOf"] = alternatives
}

// anyOfProblem reports, for a body read as obj, whether it meets none of t's alternatives,
// and where and how that is said.
func (b Binder) anyOfProblem(obj map[string]json.RawMessage, t reflect.Type, path string) (string, string, bool) {
	rule, ok := anyOfRule(t)
	if !ok || len(rule.Alternatives) == 0 {
		return "", "", false
	}
	fields := structFieldsOf(t)
	for _, alt := range rule.Alternatives {
		if b.meets(obj, fields, alt, path) {
			return "", "", false
		}
	}
	where := at(path)
	if rule.Field != "" {
		where = join(path, rule.Field)
	}
	message := rule.Message
	if message == "" {
		message = "must meet one of its alternatives"
	}
	return where, message, true
}

func (b Binder) meets(obj map[string]json.RawMessage, fields *structFields, alt Alternative, path string) bool {
	for _, name := range alt.Required {
		value, present := obj[name]
		if !present || isNull(value) {
			return false
		}
	}
	for name, tag := range alt.Fields {
		value, present := obj[name]
		f, declared := fields.byName[name]
		if !present || !declared {
			continue
		}
		problems := Violations{}
		b.check(value, f.typ, reflect.StructTag(tag), join(path, name), problems)
		if len(problems) > 0 {
			return false
		}
	}
	return true
}
