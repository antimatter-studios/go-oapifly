# Human Code Report — 2026-08-18

**Scope:** `parser.go`, `oapifly.go`, `types.go` (the whole library), on branch `cth/array-params`.
**Found:** 11 items. **Fixed:** 10. **Skipped:** 1 (already done by an earlier commit on the branch).

The pass ran after the array-parameter feature landed (`c53f858`) and used the array work as the
lens: everywhere the feature had to touch turned out to be a place with a second copy of the
same logic.

## Changes Made

### 1. The reflection-based schema path had no callers — removed

- **Files:** [parser.go](../parser.go), [types.go](../types.go), [parser_test.go](../parser_test.go)
- **What changed:** deleted `goKindToJSONType`, `goKindToOpenAPIFormat`, `resolveJSONFieldName`,
  `resolveFieldTypeReflect`, `generateSchemaForTypeReflect`, the `fieldTypeInfo` struct, and the
  `findReflectTypeByName` stub (a `TODO` returning `nil`), plus the `reflect` import from both
  files and the 14 tests that existed only to exercise them.

  ```go
  // before: two generators for the same input, one of them unreachable
  func generateSchemaForTypeReflect(rt reflect.Type) map[string]interface{} { ... }   // no callers
  func findReflectTypeByName(name string) reflect.Type { return nil }                // TODO stub
  ```
- **Why it's better:** a reader tracing how a schema is produced no longer finds two answers and
  has to work out which one is live. The AST path's own comment said it "replaces the broken
  reflection-based approach" — the replacement had happened, the replaced code just never left.
  ~125 lines of code and ~100 lines of tests that could only ever pass.

### 2. `generateSchemaForTypeAST` was a second copy of `schemaForNamedTypeAST`

- **Files:** [parser.go](../parser.go)
- **What changed:**
  ```go
  // before: 25 lines walking f.Decls → GenDecl → TypeSpec → StructType, identical to
  //         schemaForNamedTypeAST except that it returned nil for non-structs
  // after:
  func generateSchemaForTypeAST(typeName, filePath string, reg *schemaRegistry) map[string]interface{} {
      schema, isStruct := schemaForNamedTypeAST(typeName, filePath, reg)
      if !isStruct {
          return nil
      }
      return schema
  }
  ```
- **Why it's better:** the AST walk exists once. A fix to how a type declaration is found — the
  alias handling in `findTypeFile`, say — now reaches every caller. Its 15 test callers still see
  the same struct-only contract.

### 3. `buildRequestBody` built scalar schemas by hand, twice

- **Files:** [parser.go](../parser.go)
- **What changed:**
  ```go
  // before (body branch, and again for every formData param):
  schema = map[string]interface{}{"type": dataTypeToOpenAPIType(p.DataType)}
  if f := dataTypeToFormat(p.DataType); f != "" {
      schema["format"] = f
  }
  // after:
  schema = dataTypeSchema(p.DataType)
  ```
- **Why it's better:** it was `dataTypeSchema` inlined, and the inlining had a real cost: when
  `dataTypeSchema` learned about `[]int`, the request-body path did not — a `[]string` formData
  parameter (several uploads, several tags) still came out as a string. Now one function decides
  what a data type's schema is, and formData arrays got the feature for free.
  Test: `TestBuildRequestBody_FormDataArray`.

### 4. Two functions read "text as a typed value", and disagreed

- **Files:** [parser.go](../parser.go)
- **What changed:** `typedTagValue` (struct tags: `enums:"1,2"`, `example:"3"`) and the new
  `parameterExample` (`@Param ... example(3)`) each parsed text against a type. They returned
  different Go types for the same integer text (`float64` vs `int64`). Both now delegate to one
  `typedValue(raw, openapiType)`, and integers read as `float64` — the one numeric type JSON has,
  and what a decoded document already holds.
  ```go
  func typedTagValue(raw string, schema map[string]interface{}) interface{} {
      openapiType, _ := schema["type"].(string)
      return typedValue(raw, openapiType)
  }
  ```
- **Why it's better:** an example is the same kind of value whichever annotation it came from,
  and there is one place to look when the rule changes. Test:
  `TestTypedValue_SharedBetweenTagsAndParams`.

### 5. `isStructRef` did not know a list of primitives is not a struct

- **Files:** [parser.go](../parser.go)
- **What changed:** `isStructRef("[]int")` returned true, so a `[]int` body parameter would have
  been sent through `reg.resolve` looking for a type named `[]int`. It now recurses into the
  element type. Test: `TestIsStructRef_ListShapes`.
- **Why it's better:** the array feature is consistent across body, formData, query and header
  parameters, rather than working in two of the four.

### 6–9. Coverage of the malformed-input branches

- **Files:** [edge_cases_test.go](../edge_cases_test.go)
- **What changed:** table tests for `extractTagValue` (missing quotes), `resolveContentType`
  (unknown words, MIME passthrough), `parseExample` (too few fields, unknown side),
  `splitExampleSummary` (lone trailing quote, whole-value-quoted), and `embeddedTypeName` on the
  shapes Go syntax cannot write as an embedded field, built as AST nodes by hand.
- **Why it's better:** these are the paths a well-formed annotation never reaches, so nothing
  else exercised them; each is a pure function and each test is a table. `embeddedTypeName`,
  `extractTagValue`, `parseExample`, `splitExampleSummary` are at 100%.

### 10. Stale section header and doc comment

- **Files:** [parser.go](../parser.go)
- **What changed:** the "Reflection-based type mapping (for runtime schema generation)" section
  and the `generateSchemaForTypeAST` comment referencing `findReflectTypeByName` went with item 1.
- **Why it's better:** comments that describe code that no longer exists are the kind a reader
  trusts and is misled by.

### 11. `gofmt`

- **Files:** [types.go](../types.go)
- **What changed:** struct field alignment after `Parameter.Schema` widened.

## Items Skipped

| # | Item | Reason |
|---|---|---|
| — | `Generate` at 96% | *Acceptable pattern* — one linear pipeline with good comments; the uncovered lines are the "no files matched" and "parse failed" warnings, which need a filesystem fixture and are already exercised in spirit by the resolver tests. Not worth a fragile test. |
| — | `findTypeFile` four-level nesting | *Acceptable pattern* — it is an AST walk; flattening would hide the shape it is walking. |
| — | `"#/components/schemas/"` appears 6× | *Below threshold* for a named constant — it is the OpenAPI-defined literal, and `refPrefix` would add a lookup for no meaning gained. |

## Test Results

| | Before | After |
|---|---|---|
| Tests passing | 142 | 140 |
| Tests failing | 0 | 0 |
| Tests removed (dead reflect path, authorised) | — | 14 |
| Tests added | — | 12 |
| Coverage | 92.4% | 95.0% |
| `go vet` | clean | clean |
| `gofmt -l` | clean | clean |

Two pre-existing assertions were changed, both intentionally and both for the same reason: they
pinned an integer parameter's example as the *string* it was written as (`"123"`, `"20"`), which is
the untyped-example defect the array commit fixed. They now assert `float64(123)` / `float64(20)`.

Baseline contract held throughout: every test that passed before the pass still passes after it,
except the 14 that only covered code which no longer exists.
