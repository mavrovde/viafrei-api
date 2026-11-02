// Command gotestsum-lite reads `go test -json` on stdin and prints a short
// summary: every failing test with its output, then the counts. It exits 1
// when a test or a package failed, when a line is not test JSON, or when no
// test ran at all, because a run that executed nothing is not a pass (TST-011).
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

type event struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Output  string  `json:"Output"`
	Elapsed float64 `json:"Elapsed"`
}

// tally is what one run of `go test -json` added up to.
type tally struct {
	pass, fail, skip, bad int
	failed, pkgFail       []string
	output                map[string]*strings.Builder
}

func main() {
	os.Exit(run(os.Stdin, os.Stdout))
}

func run(in io.Reader, out io.Writer) int {
	t := &tally{output: map[string]*strings.Builder{}}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		t.line(sc.Bytes(), out)
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(out, "reading the test stream: %v\n", err)
		return 1
	}
	t.report(out)
	return t.verdict(out)
}

// line reads one line of the stream.
func (t *tally) line(line []byte, out io.Writer) {
	if strings.TrimSpace(string(line)) == "" {
		return
	}
	var e event
	if err := json.Unmarshal(line, &e); err != nil || e.Action == "" {
		t.bad++
		fmt.Fprintf(out, "not test JSON: %s\n", line)
		return
	}
	key := e.Package + " " + e.Test
	switch {
	case e.Action == "output":
		t.buffer(key).WriteString(e.Output)
	case e.Test == "":
		if e.Action == "fail" {
			t.pkgFail = append(t.pkgFail, e.Package)
		}
	case e.Action == "pass":
		t.pass++
	case e.Action == "skip":
		t.skip++
	case e.Action == "fail":
		t.fail++
		t.failed = append(t.failed, key)
	}
}

func (t *tally) buffer(key string) *strings.Builder {
	b, ok := t.output[key]
	if !ok {
		b = &strings.Builder{}
		t.output[key] = b
	}
	return b
}

// report prints every failing test and package with its output, then counts.
func (t *tally) report(out io.Writer) {
	sort.Strings(t.failed)
	for _, k := range t.failed {
		fmt.Fprintf(out, "--- FAIL %s\n%s\n", k, t.output[k])
	}
	for _, p := range t.pkgFail {
		if b, ok := t.output[p+" "]; ok {
			fmt.Fprintf(out, "--- FAIL package %s\n%s\n", p, b)
		}
	}
	fmt.Fprintf(out, "tests: %d passed, %d failed, %d skipped; packages failed: %d\n", t.pass, t.fail, t.skip, len(t.pkgFail))
}

func (t *tally) verdict(out io.Writer) int {
	switch {
	case t.fail > 0 || len(t.pkgFail) > 0:
		return 1
	case t.bad > 0:
		fmt.Fprintf(out, "%d line(s) were not test JSON\n", t.bad)
		return 1
	case t.pass == 0:
		fmt.Fprintln(out, "no test passed: a run that executed nothing is not a pass")
		return 1
	}
	return 0
}
