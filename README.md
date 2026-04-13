# go-oapifly

Generate OpenAPI 3.0 specs on the fly from annotated Go source code.

Unlike traditional OpenAPI tools that generate code *from* a spec, oapifly works in reverse: it reads your Go source files at runtime, parses [swaggo-style](https://github.com/swaggo/swag) annotations, and produces a live OpenAPI specification. Your docs stay in sync with your code automatically, no build step required.

## Features

- **Runtime spec generation** - No code generation step, no static files to keep in sync
- **Swaggo-compatible annotations** - Uses the same `@Summary`, `@Router`, `@Param`, `@Success`, `@Failure`, `@Tags` annotations
- **JSON and YAML output** - Serialize the spec in either format
- **Schema extraction** - Automatically generates JSON Schema from Go struct types via reflection
- **Framework agnostic** - Pure Go, no HTTP framework dependency. Wire it into any router yourself
- **Zero config defaults** - Just point it at your source files

## Installation

```bash
go get github.com/antimatter-studios/go-oapifly
```

## Quick start

```go
package main

import (
    "fmt"
    "github.com/antimatter-studios/go-oapifly"
)

func main() {
    gen := oapifly.New(oapifly.Config{
        Title:   "My API",
        Version: "1.0.0",
        ScanPatterns: []string{
            "internal/controllers/**/*.go",
            "internal/types/*.go",
        },
    })

    // Get the full spec as a map
    spec := gen.Generate()

    // Or serialize directly
    jsonBytes, _ := gen.JSON()
    yamlBytes, _ := gen.YAML()

    fmt.Println(string(jsonBytes))
    _ = yamlBytes
    _ = spec
}
```

## Supported annotations

Annotate your handler methods with standard swaggo-style comments:

```go
// GetUser returns a user by ID
// @Summary Get user by ID
// @Description Returns a single user
// @Tags users
// @Produce json
// @Param id path string true "User ID" example(abc-123)
// @Success 200 {object} User "User found"
// @Failure 404 {object} ErrorResponse "User not found"
// @Router /api/v1/users/{id} [get]
func (c *UserController) GetUser(ctx *fiber.Ctx) error {
    // ...
}
```

oapifly will parse these annotations and produce the corresponding OpenAPI path entries, parameters, and response schemas.

## How it works

1. **Glob** your source files using the configured `ScanPatterns`
2. **Parse** each file into a Go AST
3. **Extract** swaggo `@Tag` annotations from handler method doc comments
4. **Build** OpenAPI path items with parameters, responses, and schema references
5. **Resolve** Go struct types to JSON Schema via reflection (for `@Success`/`@Failure` type references)
6. **Return** a complete OpenAPI 3.0 spec as `map[string]interface{}`

Since it reads source files at runtime, your application must be deployed alongside its source code (or at least the annotated files). This is the tradeoff for zero-build-step documentation.

## License

MIT
