// Command reqreport maps test results to the requirement catalogue
// (TEST-001, TEST-002, TST-011).
//
//	reqreport catalogue -wiki DIR > test/requirements/catalogue.tsv
//	reqreport check -scope test/requirements/scope/0.2.0.txt \
//	    -results unit=test-unit.json -results integration=test-integration.json \
//	    -report requirement-report.md -completion completion-report.md
//
// check exits 1 on a finding (an in-scope requirement without a passing test,
// an unknown ID on a test, a failed or over-long test) and 2 when it cannot
// read its inputs; it never passes over an empty input.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mavrovde/viafrei-api/internal/reqreport"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: reqreport catalogue|check [flags]")
		return 2
	}
	switch args[0] {
	case "catalogue":
		return catalogue(args[1:], stdout, stderr)
	case "check":
		return check(args[1:], stdout, stderr)
	}
	fmt.Fprintf(stderr, "unknown command %q; known: catalogue, check\n", args[0])
	return 2
}

func catalogue(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("catalogue", flag.ContinueOnError)
	fs.SetOutput(stderr)
	wiki := fs.String("wiki", "", "path of a checkout of the wiki")
	if err := fs.Parse(args); err != nil || *wiki == "" {
		fmt.Fprintln(stderr, "catalogue needs -wiki DIR")
		return 2
	}
	reqs, err := reqreport.FromWiki(*wiki)
	if err == nil {
		err = reqreport.WriteCatalogue(stdout, reqs)
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	return 0
}

type resultFlags []string

func (r *resultFlags) String() string     { return strings.Join(*r, ",") }
func (r *resultFlags) Set(v string) error { *r = append(*r, v); return nil }

func check(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	cat := fs.String("catalogue", "test/requirements/catalogue.tsv", "the requirement catalogue")
	scope := fs.String("scope", "", "the milestone's in-scope list")
	report := fs.String("report", "", "write the requirement report (Markdown) here")
	completion := fs.String("completion", "", "write the draft test completion report (Markdown) here")
	version := fs.String("version", "unreleased", "release or milestone the report is for")
	commit := fs.String("commit", "unknown", "commit the results belong to")
	var results resultFlags
	fs.Var(&results, "results", "LEVEL=FILE of `go test -json` output; repeat per level")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	r, err := load(*cat, *scope, results)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	m := reqreport.Meta{Version: *version, Commit: *commit, Generated: time.Now().UTC().Format(time.RFC3339)}
	if err := writeTo(*report, r, m, reqreport.WriteRequirementReport); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	if err := writeTo(*completion, r, m, reqreport.WriteCompletionDraft); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}
	for _, f := range r.Findings {
		fmt.Fprintln(stdout, "FAIL:", f)
	}
	fmt.Fprintln(stdout, r.Summary())
	if r.Failed() {
		return 1
	}
	return 0
}

func load(catPath, scopePath string, results []string) (*reqreport.Report, error) {
	if scopePath == "" || len(results) == 0 {
		return nil, errors.New("check needs -scope and at least one -results LEVEL=FILE")
	}
	f, err := openNamed(catPath)
	if err != nil {
		return nil, err
	}
	cat, err := reqreport.ReadCatalogue(f)
	_ = f.Close()
	if err != nil {
		return nil, err
	}
	s, err := openNamed(scopePath)
	if err != nil {
		return nil, err
	}
	scope, err := reqreport.ReadScope(s)
	_ = s.Close()
	if err != nil {
		return nil, err
	}
	var tests []reqreport.TestResult
	for _, spec := range results {
		level, path, ok := strings.Cut(spec, "=")
		if !ok || level == "" || path == "" {
			return nil, fmt.Errorf("-results %q is not LEVEL=FILE", spec)
		}
		rf, err := openNamed(path)
		if err != nil {
			return nil, err
		}
		ts, err := reqreport.ReadResults(level, rf)
		_ = rf.Close()
		if err != nil {
			return nil, err
		}
		tests = append(tests, ts...)
	}
	return reqreport.Check(cat, scope, len(results), tests), nil
}

// openNamed opens a file the caller names on the command line.
func openNamed(path string) (*os.File, error) {
	return os.Open(path) // #nosec G304 -- a path the caller names on the command line
}

func writeTo(path string, r *reqreport.Report, m reqreport.Meta, write func(io.Writer, *reqreport.Report, reqreport.Meta) error) error {
	if path == "" {
		return nil
	}
	f, err := os.Create(path) // #nosec G304 -- a path the caller names on the command line
	if err != nil {
		return err
	}
	return errors.Join(write(f, r, m), f.Close())
}
