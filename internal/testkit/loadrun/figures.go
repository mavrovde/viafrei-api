package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"time"
)

// figuresVersion is the shape of the figures file; a file of another shape
// is unreadable, never compared field by field (TST-021).
const figuresVersion = 1

// minCompared is the fewest 2xx answers of a class, in both runs, for its
// percentiles to be judged against the tolerance: a percentile of a handful
// of answers is one answer, and its change says nothing.
const minCompared = 20

// figures are a run's numbers in machine-readable form, written beside the
// Markdown report and read back by the next run (TST-021).
type figures struct {
	Version   int               `json:"version"`
	Profile   string            `json:"profile"`
	Scenarios []scenarioFigures `json:"scenarios"`
}

type scenarioFigures struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	// MemoryGrowth is the resident memory's growth after warm-up, as a
	// fraction; only the soak measures it.
	MemoryGrowth *float64       `json:"memory_growth,omitempty"`
	Classes      []classFigures `json:"classes"`
}

// classFigures are one endpoint class's numbers in one scenario. The
// percentiles are of the 2xx answers (the answers NFR-PERF-003 budgets);
// the slowest is of every answer.
type classFigures struct {
	Class        string  `json:"class"`
	Requests     int     `json:"requests"`
	Status2xx    int     `json:"status_2xx"`
	Status4xx    int     `json:"status_4xx"`
	Status5xx    int     `json:"status_5xx"`
	NoAnswer     int     `json:"no_answer"`
	OK           int     `json:"ok_answers"`
	P50Ms        float64 `json:"p50_ms"`
	P95Ms        float64 `json:"p95_ms"`
	P99Ms        float64 `json:"p99_ms"`
	SlowestMs    float64 `json:"slowest_ms"`
	RefusalShare float64 `json:"refusal_share"`
}

func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// collect turns the outcomes into figures, classes in name order.
func collect(prof string, outs []*outcome) figures {
	f := figures{Version: figuresVersion, Profile: prof, Scenarios: []scenarioFigures{}}
	for _, o := range outs {
		s := scenarioFigures{Name: o.name, Passed: o.passed(), MemoryGrowth: o.memGrowth, Classes: []classFigures{}}
		var classes []string
		for _, r := range o.results {
			if !slices.Contains(classes, r.class) {
				classes = append(classes, r.class)
			}
		}
		slices.Sort(classes)
		for _, c := range classes {
			s.Classes = append(s.Classes, classFigure(o.results, c))
		}
		f.Scenarios = append(f.Scenarios, s)
	}
	return f
}

// classFigure is the figures of the results of class c.
func classFigure(rs []result, c string) classFigures {
	in := func(r result) bool { return r.class == c }
	var st [6]int
	for _, r := range rs {
		if in(r) {
			st[min(r.status/100, 5)]++
		}
	}
	good := took(rs, func(r result) bool { return in(r) && ok(r) })
	n := count(rs, in)
	return classFigures{
		Class: c, Requests: n,
		Status2xx: st[2], Status4xx: st[4], Status5xx: st[5], NoAnswer: st[0], OK: len(good),
		P50Ms: ms(percentile(good, 0.50)), P95Ms: ms(percentile(good, 0.95)), P99Ms: ms(percentile(good, 0.99)),
		SlowestMs:    ms(percentile(took(rs, in), 1)),
		RefusalShare: float64(count(rs, func(r result) bool { return in(r) && refused(r) })) / float64(n),
	}
}

// writeFigures writes f as indented JSON.
func writeFigures(path string, f figures) error {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}

// figuresPath is the figures file beside the report: its name with .json
// for .md.
func figuresPath(report string) string {
	return strings.TrimSuffix(report, ".md") + ".json"
}

// baseline is what became of the previous run's figures.
type baseline int

const (
	notAsked   baseline = iota // no -previous given
	absent                     // the file does not exist
	unreadable                 // it exists but cannot be read as figures
	otherShape                 // figures of another profile
	compared                   // read and compared
)

// readPrevious reads the previous run's figures. It never turns an absent
// or unreadable file into an empty set of figures: each is its own state,
// with the reason.
func readPrevious(path, prof string) (figures, baseline, string) {
	var f figures
	if path == "" {
		return f, notAsked, "no previous figures were given (-previous)"
	}
	b, err := os.ReadFile(path) //nolint:gosec // G304: the path is the operator's own flag
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return f, absent, fmt.Sprintf("%s does not exist: no earlier successful main run left figures", path)
	case err != nil:
		return f, unreadable, fmt.Sprintf("%s could not be read: %v", path, err)
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return f, unreadable, fmt.Sprintf("%s is not a figures file: %v", path, err)
	}
	switch {
	case f.Version != figuresVersion:
		return f, unreadable, fmt.Sprintf("%s has figures version %d, want %d", path, f.Version, figuresVersion)
	case len(f.Scenarios) == 0:
		return f, unreadable, fmt.Sprintf("%s holds no scenario", path)
	case f.Profile != prof:
		return f, otherShape, fmt.Sprintf("%s is of the %s budget, this run of the %s budget; runs of different budgets are not compared", path, f.Profile, prof)
	}
	return f, compared, path
}

// comparison is the previous run set against this one.
type comparison struct {
	state       baseline
	reason      string
	rows        []string // Markdown table rows
	regressions []string // each names the scenario, the class and the figure
	unjudged    []string // classes in both runs with too few 2xx answers to judge
}

// failed: a regression beyond the tolerance, or a previous file that exists
// and cannot be read.
func (c comparison) failed() bool { return len(c.regressions) > 0 || c.state == unreadable }

// change is "prev → now (±x %)".
func change(prev, now float64, unit string) string {
	if prev == 0 {
		return fmt.Sprintf("%g%s → %g%s", prev, unit, now, unit)
	}
	return fmt.Sprintf("%g%s → %g%s (%+.1f %%)", prev, unit, now, unit, (now-prev)/prev*100)
}

// compare sets cur against the previous figures read from path. A p95 or
// p99 of a class's 2xx answers that grew by more than tol (a fraction) and
// by more than floor is a regression, when both runs have at least
// minCompared such answers.
func compare(path string, cur figures, tol float64, floor time.Duration) comparison {
	prev, state, reason := readPrevious(path, cur.Profile)
	c := comparison{state: state, reason: reason}
	if state != compared {
		return c
	}
	for _, s := range cur.Scenarios {
		i := slices.IndexFunc(prev.Scenarios, func(p scenarioFigures) bool { return p.Name == s.Name })
		if i < 0 {
			c.rows = append(c.rows, fmt.Sprintf("| %s | all | new in this run | | | | | | |", s.Name))
			continue
		}
		c.scenario(prev.Scenarios[i], s, tol, floor)
	}
	for _, p := range prev.Scenarios {
		if !slices.ContainsFunc(cur.Scenarios, func(s scenarioFigures) bool { return s.Name == p.Name }) {
			c.rows = append(c.rows, fmt.Sprintf("| %s | all | not run this time | | | | | | |", p.Name))
		}
	}
	return c
}

// scenario adds the rows, regressions and unjudged classes of scenario s,
// present in both runs (p the previous).
func (c *comparison) scenario(p, s scenarioFigures, tol float64, floor time.Duration) {
	if s.MemoryGrowth != nil && p.MemoryGrowth != nil {
		c.rows = append(c.rows, fmt.Sprintf("| %s | memory growth | %s | | | | | | |", s.Name, change(*p.MemoryGrowth*100, *s.MemoryGrowth*100, " %")))
	}
	for _, k := range s.Classes {
		j := slices.IndexFunc(p.Classes, func(x classFigures) bool { return x.Class == k.Class })
		if j < 0 {
			c.rows = append(c.rows, fmt.Sprintf("| %s | %s | new in this run | | | | | | |", s.Name, k.Class))
			continue
		}
		c.class(s.Name, p.Classes[j], k, tol, floor)
	}
}

// class adds the row of class k of scenario name (q the previous), and its
// regressions, or says it is not judged.
func (c *comparison) class(name string, q, k classFigures, tol float64, floor time.Duration) {
	c.rows = append(c.rows, fmt.Sprintf("| %s | %s | %s | %d/%d/%d/%d → %d/%d/%d/%d | %s | %s | %s | %s | %s |",
		name, k.Class, change(float64(q.Requests), float64(k.Requests), ""),
		q.Status2xx, q.Status4xx, q.Status5xx, q.NoAnswer, k.Status2xx, k.Status4xx, k.Status5xx, k.NoAnswer,
		change(q.P50Ms, k.P50Ms, " ms"), change(q.P95Ms, k.P95Ms, " ms"), change(q.P99Ms, k.P99Ms, " ms"),
		change(q.SlowestMs, k.SlowestMs, " ms"), change(q.RefusalShare*100, k.RefusalShare*100, " %")))
	if q.OK < minCompared || k.OK < minCompared {
		c.unjudged = append(c.unjudged, fmt.Sprintf("%s, class %s: %d → %d 2xx answers, fewer than %d in a run", name, k.Class, q.OK, k.OK, minCompared))
		return
	}
	for _, fig := range []struct {
		name      string
		prev, now float64
	}{{"p95", q.P95Ms, k.P95Ms}, {"p99", q.P99Ms, k.P99Ms}} {
		if regressed(fig.prev, fig.now, tol, floor) {
			c.regressions = append(c.regressions, fmt.Sprintf("%s, class %s: %s %s, more than %.0f %% and %s above the previous run",
				name, k.Class, fig.name, change(fig.prev, fig.now, " ms"), tol*100, floor))
		}
	}
}

// regressed: now is more than tol (a fraction) and more than floor above
// prev, both in milliseconds.
func regressed(prev, now, tol float64, floor time.Duration) bool {
	return now > prev*(1+tol) && now-prev > ms(floor)
}

// render is the report's comparison section.
func (c comparison) render(tol float64, floor time.Duration) string {
	var b strings.Builder
	b.WriteString("\n## Comparison with the previous main run\n\n")
	switch c.state {
	case notAsked, absent, otherShape:
		fmt.Fprintf(&b, "Not compared: %s. This run is judged on the absolute budgets alone.\n", c.reason)
		return b.String()
	case unreadable:
		fmt.Fprintf(&b, "**FAIL**: the previous figures are unreadable: %s. An unreadable file is never read as \"no change\".\n", c.reason)
		return b.String()
	}
	fmt.Fprintf(&b, "Against %s. A p95 or p99 of a class's 2xx answers more than %.0f %% and %s above the previous run fails it, when both runs have at least %d such answers. Each cell is the previous value, this run's and the change; statuses are 2xx/4xx/5xx/no answer.\n\n",
		c.reason, tol*100, floor, minCompared)
	b.WriteString("| Scenario | Class | Requests | Statuses | 2xx p50 | 2xx p95 | 2xx p99 | Slowest | Refused |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|---|\n")
	for _, r := range c.rows {
		b.WriteString(r + "\n")
	}
	if len(c.unjudged) > 0 {
		b.WriteString("\nNot judged, too few 2xx answers for a p95 or p99 to compare:\n\n")
		for _, u := range c.unjudged {
			fmt.Fprintf(&b, "- %s\n", u)
		}
	}
	if len(c.regressions) == 0 {
		b.WriteString("\nNo regression beyond the tolerance.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "\n%d regression(s) beyond the tolerance:\n\n", len(c.regressions))
	for _, r := range c.regressions {
		fmt.Fprintf(&b, "- **FAIL**: %s\n", r)
	}
	return b.String()
}
