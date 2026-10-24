package oapifly

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// twoAttachments writes two packages that each declare an Attachment, as REST Mail's pipeline
// and models packages do, and a type in each that holds a list of its own (#26).
func twoAttachments(t *testing.T) (pipelineDir, modelsDir string) {
	t.Helper()
	root := t.TempDir()
	pipelineDir, modelsDir = filepath.Join(root, "pipeline"), filepath.Join(root, "models")
	for _, dir := range []string{pipelineDir, modelsDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeGoFile(t, pipelineDir, "types.go", "package pipeline\n\n"+
		"type Attachment struct {\n\tFilename string `json:\"filename\"`\n\tSize int64 `json:\"size\"`\n}\n\n"+
		"type Email struct {\n\tAttachments []Attachment `json:\"attachments\"`\n}\n")
	writeGoFile(t, modelsDir, "models.go", "package models\n\n"+
		"type Attachment struct {\n\tID uint `json:\"id\"`\n\tStorageRef string `json:\"storage_ref\"`\n}\n\n"+
		"type Message struct {\n\tAttachments []Attachment `json:\"attachments\"`\n}\n\n"+
		"type Domain struct {\n\tName string `json:\"name\"`\n}\n\n"+
		"type Mailbox struct {\n\tDomain Domain `json:\"domain\"`\n}\n")
	return pipelineDir, modelsDir
}

func itemsRef(t *testing.T, schema map[string]interface{}) string {
	t.Helper()
	items, _ := propOf(t, schema, "attachments")["items"].(map[string]interface{})
	ref, _ := items["$ref"].(string)
	return strings.TrimPrefix(ref, "#/components/schemas/")
}

func propertyNames(schema map[string]interface{}) []string {
	props, _ := schema["properties"].(map[string]interface{})
	var names []string
	for name := range props {
		names = append(names, name)
	}
	return names
}

// Two declarations of one name are two types: each struct's bare Attachment is its own
// package's, each is described with its own fields under a key of its own, and the shared
// name is reported (#26).
func TestSameNamedTypesInTwoPackagesStayTwo(t *testing.T) {
	pipelineDir, modelsDir := twoAttachments(t)
	reg := newSchemaRegistry([]string{pipelineDir, modelsDir})

	email := reg.schemas[reg.resolve("pipeline.Email")]
	message := reg.schemas[reg.resolve("models.Message")]
	emailItem, messageItem := itemsRef(t, email), itemsRef(t, message)
	if emailItem == messageItem {
		t.Fatalf("both lists refer to %s", emailItem)
	}
	if got := strings.Join(propertyNames(reg.schemas[emailItem]), ","); !strings.Contains(got, "filename") || strings.Contains(got, "storage_ref") {
		t.Errorf("pipeline.Email's attachment %s has %s, want pipeline's fields", emailItem, got)
	}
	if got := strings.Join(propertyNames(reg.schemas[messageItem]), ","); !strings.Contains(got, "storage_ref") || strings.Contains(got, "filename") {
		t.Errorf("models.Message's attachment %s has %s, want models' fields", messageItem, got)
	}
	if emailItem != "Attachment" || messageItem != "models.Attachment" {
		t.Errorf("keys %s and %s, want the first to keep the name and the second qualified by its package", emailItem, messageItem)
	}
	if !strings.Contains(strings.Join(reg.warnings, "\n"), "share the name Attachment") {
		t.Errorf("the shared name is not reported: %v", reg.warnings)
	}
}

// A type an annotation names with its package, and a field reaches by its bare name, is one
// component under one key (#26).
func TestATypeReachedTwoWaysIsOneComponent(t *testing.T) {
	_, modelsDir := twoAttachments(t)
	reg := newSchemaRegistry([]string{modelsDir})

	named := reg.resolve("models.Domain")
	mailbox := reg.schemas[reg.resolve("models.Mailbox")]
	if got := strings.TrimPrefix(refOf(t, mailbox, "domain"), "#/components/schemas/"); got != named || named != "Domain" {
		t.Fatalf("the annotation keys %s and the field refers to %s, want both Domain", named, got)
	}
	for key := range reg.schemas {
		if strings.HasSuffix(key, ".Domain") {
			t.Errorf("Domain is also described as %s", key)
		}
	}
	if len(reg.warnings) > 0 {
		t.Errorf("warnings: %v", reg.warnings)
	}
}

// An annotation's package qualifier says which of two declarations is meant; a bare name
// that could be either is reported, and the first is described (#26).
func TestAnAmbiguousBareNameIsReported(t *testing.T) {
	pipelineDir, modelsDir := twoAttachments(t)

	qualified := newSchemaRegistry([]string{pipelineDir, modelsDir})
	key := qualified.resolve("models.Attachment")
	if got := strings.Join(propertyNames(qualified.schemas[key]), ","); !strings.Contains(got, "storage_ref") {
		t.Errorf("models.Attachment is described with %s, want models' fields", got)
	}
	if len(qualified.warnings) > 0 {
		t.Errorf("a qualified name is not ambiguous: %v", qualified.warnings)
	}

	bare := newSchemaRegistry([]string{pipelineDir, modelsDir})
	key = bare.resolve("Attachment")
	if got := strings.Join(propertyNames(bare.schemas[key]), ","); !strings.Contains(got, "filename") {
		t.Errorf("the bare Attachment is described with %s, want the first TypeDirs' fields", got)
	}
	if !strings.Contains(strings.Join(bare.warnings, "\n"), "nothing says which is meant") {
		t.Errorf("an ambiguous bare name is not reported: %v", bare.warnings)
	}
}
