package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// someAnswers are n answers of class c taking d each, with the given status.
func someAnswers(c string, n, status int, d time.Duration) []result {
	rs := make([]result, n)
	for i := range rs {
		rs[i] = result{class: c, status: status, took: d}
	}
	return rs
}

func TestCollectGivesEachClassItsFigures(t *testing.T) {
	req.Covers(t, "TST-021")
	growth := 0.04
	o := &outcome{name: "soak", memGrowth: &growth}
	o.add("held", true, "")
	o.results = append(o.results, someAnswers(classNear, 3, http.StatusOK, 10*time.Millisecond)...)
	o.results = append(o.results, result{class: classNear, status: http.StatusOK, took: 30 * time.Millisecond})
	o.results = append(o.results, result{class: classNear, status: http.StatusTooManyRequests, problem: "overloaded", took: 90 * time.Millisecond})
	o.results = append(o.results, result{class: classLookup, status: http.StatusNotFound, took: 2 * time.Millisecond})
	o.results = append(o.results, result{class: classLookup, took: 5 * time.Millisecond})
	o.results = append(o.results, result{class: classLookup, status: http.StatusServiceUnavailable, took: time.Millisecond})
	f := collect("nightly", []*outcome{o})
	if f.Version != figuresVersion || f.Profile != "nightly" || len(f.Scenarios) != 1 {
		t.Fatalf("figures: %+v", f)
	}
	s := f.Scenarios[0]
	if s.Name != "soak" || !s.Passed || s.MemoryGrowth == nil || *s.MemoryGrowth != growth || len(s.Classes) != 2 {
		t.Fatalf("scenario: %+v", s)
	}
	lookup, near := s.Classes[0], s.Classes[1]
	want := classFigures{Class: classLookup, Requests: 3, Status4xx: 1, Status5xx: 1, NoAnswer: 1, SlowestMs: 5}
	if lookup != want {
		t.Errorf("lookup: %+v, want %+v", lookup, want)
	}
	want = classFigures{Class: classNear, Requests: 5, Status2xx: 4, Status4xx: 1, OK: 4, P50Ms: 10, P95Ms: 30, P99Ms: 30, SlowestMs: 90, RefusalShare: 0.2}
	if near != want {
		t.Errorf("near: %+v, want %+v", near, want)
	}
}

// figuresWith is one scenario of one class whose 2xx answers have the given
// p95 and p99, over n answers.
func figuresWith(prof, scen, class string, n int, p95, p99 float64) figures {
	return figures{Version: figuresVersion, Profile: prof, Scenarios: []scenarioFigures{{
		Name: scen, Passed: true, Classes: []classFigures{{Class: class, Requests: n, Status2xx: n, OK: n, P50Ms: 1, P95Ms: p95, P99Ms: p99, SlowestMs: p99}},
	}}}
}

func writeJSON(t *testing.T, v any) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "previous.json")
	if err := writeFigures(p, v.(figures)); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestComparisonFailsAP95OrP99RegressionAndNamesIt(t *testing.T) {
	req.Covers(t, "TST-021")
	prev := writeJSON(t, figuresWith("nightly", "soak", classNear, 100, 100, 200))
	for name, tc := range map[string]struct {
		p95, p99 float64
		want     []string
	}{
		"p95 up 20 %":          {120, 200, []string{"soak, class near: p95 100 ms → 120 ms (+20.0 %)"}},
		"p99 up 20 %":          {100, 240, []string{"soak, class near: p99 200 ms → 240 ms (+20.0 %)"}},
		"both up":              {150, 300, []string{"p95 100 ms → 150 ms", "p99 200 ms → 300 ms"}},
		"within the tolerance": {109, 219, nil},
		"faster":               {50, 100, nil},
	} {
		t.Run(name, func(t *testing.T) {
			c := compare(prev, figuresWith("nightly", "soak", classNear, 100, tc.p95, tc.p99), 0.10, 5*time.Millisecond)
			assertRegressions(t, c, tc.want)
		})
	}
}

// assertRegressions checks that c compared, found exactly the regressions
// want names (in order), and says so in its report.
func assertRegressions(t *testing.T, c comparison, want []string) {
	t.Helper()
	if c.state != compared || len(c.regressions) != len(want) || c.failed() != (len(want) > 0) {
		t.Fatalf("%+v", c)
	}
	for i, w := range want {
		if !strings.Contains(c.regressions[i], w) {
			t.Errorf("regression %q, want %q", c.regressions[i], w)
		}
	}
	text := c.render(0.10, 5*time.Millisecond)
	if !strings.Contains(text, "| soak | near | 100 → 100 (+0.0 %) | 100/0/0/0 → 100/0/0/0 |") {
		t.Errorf("table:\n%s", text)
	}
	if (len(want) > 0) != strings.Contains(text, "**FAIL**") || (len(want) == 0) != strings.Contains(text, "No regression beyond the tolerance") {
		t.Errorf("verdict:\n%s", text)
	}
}

// The edges of the tolerance and the floor, on values whose products are
// exact in float64: equal to the bound is no regression, a hair above is.
func TestRegressionEdges(t *testing.T) {
	req.Covers(t, "TST-021")
	for _, tc := range []struct {
		prev, now, tol float64
		floor          time.Duration
		want           bool
	}{
		{100, 125, 0.25, 0, false},                       // exactly the tolerance
		{100, 125.001, 0.25, 0, true},                    // just above it
		{100, 105, 0.01, 5 * time.Millisecond, false},    // above the tolerance, exactly the floor
		{100, 105.001, 0.01, 5 * time.Millisecond, true}, // just above the floor
	} {
		if got := regressed(tc.prev, tc.now, tc.tol, tc.floor); got != tc.want {
			t.Errorf("regressed(%v, %v, %v, %v) = %v, want %v", tc.prev, tc.now, tc.tol, tc.floor, got, tc.want)
		}
	}
	// The same edge through compare, so compare is the predicate's caller.
	prev := writeJSON(t, figuresWith("nightly", "soak", classNear, 100, 100, 100))
	if c := compare(prev, figuresWith("nightly", "soak", classNear, 100, 125, 100), 0.25, 0); len(c.regressions) != 0 {
		t.Errorf("at the tolerance: %v", c.regressions)
	}
	if c := compare(prev, figuresWith("nightly", "soak", classNear, 100, 125.001, 100), 0.25, 0); len(c.regressions) != 1 {
		t.Errorf("above the tolerance: %v", c.regressions)
	}
}

func TestComparisonIgnoresSmallOrThinChanges(t *testing.T) {
	req.Covers(t, "TST-021")
	// 2 ms to 4 ms is +100 %, but within the floor.
	prev := writeJSON(t, figuresWith("nightly", "soak", classLookup, 100, 2, 3))
	if c := compare(prev, figuresWith("nightly", "soak", classLookup, 100, 4, 6), 0.10, 5*time.Millisecond); c.failed() {
		t.Errorf("within the floor: %+v", c.regressions)
	}
	// With no floor the same change is a regression.
	if c := compare(prev, figuresWith("nightly", "soak", classLookup, 100, 4, 6), 0.10, 0); len(c.regressions) != 2 {
		t.Errorf("no floor: %+v", c.regressions)
	}
	// Fewer than minCompared 2xx answers in either run are not judged.
	thin := writeJSON(t, figuresWith("nightly", "soak", classLookup, minCompared-1, 100, 100))
	c := compare(thin, figuresWith("nightly", "soak", classLookup, 100, 500, 500), 0.10, 0)
	if c.failed() {
		t.Errorf("thin previous: %+v", c.regressions)
	}
	want := fmt.Sprintf("- soak, class lookup: %d → 100 2xx answers, fewer than %d in a run", minCompared-1, minCompared)
	if text := c.render(0.10, 0); !strings.Contains(text, "Not judged, too few 2xx answers") || !strings.Contains(text, want) {
		t.Errorf("an unjudged class is not named:\n%s", text)
	}
	if c := compare(prev, figuresWith("nightly", "soak", classLookup, minCompared-1, 500, 500), 0.10, 0); c.failed() {
		t.Errorf("thin now: %+v", c.regressions)
	}
	if c := compare(prev, figuresWith("nightly", "soak", classLookup, minCompared, 500, 500), 0.10, 0); len(c.regressions) != 2 || len(c.unjudged) != 0 {
		t.Errorf("exactly minCompared: %+v", c)
	}
}

func TestComparisonNamesNewAndMissingScenariosAndClasses(t *testing.T) {
	req.Covers(t, "TST-021")
	g1, g2 := 0.02, 0.05
	old := figuresWith("nightly", "soak", classNear, 100, 10, 20)
	old.Scenarios[0].MemoryGrowth = &g1
	old.Scenarios = append(old.Scenarios, scenarioFigures{Name: "spike", Classes: []classFigures{}})
	cur := figuresWith("nightly", "soak", classNear, 100, 10, 20)
	cur.Scenarios[0].MemoryGrowth = &g2
	cur.Scenarios[0].Classes = append(cur.Scenarios[0].Classes, classFigures{Class: classComposed, Requests: 1})
	cur.Scenarios = append(cur.Scenarios, scenarioFigures{Name: "stress", Classes: []classFigures{}})
	c := compare(writeJSON(t, old), cur, 0.10, 0)
	text := c.render(0.10, 0)
	for _, w := range []string{
		"| soak | memory growth | 2 % → 5 % (+150.0 %) |",
		"| soak | composed | new in this run |",
		"| stress | all | new in this run |",
		"| spike | all | not run this time |",
	} {
		if !strings.Contains(text, w) {
			t.Errorf("missing %q in\n%s", w, text)
		}
	}
	if c.failed() {
		t.Errorf("failed: %+v", c.regressions)
	}
}

func TestChangeFromZero(t *testing.T) {
	req.Covers(t, "TST-021")
	if got := change(0, 3, " ms"); got != "0 ms → 3 ms" {
		t.Errorf("%q", got)
	}
}

func TestPreviousFiguresAreNeverReadAsNoChange(t *testing.T) {
	req.Covers(t, "TST-021")
	dir := t.TempDir()
	file := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cur := figuresWith("nightly", "soak", classNear, 100, 10, 20)
	for name, tc := range map[string]struct {
		path   string
		state  baseline
		failed bool
		want   string
	}{
		"not asked":     {"", notAsked, false, "Not compared: no previous figures were given (-previous)."},
		"absent":        {filepath.Join(dir, "absent.json"), absent, false, "absent.json does not exist: no earlier successful main run left figures"},
		"a directory":   {dir, unreadable, true, "could not be read"},
		"not JSON":      {file("junk.json", "<html>"), unreadable, true, "junk.json is not a figures file"},
		"other version": {file("v9.json", `{"version":9,"profile":"nightly","scenarios":[{"name":"soak"}]}`), unreadable, true, "has figures version 9, want 1"},
		"no scenario":   {file("empty.json", `{"version":1,"profile":"nightly","scenarios":[]}`), unreadable, true, "empty.json holds no scenario"},
		"other budget":  {file("pr.json", `{"version":1,"profile":"pr","scenarios":[{"name":"soak"}]}`), otherShape, false, "is of the pr budget, this run of the nightly budget"},
	} {
		t.Run(name, func(t *testing.T) {
			c := compare(tc.path, cur, 0.10, 0)
			text := c.render(0.10, 0)
			if c.state != tc.state || c.failed() != tc.failed || !strings.Contains(text, tc.want) || len(c.rows) != 0 {
				t.Errorf("%+v\n%s", c, text)
			}
			if tc.failed != strings.Contains(text, "**FAIL**: the previous figures are unreadable") {
				t.Errorf("verdict:\n%s", text)
			}
			if !tc.failed && !strings.Contains(text, "judged on the absolute budgets alone") {
				t.Errorf("not compared:\n%s", text)
			}
		})
	}
}

func TestWriteFiguresRefusesWhatJSONCannotHold(t *testing.T) {
	req.Covers(t, "TST-021")
	nan := math.NaN()
	f := figures{Scenarios: []scenarioFigures{{MemoryGrowth: &nan}}}
	if err := writeFigures(filepath.Join(t.TempDir(), "f.json"), f); err == nil {
		t.Error("NaN written")
	}
}

func TestRunWritesTheFiguresBesideTheReport(t *testing.T) {
	req.Covers(t, "TST-021")
	c := newCLI(t, healthyAPI)
	code, out, errs := c.run(c.args()...)
	if code != 0 || !strings.Contains(out, "not compared with the previous main run: no previous figures were given") {
		t.Fatalf("exit %d\n%s\n%s", code, out, errs)
	}
	b, err := os.ReadFile(filepath.Join(c.dir, "report.json"))
	if err != nil {
		t.Fatal(err)
	}
	var f figures
	if err := json.Unmarshal(b, &f); err != nil || f.Profile != "test" || len(f.Scenarios) != 1 || f.Scenarios[0].Name != "soak" ||
		f.Scenarios[0].MemoryGrowth == nil || len(f.Scenarios[0].Classes) == 0 {
		t.Errorf("figures: %v\n%s", err, b)
	}
	report, _ := os.ReadFile(c.report)
	if !strings.Contains(string(report), "## Comparison with the previous main run") {
		t.Errorf("report:\n%s", report)
	}
	// The next run compares with these figures: the same stack shows no
	// regression; a previous run ten times faster fails the run by name.
	again := filepath.Join(c.dir, "previous.json")
	if err := os.Rename(filepath.Join(c.dir, "report.json"), again); err != nil {
		t.Fatal(err)
	}
	if code, out, _ := c.run(c.args("-previous", again, "-regression-tolerance", "10")...); code != 0 || strings.Contains(out, "regression:") {
		t.Errorf("same stack: exit %d\n%s", code, out)
	}
	for i := range f.Scenarios[0].Classes {
		k := &f.Scenarios[0].Classes[i]
		k.P95Ms, k.P99Ms, k.OK = k.P95Ms/1000, k.P99Ms/1000, 1000
	}
	if err := writeFigures(again, f); err != nil {
		t.Fatal(err)
	}
	code, out, _ = c.run(c.args("-previous", again, "-regression-floor", "0s")...)
	if code != 1 || !strings.Contains(out, "loadrun: regression: soak, class ") {
		t.Errorf("regression: exit %d\n%s", code, out)
	}
	// An unreadable previous file fails the run, though every scenario passed.
	if err := os.WriteFile(again, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, _ = c.run(c.args("-previous", again)...)
	if code != 1 || !strings.Contains(out, "0 failed") {
		t.Errorf("unreadable: exit %d\n%s", code, out)
	}
}

func TestRunRefusesAnUnwritableFiguresFile(t *testing.T) {
	req.Covers(t, "TST-021")
	c := newCLI(t, healthyAPI)
	if err := os.Mkdir(filepath.Join(c.dir, "report.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if code, _, errs := c.run(c.args()...); code != 2 || !strings.Contains(errs, "report.json") {
		t.Errorf("exit %d, %s", code, errs)
	}
}
