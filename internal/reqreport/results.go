package reqreport

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Marker is the line req.Covers writes (internal/testkit/req).
const Marker = "REQ-ID:"

// TestBound is the ceiling on one test (TST-012): a leaf test that took
// longer fails the report even when it passed.
const TestBound = 2 * time.Second

// TestResult is one test of one result file.
type TestResult struct {
	Level   string // the label of the result file: unit, integration, ...
	Package string
	Name    string
	Outcome string // pass, fail or skip
	Elapsed time.Duration
	IDs     []string
	leaf    bool
}

// Key names the test the way the report prints it.
func (t TestResult) Key() string {
	return t.Level + " " + shortPackage(t.Package) + "." + t.Name
}

func shortPackage(p string) string {
	if i := strings.Index(p, "/internal/"); i >= 0 {
		return p[i+1:]
	}
	if i := strings.Index(p, "/cmd/"); i >= 0 {
		return p[i+1:]
	}
	return p
}

type event struct {
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Output  string  `json:"Output"`
	Elapsed float64 `json:"Elapsed"`
}

// ReadResults reads one `go test -json` stream. A line that is not test
// JSON, or a stream with no test result at all, is an error (TST-011).
func ReadResults(level string, r io.Reader) ([]TestResult, error) {
	byKey := map[string]*TestResult{}
	var order []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	n := 0
	for sc.Scan() {
		n++
		if strings.TrimSpace(sc.Text()) == "" {
			continue
		}
		var e event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil || e.Action == "" {
			return nil, fmt.Errorf("%s results, line %d is not test JSON", level, n)
		}
		if e.Test == "" {
			continue
		}
		k := e.Package + "\x00" + e.Test
		t, ok := byKey[k]
		if !ok {
			t = &TestResult{Level: level, Package: e.Package, Name: e.Test}
			byKey[k] = t
			order = append(order, k)
		}
		record(t, e)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	out := make([]TestResult, 0, len(order))
	for _, k := range order {
		if t := byKey[k]; t.Outcome != "" {
			out = append(out, *t)
		}
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s results hold no test result", level)
	}
	markLeaves(out)
	return out, nil
}

// markerLine is the line req.Covers writes through t.Log: the file:line
// prefix go test adds, then the marker. "REQ-ID:" anywhere else in a test's
// output (a message quoting it, a log of a fixture) is not a claim.
var markerLine = regexp.MustCompile(`^\s*[\w.-]+\.go:\d+: ` + regexp.QuoteMeta(Marker) + `(.*)$`)

func record(t *TestResult, e event) {
	switch e.Action {
	case "output":
		if m := markerLine.FindStringSubmatch(strings.TrimRight(e.Output, "\n")); m != nil {
			t.IDs = append(t.IDs, strings.Fields(m[1])...)
		}
	case "pass", "fail", "skip":
		t.Outcome = e.Action
		t.Elapsed = time.Duration(e.Elapsed * float64(time.Second))
	}
}

// markLeaves flags the tests without subtests; a parent's elapsed time is
// the sum of its children's, so only leaves are held to TestBound.
func markLeaves(ts []TestResult) {
	parents := map[string]bool{}
	for _, t := range ts {
		if i := strings.LastIndex(t.Name, "/"); i >= 0 {
			parents[t.Package+"\x00"+t.Name[:i]] = true
		}
	}
	for i := range ts {
		ts[i].leaf = !parents[ts[i].Package+"\x00"+ts[i].Name]
	}
}

// sortTests orders tests by their printed key.
func sortTests(ts []TestResult) {
	sort.Slice(ts, func(i, j int) bool { return ts[i].Key() < ts[j].Key() })
}
