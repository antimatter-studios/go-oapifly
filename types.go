package oapifly

import "io/fs"

// Config holds the configuration for the OpenAPI generator.
type Config struct {
	// Title is the API title shown in the OpenAPI spec.
	Title string

	// Description is the API description shown in the OpenAPI spec.
	Description string

	// Version is the API version shown in the OpenAPI spec.
	Version string

	// ScanPatterns are glob patterns for Go source files to scan for swaggo-style annotations.
	// Example: []string{"internal/controllers/**/*.go", "internal/types/*.go"}
	ScanPatterns []string

	// TypeDirs are directories to search for Go type definitions referenced by annotations.
	// Defaults to ["types"] if empty.
	TypeDirs []string

	// OpenStructs describes every struct as an open object, one that accepts properties it
	// does not declare, as releases before v0.18.0 did. By default a struct's schema is
	// closed (additionalProperties: false), and a struct is opened one at a time with a
	// field tagged oapi:"open" (#30).
	OpenStructs bool

	// SecuritySchemes are the ways a client can authenticate, written to
	// components.securitySchemes under the names requirements use for them.
	SecuritySchemes map[string]SecurityScheme

	// Security is what an operation requires when it states nothing itself: alternatives,
	// any one of which is enough. An operation overrides it with its own requirements, or
	// with Public. Empty means an operation that states nothing needs no credential.
	Security []SecurityRequirement

	// Strict makes the warnings within it errors: Report marks them, and Check
	// returns them (#31). The zero value makes none strict.
	Strict StrictScope

	// Examples holds the document's example pairs (#20), read from ExamplesFile in it: by
	// convention <document>-example.json beside the document (ExamplesFileFor), embedded
	// in the binary as the document's source is, so nothing is read from disk at run
	// time. With them set, the pairs are merged into the document, and a pair that does
	// not fit, a body that does not validate as it should, and a documented status no pair
	// shows are warnings.
	Examples     fs.FS
	ExamplesFile string

	// ExampleRequirements is the vocabulary a pair's Requires is held to (#62): each kind
	// of requirement with the values it may take, such as "fault": {"database"}. A pair
	// requiring a kind or a value not listed here is reported, so a typo is an error
	// rather than a pair no tester proves.
	ExampleRequirements map[string][]string
}

// Parameter represents an OpenAPI parameter (path, query, or header).
type Parameter struct {
	Name        string                 `json:"name" yaml:"name"`
	In          string                 `json:"in" yaml:"in"`
	Description string                 `json:"description,omitempty" yaml:"description,omitempty"`
	Required    bool                   `json:"required" yaml:"required"`
	Schema      map[string]interface{} `json:"schema,omitempty" yaml:"schema,omitempty"`
	Example     interface{}            `json:"example,omitempty" yaml:"example,omitempty"`
	// Examples are the parameter's named values, one per example pair (#20).
	Examples map[string]interface{} `json:"examples,omitempty" yaml:"examples,omitempty"`
}

// RequestBody represents an OpenAPI request body.
type RequestBody struct {
	Description string                 `json:"description,omitempty" yaml:"description,omitempty"`
	Required    bool                   `json:"required" yaml:"required"`
	Content     map[string]interface{} `json:"content,omitempty" yaml:"content,omitempty"`
}

// Example is a single named entry of an Examples Object. One operation can describe several
// exchanges - a request that is accepted beside one that is rejected - rather than a single
// specimen body, and a consumer pairs a request with its response by the name they share.
type Example struct {
	Summary     string      `json:"summary,omitempty" yaml:"summary,omitempty"`
	Description string      `json:"description,omitempty" yaml:"description,omitempty"`
	Value       interface{} `json:"value" yaml:"value"`
}

// Response represents an OpenAPI response.
type Response struct {
	Description string                 `json:"description" yaml:"description"`
	Content     map[string]interface{} `json:"content,omitempty" yaml:"content,omitempty"`
	// Links are omitted when empty on purpose: a response with no links and a response with an
	// empty links object are different documents, and only the first is true when none were
	// declared.
	Links map[string]Link `json:"links,omitempty" yaml:"links,omitempty"`
	// Headers are the headers the response carries, by name.
	Headers map[string]Header `json:"headers,omitempty" yaml:"headers,omitempty"`
}

// Link represents an OpenAPI Link Object: a named step from this response to another operation,
// with the target's parameters filled from runtime expressions over this exchange.
type Link struct {
	OperationID string            `json:"operationId" yaml:"operationId"`
	Parameters  map[string]string `json:"parameters,omitempty" yaml:"parameters,omitempty"`
}

// PathItem represents an OpenAPI path entry.
type PathItem struct {
	Summary     string              `json:"summary,omitempty" yaml:"summary,omitempty"`
	Description string              `json:"description,omitempty" yaml:"description,omitempty"`
	OperationID string              `json:"operationId,omitempty" yaml:"operationId,omitempty"`
	Tags        []string            `json:"tags,omitempty" yaml:"tags,omitempty"`
	Deprecated  bool                `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`
	Parameters  []Parameter         `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	RequestBody *RequestBody        `json:"requestBody,omitempty" yaml:"requestBody,omitempty"`
	Responses   map[string]Response `json:"responses,omitempty" yaml:"responses,omitempty"`
	// Security is the operation's own requirements, nil to inherit the document's. An empty
	// list is written, and means the operation needs no credential.
	Security *[]SecurityRequirement `json:"security,omitempty" yaml:"security,omitempty"`
}
