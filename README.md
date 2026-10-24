# go-oapifly

Generate OpenAPI 3.1 specs on the fly from annotated Go source code.

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

## Registering operations in code

An operation can also be registered with the Go types it binds and answers, instead of described by annotations. Its structure is read from those types by reflection, so the document needs no source on disk at runtime.

```go
type DomainPath struct {
	ID int64 `path:"id" description:"The domain's id"`
}

type UpdateDomain struct {
	ID   int64          `path:"id"`
	Body *DomainChanges `body:""`
}

generator.Register(
	oapifly.Endpoint{
		Method: "GET", Path: "/api/v1/domains/{id}",
		OperationID: "getDomain", Summary: "Get a domain", Tags: []string{"domains"},
		Request: oapifly.TypeOf[DomainPath](),
		Responses: []oapifly.EndpointResponse{
			{Status: 200, Type: oapifly.TypeOf[Domain]()},
			{Status: 404, Type: oapifly.TypeOf[ErrorResponse]()},
		},
	},
	oapifly.Endpoint{
		Method: "PATCH", Path: "/api/v1/domains/{id}",
		Request:   oapifly.TypeOf[UpdateDomain](),
		Responses: []oapifly.EndpointResponse{{Status: 200, Type: oapifly.TypeOf[Domain]()}},
	},
)
```

- **Parameters:** fields tagged `path:"name"`, `query:"name"` and `header:"name"` are parameters. Query and header parameters are optional unless the tag says `,required`. A `description` tag describes one, and `enums`, `format` and `example` constrain it, as they do a body field.
- **Body:** the field tagged `body:""` is the request body; `body:"<media type>"` names one that is not JSON, a `string` or `[]byte` field. A body field that is a pointer is optional (`required: false`): a JSON one is left nil when the request has no body, and so is one in another media type, `*string` or `*[]byte`. A request type with no parameter fields is the body itself.
- **Responses:** each `EndpointResponse` has its own `MediaType` (JSON by default) and `Headers`. A response with a media type and no `Type`, or a `[]byte` under a media type that is not JSON, is a body of that type whose bytes are not described (a `message/rfc822` message, a download). One status may be registered once per media type, for an answer that comes in more than one.
- **Schemas:** follow encoding/json, and are the same ones the annotation path produces for the same types:
  - `omitempty` is not required;
  - a pointer is nullable;
  - an embedded struct's fields are promoted;
  - a named struct is a component;
  - `Page[Item]` is the component `Page-Item`.
- **What it cannot place** is a warning, not a guess: a template variable with no field, a path field the template does not name, and fields that are neither parameter nor body.
- **A route that is both annotated and registered** takes its structure from the registration. Where the registration leaves them out, the summary, description, operationId and tags come from the comments.

### Security

Configure how clients authenticate once, and state on each operation which of those ways it accepts and the roles it needs. OpenAPI 3.1 allows roles on any scheme, so a capability such as `domains:write` is part of the requirement:

```go
g := oapifly.New(oapifly.Config{
    SecuritySchemes: map[string]oapifly.SecurityScheme{
        "SessionCookie": {Type: "apiKey", In: "cookie", Name: "session"},
        "CSRFHeader":    {Type: "apiKey", In: "header", Name: "X-CSRF-Token"},
        "BearerAuth":    {Type: "http", Scheme: "bearer"},
        "GatewayCert":   {Type: "mutualTLS"},
    },
    // What an operation needs when it states nothing.
    Security: []oapifly.SecurityRequirement{{"BearerAuth": nil}},
})

g.Register(oapifly.Endpoint{
    Method: "POST", Path: "/domains",
    // Alternatives: a session with its CSRF header, or a bearer token. Either needs domains:write.
    Security: []oapifly.SecurityRequirement{
        {"SessionCookie": {"domains:write"}, "CSRFHeader": nil},
        {"BearerAuth": {"domains:write"}},
    },
})
g.Register(oapifly.Endpoint{Method: "GET", Path: "/health", Security: oapifly.Public})
```

- `Security: nil` inherits `Config.Security`, written as the document's top-level `security`.
- `oapifly.Public` (an empty list) needs no credential and is written as `security: []`.
- A scheme without roles is written with an empty list.
- In annotations, swaggo's `@Security` does the same. Each line, and each `||` within a line, is an alternative; `&&` joins schemes that are all needed; roles go in brackets: `@Security SessionCookie && CSRFHeader || BearerAuth[domains:read]`.
- A requirement naming a scheme that `Config.SecuritySchemes` does not define is reported in `Warnings`.

### Middleware and response headers

Much of what an operation answers is written by its middleware: a 401 from authentication, a 403 from a capability check, a 429 from a rate limit. A middleware describes that once, and every operation it wraps carries it:

```go
var rateLimited = oapifly.Middleware{Responses: []oapifly.EndpointResponse{{
    Status: 429, Type: oapifly.TypeOf[ErrorResponse](),
    Headers: map[string]oapifly.ResponseHeader{"Retry-After": {Type: oapifly.TypeOf[int](), Required: true}},
}}}

func require(capability string) oapifly.Middleware {
    return oapifly.Middleware{
        Roles:     []string{capability},
        Responses: []oapifly.EndpointResponse{{Status: 403, Type: oapifly.TypeOf[ErrorResponse]()}},
    }
}

g.Register(oapifly.Endpoint{Method: "POST", Path: "/domains",
    Middleware: []oapifly.Middleware{rateLimited, authenticated, require("domains:write")}})
```

- `Responses` are added to the operation's own. Where the operation states the same status, its own description is kept, and it gains any header it does not state.
- `Security` is the requirement an authenticating middleware enforces, used when the operation states none (the innermost wins).
- `Roles` are added to every scheme of every alternative of the operation's requirement: its own, else its middleware's, else `Config.Security`. A role on an operation that needs no credential is reported.
- `EndpointResponse.Headers` describes a response's headers. In annotations, swaggo's `@Header 429 {integer} Retry-After "Seconds to wait"` does the same; the status may be a comma-separated list or `all`.

### Lists are never null

The document describes a slice or map field without `omitempty` as a required array or object, never `null`, so a client gets `T[]` and needs no null check. Go's `encoding/json` writes a nil slice or map as `null`, so call `oapifly.EmptyLists(response)` just before encoding a response. It returns a copy with every nil list it would write replaced by `[]` (and every nil map by `{}`). Fields that are `omitempty`, tagged `json:"-"`, unexported, or a `[]byte` are left alone, as encoding/json leaves them. A field where null means something different from empty belongs in its own, explicitly nullable field.

### Binding requests

The rules a request type describes are enforced by `oapifly.Bind`, from the same declaration, so the document and the server cannot disagree about them:

```go
type CreateDomain struct {
    Name  string `json:"name" minLength:"1" maxLength:"253" format:"hostname"`
    Kind  string `json:"kind" enums:"primary,alias"`
    Quota *int   `json:"quota,omitempty" minimum:"0"`
}

var req CreateDomain
err := oapifly.Bind(&req, func(in, name string) (string, bool) {
    switch in {
    case "path":
        v := chi.URLParam(r, name)
        return v, v != ""
    case "query":
        _, ok := r.URL.Query()[name]
        return r.URL.Query().Get(name), ok
    }
    v := r.Header.Get(name)
    return v, v != ""
}, body)
```

- `err` is `nil`, `oapifly.Violations` (answer 422 with the fields), an error wrapping `oapifly.ErrMalformedBody` (answer 400) for a body that is not JSON, is not the shape its type reads (an array for an object, or `null` where it cannot be), or is missing, or, for a request type that cannot be bound, a plain error.
- Violations are keyed by parameter name (`limit`), or by the body field's JSON path (`owner.email`, `members[2].name`); the body as a whole is `body`. Every broken rule is reported, not just the first.
- A parameter is required when it is in the path or its tag says `,required`. A body field without `omitempty` is required, and only a pointer field may be `null`.
- Structs are closed: an undeclared property is refused unless the struct is open (see below). `oapifly.Binder{OpenStructs: true}.Bind` goes with `Config.OpenStructs`.
- Values must have their described type, and the constraint tags must hold: `minimum` and `maximum` on numbers; `minLength`, `maxLength` (in characters), `pattern`, `enums` and `format` on strings; `enums` on numbers; `minItems` and `maxItems` on lists. A tag that is not a number of the right kind is left out of the document and is not enforced either.
- `format` is checked for `email`, `uuid`, `date-time`, `date`, `uri`, `hostname`, `ipv4` and `ipv6`. Other formats are described and not checked, as JSON Schema treats a format it does not know as an annotation.
- Types that read themselves (`time.Time`, `uuid.UUID`) are checked by reading them, and `json.RawMessage` accepts anything.
- A field's `message:"..."` tag replaces the wording for any of its constraints that does not hold, so an API keeps the messages its clients already show as its rules move into the type. A missing field is still `required`, and a value of the wrong type still says which type it must be.
- Rules that depend on state, such as a name already being taken, stay in the handler and are documented as its error responses.

## Struct schemas are closed

A struct is described as a closed object, `additionalProperties: false`, whichever path describes it: a property the struct does not declare is refused. A response that starts leaking a field (a storage path, a hash, a token) then no longer matches its own description, and a contract tester can expect a request with an unknown field to be refused. A map is still an open object whose values follow the map's value type.

- An embedded struct's fields are the object's own, as Go marshals them, so they are allowed.
- A nullable reference is `oneOf: [{$ref}, {type: null}]`: the closed component or null, so it still refuses what the component does not declare.
- Open one struct with a field tagged `oapi:"open"`, usually a blank one. A struct that embeds an open struct is open too, and so is one embedding a type the scanner cannot read, since its schema could not name those fields:

  ```go
  type Settings struct {
      _     struct{} `oapi:"open"`
      Theme string   `json:"theme"`
  }
  ```

- `Config.OpenStructs: true` describes every struct as open, as releases before v0.18.0 did, for an API whose server accepts and ignores unknown fields.

## How it works

1. **Glob** your source files using the configured `ScanPatterns`
2. **Parse** each file into a Go AST
3. **Extract** swaggo `@Tag` annotations from handler method doc comments
4. **Build** OpenAPI path items with parameters, responses, and schema references
5. **Resolve** Go types to JSON Schema from their declarations, binding type parameters where a generic is instantiated. A declaration is known by its package and name: a bare name in a struct is its own package's, a qualified one (`models.Domain`, in an annotation or a field) is found by its package's name, and a type reached both ways is one component. Two declarations sharing a name are two components, the second keyed with its package (`models.Attachment`); a name that could be either and says neither is reported
6. **Return** a complete OpenAPI 3.1 spec as `map[string]interface{}`

The annotation path reads source files at runtime, so an application using it must be deployed alongside its source code, or at least the annotated files. Registered operations need no source at all.

## Releases

Every merge to `main` is released: `.github/workflows/release-on-merge.yaml` runs the tests, tags the merge with the next version and publishes the release. The version follows the merged commit's conventional-commit type:
- `feat:` bumps the minor version;
- a breaking change (`feat!:`, or a `BREAKING CHANGE:` footer) bumps the minor version while on 0.x, and the major version from 1.0;
- anything else bumps the patch version.

`chore release:next` prints the version the current commit would get. `chore test`, `chore race` and `chore lint` run what CI runs.

## License

MIT
