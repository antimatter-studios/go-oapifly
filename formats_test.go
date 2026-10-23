package oapifly

import (
	"reflect"
	"testing"
)

type formatsShape struct {
	Small  int32   `json:"small"`
	Big    int64   `json:"big"`
	Plain  int     `json:"plain"`
	Count  uint32  `json:"count"`
	Ratio  float32 `json:"ratio"`
	Amount float64 `json:"amount"`
	Kind   *string `json:"kind" enums:"a,b"`
	Level  *int64  `json:"level" enums:"1,2"`
}

const formatsSource = `package types

type Shape struct {
	Small  int32   ` + "`json:\"small\"`" + `
	Big    int64   ` + "`json:\"big\"`" + `
	Plain  int     ` + "`json:\"plain\"`" + `
	Count  uint32  ` + "`json:\"count\"`" + `
	Ratio  float32 ` + "`json:\"ratio\"`" + `
	Amount float64 ` + "`json:\"amount\"`" + `
	Kind   *string ` + "`json:\"kind\" enums:\"a,b\"`" + `
	Level  *int64  ` + "`json:\"level\" enums:\"1,2\"`" + `
}
`

// A sized number carries its OpenAPI format, so a client knows the range it holds, and a
// nullable field's enum lists null, since an enum lists every value allowed. Both paths agree.
func TestNumberFormatsAndNullableEnums(t *testing.T) {
	want := map[string]string{
		"small":  `{"format":"int32","type":"integer"}`,
		"big":    `{"format":"int64","type":"integer"}`,
		"plain":  `{"type":"integer"}`,
		"count":  `{"type":"integer"}`,
		"ratio":  `{"format":"float","type":"number"}`,
		"amount": `{"format":"double","type":"number"}`,
		"kind":   `{"enum":["a","b",null],"type":["string","null"]}`,
		"level":  `{"enum":[1,2,null],"format":"int64","type":["integer","null"]}`,
	}

	reflected := newSchemaRegistry(nil)
	reflected.reflectSchema(reflect.TypeOf(formatsShape{}))
	dir := t.TempDir()
	writeGoFile(t, dir, "shape.go", formatsSource)
	annotated := newSchemaRegistry([]string{dir})
	name := annotated.resolve("types.Shape")

	for path, schema := range map[string]map[string]interface{}{
		"reflection":  reflected.schemas["formatsShape"],
		"annotations": annotated.schemas[name],
	} {
		props, _ := schema["properties"].(map[string]interface{})
		for field, expected := range want {
			if got := asJSON(t, props[field]); got != expected {
				t.Errorf("%s: %s is %s, want %s", path, field, got, expected)
			}
		}
	}
}
