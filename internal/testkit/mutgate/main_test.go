package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// files writes name -> content into a fresh directory and returns its path.
func files(t *testing.T, m map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, content := range m {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func mutation(typ, status string, line, col int) string {
	return `{"type":"` + typ + `","status":"` + status + `","line":` + strconv.Itoa(line) + `,"column":` + strconv.Itoa(col) + `}`
}

func gremlins(file string, ms ...string) string {
	return `{"go_module":"m","files":[{"file_name":"` + file + `","mutations":[` + strings.Join(ms, ",") + `]}]}`
}

const scope = "# root\tfiles\treason\nd\t*\tthe domain\nh\tauth.go\tthe key check\n"

// fourOfFive: in d/a.go three mutants killed, one timed out and one lived
// at line 9; one not viable, which is not scored.
var fourOfFive = gremlins("a.go",
	mutation("CONDITIONALS_NEGATION", "KILLED", 3, 2),
	mutation("CONDITIONALS_BOUNDARY", "KILLED", 4, 2),
	mutation("INVERT_LOGICAL", "KILLED", 5, 2),
	mutation("ARITHMETIC_BASE", "TIMED OUT", 6, 2),
	mutation("CONDITIONALS_NEGATION", "LIVED", 9, 5),
	mutation("INVERT_NEGATIVES", "NOT VIABLE", 10, 1),
)

func base() map[string]string {
	return map[string]string{
		"scope.tsv": scope,
		"eq.tsv":    "# location\tmutator\treason\treviewer\n",
		"d.json":    fourOfFive,
		"h.json":    gremlins("auth.go", mutation("CONDITIONALS_NEGATION", "KILLED", 7, 1)),
		"chg.tsv":   "d/a.go\t1\t20\n",
	}
}

func with(over map[string]string) map[string]string {
	m := base()
	for k, v := range over {
		m[k] = v
	}
	return m
}

func runGate(dir string, args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	full := []string{"-scope", filepath.Join(dir, "scope.tsv"), "-equivalent", filepath.Join(dir, "eq.tsv")}
	for _, a := range args {
		if root, file, ok := strings.Cut(a, "="); ok && strings.HasSuffix(file, ".json") {
			a = root + "=" + filepath.Join(dir, file)
		}
		if strings.HasSuffix(a, ".tsv") || strings.HasSuffix(a, ".md") {
			a = filepath.Join(dir, a)
		}
		full = append(full, a)
	}
	code := run(full, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestScoreCountsDetectedOverGeneratedMinusEquivalent(t *testing.T) {
	req.Covers(t, "TST-002")
	dir := files(t, base())
	code, out, errs := runGate(dir, "-mode", "full", "-min", "60", "-report", "r.md", "d=d.json", "h=h.json")
	if code != 0 {
		t.Fatalf("exit %d: %s%s", code, out, errs)
	}
	want := "generated 6, killed 4, survived 1, timed out 1, equivalent 0, not covered 0, not viable 1; score 66.7% (min 60%)"
	if !strings.Contains(out, want) {
		t.Fatalf("summary %q, want %q", out, want)
	}
	if !strings.Contains(out, "not detected d/a.go:9:5 CONDITIONALS_NEGATION (LIVED)") {
		t.Fatalf("the survivor is not named: %s", out)
	}
	rep, err := os.ReadFile(filepath.Join(dir, "r.md"))
	if err != nil || !strings.Contains(string(rep), "| d/a.go:9:5 | CONDITIONALS_NEGATION | LIVED |") || !strings.Contains(string(rep), "full mode") {
		t.Fatalf("report %q %v", rep, err)
	}
	if !strings.Contains(out, "not detected d/a.go:6:2 ARITHMETIC_BASE (TIMED OUT)") {
		t.Fatalf("a timeout is not listed: %s", out)
	}
	// The same input under the full-run bar fails: a timeout never counts.
	if code, _, errs := runGate(dir, "-mode", "full", "-min", "70", "d=d.json", "h=h.json"); code != 1 || !strings.Contains(errs, "below 70%") {
		t.Fatalf("66.7%% passed a 70%% bar: %d %s", code, errs)
	}
}

func TestPRModeCountsOnlyChangedLinesInScope(t *testing.T) {
	req.Covers(t, "TST-002")
	dir := files(t, with(map[string]string{
		"chg.tsv": "d/a.go\t3\t4\nd/a.go\t9\t9\nd/a_test.go\t1\t5\nh/other.go\t*\n",
		"h.json":  `{"files":[{"file_name":"other.go","mutations":[` + mutation("CONDITIONALS_NEGATION", "LIVED", 1, 1) + `]}]}`,
	}))
	code, out, _ := runGate(dir, "-mode", "pr", "-min", "80", "-changed", "chg.tsv", "d=d.json", "h=h.json")
	// Lines 3, 4 and 9 of d/a.go: two killed, one lived; h/other.go is out of scope.
	if code != 1 || !strings.Contains(out, "generated 3, killed 2, survived 1") {
		t.Fatalf("exit %d: %s", code, out)
	}
	whole := files(t, with(map[string]string{"chg.tsv": "d/a.go\t*\n"}))
	if code, out, _ := runGate(whole, "-mode", "pr", "-min", "80", "-changed", "chg.tsv", "d=d.json"); code != 1 || !strings.Contains(out, "generated 5,") {
		t.Fatalf("a whole changed file: %d %s", code, out)
	}
}

func TestPlanNamesTheTouchedRoots(t *testing.T) {
	dir := files(t, with(map[string]string{"chg.tsv": "d/sub/a.go\t1\t2\nh/server.go\t*\nx/y.go\t*\n"}))
	code, out, errs := runGate(dir, "-mode", "pr", "-min", "80", "-changed", "chg.tsv", "-plan")
	if code != 0 || out != "d\n" {
		t.Fatalf("exit %d %q %s", code, out, errs)
	}
	both := files(t, with(map[string]string{"chg.tsv": "d/a.go\t1\t2\nh/auth.go\t3\t3\n"}))
	if _, out, _ := runGate(both, "-mode", "pr", "-min", "80", "-changed", "chg.tsv", "-plan"); out != "d\nh\n" {
		t.Fatalf("%q", out)
	}
	none := files(t, with(map[string]string{"chg.tsv": "x/y.go\t*\n"}))
	if code, out, _ := runGate(none, "-mode", "pr", "-min", "80", "-changed", "chg.tsv", "-plan"); code != 0 || out != "" {
		t.Fatalf("an out-of-scope change: %d %q", code, out)
	}
	if code, _, errs := runGate(dir, "-mode", "full", "-min", "70", "-plan"); code != 2 || !strings.Contains(errs, "-plan needs pr mode") {
		t.Fatalf("-plan in full mode: %d %s", code, errs)
	}
	if code, _, _ := runGate(dir, "-mode", "pr", "-min", "80", "-changed", "chg.tsv", "-plan", "d=d.json"); code != 2 {
		t.Fatalf("-plan with a report: %d", code)
	}
}

func TestABaselineCatchesAWeakenedTest(t *testing.T) {
	req.Covers(t, "TST-002", "TST-013")
	// The base killed every mutant of d/a.go; the change touched only the
	// package's tests and the one at line 3 lives now.
	head := gremlins("a.go",
		mutation("CONDITIONALS_NEGATION", "LIVED", 3, 2),
		mutation("CONDITIONALS_BOUNDARY", "KILLED", 4, 2),
		mutation("INVERT_LOGICAL", "KILLED", 5, 2),
		mutation("ARITHMETIC_BASE", "KILLED", 6, 2),
		mutation("INVERT_NEGATIVES", "KILLED", 7, 2),
		mutation("INCREMENT_DECREMENT", "KILLED", 8, 2),
	)
	base := strings.ReplaceAll(head, "LIVED", "KILLED")
	dir := files(t, with(map[string]string{"head.json": head, "base.json": base, "chg.tsv": "d/a.go\t*\n"}))
	code, out, errs := runGate(dir, "-mode", "pr", "-min", "80", "-changed", "chg.tsv", "-baseline", "d=base.json", "d=head.json")
	if code != 1 || !strings.Contains(out, "score 83.3%") || !strings.Contains(errs, "weakened a test: d/a.go:3:2 CONDITIONALS_NEGATION was killed at the base and is lived now") {
		t.Fatalf("a weakened test passed: %d %s %s", code, out, errs)
	}
	// The same survivor on a changed line is the score's business, not a
	// weakening: the line's code moved, so the base says nothing about it.
	lines := files(t, with(map[string]string{"head.json": head, "base.json": base, "chg.tsv": "d/a.go\t1\t20\n"}))
	if code, out, errs := runGate(lines, "-mode", "pr", "-min", "80", "-changed", "chg.tsv", "-baseline", "d=base.json", "d=head.json"); code != 0 {
		t.Fatalf("a changed line compared with the base: %d %s %s", code, out, errs)
	}
	// Without the baseline the same run is green: the base is what catches it.
	if code, _, _ := runGate(dir, "-mode", "pr", "-min", "80", "-changed", "chg.tsv", "d=head.json"); code != 0 {
		t.Fatalf("without a baseline: %d", code)
	}
	for name, args := range map[string][]string{
		"full mode":   {"-mode", "full", "-min", "70", "-baseline", "d=base.json", "d=head.json", "h=h.json"},
		"with -plan":  {"-mode", "pr", "-min", "80", "-changed", "chg.tsv", "-plan", "-baseline", "d=base.json"},
		"bad element": {"-mode", "pr", "-min", "80", "-changed", "chg.tsv", "-baseline", "nope", "d=head.json"},
	} {
		if code, _, _ := runGate(dir, args...); code != 2 {
			t.Errorf("%s: exit %d", name, code)
		}
	}
}

func TestDuplicatesCountOnce(t *testing.T) {
	dir := files(t, base())
	_, out, _ := runGate(dir, "-mode", "full", "-min", "70", "d=d.json", "d=d.json", "h=h.json")
	if !strings.Contains(out, "generated 6,") {
		t.Fatalf("a report read twice counted twice: %s", out)
	}
}

func TestZeroMutantsForChangedCodeFails(t *testing.T) {
	req.Covers(t, "TST-002")
	dir := files(t, with(map[string]string{"chg.tsv": "d/a.go\t50\t60\n"}))
	code, _, errs := runGate(dir, "-mode", "pr", "-min", "80", "-changed", "chg.tsv", "d=d.json")
	if code != 1 || !strings.Contains(errs, "zero mutants") {
		t.Fatalf("zero mutants passed: %d %s", code, errs)
	}
	empty := files(t, with(map[string]string{"chg.tsv": "\n"}))
	if code, _, errs := runGate(empty, "-mode", "pr", "-min", "80", "-changed", "chg.tsv", "d=d.json"); code != 1 || !strings.Contains(errs, "empty change list") {
		t.Fatalf("an empty change list passed: %d %s", code, errs)
	}
}

func TestEquivalents(t *testing.T) {
	req.Covers(t, "TST-002")
	ok := "d/a.go:9:5\tCONDITIONALS_NEGATION\tthe branch only reorders equal values\treviewer-a\n"
	dir := files(t, with(map[string]string{"eq.tsv": ok}))
	code, out, _ := runGate(dir, "-mode", "full", "-min", "80", "d=d.json", "h=h.json")
	if code != 0 || !strings.Contains(out, "equivalent 1") || strings.Contains(out, "(LIVED)") {
		t.Fatalf("an equivalent survivor: %d %s", code, out)
	}
	for name, c := range map[string]struct{ eq, want string }{
		"no reason":       {"d/a.go:9:5\tCONDITIONALS_NEGATION\t \treviewer-a\n", "needs a reason and a reviewer"},
		"no reviewer":     {"d/a.go:9:5\tCONDITIONALS_NEGATION\tsame\t\n", "needs a reason and a reviewer"},
		"unknown mutator": {"d/a.go:9:5\tNOPE\tsame\tr\n", "unknown mutator"},
		"bad location":    {"d/a.go:9\tCONDITIONALS_NEGATION\tsame\tr\n", "not file:line:column"},
		"fields":          {"d/a.go:9:5\tCONDITIONALS_NEGATION\tsame\n", "want 4 tab-separated fields"},
	} {
		dir := files(t, with(map[string]string{"eq.tsv": c.eq}))
		if code, _, errs := runGate(dir, "-mode", "full", "-min", "70", "d=d.json", "h=h.json"); code != 2 || !strings.Contains(errs, c.want) {
			t.Errorf("%s: exit %d %s", name, code, errs)
		}
	}
	for name, c := range map[string]struct {
		eq, mode, want string
		code           int
	}{
		"names a killed mutant": {"d/a.go:3:2\tCONDITIONALS_NEGATION\tsame\tr\n", "full", "names a mutant the tests kill", 1},
		"stale in full mode":    {"d/a.go:99:1\tCONDITIONALS_NEGATION\tsame\tr\n", "full", "matches no mutant (stale)", 1},
		"stale in pr mode":      {"d/a.go:99:1\tCONDITIONALS_NEGATION\tsame\tr\n", "pr", "", 0},
	} {
		dir := files(t, with(map[string]string{"eq.tsv": c.eq, "d.json": gremlins("a.go", mutation("CONDITIONALS_NEGATION", "KILLED", 3, 2))}))
		args := []string{"-mode", c.mode, "-min", "70", "d=d.json", "h=h.json"}
		if c.mode == "pr" {
			args = []string{"-mode", c.mode, "-min", "70", "-changed", "chg.tsv", "d=d.json"}
		}
		code, _, errs := runGate(dir, args...)
		if code != c.code || !strings.Contains(errs, c.want) {
			t.Errorf("%s: exit %d %s", name, code, errs)
		}
	}
}

func TestNotCoveredIsASurvivor(t *testing.T) {
	dir := files(t, with(map[string]string{"d.json": gremlins("a.go", mutation("CONDITIONALS_NEGATION", "NOT COVERED", 3, 2))}))
	code, out, _ := runGate(dir, "-mode", "full", "-min", "70", "d=d.json", "h=h.json")
	if code != 1 || !strings.Contains(out, "not covered 1") || !strings.Contains(out, "(NOT COVERED)") {
		t.Fatalf("exit %d: %s", code, out)
	}
}

func TestInScope(t *testing.T) {
	roots := []root{{dir: "d"}, {dir: "h", files: []string{"auth.go"}}}
	for file, want := range map[string]bool{
		"d/a.go": true, "d/sub/b.go": true, "d/a_test.go": false, "d/notes.txt": false,
		"h/auth.go": true, "h/server.go": false, "dx/a.go": false,
	} {
		if inScope(roots, file) != want {
			t.Errorf("%s: want %v", file, want)
		}
	}
}

func TestRefusals(t *testing.T) {
	dir := files(t, with(map[string]string{
		"bad.json":      "{",
		"nofiles.json":  `{"go_module":"m"}`,
		"status.json":   gremlins("a.go", mutation("CONDITIONALS_NEGATION", "ODD", 1, 1)),
		"empty.tsv":     "# nothing\n",
		"badscope.tsv":  "d/../x\t*\treason\n",
		"noreason.tsv":  "d\t*\t \n",
		"badchg.tsv":    "d/a.go\t1\n",
		"badrange.tsv":  "d/a.go\t5\t2\n",
		"zerorange.tsv": "d/a.go\t0\t2\n",
		"nonnum.tsv":    "d/a.go\tx\t2\n",
	}))
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"no mode":         {[]string{"-min", "70", "d=d.json"}, "-mode must be pr or full"},
		"no min":          {[]string{"-mode", "full", "d=d.json"}, "-min must be"},
		"min above 100":   {[]string{"-mode", "full", "-min", "101", "d=d.json"}, "-min must be"},
		"no report":       {[]string{"-mode", "full", "-min", "70"}, "at least one ROOT=REPORT"},
		"pr no changed":   {[]string{"-mode", "pr", "-min", "80", "d=d.json"}, "-changed is required"},
		"full changed":    {[]string{"-mode", "full", "-min", "70", "-changed", "chg.tsv", "d=d.json"}, "refused in full mode"},
		"not root=report": {[]string{"-mode", "full", "-min", "70", "d.json"}, "is not ROOT=REPORT"},
		"unknown root":    {[]string{"-mode", "full", "-min", "70", "x=d.json"}, "is not a root"},
		"missing root":    {[]string{"-mode", "full", "-min", "70", "d=d.json"}, "no report for the scope root h"},
		"bad json":        {[]string{"-mode", "full", "-min", "70", "d=bad.json"}, "bad.json"},
		"no files member": {[]string{"-mode", "full", "-min", "70", "d=nofiles.json"}, "not a Gremlins report"},
		"unknown status":  {[]string{"-mode", "full", "-min", "70", "d=status.json"}, "unknown status"},
		"missing file":    {[]string{"-mode", "full", "-min", "70", "d=absent.json"}, "absent.json"},
		"bad changed":     {[]string{"-mode", "pr", "-min", "80", "-changed", "badchg.tsv", "d=d.json"}, "is not FILE FROM TO"},
		"bad range":       {[]string{"-mode", "pr", "-min", "80", "-changed", "badrange.tsv", "d=d.json"}, "no valid range"},
		"zero range":      {[]string{"-mode", "pr", "-min", "80", "-changed", "zerorange.tsv", "d=d.json"}, "no valid range"},
		"non-numeric":     {[]string{"-mode", "pr", "-min", "80", "-changed", "nonnum.tsv", "d=d.json"}, "no valid range"},
		"absent changed":  {[]string{"-mode", "pr", "-min", "80", "-changed", "absent.tsv", "d=d.json"}, "absent.tsv"},
		"unknown flag":    {[]string{"-nope"}, "-nope"},
	} {
		code, _, errs := runGate(dir, c.args...)
		if code != 2 || !strings.Contains(errs, c.want) {
			t.Errorf("%s: exit %d %q", name, code, errs)
		}
	}
	for name, c := range map[string]struct{ scope, eq, want string }{
		"empty scope":    {"empty.tsv", "eq.tsv", "no scope root"},
		"unclean root":   {"badscope.tsv", "eq.tsv", "needs a clean root"},
		"no reason":      {"noreason.tsv", "eq.tsv", "needs a clean root"},
		"absent scope":   {"absent.tsv", "eq.tsv", "absent.tsv"},
		"absent eq file": {"scope.tsv", "absent.tsv", "absent.tsv"},
	} {
		var stdout, stderr bytes.Buffer
		code := run([]string{"-mode", "full", "-min", "70", "-scope", filepath.Join(dir, c.scope), "-equivalent", filepath.Join(dir, c.eq), "d=" + filepath.Join(dir, "d.json")}, &stdout, &stderr)
		if code != 2 || !strings.Contains(stderr.String(), c.want) {
			t.Errorf("%s: exit %d %q", name, code, stderr.String())
		}
	}
	if code, _, errs := runGate(dir, "-mode", "full", "-min", "70", "-report", filepath.Join(dir, "no", "dir", "r.md"), "d=d.json", "h=h.json"); code != 2 || !strings.Contains(errs, "r.md") {
		t.Errorf("an unwritable report: %d %s", code, errs)
	}
}
