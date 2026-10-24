package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const handlers = `package handlers

// Thing gets a thing.
// @ID getThing
// @Tags things
// @Success 200 {object} types.Missing "OK"
// @Router /things [get]
func Thing() {}
`

// report runs the command over one annotated handler that names a type
// nowhere to be found.
func report(t *testing.T, flags ...string) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "handlers.go"), []byte(handlers), 0o600); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"report", "--scan", filepath.Join(dir, "*.go"), "--types", filepath.Join(dir, "types")}, flags...)
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// The report lists each warning with its place, as text or JSON, and exits 1
// only for a warning within the strict scope.
func TestReport(t *testing.T) {
	code, out, _ := report(t)
	if code != 0 || !strings.Contains(out, "handlers.go:8: warning: unresolved-type: type types.Missing") {
		t.Errorf("text: exit %d, %q", code, out)
	}

	code, out, _ = report(t, "--json")
	var doc struct {
		Warnings []map[string]interface{} `json:"warnings"`
	}
	if err := json.Unmarshal([]byte(out), &doc); err != nil || code != 0 {
		t.Fatalf("json: exit %d, %v: %q", code, err, out)
	}
	if len(doc.Warnings) != 1 || doc.Warnings[0]["kind"] != "unresolved-type" || doc.Warnings[0]["operationId"] != "getThing" {
		t.Errorf("json: %v", doc.Warnings)
	}

	for _, flags := range [][]string{{"--strict"}, {"--strict-operation", "getThing"}, {"--strict-tag", "things"}, {"--strict-path", "/things"}} {
		if code, out, _ := report(t, flags...); code != 1 || !strings.Contains(out, ": error: unresolved-type:") {
			t.Errorf("%v: exit %d, %q", flags, code, out)
		}
	}
	if code, _, _ := report(t, "--strict-tag", "other", "--strict-path", "/thingsx"); code != 0 {
		t.Errorf("a scope that covers nothing: exit %d", code)
	}
}

// A command line it cannot read exits 2 with the usage.
func TestReportUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"describe"}, {"report"}, {"report", "--bogus"}, {"report", "--scan", "x", "extra"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage: oapifly report") {
			t.Errorf("%q: exit %d, %q", args, code, stderr.String())
		}
	}
}

const answering = `package handlers

// Thing gets a thing.
// @ID getThing
// @Success 200 {string} string "OK"
// @Failure 404 {string} string "No such thing"
// @Router /things [get]
func Thing() {}
`

// examplesRun runs examples sync or check over one annotated handler, with the example file
// in the same directory.
func examplesRun(t *testing.T, dir, command string) (int, string, string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "handlers.go"), []byte(answering), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"examples", command, "--scan", filepath.Join(dir, "*.go"), "--file", filepath.Join(dir, "openapi-example.json")}, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// sync writes a stub for each documented status, after which check finds nothing; check
// reports a pair that does not fit and exits 1; a missing --file is a usage error.
func TestExamples(t *testing.T) {
	dir := t.TempDir()
	if code, out, _ := examplesRun(t, dir, "check"); code != 1 || !strings.Contains(out, "examples-unreadable") {
		t.Errorf("check with no file: exit %d, %q", code, out)
	}
	if code, out, errs := examplesRun(t, dir, "sync"); code != 0 || out != "" {
		t.Fatalf("sync: exit %d, %q %q", code, out, errs)
	}
	written, err := os.ReadFile(filepath.Join(dir, "openapi-example.json"))
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]map[string]map[string]interface{}
	if err := json.Unmarshal(written, &file); err != nil {
		t.Fatal(err)
	}
	if _, ok := file["getThing"]["status200"]; !ok || len(file["getThing"]) != 2 {
		t.Errorf("stubs: %s", written)
	}
	if code, out, _ := examplesRun(t, dir, "check"); code != 0 || out != "" {
		t.Errorf("check after sync: exit %d, %q", code, out)
	}

	broken := strings.Replace(string(written), `"status": 404`, `"status": 418`, 1)
	if err := os.WriteFile(filepath.Join(dir, "openapi-example.json"), []byte(broken), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := examplesRun(t, dir, "check"); code != 1 || !strings.Contains(out, "example-orphaned") || !strings.Contains(out, "example-missing") {
		t.Errorf("check with a teapot: exit %d, %q", code, out)
	}
	if code, out, _ := examplesRun(t, dir, "sync"); code != 0 || !strings.Contains(out, "example-orphaned") {
		t.Errorf("sync reports what it cannot fix: exit %d, %q", code, out)
	}

	var stdout, stderr bytes.Buffer
	if code := run([]string{"examples", "check", "--scan", filepath.Join(dir, "*.go")}, &stdout, &stderr); code != 2 {
		t.Errorf("no --file: exit %d", code)
	}
}
