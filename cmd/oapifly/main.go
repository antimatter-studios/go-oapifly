// Command oapifly reports what the generator could not describe in an API
// documented by annotations.
//
//	oapifly report [--json] --scan 'internal/handlers/*.go' --types internal/types
//
// It prints one line per warning, or with --json the report as JSON
// ({"warnings": [...]}), the same bytes for the same input. With a strict
// scope (--strict, --strict-operation, --strict-tag, --strict-path) the
// warnings within it are errors, and the command exits 1 when there are any.
// A program that registers its operations in code calls Generator.Report and
// Generator.Check instead.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	oapifly "github.com/antimatter-studios/go-oapifly"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// list is a flag that may be given more than once.
type list []string

func (l *list) String() string     { return strings.Join(*l, ",") }
func (l *list) Set(v string) error { *l = append(*l, v); return nil }

const usage = `usage: oapifly report [flags]

Report what the generator could not describe, one warning per line.

`

// run is the command: it returns 0 when nothing is within the strict scope,
// 1 when something is, and 2 for a mistake in the command line.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "report" {
		fmt.Fprint(stderr, usage)
		return 2
	}
	fs := flag.NewFlagSet("oapifly report", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
	}
	var scan, types, strictOps, strictTags, strictPaths list
	asJSON := fs.Bool("json", false, "print the report as JSON")
	title := fs.String("title", "API", "the document's title")
	version := fs.String("version", "", "the document's version")
	strictAll := fs.Bool("strict", false, "make every warning an error")
	fs.Var(&scan, "scan", "a glob of annotated Go files (repeatable)")
	fs.Var(&types, "types", "a directory of the types annotations name (repeatable)")
	fs.Var(&strictOps, "strict-operation", "make the warnings about an operationId errors (repeatable)")
	fs.Var(&strictTags, "strict-tag", "make the warnings about a tag's operations errors (repeatable)")
	fs.Var(&strictPaths, "strict-path", "make the warnings about paths under a prefix errors (repeatable)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if len(scan) == 0 || fs.NArg() > 0 {
		fs.Usage()
		return 2
	}

	g := oapifly.New(oapifly.Config{
		Title:        *title,
		Version:      *version,
		ScanPatterns: scan,
		TypeDirs:     types,
		Strict: oapifly.StrictScope{
			All:          *strictAll,
			Operations:   strictOps,
			Tags:         strictTags,
			PathPrefixes: strictPaths,
		},
	})
	report := g.Report()
	if *asJSON {
		b, err := g.ReportJSON()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		_, _ = stdout.Write(b)
	} else {
		for _, w := range report {
			fmt.Fprintf(stdout, "%s: %s: %s: %s\n", where(w), w.Severity, w.Kind, w.Message)
		}
	}
	for _, w := range report {
		if w.Severity == oapifly.SeverityError {
			return 1
		}
	}
	return 0
}

// where is a warning's place: its file and line, else its operation.
func where(w oapifly.Warning) string {
	switch {
	case w.File != "" && w.Line > 0:
		return fmt.Sprintf("%s:%d", w.File, w.Line)
	case w.File != "":
		return w.File
	case w.Path != "":
		return w.Method + " " + w.Path
	}
	return "-"
}
