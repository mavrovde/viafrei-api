// Command mutgate scores Gremlins mutation reports against the mutation
// targets of TST-002 and fails below them.
//
//	go run ./internal/testkit/mutgate -mode pr -min 80 \
//	    -scope test/mutation/scope.tsv -equivalent test/mutation/equivalent.tsv \
//	    -changed changed.tsv -report mutation-report.md \
//	    internal/domain=domain.json internal/app/auth=auth.json
//
// Each argument is a scope root and the Gremlins JSON report of a run over
// it (gremlins unleash -o). A mutant counts only when its file is in scope
// (test/mutation/scope.tsv). In pr mode it also counts only on a changed
// line: -changed lists, one per line, a module-relative file and the first
// and last changed line, or "*" for the whole file (a package whose tests
// changed is mutated whole). In full mode every in-scope mutant counts.
//
// Score = killed ÷ (generated − equivalent) (Testing Strategy, section 5),
// where generated is every mutant that compiled (not viable ones are
// reported, not scored). A mutant that lived, was not covered or timed out
// is listed as not detected: a timeout is more often a starved runner than
// a hang, so it never raises the score. One that lived or was not covered
// is not counted against the score when the equivalents file lists it.
//
// The equivalents file lists, tab-separated, a location (file:line:column),
// the mutator, the reason and the reviewer. An entry without a reason or a
// reviewer, with an unknown mutator, or matching a mutant the tests killed
// fails the gate; in full mode so does an entry that matches no mutant
// (stale), so the file can only list what it names on purpose.
//
// In pr mode, -baseline ROOT=REPORT names a run at the base. A file under
// test only because its package's tests changed has the same mutants at
// both ends, and one the base killed that the change does not is a finding
// whatever the score: deleting an assertion turns the run red.
//
// With -plan (pr mode) it reads no report and prints the scope roots that
// the change list touches, so the caller mutates those and nothing else.
//
// Exit 0 when the score reaches -min, 1 below it, on zero scored mutants
// for changed in-scope code, or on a bad equivalent entry, 2 on a usage
// error or an unreadable input.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

const prog = "mutgate"

// Gremlins statuses.
const (
	killed     = "KILLED"
	lived      = "LIVED"
	notCovered = "NOT COVERED"
	timedOut   = "TIMED OUT"
	notViable  = "NOT VIABLE"
)

// mutators are the Gremlins v0.6 mutator names an equivalent entry may name.
var mutators = []string{
	"ARITHMETIC_BASE", "CONDITIONALS_BOUNDARY", "CONDITIONALS_NEGATION",
	"INCREMENT_DECREMENT", "INVERT_ASSIGNMENTS", "INVERT_BITWISE",
	"INVERT_BWASSIGN", "INVERT_LOGICAL", "INVERT_LOOPCTRL", "INVERT_NEGATIVES",
	"REMOVE_SELF_ASSIGNMENTS",
}

type report struct {
	Files *[]struct {
		Name      string `json:"file_name"`
		Mutations []struct {
			Type   string `json:"type"`
			Status string `json:"status"`
			Line   int    `json:"line"`
			Column int    `json:"column"`
		} `json:"mutations"`
	} `json:"files"`
}

type mutant struct {
	file, mutator, status string
	line, col             int
}

func (m mutant) location() string { return fmt.Sprintf("%s:%d:%d", m.file, m.line, m.col) }

type root struct {
	dir   string
	files []string // nil: every file under dir
}

type span struct{ from, to int }

type equivalent struct {
	location, mutator, reason, reviewer string
	used                                bool
}

type tally struct {
	generated, killed, survived, timedOut, equivalent, notCovered, notViable int
	survivors                                                                []mutant
}

func usageErr(stderr io.Writer, format string, a ...any) int {
	fmt.Fprintf(stderr, prog+": "+format+"\n", a...)
	return 2
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(prog, flag.ContinueOnError)
	fs.SetOutput(stderr)
	mode := fs.String("mode", "", "pr (changed lines) or full (every in-scope mutant)")
	minPct := fs.Float64("min", 0, "minimum score, in percent")
	scopePath := fs.String("scope", "", "scope file (required)")
	eqPath := fs.String("equivalent", "", "equivalents file (required; it may list nothing)")
	changedPath := fs.String("changed", "", "changed lines (pr mode, required there)")
	reportPath := fs.String("report", "", "write a Markdown report here")
	plan := fs.Bool("plan", false, "pr mode: print the scope roots the change list touches, one per line, and exit")
	var baseline []string
	fs.Func("baseline", "pr mode: ROOT=REPORT of a run at the base (repeatable)", func(v string) error {
		baseline = append(baseline, v)
		return nil
	})
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *mode != "pr" && *mode != "full" {
		return usageErr(stderr, "-mode must be pr or full")
	}
	if *minPct <= 0 || *minPct > 100 {
		return usageErr(stderr, "-min must be above 0 and at most 100")
	}
	if *scopePath == "" || *eqPath == "" || (fs.NArg() == 0) != *plan {
		return usageErr(stderr, "-scope FILE, -equivalent FILE and at least one ROOT=REPORT are required (none with -plan)")
	}
	if (*mode == "pr") != (*changedPath != "") {
		return usageErr(stderr, "-changed is required in pr mode and refused in full mode")
	}
	if len(baseline) > 0 && (*mode != "pr" || *plan) {
		return usageErr(stderr, "-baseline needs pr mode and a report")
	}
	roots, err := readScope(*scopePath)
	if err != nil {
		return usageErr(stderr, "%v", err)
	}
	eqs, err := readEquivalents(*eqPath)
	if err != nil {
		return usageErr(stderr, "%v", err)
	}
	var changed map[string][]span
	if *changedPath != "" {
		if changed, err = readChanged(*changedPath); err != nil {
			return usageErr(stderr, "%v", err)
		}
	}
	if *plan {
		if *mode != "pr" {
			return usageErr(stderr, "-plan needs pr mode")
		}
		for _, r := range roots {
			for file := range changed {
				if inScope([]root{r}, file) {
					fmt.Fprintln(stdout, r.dir)
					break
				}
			}
		}
		return 0
	}
	mutants, seen, err := readReports(fs.Args(), roots, *scopePath)
	if err != nil {
		return usageErr(stderr, "%v", err)
	}
	before, _, err := readReports(baseline, roots, *scopePath)
	if err != nil {
		return usageErr(stderr, "%v", err)
	}
	if *mode == "full" {
		for _, r := range roots {
			if !seen[r.dir] {
				return usageErr(stderr, "full mode: no report for the scope root %s", r.dir)
			}
		}
	}
	counted := mutants[:0]
	dup := map[string]bool{}
	for _, m := range mutants {
		key := m.location() + " " + m.mutator
		if dup[key] || !inScope(roots, m.file) || (changed != nil && !onChangedLine(changed, m)) {
			continue
		}
		dup[key] = true
		counted = append(counted, m)
	}
	t, findings := score(counted, eqs, *mode == "full")
	findings = append(findings, weakened(counted, before, changed)...)
	return verdict(t, findings, *mode, *minPct, changed, *reportPath, stdout, stderr)
}

func verdict(t tally, findings []string, mode string, minPct float64, changed map[string][]span, reportPath string, stdout, stderr io.Writer) int {
	scored := t.generated - t.equivalent
	pct := 100.0
	if scored > 0 {
		pct = 100 * float64(t.killed) / float64(scored)
	}
	summary := fmt.Sprintf("generated %d, killed %d, survived %d, timed out %d, equivalent %d, not covered %d, not viable %d",
		t.generated, t.killed, t.survived, t.timedOut, t.equivalent, t.notCovered, t.notViable)
	switch {
	case mode == "pr" && len(changed) == 0:
		findings = append(findings, "pr mode with an empty change list: the caller must not score nothing")
	case t.generated == 0:
		findings = append(findings, "zero mutants for in-scope code: a run that mutated nothing is not a pass")
	case pct < minPct:
		findings = append(findings, fmt.Sprintf("score %.1f%% is below %.0f%%", pct, minPct))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Mutation testing, %s mode\n\n%s.\n\nScore %.1f%% (killed ÷ (generated − equivalent)), minimum %.0f%%.\n", mode, summary, pct, minPct)
	if len(t.survivors) > 0 {
		b.WriteString("\n| Not detected | Mutator | Status |\n|---|---|---|\n")
		for _, m := range t.survivors {
			fmt.Fprintf(&b, "| %s | %s | %s |\n", m.location(), m.mutator, m.status)
		}
	}
	for _, f := range findings {
		fmt.Fprintf(&b, "\nFAIL: %s\n", f)
	}
	if reportPath != "" {
		if err := os.WriteFile(reportPath, []byte(b.String()), 0o600); err != nil {
			return usageErr(stderr, "%v", err)
		}
	}
	fmt.Fprintf(stdout, "%s: %s; score %.1f%% (min %.0f%%)\n", prog, summary, pct, minPct)
	for _, m := range t.survivors {
		fmt.Fprintf(stdout, "%s: not detected %s %s (%s)\n", prog, m.location(), m.mutator, m.status)
	}
	for _, f := range findings {
		fmt.Fprintf(stderr, "%s: FAIL: %s\n", prog, f)
	}
	if len(findings) > 0 {
		return 1
	}
	return 0
}

// readReports reads ROOT=REPORT arguments.
func readReports(args []string, roots []root, scopePath string) ([]mutant, map[string]bool, error) {
	seen := map[string]bool{}
	var out []mutant
	for _, a := range args {
		dir, file, ok := strings.Cut(a, "=")
		if !ok || dir == "" || file == "" {
			return nil, nil, fmt.Errorf("argument %q is not ROOT=REPORT", a)
		}
		if !slices.ContainsFunc(roots, func(r root) bool { return r.dir == dir }) {
			return nil, nil, fmt.Errorf("%s is not a root of %s", dir, scopePath)
		}
		seen[dir] = true
		ms, err := readReport(dir, file)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, ms...)
	}
	return out, seen, nil
}

// weakened finds what a test change lost: a mutant of a file whose code did
// not change (it is under test only because its package's tests did) that
// the base killed and the change no longer does. Its location is the same
// at both ends because the file did not change.
func weakened(counted, before []mutant, changed map[string][]span) []string {
	killedBefore := map[string]bool{}
	for _, m := range before {
		if m.status == killed {
			killedBefore[m.location()+" "+m.mutator] = true
		}
	}
	var out []string
	for _, m := range counted {
		if m.status != killed && killedBefore[m.location()+" "+m.mutator] && !slices.ContainsFunc(changed[m.file], func(s span) bool { return s.from > 0 }) {
			out = append(out, fmt.Sprintf("the change weakened a test: %s %s was killed at the base and is %s now", m.location(), m.mutator, strings.ToLower(m.status)))
		}
	}
	return out
}

func score(ms []mutant, eqs []*equivalent, full bool) (tally, []string) {
	var t tally
	var findings []string
	for _, m := range ms {
		if m.status == notViable {
			t.notViable++
			continue
		}
		t.generated++
		var eq *equivalent
		for _, e := range eqs {
			if e.location == m.location() && e.mutator == m.mutator {
				eq = e
			}
		}
		if eq != nil {
			eq.used = true
			if m.status == killed {
				findings = append(findings, fmt.Sprintf("equivalent entry %s %s names a mutant the tests kill; remove it", eq.location, eq.mutator))
			}
		}
		switch {
		case m.status == killed:
			t.killed++
		case m.status == timedOut:
			t.timedOut++
			t.survivors = append(t.survivors, m)
		case eq != nil:
			t.equivalent++
		default:
			if m.status == notCovered {
				t.notCovered++
			} else {
				t.survived++
			}
			t.survivors = append(t.survivors, m)
		}
	}
	for _, e := range eqs {
		if full && !e.used {
			findings = append(findings, fmt.Sprintf("equivalent entry %s %s matches no mutant (stale); remove it", e.location, e.mutator))
		}
	}
	return t, findings
}

func inScope(roots []root, file string) bool {
	if strings.HasSuffix(file, "_test.go") || !strings.HasSuffix(file, ".go") {
		return false
	}
	for _, r := range roots {
		rel, ok := strings.CutPrefix(file, r.dir+"/")
		if ok && (r.files == nil || slices.Contains(r.files, rel)) {
			return true
		}
	}
	return false
}

func onChangedLine(changed map[string][]span, m mutant) bool {
	for _, s := range changed[m.file] {
		if s.from == 0 || (m.line >= s.from && m.line <= s.to) {
			return true
		}
	}
	return false
}

// lines returns the non-comment, non-blank lines of a tab-separated file,
// each split into exactly n fields.
func lines(name string, n int) ([][]string, error) {
	f, err := os.Open(name) // #nosec G304 -- a path the caller names
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out [][]string
	sc := bufio.NewScanner(f)
	for no := 1; sc.Scan(); no++ {
		l := sc.Text()
		if strings.TrimSpace(l) == "" || strings.HasPrefix(l, "#") {
			continue
		}
		fields := strings.Split(l, "\t")
		if len(fields) != n {
			return nil, fmt.Errorf("%s:%d: want %d tab-separated fields, got %d", name, no, n, len(fields))
		}
		out = append(out, fields)
	}
	return out, sc.Err()
}

// readScope reads ROOT, FILES ("*" or a comma-separated list of paths under
// ROOT) and REASON per line.
func readScope(name string) ([]root, error) {
	rows, err := lines(name, 3)
	if err != nil {
		return nil, err
	}
	var out []root
	for _, r := range rows {
		if r[0] == "" || path.Clean(r[0]) != r[0] || r[1] == "" || strings.TrimSpace(r[2]) == "" {
			return nil, fmt.Errorf("%s: scope line %q needs a clean root, files and a reason", name, strings.Join(r, "\t"))
		}
		rt := root{dir: r[0]}
		if r[1] != "*" {
			rt.files = strings.Split(r[1], ",")
		}
		out = append(out, rt)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("%s: no scope root; a gate over nothing proves nothing", name)
	}
	return out, nil
}

// readEquivalents reads LOCATION, MUTATOR, REASON and REVIEWER per line.
func readEquivalents(name string) ([]*equivalent, error) {
	rows, err := lines(name, 4)
	if err != nil {
		return nil, err
	}
	var out []*equivalent
	for _, r := range rows {
		e := &equivalent{location: r[0], mutator: r[1], reason: strings.TrimSpace(r[2]), reviewer: strings.TrimSpace(r[3])}
		if e.reason == "" || e.reviewer == "" {
			return nil, fmt.Errorf("%s: equivalent entry %s %s needs a reason and a reviewer", name, e.location, e.mutator)
		}
		if !slices.Contains(mutators, e.mutator) {
			return nil, fmt.Errorf("%s: equivalent entry %s names an unknown mutator %q", name, e.location, e.mutator)
		}
		if strings.Count(e.location, ":") != 2 {
			return nil, fmt.Errorf("%s: equivalent entry location %q is not file:line:column", name, e.location)
		}
		out = append(out, e)
	}
	return out, nil
}

// readChanged reads FILE, FROM and TO per line, or FILE and "*".
func readChanged(name string) (map[string][]span, error) {
	f, err := os.ReadFile(name) // #nosec G304 -- a path the caller names
	if err != nil {
		return nil, err
	}
	out := map[string][]span{}
	for l := range strings.SplitSeq(strings.TrimSpace(string(f)), "\n") {
		if l == "" {
			continue
		}
		fields := strings.Split(l, "\t")
		if len(fields) == 2 && fields[1] == "*" {
			out[fields[0]] = append(out[fields[0]], span{})
			continue
		}
		if len(fields) != 3 {
			return nil, fmt.Errorf("%s: changed line %q is not FILE FROM TO or FILE *", name, l)
		}
		from, err1 := strconv.Atoi(fields[1])
		to, err2 := strconv.Atoi(fields[2])
		if err := errors.Join(err1, err2); err != nil || from < 1 || to < from {
			return nil, fmt.Errorf("%s: changed line %q has no valid range", name, l)
		}
		out[fields[0]] = append(out[fields[0]], span{from, to})
	}
	return out, nil
}

func readReport(dir, name string) ([]mutant, error) {
	b, err := os.ReadFile(name) // #nosec G304 -- a path the caller names
	if err != nil {
		return nil, err
	}
	var r report
	if err := json.Unmarshal(b, &r); err != nil {
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	if r.Files == nil {
		return nil, fmt.Errorf("%s: no files member; not a Gremlins report", name)
	}
	var out []mutant
	for _, f := range *r.Files {
		for _, m := range f.Mutations {
			switch m.Status {
			case killed, lived, notCovered, timedOut, notViable:
			default:
				return nil, fmt.Errorf("%s: unknown status %q", name, m.Status)
			}
			out = append(out, mutant{file: dir + "/" + f.Name, mutator: m.Type, status: m.Status, line: m.Line, col: m.Column})
		}
	}
	return out, nil
}
