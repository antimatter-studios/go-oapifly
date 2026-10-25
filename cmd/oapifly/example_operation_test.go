package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExamplesCommandWithOperationOverride(t *testing.T) {
	dir := t.TempDir()
	file := `{"getThing":{"found":{"operation":"getThing","request":{},"response":{"status":200}},"missing":{"operation":"getThing","request":{},"response":{"status":404}}}}`
	name := filepath.Join(dir, "openapi-example.json")
	if err := os.WriteFile(name, []byte(file), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, command := range []string{"check", "sync", "check"} {
		if code, out, errs := examplesRun(t, dir, command); code != 0 || out != "" || errs != "" {
			t.Fatalf("%s: exit %d, %q, %q", command, code, out, errs)
		}
	}
	written, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(written), `"operation": "getThing"`) != 2 || strings.Contains(string(written), "status200") || strings.Contains(string(written), "status404") {
		t.Fatalf("sync rewrote the targets or added redundant stubs: %s", written)
	}
	if err := os.WriteFile(name, []byte(strings.Replace(file, `"operation":"getThing"`, `"operation":"nowhere"`, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := examplesRun(t, dir, "check"); code != 1 || !strings.Contains(out, "example-orphaned") || !strings.Contains(out, "example-missing") {
		t.Fatalf("unknown target: exit %d, %q", code, out)
	}
}
