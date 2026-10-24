package oapifly

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/mail"
	"net/url"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ErrMalformedBody is what Bind answers for a body that is not JSON at all, is not the shape
// its type reads (an array for an object), or is a required body that is missing: the request
// cannot be read, as opposed to read and found to break the rules (Violations). A server
// answers it with 400.
var ErrMalformedBody = errors.New("oapifly: malformed request body")

// ErrUnsupportedMediaType is what Bind answers for a body whose Content-Type is not one of the
// forms a request type takes it in, when it takes it in several (#55). The error names the
// media types it does take. A server answers it with 415.
var ErrUnsupportedMediaType = errors.New("oapifly: unsupported media type")

// Violations are the parts of a request that break the rules its type declares, each with
// what is wrong with it. A parameter is named as it is in the request ("limit"), a body field
// by its JSON path ("name", "owner.email", "members[2]"), and the body itself as "body". A
// server answers it with 422 and the fields.
type Violations map[string]string

func (v Violations) Error() string {
	names := make([]string, 0, len(v))
	for name := range v {
		names = append(names, name)
	}
	sort.Strings(names)
	parts := make([]string, len(names))
	for i, name := range names {
		parts[i] = name + ": " + v[name]
	}
	return "oapifly: " + strings.Join(parts, "; ")
}

// A field's message tag, when it has one, is the text reported for any of its constraints that
// does not hold, in place of Bind's own wording; a missing field is still "required" and a
// value of the wrong type still says what type it must be.
//
// Binder reads requests into the types registered for them (Endpoint.Request), enforcing the
// rules the document describes from those same types (#23):
//   - a parameter is required if it is in the path or its tag says ,required;
//   - a body field without omitempty is required, and may be null only if it is a pointer;
//   - a struct is closed: a property it does not declare is refused, unless the struct is
//     open (a field tagged oapi:"open", or OpenStructs);
//   - a value has the type it is described with;
//   - the minimum, maximum, minLength, maxLength, pattern, enums, format, minItems and
//     maxItems tags hold, wherever the field is.
//
// Rules that depend on state, such as a name already being taken, stay in the handler.
type Binder struct {
	// OpenStructs accepts and ignores properties a struct does not declare, matching a
	// document generated with Config.OpenStructs.
	OpenStructs bool
}

// Bind reads a request into v with the default Binder. See Binder.Bind.
func Bind(v interface{}, param func(in, name string) (string, bool), body []byte) error {
	return Binder{}.Bind(v, param, body)
}

// Bind reads a request into v, a pointer to the request type. param looks a parameter up by
// where it is ("path", "query" or "header") and its name, reporting whether the request has
// it; body is the request body. Fields are read as Endpoint.Request describes them: tagged
// path, query and header fields are parameters, the field tagged body:"" is the body, and a
// type with neither is the body itself.
//
// A request type that takes its body in several forms, a body field each, has the one its
// Content-Type header names set and the others left nil; with no Content-Type it is the
// first. A body is required in that case.
//
// It returns nil, Violations for a request that breaks the rules, an error wrapping
// ErrMalformedBody for a body that cannot be read, one wrapping ErrUnsupportedMediaType for a
// body in a form the type does not take, or another error for a request type it cannot bind,
// which is a programming error.
func (b Binder) Bind(v interface{}, param func(in, name string) (string, bool), body []byte) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("oapifly: Bind needs a non-nil pointer, not %T", v)
	}
	target := rv.Elem()
	t := target.Type()
	violations := Violations{}

	params, forms := requestLayout(t)
	for _, p := range params {
		if err := b.bindParameter(target.FieldByIndex(p.index), p, param, violations); err != nil {
			return err
		}
	}

	switch {
	case len(forms) == 1:
		if err := b.bindBody(target.FieldByIndex(forms[0].index), forms[0].media[0], body, violations); err != nil {
			return err
		}
	case len(forms) > 1:
		form, media, err := chooseForm(forms, param)
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(body)) == 0 {
			return fmt.Errorf("%w: the request has no body", ErrMalformedBody)
		}
		if err := b.bindBody(target.FieldByIndex(form.index), media, body, violations); err != nil {
			return err
		}
	case len(params) == 0 && !(t.Kind() == reflect.Struct && t.NumField() == 0):
		if err := b.bindBody(target, "", body, violations); err != nil {
			return err
		}
	}

	if len(violations) > 0 {
		return violations
	}
	return nil
}

type paramField struct {
	index    []int
	in, name string
	required bool
	tag      reflect.StructTag
}

// bodyForm is a body field and the media types it takes the body in.
type bodyForm struct {
	index []int
	media []string
}

// bodyMediaTypes is the media types a body tag names, comma-separated: JSON when it names
// none.
func bodyMediaTypes(tag string) []string {
	var media []string
	for _, m := range strings.Split(tag, ",") {
		if m = strings.TrimSpace(m); m != "" {
			media = append(media, m)
		}
	}
	if len(media) == 0 {
		return []string{"application/json"}
	}
	return media
}

// chooseForm is the body form, and the media type in it, a request's Content-Type names: the
// first when it names none.
func chooseForm(forms []bodyForm, param func(in, name string) (string, bool)) (bodyForm, string, error) {
	contentType, ok := param("header", "Content-Type")
	if !ok || strings.TrimSpace(contentType) == "" {
		return forms[0], forms[0].media[0], nil
	}
	got := strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	var accepted []string
	for _, form := range forms {
		for _, m := range form.media {
			if strings.ToLower(strings.TrimSpace(strings.SplitN(m, ";", 2)[0])) == got {
				return form, m, nil
			}
			accepted = append(accepted, m)
		}
	}
	return bodyForm{}, "", fmt.Errorf("%w: %s; the body is taken as %s", ErrUnsupportedMediaType, got, strings.Join(accepted, ", "))
}

// requestLayout finds a request type's parameter fields and its body forms, by the rules
// endpointRequest describes them with: a body field that names a media type an earlier one
// takes is not another form.
func requestLayout(t reflect.Type) ([]paramField, []bodyForm) {
	if t.Kind() != reflect.Struct {
		return nil, nil
	}
	var params []paramField
	var forms []bodyForm
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		in := ""
		for _, where := range []string{"path", "query", "header"} {
			if f.Tag.Get(where) != "" {
				in = where
				break
			}
		}
		switch {
		case in != "":
			name, opts, _ := strings.Cut(f.Tag.Get(in), ",")
			params = append(params, paramField{
				index: f.Index, in: in, name: name, tag: f.Tag,
				required: in == "path" || hasOption(opts, "required"),
			})
		case hasTag(f, "body"):
			media := bodyMediaTypes(f.Tag.Get("body"))
			if mediaTaken(forms, media) == "" {
				forms = append(forms, bodyForm{index: f.Index, media: media})
			}
		}
	}
	return params, forms
}

// mediaTaken is a media type in media that one of forms already takes, "" when there is none.
func mediaTaken(forms []bodyForm, media []string) string {
	for _, form := range forms {
		for _, a := range form.media {
			for _, b := range media {
				if strings.EqualFold(strings.TrimSpace(strings.SplitN(a, ";", 2)[0]), strings.TrimSpace(strings.SplitN(b, ";", 2)[0])) {
					return b
				}
			}
		}
	}
	return ""
}

func (b Binder) bindParameter(field reflect.Value, p paramField, param func(in, name string) (string, bool), out Violations) error {
	raw, present := param(p.in, p.name)
	if !present {
		if p.required {
			out[p.name] = "required"
		}
		return nil
	}

	dst := field
	if dst.Kind() == reflect.Pointer {
		dst = reflect.New(field.Type().Elem()).Elem()
	}
	problem, err := parseParameter(dst, raw)
	if err != nil {
		return fmt.Errorf("oapifly: parameter %s: %w", p.name, err)
	}
	if problem == "" {
		problem = valueProblem(dst, p.tag)
	}
	if problem != "" {
		out[p.name] = problem
		return nil
	}
	if field.Kind() == reflect.Pointer {
		field.Set(dst.Addr())
	}
	return nil
}

// parseParameter parses a parameter's text into dst. It answers what is wrong with the text,
// or an error for a field kind no parameter can have.
func parseParameter(dst reflect.Value, raw string) (string, error) {
	if u, ok := dst.Addr().Interface().(encoding.TextUnmarshaler); ok {
		if err := u.UnmarshalText([]byte(raw)); err != nil {
			return "must be " + describedAs(dst.Type()), nil
		}
		return "", nil
	}
	switch dst.Kind() {
	case reflect.String:
		dst.SetString(raw)
	case reflect.Bool:
		// A boolean parameter is serialized as true or false: the other spellings
		// strconv.ParseBool reads (1, t, TRUE and so on) are not the schema's.
		switch raw {
		case "true":
			dst.SetBool(true)
		case "false":
			dst.SetBool(false)
		default:
			return "must be a boolean", nil
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		v, err := strconv.ParseInt(raw, 10, dst.Type().Bits())
		if err != nil {
			return "must be an integer", nil
		}
		dst.SetInt(v)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		v, err := strconv.ParseUint(raw, 10, dst.Type().Bits())
		if err != nil {
			return "must be a non-negative integer", nil
		}
		dst.SetUint(v)
	case reflect.Float32, reflect.Float64:
		v, err := strconv.ParseFloat(raw, dst.Type().Bits())
		if err != nil {
			return "must be a number", nil
		}
		dst.SetFloat(v)
	default:
		return "", fmt.Errorf("a %s cannot be a parameter", dst.Type())
	}
	return "", nil
}

func (b Binder) bindBody(dst reflect.Value, media string, body []byte, out Violations) error {
	if media != "" && !isJSONMedia(media) {
		// A body that is not JSON is handed over as it is. A pointer field is an optional
		// body: left nil when the request has none (#50).
		field := dst.Type()
		text := field
		if text.Kind() == reflect.Pointer {
			text = text.Elem()
		}
		if text.Kind() != reflect.String && (text.Kind() != reflect.Slice || text.Elem().Kind() != reflect.Uint8) {
			return fmt.Errorf("oapifly: a %s body must be a string or []byte field, or a pointer to one, not %s", media, field)
		}
		if field.Kind() == reflect.Pointer {
			if len(body) == 0 {
				return nil
			}
			dst.Set(reflect.New(text))
			dst = dst.Elem()
		}
		if text.Kind() == reflect.String {
			dst.SetString(string(body))
		} else {
			dst.SetBytes(append([]byte(nil), body...))
		}
		return nil
	}

	if len(bytes.TrimSpace(body)) == 0 {
		if dst.Kind() == reflect.Pointer {
			// An optional body, left out.
			return nil
		}
		return fmt.Errorf("%w: the request has no body", ErrMalformedBody)
	}
	if !json.Valid(body) {
		return fmt.Errorf("%w: not JSON", ErrMalformedBody)
	}
	if problem := rootShapeProblem(body, dst.Type()); problem != "" {
		return fmt.Errorf("%w: %s", ErrMalformedBody, problem)
	}

	b.check(body, dst.Type(), "", "", out)
	if len(out) > 0 {
		return nil
	}
	if err := json.Unmarshal(body, dst.Addr().Interface()); err != nil {
		return fmt.Errorf("%w: %v", ErrMalformedBody, err)
	}
	return nil
}

var (
	jsonUnmarshalerType = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()
	textUnmarshalerType = reflect.TypeOf((*encoding.TextUnmarshaler)(nil)).Elem()
)

// check walks a JSON value beside the Go type it is read into, recording what breaks the
// rules. Walking the JSON rather than the decoded value is what tells a field left out from
// one sent as its zero value.
func (b Binder) check(raw json.RawMessage, t reflect.Type, tag reflect.StructTag, path string, out Violations) {
	if _, stated, _ := statedSchema(string(tag)); stated {
		// The field states its JSON type (#46): the value is checked against that, not the Go
		// type, which cannot say it.
		if isNull(raw) {
			if t.Kind() != reflect.Pointer {
				out[at(path)] = nullWording(tag)
			}
			return
		}
		checkStated(raw, tag, path, out)
		return
	}
	if known, ok := qualifiedJSONTypes[qualifiedName(t)]; ok && len(known) == 0 {
		// json.RawMessage: whatever the client sent.
		return
	}
	if isNull(raw) {
		if t.Kind() != reflect.Pointer && t.Kind() != reflect.Interface && !nullableKnownType(t) {
			out[at(path)] = nullWording(tag)
		}
		return
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	// A type that reads itself (time.Time, uuid.UUID) is checked by reading it.
	if reflect.PointerTo(t).Implements(jsonUnmarshalerType) || reflect.PointerTo(t).Implements(textUnmarshalerType) {
		if err := json.Unmarshal(raw, reflect.New(t).Interface()); err != nil {
			out[at(path)] = "must be " + describedAs(t)
		}
		return
	}

	switch t.Kind() {
	case reflect.Interface:
		return
	case reflect.Struct:
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			out[at(path)] = "must be an object"
			return
		}
		fields := structFieldsOf(t)
		for _, f := range fields.list {
			if _, present := obj[f.name]; !present && f.required {
				out[join(path, f.name)] = "required"
			}
		}
		for key, value := range obj {
			f, declared := fields.byName[key]
			if !declared {
				if !fields.open && !b.OpenStructs {
					out[join(path, key)] = "not allowed"
				}
				continue
			}
			b.check(value, f.typ, f.tag, join(path, key), out)
		}
	case reflect.Map:
		var obj map[string]json.RawMessage
		if json.Unmarshal(raw, &obj) != nil {
			out[at(path)] = "must be an object"
			return
		}
		for key, value := range obj {
			b.check(value, t.Elem(), "", join(path, key), out)
		}
	case reflect.Slice, reflect.Array:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			if json.Unmarshal(raw, new([]byte)) != nil {
				out[at(path)] = "must be a base64 string"
			}
			return
		}
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			out[at(path)] = "must be an array"
			return
		}
		if problem := worded(itemsProblem(len(items), tag))(tag); problem != "" {
			out[at(path)] = problem
		}
		for i, item := range items {
			b.check(item, t.Elem(), "", fmt.Sprintf("%s[%d]", path, i), out)
		}
	default:
		v := reflect.New(t).Elem()
		if json.Unmarshal(raw, v.Addr().Interface()) != nil {
			out[at(path)] = "must be " + describedAs(t)
			return
		}
		if problem := valueProblem(v, tag); problem != "" {
			out[at(path)] = problem
		}
	}
}

// rootShapeProblem says what is wrong with a body that is not even the shape its type reads:
// an array where an object is read, an object where a list is, or null where the body cannot
// be. That is a body the request cannot be read from, not one read and found to break a rule.
func rootShapeProblem(body []byte, t reflect.Type) string {
	if isNull(body) {
		if t.Kind() == reflect.Pointer || t.Kind() == reflect.Interface {
			return ""
		}
		return "the body is null"
	}
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if reflect.PointerTo(t).Implements(jsonUnmarshalerType) || reflect.PointerTo(t).Implements(textUnmarshalerType) {
		return ""
	}
	first := bytes.TrimSpace(body)[0]
	switch t.Kind() {
	case reflect.Struct, reflect.Map:
		if first != '{' {
			return "the body is not a JSON object"
		}
	case reflect.Slice, reflect.Array:
		if t.Kind() == reflect.Slice && t.Elem().Kind() == reflect.Uint8 {
			return ""
		}
		if first != '[' {
			return "the body is not a JSON array"
		}
	}
	return ""
}

func isNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

func nullableKnownType(t reflect.Type) bool {
	known, ok := qualifiedJSONTypes[qualifiedName(t)]
	return ok && permitsNull(known)
}

func at(path string) string {
	if path == "" {
		return "body"
	}
	return path
}

func join(path, name string) string {
	if path == "" {
		return name
	}
	return path + "." + name
}

// describedAs names what a value of type t must be, in the terms the document uses.
func describedAs(t reflect.Type) string {
	if known, ok := qualifiedJSONTypes[qualifiedName(t)]; ok {
		if format, ok := known["format"].(string); ok {
			return "a " + format
		}
	}
	switch t.Kind() {
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "a boolean"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "an integer"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "a non-negative integer"
	case reflect.Float32, reflect.Float64:
		return "a number"
	}
	return "a valid " + t.String()
}

// jsonField is one property of a struct as encoding/json reads it.
type jsonField struct {
	name     string
	typ      reflect.Type
	tag      reflect.StructTag
	required bool
}

type structFields struct {
	list   []jsonField
	byName map[string]jsonField
	open   bool
}

var structFieldCache sync.Map // reflect.Type -> *structFields

// structFieldsOf lists a struct's properties by the rules reflectStructBody describes them
// with: exported fields, the json tag's name, "-" to leave a field out, omitempty for one
// that need not be present, embedded structs' fields promoted, and oapi:"open" to open it.
func structFieldsOf(t reflect.Type) *structFields {
	if cached, ok := structFieldCache.Load(t); ok {
		return cached.(*structFields)
	}
	fields := &structFields{byName: map[string]jsonField{}}
	collectFields(t, fields, map[reflect.Type]bool{})
	for _, f := range fields.list {
		fields.byName[f.name] = f
	}
	structFieldCache.Store(t, fields)
	return fields
}

func collectFields(t reflect.Type, into *structFields, visiting map[reflect.Type]bool) {
	if visiting[t] {
		return
	}
	visiting[t] = true
	defer delete(visiting, t)

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if opensSchema(string(f.Tag)) {
			into.open = true
		}
		jsonTag, hasJSON := f.Tag.Lookup("json")
		name, opts, _ := strings.Cut(jsonTag, ",")

		if f.Anonymous && (!hasJSON || name == "") {
			embedded := f.Type
			for embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				collectFields(embedded, into, visiting)
				continue
			}
		}
		if !f.IsExported() || jsonTag == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		into.list = append(into.list, jsonField{
			name:     name,
			typ:      f.Type,
			tag:      f.Tag,
			required: !hasOption(opts, "omitempty"),
		})
	}
}

// valueProblem checks a scalar value against its field's constraint tags, the ones
// applyTagConstraints describes, and says what is wrong with it.
func valueProblem(v reflect.Value, tag reflect.StructTag) string {
	return worded(constraintProblem(v, tag))(tag)
}

// worded is a field's own wording for a broken constraint: the constraint's own message tag
// (maxLengthMessage for maxLength, pattern2Message for pattern2, #58), else the message tag
// for any of them, else the generator's. An API keeps the words its clients already show
// while its rules move into the type.
func worded(problem, constraint string) func(reflect.StructTag) string {
	return func(tag reflect.StructTag) string {
		if problem == "" {
			return ""
		}
		if constraint != "" {
			if message := tag.Get(constraint + "Message"); message != "" {
				return message
			}
		}
		if message := tag.Get("message"); message != "" {
			return message
		}
		return problem
	}
}

// nullWording is what a field that may not be null says when it is: its nullMessage tag, or
// the generator's words. The message tag is for the rules a value breaks, not for its type.
func nullWording(tag reflect.StructTag) string {
	if message := tag.Get("nullMessage"); message != "" {
		return message
	}
	return "must not be null"
}

// patternsOf is a field's patterns in order, pattern then pattern2, pattern3 and on, each
// with the tag key it is under: a value must match all of them (#58).
func patternsOf(tag reflect.StructTag) (keys, values []string) {
	for i := 1; ; i++ {
		key := "pattern"
		if i > 1 {
			key += strconv.Itoa(i)
		}
		value := tag.Get(key)
		if value == "" {
			return keys, values
		}
		keys, values = append(keys, key), append(values, value)
	}
}

// constraintProblem says what is wrong with v under its field's constraint tags, and which
// constraint it breaks.
func constraintProblem(v reflect.Value, tag reflect.StructTag) (problem, constraint string) {
	if tag == "" {
		return "", ""
	}
	switch v.Kind() {
	case reflect.String:
		s := v.String()
		if enums := tag.Get("enums"); enums != "" && !oneOf(s, enums) {
			return "must be one of " + listedQuoted(enums), "enums"
		}
		length := utf8.RuneCountInString(s)
		if n, ok := lengthBound(tag, "minLength"); ok && length < n {
			return "must be at least " + counted(n, "character"), "minLength"
		}
		if n, ok := lengthBound(tag, "maxLength"); ok && length > n {
			return "must be at most " + counted(n, "character"), "maxLength"
		}
		keys, values := patternsOf(tag)
		for i, pattern := range values {
			if re := compiledPattern(pattern); re != nil && !re.MatchString(s) {
				return "must match " + pattern, keys[i]
			}
		}
		if format := tag.Get("format"); format != "" && !formatHolds(format, s) {
			return "must be a valid " + format, "format"
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		var n float64
		switch v.Kind() {
		case reflect.Float32, reflect.Float64:
			n = v.Float()
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			n = float64(v.Uint())
		default:
			n = float64(v.Int())
		}
		kind := numberKind(v.Kind())
		if enums := tag.Get("enums"); enums != "" && !numberOneOf(n, enums, kind) {
			return "must be one of " + listed(enums), "enums"
		}
		if bound, ok := numberBound(tag, "minimum", kind); ok && n < bound {
			return "must be at least " + strconv.FormatFloat(bound, 'f', -1, 64), "minimum"
		}
		if bound, ok := numberBound(tag, "maximum", kind); ok && n > bound {
			return "must be at most " + strconv.FormatFloat(bound, 'f', -1, 64), "maximum"
		}
	}
	return "", ""
}

// itemsProblem says what is wrong with a list of n items under minItems and maxItems, and
// which of them it breaks.
func itemsProblem(n int, tag reflect.StructTag) (problem, constraint string) {
	if bound, ok := lengthBound(tag, "minItems"); ok && n < bound {
		return "must have at least " + counted(bound, "item"), "minItems"
	}
	if bound, ok := lengthBound(tag, "maxItems"); ok && n > bound {
		return "must have at most " + counted(bound, "item"), "maxItems"
	}
	return "", ""
}

// counted is "1 item" or "3 items".
func counted(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return strconv.Itoa(n) + " " + noun + "s"
}

func numberKind(k reflect.Kind) string {
	if k == reflect.Float32 || k == reflect.Float64 {
		return "number"
	}
	return "integer"
}

// numberBound reads a minimum or maximum the way applyTagConstraints does, so a bound the
// document leaves off is not enforced either.
func numberBound(tag reflect.StructTag, key, kind string) (float64, bool) {
	n, ok := typedValue(tag.Get(key), kind).(float64)
	return n, ok
}

func lengthBound(tag reflect.StructTag, key string) (int, bool) {
	n, ok := typedValue(tag.Get(key), "integer").(float64)
	if !ok || n < 0 {
		return 0, false
	}
	return int(n), true
}

func oneOf(s, enums string) bool {
	for _, option := range strings.Split(enums, ",") {
		if strings.TrimSpace(option) == s {
			return true
		}
	}
	return false
}

func numberOneOf(n float64, enums, kind string) bool {
	for _, option := range strings.Split(enums, ",") {
		if value, ok := typedValue(strings.TrimSpace(option), kind).(float64); ok && value == n {
			return true
		}
	}
	return false
}

// listedQuoted lists string values quoted, so an empty one can be told apart: "a", "b" or "".
func listedQuoted(enums string) string {
	parts := strings.Split(enums, ",")
	for i := range parts {
		parts[i] = strconv.Quote(strings.TrimSpace(parts[i]))
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return strings.Join(parts[:len(parts)-1], ", ") + " or " + parts[len(parts)-1]
}

func listed(enums string) string {
	parts := strings.Split(enums, ",")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, ", ")
}

var patterns sync.Map // string -> *regexp.Regexp, nil for one that does not compile

func compiledPattern(pattern string) *regexp.Regexp {
	if cached, ok := patterns.Load(pattern); ok {
		re, _ := cached.(*regexp.Regexp)
		return re
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		re = nil
	}
	patterns.Store(pattern, re)
	return re
}

var (
	uuidPattern     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	hostnamePattern = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)
)

// formatHolds checks the formats this package can check. JSON Schema treats a format as an
// annotation unless a validator knows it, so one not listed here is described and not
// enforced.
func formatHolds(format, s string) bool {
	switch format {
	case "email":
		addr, err := mail.ParseAddress(s)
		return err == nil && addr.Address == s
	case "uuid":
		return uuidPattern.MatchString(s)
	case "date-time":
		_, err := time.Parse(time.RFC3339, s)
		return err == nil
	case "date":
		_, err := time.Parse("2006-01-02", s)
		return err == nil
	case "uri":
		u, err := url.Parse(s)
		return err == nil && u.IsAbs()
	case "hostname":
		return len(s) <= 253 && hostnamePattern.MatchString(s)
	case "ipv4":
		ip := net.ParseIP(s)
		return ip != nil && ip.To4() != nil && !strings.Contains(s, ":")
	case "ipv6":
		ip := net.ParseIP(s)
		return ip != nil && strings.Contains(s, ":")
	}
	return true
}
