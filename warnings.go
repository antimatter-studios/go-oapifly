package oapifly

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// WarningKind names what a warning is about, stably: a program that reads the
// report can rely on it from one release to the next.
type WarningKind string

// The kinds of warning the generator emits.
const (
	// WarnScanMatchedNothing: Config.ScanPatterns match no file.
	WarnScanMatchedNothing WarningKind = "scan-matched-nothing"
	// WarnSourceUnparsable: a scanned file is not Go the parser can read.
	WarnSourceUnparsable WarningKind = "source-unparsable"
	// WarnMethodNotDescribable: a route's method is not one a Path Item Object
	// may carry, so the route is left out.
	WarnMethodNotDescribable WarningKind = "method-not-describable"
	// WarnRouteDescribedTwice: two handlers, or two registrations, describe one
	// method and path; the later is used.
	WarnRouteDescribedTwice WarningKind = "route-described-twice"
	// WarnRegistrationIncomplete: a registered endpoint has no method or path.
	WarnRegistrationIncomplete WarningKind = "registration-incomplete"
	// WarnResponseRegisteredTwice: a registration gives one status the same
	// media type twice; the later is used.
	WarnResponseRegisteredTwice WarningKind = "response-registered-twice"
	// WarnBodyFieldAmbiguous: a request type marks more than one body field.
	WarnBodyFieldAmbiguous WarningKind = "body-field-ambiguous"
	// WarnFieldUnplaced: request fields are neither parameters nor the body.
	WarnFieldUnplaced WarningKind = "field-unplaced"
	// WarnPathParameterUntyped: a path parameter has no field in the request
	// type, so it is described as a string.
	WarnPathParameterUntyped WarningKind = "path-parameter-untyped"
	// WarnPathParameterUnrouted: a request field is a path parameter the path
	// template does not have.
	WarnPathParameterUnrouted WarningKind = "path-parameter-unrouted"
	// WarnSecuritySchemeUndefined: a security requirement names a scheme
	// Config.SecuritySchemes does not define.
	WarnSecuritySchemeUndefined WarningKind = "security-scheme-undefined"
	// WarnMiddlewareCredentialMissing: a middleware requires roles of an
	// operation that takes no credential to hold them with.
	WarnMiddlewareCredentialMissing WarningKind = "middleware-credential-missing"
	// WarnAnnotationUnreadable: an annotation line (@Header, @Security, @Link)
	// is not in its form, so it is left out.
	WarnAnnotationUnreadable WarningKind = "annotation-unreadable"
	// WarnHeaderResponseUndescribed: @Header names a response the operation
	// does not describe.
	WarnHeaderResponseUndescribed WarningKind = "header-response-undescribed"
	// WarnLinkResponseUndeclared: @Link hangs a link off a response the
	// operation does not declare.
	WarnLinkResponseUndeclared WarningKind = "link-response-undeclared"
	// WarnLinkDeclaredTwice: one response declares a link of the same name
	// twice; the last survives.
	WarnLinkDeclaredTwice WarningKind = "link-declared-twice"
	// WarnLinkTargetUnknown: a link's target operation is in no operation of
	// the document.
	WarnLinkTargetUnknown WarningKind = "link-target-unknown"
	// WarnLinkTargetAmbiguous: several operations carry a link's target
	// operationId.
	WarnLinkTargetAmbiguous WarningKind = "link-target-ambiguous"
	// WarnUnresolvedType: a type is in none of Config.TypeDirs, so it is
	// described as an untyped object, or an embedded one's fields are missing.
	WarnUnresolvedType WarningKind = "unresolved-type"
	// WarnTypeAmbiguous: a bare type name is declared in several TypeDirs and
	// nothing says which is meant; the first is used.
	WarnTypeAmbiguous WarningKind = "type-ambiguous"
	// WarnTypeUnexpressible: a type has no JSON form the generator can
	// express, so it is described as an untyped object.
	WarnTypeUnexpressible WarningKind = "type-unexpressible"
	// WarnTypeUnnameable: a generic field or type argument has a type the
	// generator cannot name.
	WarnTypeUnnameable WarningKind = "type-unnameable"
	// WarnTypeArgumentsMismatch: a generic type is named with the wrong number
	// of type arguments, or without any.
	WarnTypeArgumentsMismatch WarningKind = "type-arguments-mismatch"
	// WarnTypeChosenAtRuntime: an interface with methods stands for a value
	// whose JSON shape is chosen at run time, so it is any value.
	WarnTypeChosenAtRuntime WarningKind = "type-chosen-at-runtime"
	// WarnEmbeddedNotStruct: an embedded type is not a struct, so its fields
	// are missing.
	WarnEmbeddedNotStruct WarningKind = "embedded-not-struct"
	// WarnComponentNameCollision: two types reduce to one component name.
	WarnComponentNameCollision WarningKind = "component-name-collision"
	// WarnConstraintInvalid: a parameter's bound or enum value is not of its
	// type, so the constraint is left off.
	WarnConstraintInvalid WarningKind = "constraint-invalid"
	// WarnStatedTypeInvalid: a field states a JSON type that is not one, so
	// it is described by its Go type.
	WarnStatedTypeInvalid WarningKind = "stated-type-invalid"
	// WarnAnyOfUnknownField: an AnyOf rule constrains a property its type does
	// not declare, so that constraint is left out of the schema.
	WarnAnyOfUnknownField WarningKind = "any-of-unknown-field"
	// WarnExamplesUnreadable: Config.ExamplesFile cannot be read, or is not an
	// example file.
	WarnExamplesUnreadable WarningKind = "examples-unreadable"
	// WarnExampleOrphaned: an example pair names an operation, a parameter, a
	// request body, a status or a media type the document does not have.
	WarnExampleOrphaned WarningKind = "example-orphaned"
	// WarnExampleMissing: a documented status that no example pair shows.
	WarnExampleMissing WarningKind = "example-missing"
	// WarnExampleInvalid: an example body that does not validate against its
	// schema, or a request marked invalid whose body does.
	WarnExampleInvalid WarningKind = "example-invalid"
	// WarnExampleRequirementUnknown: an example pair requires a kind or a value
	// that Config.ExampleRequirements does not list.
	WarnExampleRequirementUnknown WarningKind = "example-requirement-unknown"
)

// Warning is one thing the generator described only in part, or left out. The
// operation it concerns is named when there is one: the operation being
// described when the warning was found. A type is described once, at its first
// use, so a warning about a type shared by several operations names the first
// of them.
type Warning struct {
	Kind        WarningKind `json:"kind"`
	Severity    string      `json:"severity"` // "warning", or "error" within Config.Strict
	OperationID string      `json:"operationId,omitempty"`
	Method      string      `json:"method,omitempty"` // upper case
	Path        string      `json:"path,omitempty"`
	Tags        []string    `json:"tags,omitempty"`

	// What within the operation it concerns, where it is one of these.
	Status    string `json:"status,omitempty"`
	Parameter string `json:"parameter,omitempty"`
	Field     string `json:"field,omitempty"`
	Type      string `json:"type,omitempty"`
	Header    string `json:"header,omitempty"`
	Link      string `json:"link,omitempty"`
	Scheme    string `json:"scheme,omitempty"`
	Example   string `json:"example,omitempty"`

	// Facts are the competing values, each with its source.
	Facts []Fact `json:"facts,omitempty"`

	// File and Line locate it in the source, where the generator read it from
	// source: an annotated handler's doc comment, or a type's file.
	File string `json:"file,omitempty"`
	Line int    `json:"line,omitempty"`

	// Message says it in one sentence; it is what Generator.Warnings holds.
	Message string `json:"message"`
}

// Fact is one source's say about something a warning concerns.
type Fact struct {
	Source string `json:"source"` // "code", "registration" or "comment"
	Value  string `json:"value"`
}

// The severities of a warning in the report.
const (
	SeverityWarning = "warning"
	SeverityError   = "error"
)

// StrictScope names the warnings that are errors (Config.Strict): every one,
// or those about the operations it names, by operationId, tag or path prefix.
// A project can drive one area of its API to no warnings, then make that area
// strict so it cannot slip back. The zero value makes none strict.
type StrictScope struct {
	// All makes every warning an error, those about no operation included.
	All bool
	// Operations are operationIds.
	Operations []string
	// Tags are operation tags.
	Tags []string
	// PathPrefixes are path templates, matched at a segment boundary:
	// /api/v1/domains covers /api/v1/domains and /api/v1/domains/{id}, not
	// /api/v1/domains-x.
	PathPrefixes []string
}

// covers reports whether a warning is within the scope.
func (s StrictScope) covers(w Warning) bool {
	if s.All {
		return true
	}
	for _, id := range s.Operations {
		if w.OperationID != "" && w.OperationID == id {
			return true
		}
	}
	for _, tag := range s.Tags {
		for _, t := range w.Tags {
			if t == tag {
				return true
			}
		}
	}
	for _, prefix := range s.PathPrefixes {
		prefix = strings.TrimSuffix(prefix, "/")
		if w.Path != "" && (prefix == "" || w.Path == prefix || strings.HasPrefix(w.Path, prefix+"/")) {
			return true
		}
	}
	return false
}

// Report generates the document, if it is not yet, and returns its warnings in
// a stable order: by path, method, operationId, kind, what each concerns,
// file, line and message. Each has its severity under Config.Strict. The
// slice is the caller's.
func (g *Generator) Report() []Warning {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.generate()
	report := make([]Warning, len(g.report))
	for i, w := range g.report {
		w.Tags = append([]string(nil), w.Tags...)
		w.Facts = append([]Fact(nil), w.Facts...)
		w.Severity = SeverityWarning
		if g.Config.Strict.covers(w) {
			w.Severity = SeverityError
		}
		report[i] = w
	}
	sort.SliceStable(report, func(i, j int) bool { return warningLess(report[i], report[j]) })
	return report
}

// ReportJSON is Report as a JSON document, {"warnings": [...]}, indented and
// ending in a newline: a run that changes nothing gives the same bytes.
func (g *Generator) ReportJSON() ([]byte, error) {
	b, err := json.MarshalIndent(struct {
		Warnings []Warning `json:"warnings"`
	}{g.Report()}, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// StrictError is what Check returns when warnings fall within Config.Strict.
type StrictError struct {
	// Warnings are those within the scope, in Report's order.
	Warnings []Warning
}

func (e *StrictError) Error() string {
	lines := make([]string, len(e.Warnings))
	for i, w := range e.Warnings {
		lines[i] = fmt.Sprintf("%s: %s", w.Kind, w.Message)
	}
	return fmt.Sprintf("oapifly: %d warning(s) within the strict scope:\n%s", len(e.Warnings), strings.Join(lines, "\n"))
}

// Check generates the document, if it is not yet, and returns a *StrictError
// listing the warnings within Config.Strict, or nil when there are none.
func (g *Generator) Check() error {
	var errs []Warning
	for _, w := range g.Report() {
		if w.Severity == SeverityError {
			errs = append(errs, w)
		}
	}
	if len(errs) == 0 {
		return nil
	}
	return &StrictError{Warnings: errs}
}

// warningLess orders warnings for the report.
func warningLess(a, b Warning) bool {
	ka := []string{a.Path, a.Method, a.OperationID, string(a.Kind), a.Status, a.Parameter, a.Field, a.Type, a.Header, a.Link, a.Scheme, a.Example, a.File}
	kb := []string{b.Path, b.Method, b.OperationID, string(b.Kind), b.Status, b.Parameter, b.Field, b.Type, b.Header, b.Link, b.Scheme, b.Example, b.File}
	for i := range ka {
		if ka[i] != kb[i] {
			return ka[i] < kb[i]
		}
	}
	if a.Line != b.Line {
		return a.Line < b.Line
	}
	return a.Message < b.Message
}

// describing makes the operation op is about the one later warnings concern,
// until the returned function puts the previous one back.
func (r *schemaRegistry) describing(op Warning) func() {
	previous := r.operation
	r.operation = op
	return func() { r.operation = previous }
}

// warn records a warning: w says its kind and what it concerns, and, unless it
// names an operation itself, the operation being described fills in the rest.
func (r *schemaRegistry) warn(w Warning, format string, args ...interface{}) {
	op := r.operation
	if w.OperationID == "" && w.Method == "" && w.Path == "" {
		w.OperationID, w.Method, w.Path, w.Tags = op.OperationID, op.Method, op.Path, op.Tags
	}
	if w.File == "" {
		w.File, w.Line = op.File, op.Line
	}
	w.Message = fmt.Sprintf(format, args...)
	r.warnings = append(r.warnings, w.Message)
	r.report = append(r.report, w)
}
