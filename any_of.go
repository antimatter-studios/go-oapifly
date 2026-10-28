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
// says "must meet one of its alternatives". An alternative with its own Message explains
// the refusal instead.
type AnyOfRule struct {
	Alternatives []Alternative
	Field        string
	Message      string
}

// Alternative is one way to meet an AnyOfRule: the properties it needs present and not null,
// and constraints on properties in the tag vocabulary (`minItems:"1"`), which hold of a
// property only when it is present. With a Message or Messages, a body meeting no alternative
// is refused at each property this one lacks or breaks, saying that property's entry in
// Messages, else Message: "required when enabled" at subject, body or both.
type Alternative struct {
	Required []string
	Fields   map[string]string
	Message  string
	Messages map[string]string
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
			// The constraint holds of a present value, so null, which the field's own
			// schema answers for, does not join an enum here.
			constraint := map[string]interface{}{"type": nonNullType(r.reflectSchema(f.typ)["type"])}
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

// nonNullType is a schema type without null: "boolean" for ["boolean","null"].
func nonNullType(t interface{}) interface{} {
	types, ok := t.([]interface{})
	if !ok {
		return t
	}
	var kept []interface{}
	for _, each := range types {
		if each != "null" {
			kept = append(kept, each)
		}
	}
	if len(kept) == 1 {
		return kept[0]
	}
	return kept
}

// anyOfProblems reports, for a body read as obj, where and how it is refused when it meets
// none of t's alternatives; nothing when it meets one.
func (b Binder) anyOfProblems(obj map[string]json.RawMessage, t reflect.Type, path string) Violations {
	rule, ok := anyOfRule(t)
	if !ok || len(rule.Alternatives) == 0 {
		return nil
	}
	fields := structFieldsOf(t)
	for _, alt := range rule.Alternatives {
		if len(b.unmet(obj, fields, alt)) == 0 {
			return nil
		}
	}
	for _, alt := range rule.Alternatives {
		if alt.Message == "" && len(alt.Messages) == 0 {
			continue
		}
		problems := Violations{}
		for _, name := range b.unmet(obj, fields, alt) {
			message := alt.Messages[name]
			if message == "" {
				message = alt.Message
			}
			if message == "" {
				message = "required"
			}
			problems[join(path, name)] = message
		}
		return problems
	}
	where := at(path)
	if rule.Field != "" {
		where = join(path, rule.Field)
	}
	message := rule.Message
	if message == "" {
		message = "must meet one of its alternatives"
	}
	return Violations{where: message}
}

// unmet is the properties of obj that alt needs present and finds absent or null, or whose
// constraints they break.
func (b Binder) unmet(obj map[string]json.RawMessage, fields *structFields, alt Alternative) []string {
	var names []string
	for _, name := range alt.Required {
		value, present := obj[name]
		if !present || isNull(value) {
			names = append(names, name)
		}
	}
	for name, tag := range alt.Fields {
		value, present := obj[name]
		f, declared := fields.byName[name]
		if !present || !declared {
			continue
		}
		problems := Violations{}
		b.check(value, f.typ, reflect.StructTag(tag), name, problems)
		if len(problems) > 0 {
			names = append(names, name)
		}
	}
	return names
}
