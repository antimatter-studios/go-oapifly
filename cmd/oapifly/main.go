// Command oapifly reports what the generator could not describe in an API
// documented by annotations, and keeps its example file in step.
//
//	oapifly report [--json] --scan 'internal/handlers/*.go' --types internal/types
//	oapifly examples sync --file openapi-example.json --scan 'internal/handlers/*.go'
//	oapifly examples check --file openapi-example.json --scan 'internal/handlers/*.go'
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
	"path/filepath"
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
       oapifly examples sync --file openapi-example.json [flags]
       oapifly examples check --file openapi-example.json [flags]

report:          what the generator could not describe, one warning per line.
examples sync:   add a stub to the example file for every documented status no
                 example pair shows; nothing is removed.
examples check:  report the example file's problems: a pair that does not fit,
                 a body that does not validate, a status no pair shows.

`

// run is the command: it returns 0 when nothing is within the strict scope
// (for examples check, when the example file has no problem), 1 when
// something is, and 2 for a mistake in the command line or a file it cannot
// write.
func run(args []string, stdout, stderr io.Writer) int {
	switch {
	case len(args) >= 1 && args[0] == "report":
		return reportCommand(args[1:], stdout, stderr)
	case len(args) >= 2 && args[0] == "examples" && (args[1] == "sync" || args[1] == "check"):
		return examplesCommand(args[1], args[2:], stdout, stderr)
	}
	fmt.Fprint(stderr, usage)
	return 2
}

// options are the flags every command takes, and the generator they describe.
type options struct {
	fs                                              *flag.FlagSet
	scan, types, strictOps, strictTags, strictPaths list
	requires                                        list
	title, version, file                            *string
	strictAll                                       *bool
}

func newOptions(name string, stderr io.Writer, withFile bool) *options {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
	}
	o := &options{fs: fs}
	o.title = fs.String("title", "API", "the document's title")
	o.version = fs.String("version", "", "the document's version")
	o.strictAll = fs.Bool("strict", false, "make every warning an error")
	fs.Var(&o.scan, "scan", "a glob of annotated Go files (repeatable)")
	fs.Var(&o.types, "types", "a directory of the types annotations name (repeatable)")
	fs.Var(&o.strictOps, "strict-operation", "make the warnings about an operationId errors (repeatable)")
	fs.Var(&o.strictTags, "strict-tag", "make the warnings about a tag's operations errors (repeatable)")
	fs.Var(&o.strictPaths, "strict-path", "make the warnings about paths under a prefix errors (repeatable)")
	fs.Var(&o.requires, "requires", "a kind of requirement and the values a pair may require of it, kind=value,value (repeatable)")
	if withFile {
		o.file = fs.String("file", "", "the example file, such as openapi-example.json")
	} else {
		o.file = fs.String("examples", "", "an example file to merge, such as openapi-example.json")
	}
	return o
}

// parse reads the flags, reporting false for a command line it cannot use.
func (o *options) parse(args []string, needFile bool) bool {
	if err := o.fs.Parse(args); err != nil {
		return false
	}
	for _, entry := range o.requires {
		if kind, values, ok := strings.Cut(entry, "="); !ok || kind == "" || values == "" {
			fmt.Fprintf(o.fs.Output(), "-requires %q: want kind=value,value\n", entry)
			return false
		}
	}
	if len(o.scan) == 0 || o.fs.NArg() > 0 || (needFile && *o.file == "") {
		o.fs.Usage()
		return false
	}
	return true
}

func (o *options) generator() *oapifly.Generator {
	config := oapifly.Config{
		Title:        *o.title,
		Version:      *o.version,
		ScanPatterns: o.scan,
		TypeDirs:     o.types,
		Strict: oapifly.StrictScope{
			All:          *o.strictAll,
			Operations:   o.strictOps,
			Tags:         o.strictTags,
			PathPrefixes: o.strictPaths,
		},
	}
	for _, entry := range o.requires {
		kind, values, _ := strings.Cut(entry, "=")
		if config.ExampleRequirements == nil {
			config.ExampleRequirements = map[string][]string{}
		}
		config.ExampleRequirements[kind] = append(config.ExampleRequirements[kind], strings.Split(values, ",")...)
	}
	if *o.file != "" {
		config.Examples = os.DirFS(filepath.Dir(*o.file))
		config.ExamplesFile = filepath.Base(*o.file)
	}
	return oapifly.New(config)
}

func reportCommand(args []string, stdout, stderr io.Writer) int {
	o := newOptions("oapifly report", stderr, false)
	asJSON := o.fs.Bool("json", false, "print the report as JSON")
	if !o.parse(args, false) {
		return 2
	}
	g := o.generator()
	warnings := g.Report()
	if *asJSON {
		b, err := g.ReportJSON()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		_, _ = stdout.Write(b)
	} else {
		printWarnings(stdout, warnings)
	}
	for _, w := range warnings {
		if w.Severity == oapifly.SeverityError {
			return 1
		}
	}
	return 0
}

// exampleKinds are the warnings about an example file.
var exampleKinds = map[oapifly.WarningKind]bool{
	oapifly.WarnExamplesUnreadable: true,
	oapifly.WarnExampleOrphaned:    true,
	oapifly.WarnExampleMissing:     true,
	oapifly.WarnExampleInvalid:     true,

	oapifly.WarnExampleRequirementUnknown: true,
}

func examplesCommand(command string, args []string, stdout, stderr io.Writer) int {
	o := newOptions("oapifly examples "+command, stderr, true)
	if !o.parse(args, true) {
		return 2
	}
	g := o.generator()
	if command == "sync" {
		file, err := g.ExampleStubs()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		b, err := oapifly.MarshalExamples(file)
		if err == nil {
			err = os.WriteFile(*o.file, b, 0o644)
		}
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
		// What sync cannot fix by adding a stub is still worth saying.
		var left []oapifly.Warning
		for _, w := range oapifly.New(g.Config).Report() {
			if exampleKinds[w.Kind] && w.Kind != oapifly.WarnExampleMissing {
				left = append(left, w)
			}
		}
		printWarnings(stdout, left)
		return 0
	}
	var problems []oapifly.Warning
	for _, w := range g.Report() {
		if exampleKinds[w.Kind] {
			problems = append(problems, w)
		}
	}
	printWarnings(stdout, problems)
	if len(problems) > 0 {
		return 1
	}
	return 0
}

func printWarnings(out io.Writer, warnings []oapifly.Warning) {
	for _, w := range warnings {
		fmt.Fprintf(out, "%s: %s: %s: %s\n", where(w), w.Severity, w.Kind, w.Message)
	}
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
