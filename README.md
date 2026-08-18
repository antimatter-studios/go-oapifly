# go-oapifly

Generate OpenAPI 3.0 specs on the fly from annotated Go source code.

Unlike traditional OpenAPI tools that generate code *from* a spec, oapifly works in reverse: it reads your Go source files at runtime, parses [swaggo-style](https://github.com/swaggo/swag) annotations, and produces a live OpenAPI specification. Your docs stay in sync with your code automatically, no build step required.

## Features

- **Runtime spec generation** - No code generation step, no static files to keep in sync
- **Swaggo-compatible annotations** - Uses the same `@Summary`, `@Router`, `@Param`, `@Success`, `@Failure`, `@Tags` annotations
- **JSON and YAML output** - Serialize the spec in either format
- **Schema extraction** - Automatically generates JSON Schema from Go struct types
- **Generic types** - A generic envelope named in an annotation is described with its type parameters substituted
- **Links** - Declare that a response leads to another operation, so a consumer can follow a create to its read
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
// @Param page query int false "Page number" minimum(1)
// @Param limit query int false "Page size" minimum(1) maximum(100)
// @Success 200 {object} User "User found"
// @Failure 404 {object} ErrorResponse "User not found"
// @Router /api/v1/users/{id} [get]
func (c *UserController) GetUser(ctx *fiber.Ctx) error {
    // ...
}
```

oapifly will parse these annotations and produce the corresponding OpenAPI path entries, parameters, and response schemas.

### Links

A link says that one operation's response leads to another, and where the target's parameters
come from. It is what lets a consumer follow a create to the read of the thing it created —
and the only way a description can state a lifecycle rather than a set of unrelated calls.

```go
// @ID      addDevice
// @Success 201 {object} restclient.MessageResponse
// @Link    201 Read   readDevice   deviceGuid=$response.body#/guid
// @Link    201 Remove removeDevice deviceGuid=$response.body#/guid
```

produces, on that response:

```yaml
responses:
  "201":
    links:
      Read:
        operationId: readDevice
        parameters: {deviceGuid: "$response.body#/guid"}
```

The form is `@Link <status> <name> <targetOperationId> [<param>=<expression> ...]`. The status
must be a response the operation declares — `default` and the `2XX` ranges included, since those
are response keys too; the name is free-form and appears verbatim in a
consumer's reports, so it should name the step; the target is an `@ID` declared somewhere in the
same document. Expressions — `$response.body#/guid`, `$request.path.id`,
`$response.header.Location`, `$statusCode` — are passed through untouched, because they are
evaluated by whoever follows the link.

Nothing is inferred. Pairing `POST /device` with `GET /device/guid/{deviceGuid}` because they
look related would mint chains the author never claimed, so a link exists only where an `@Link`
says so. A link on a status the operation does not declare, one this generator cannot read, and
one whose target no operation declares, one whose target is an `@ID` two operations share (which
identifies neither), and a second link of the same name on one response (which would silently
replace the first) are each reported rather than guessed at. A response with no links carries no
`links` key at all.

### Parameter constraints

A Go type says what shape a value has, not which values are allowed: a page number is an
integer and so is `-1`. Where a handler enforces a bound, `minimum`, `maximum` and `enums`
state it in the description, so a consumer generating a request sends something the handler
accepts and a boundary-probing tester stops reporting the two as disagreeing.

```go
// @Param id   path  int    true  "Key ID"   minimum(1)
// @Param type query string false "Key type" enums(authorized,device)
```

Each is typed as the parameter is, so a bound beside an integer becomes a number. A bound that
is not a value of that type is left off rather than written as text, since describing a
parameter as accepting only the string `"one"` would reject every number it really takes.

### Generic types

One envelope usually serves every payload:

```go
type Page[T any] struct {
    Data []T  `json:"data"`
    Meta Meta `json:"meta"`
}
```

Name the instantiation the way Go spells it, and the response is described with the type
parameter substituted - `data` becomes an array of `Item`, not an untyped object:

```go
// @Success 200 {object} types.Page[types.Item]
```

The component is keyed `Page-Item`, because the Components Object requires keys to match
`^[a-zA-Z0-9.\-_]+$` and the Go spelling's brackets are not in it. Arguments may be lists
(`Page[[]Item]` → `Page-ItemList`), pointers (`Page[*Item]` → `Page-ItemOrNull`, nullable),
other instantiations (`Page[Wrapper[Item]]` → `Page-Wrapper-Item`), or primitives
(`Page[string]`). Instantiations written as Go field types are described the same way, an
alias to one (`type ItemPage = Page[Item]`) resolves through to it, and a generic that reaches
itself - a tree - terminates.

Where an argument is a shape this generator has no spelling for, such as a map or a func, the
field is left an unconstrained object and a warning says which type could not be named.

## How it works

1. **Glob** your source files using the configured `ScanPatterns`
2. **Parse** each file into a Go AST
3. **Extract** swaggo `@Tag` annotations from handler method doc comments
4. **Build** OpenAPI path items with parameters, responses, and schema references
5. **Resolve** Go types to JSON Schema from their declarations, binding type parameters where a generic is instantiated
6. **Return** a complete OpenAPI 3.0 spec as `map[string]interface{}`

Since it reads source files at runtime, your application must be deployed alongside its source code (or at least the annotated files). This is the tradeoff for zero-build-step documentation.

## License

MIT
