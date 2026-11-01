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

func main() {
	os.Exit(run(os.Stdin, os.Stdout))
}

func run(in io.Reader, out io.Writer) int {
	var (
		pass, fail, skip int
		pkgFail          []string
		bad              int
		output           = map[string]*strings.Builder{}
		failed           []string
	)
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(strings.TrimSpace(string(line))) == 0 {
			continue
		}
		var e event
		if err := json.Unmarshal(line, &e); err != nil || e.Action == "" {
			bad++
			fmt.Fprintf(out, "not test JSON: %s\n", line)
			continue
		}
		key := e.Package + " " + e.Test
		switch e.Action {
		case "output":
			b, ok := output[key]
			if !ok {
				b = &strings.Builder{}
				output[key] = b
			}
			b.WriteString(e.Output)
		case "pass":
			if e.Test != "" {
				pass++
			}
		case "skip":
			if e.Test != "" {
				skip++
			}
		case "fail":
			if e.Test != "" {
				fail++
				failed = append(failed, key)
			} else {
				pkgFail = append(pkgFail, e.Package)
			}
		}
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(out, "reading the test stream: %v\n", err)
		return 1
	}
	sort.Strings(failed)
	for _, k := range failed {
		fmt.Fprintf(out, "--- FAIL %s\n%s\n", k, output[k])
	}
	for _, p := range pkgFail {
		if b, ok := output[p+" "]; ok {
			fmt.Fprintf(out, "--- FAIL package %s\n%s\n", p, b)
		}
	}
	fmt.Fprintf(out, "tests: %d passed, %d failed, %d skipped; packages failed: %d\n", pass, fail, skip, len(pkgFail))
	switch {
	case fail > 0 || len(pkgFail) > 0:
		return 1
	case bad > 0:
		fmt.Fprintf(out, "%d line(s) were not test JSON\n", bad)
		return 1
	case pass == 0:
		fmt.Fprintln(out, "no test passed: a run that executed nothing is not a pass")
		return 1
	}
	return 0
}
