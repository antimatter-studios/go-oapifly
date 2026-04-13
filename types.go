package oapifly

// Config holds the configuration for the OpenAPI generator.
type Config struct {
	// Title is the API title shown in the OpenAPI spec.
	Title string

	// Version is the API version shown in the OpenAPI spec.
	Version string

	// ScanPatterns are glob patterns for Go source files to scan for swaggo-style annotations.
	// Example: []string{"internal/controllers/**/*.go", "internal/types/*.go"}
	ScanPatterns []string
}

// Parameter represents an OpenAPI parameter.
type Parameter struct {
	Name        string            `json:"name" yaml:"name"`
	In          string            `json:"in" yaml:"in"`
	Description string            `json:"description,omitempty" yaml:"description,omitempty"`
	Required    bool              `json:"required" yaml:"required"`
	Schema      map[string]string `json:"schema,omitempty" yaml:"schema,omitempty"`
	Example     interface{}       `json:"example,omitempty" yaml:"example,omitempty"`
}

// Response represents an OpenAPI response.
type Response struct {
	Description string                 `json:"description" yaml:"description"`
	Content     map[string]interface{} `json:"content,omitempty" yaml:"content,omitempty"`
}

// PathItem represents an OpenAPI path entry.
type PathItem struct {
	Summary     string              `json:"summary,omitempty" yaml:"summary,omitempty"`
	Description string              `json:"description,omitempty" yaml:"description,omitempty"`
	Tags        []string            `json:"tags,omitempty" yaml:"tags,omitempty"`
	Parameters  []Parameter         `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	Responses   map[string]Response `json:"responses,omitempty" yaml:"responses,omitempty"`
}

type fieldTypeInfo struct {
	Schema   map[string]interface{}
	Ref      string
	Optional bool
}
