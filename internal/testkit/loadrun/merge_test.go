package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// writeShard writes a shard's report and its figures (scenarios of class
// near, 100 answers each, p95 and p99 as given) under dir/name.
func writeShard(t *testing.T, dir, name, prof string, p95 float64, scenarios ...string) string {
	t.Helper()
	f := figures{Version: figuresVersion, Profile: prof, Scenarios: []scenarioFigures{}}
	for _, s := range scenarios {
		f.Scenarios = append(f.Scenarios, figuresWith(prof, s, classNear, 100, p95, p95).Scenarios[0])
	}
	d := filepath.Join(dir, name)
	if err := os.MkdirAll(d, 0o700); err != nil {
		t.Fatal(err)
	}
	md := filepath.Join(d, "load-report.md")
	if err := os.WriteFile(md, []byte("## Load and fault scenarios, "+prof+" budget\n\n### "+scenarios[0]+"\n\n- pass: held\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := writeFigures(figuresPath(md), f); err != nil {
		t.Fatal(err)
	}
	return md
}

// catalogueNames are the catalogue's scenario names, split at i.
func catalogueNames(i int) ([]string, []string) {
	var all []string
	for _, s := range catalogue {
		all = append(all, s.name)
	}
	return slices.Clip(all[:i]), all[i:]
}

func runMergeCLI(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(append([]string{"merge"}, args...), func(string) string { return "" }, &out, &errb)
	return code, out.String(), errb.String()
}

func TestMergeGathersEveryShardInCatalogueOrderAndCompares(t *testing.T) {
	req.Covers(t, "TST-007", "TST-021")
	dir := t.TempDir()
	first, rest := catalogueNames(3)
	b := writeShard(t, dir, "load-shard-b", "nightly", 100, rest...)
	a := writeShard(t, dir, "load-shard-a", "nightly", 100, first...)
	prev := writeJSON(t, figuresWith("nightly", "soak", classNear, 100, 100, 100))
	out := filepath.Join(dir, "load-report.md")
	code, stdout, errs := runMergeCLI("-profile", "nightly", "-report", out, "-previous", prev, b, a)
	text, _ := os.ReadFile(out)
	if code != 0 {
		t.Fatalf("exit %d\n%s\n%s\n%s", code, stdout, errs, text)
	}
	var f figures
	raw, err := os.ReadFile(figuresPath(out))
	if err != nil || json.Unmarshal(raw, &f) != nil || f.Profile != "nightly" || len(f.Scenarios) != len(catalogue) {
		t.Fatalf("merged figures: %v %+v", err, f)
	}
	for i, s := range catalogue {
		if f.Scenarios[i].Name != s.name {
			t.Errorf("scenario %d is %s, want %s (catalogue order)", i, f.Scenarios[i].Name, s.name)
		}
	}
	for _, want := range []string{"in 2 shards", "| soak | load-shard-a | pass |", "| instance-killed | load-shard-b | pass |",
		"## Shard load-shard-a", "### Load and fault scenarios, nightly budget", "#### soak", "No regression beyond the tolerance"} {
		if !strings.Contains(string(text), want) {
			t.Errorf("the merged report lacks %q:\n%s", want, text)
		}
	}
	// The same shards against a faster previous run: the merge compares the
	// whole run and fails the regression.
	fast := writeJSON(t, figuresWith("nightly", "soak", classNear, 100, 50, 50))
	if code, stdout, _ := runMergeCLI("-profile", "nightly", "-report", out, "-previous", fast, a, b); code != 1 || !strings.Contains(stdout, "regression: soak") {
		t.Errorf("a regression: exit %d\n%s", code, stdout)
	}
}

func TestMergeFailsEveryShardProblemByName(t *testing.T) {
	req.Covers(t, "TST-007")
	first, rest := catalogueNames(3)
	for name, tc := range map[string]struct {
		shards func(t *testing.T, dir string) []string
		want   string
	}{
		"a scenario no shard ran": {func(t *testing.T, dir string) []string {
			return []string{writeShard(t, dir, "a", "nightly", 1, first...)}
		}, "scenario slow-storage: no shard ran it"},
		"a scenario run twice": {func(t *testing.T, dir string) []string {
			return []string{writeShard(t, dir, "a", "nightly", 1, first...), writeShard(t, dir, "b", "nightly", 1, append(rest, "soak")...)}
		}, "scenario soak: run by shard a and by shard b"},
		"another budget": {func(t *testing.T, dir string) []string {
			return []string{writeShard(t, dir, "a", "nightly", 1, first...), writeShard(t, dir, "b", "pr", 1, rest...)}
		}, "shard b: ran the pr budget, not nightly"},
		"no figures": {func(t *testing.T, dir string) []string {
			b := writeShard(t, dir, "b", "nightly", 1, rest...)
			if err := os.Remove(figuresPath(b)); err != nil {
				t.Fatal(err)
			}
			return []string{writeShard(t, dir, "a", "nightly", 1, first...), b}
		}, "shard b: its figures could not be read"},
		"unreadable figures": {func(t *testing.T, dir string) []string {
			b := writeShard(t, dir, "b", "nightly", 1, rest...)
			if err := os.WriteFile(figuresPath(b), []byte("{"), 0o600); err != nil {
				t.Fatal(err)
			}
			return []string{writeShard(t, dir, "a", "nightly", 1, first...), b}
		}, "shard b: its figures are not a figures file"},
		"another figures version": {func(t *testing.T, dir string) []string {
			b := writeShard(t, dir, "b", "nightly", 1, rest...)
			if err := os.WriteFile(figuresPath(b), []byte(`{"version": 9, "profile": "nightly", "scenarios": [{"name": "spike"}]}`), 0o600); err != nil {
				t.Fatal(err)
			}
			return []string{writeShard(t, dir, "a", "nightly", 1, first...), b}
		}, "shard b: figures version 9, want 1"},
		"figures with no scenario": {func(t *testing.T, dir string) []string {
			b := writeShard(t, dir, "b", "nightly", 1, rest...)
			if err := os.WriteFile(figuresPath(b), []byte(`{"version": 1, "profile": "nightly", "scenarios": []}`), 0o600); err != nil {
				t.Fatal(err)
			}
			return []string{writeShard(t, dir, "a", "nightly", 1, first...), b}
		}, "shard b: its figures hold no scenario"},
		"no report": {func(t *testing.T, dir string) []string {
			return []string{writeShard(t, dir, "a", "nightly", 1, first...), filepath.Join(dir, "gone", "load-report.md")}
		}, "shard gone: its report could not be read"},
		"a scenario not in the catalogue": {func(t *testing.T, dir string) []string {
			return []string{writeShard(t, dir, "a", "nightly", 1, append(first, "nonesuch")...), writeShard(t, dir, "b", "nightly", 1, rest...)}
		}, "shard a: scenario nonesuch is not in the catalogue"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			out := filepath.Join(dir, "load-report.md")
			code, stdout, errs := runMergeCLI(append([]string{"-profile", "nightly", "-report", out}, tc.shards(t, dir)...)...)
			text, _ := os.ReadFile(out)
			if code != 1 || !strings.Contains(stdout, tc.want) || !strings.Contains(string(text), "**FAIL**: "+tc.want) {
				t.Errorf("exit %d, want 1 naming %q\n%s\n%s\n%s", code, tc.want, stdout, errs, text)
			}
		})
	}
}

func TestMergeFailsAFailedScenario(t *testing.T) {
	req.Covers(t, "TST-007")
	dir := t.TempDir()
	first, rest := catalogueNames(3)
	a := writeShard(t, dir, "a", "nightly", 1, first...)
	f := figuresWith("nightly", rest[0], classNear, 100, 1, 1)
	f.Scenarios[0].Passed = false
	for _, s := range rest[1:] {
		f.Scenarios = append(f.Scenarios, figuresWith("nightly", s, classNear, 100, 1, 1).Scenarios[0])
	}
	b := writeShard(t, dir, "b", "nightly", 1, rest...)
	if err := writeFigures(figuresPath(b), f); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "load-report.md")
	code, stdout, _ := runMergeCLI("-profile", "nightly", "-report", out, a, b)
	text, _ := os.ReadFile(out)
	if code != 1 || !strings.Contains(stdout, "1 failed") || !strings.Contains(string(text), "| slow-storage | b | **FAIL** |") {
		t.Errorf("exit %d\n%s\n%s", code, stdout, text)
	}
}

func TestMergeRefusesBadUsage(t *testing.T) {
	req.Covers(t, "TST-007")
	dir := t.TempDir()
	md := writeShard(t, dir, "a", "nightly", 1, "soak")
	// A directory where the merged figures would go: the report is written,
	// the figures cannot be.
	if err := os.Mkdir(filepath.Join(dir, "blocked.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	for want, args := range map[string][]string{
		"flag provided but not defined": {"-nonesuch"},
		"-profile must be":              {"-profile", "x", md},
		"a merge of nothing":            {"-profile", "nightly"},
		"is not a shard report":         {"-profile", "nightly", figuresPath(md)},
		"-regression-tolerance":         {"-profile", "nightly", "-regression-tolerance", "0", md},
		"-regression-floor":             {"-profile", "nightly", "-regression-floor", "-1ms", md},
		"-report must end in .md":       {"-profile", "nightly", "-report", filepath.Join(dir, "r.txt"), md},
		"r.md":                          {"-profile", "nightly", "-report", filepath.Join(dir, "absent", "r.md"), md},
		"blocked.json":                  {"-profile", "nightly", "-report", filepath.Join(dir, "blocked.md"), md},
	} {
		if code, _, errs := runMergeCLI(args...); code != 2 || !strings.Contains(errs, want) {
			t.Errorf("%v: exit %d, stderr %q, want %q", args, code, errs, want)
		}
	}
}

func TestAShardLeavesTheComparisonToTheMerge(t *testing.T) {
	req.Covers(t, "TST-007", "TST-021")
	c := newCLI(t, healthyAPI)
	code, out, errs := c.run(c.args("-shard")...)
	b, _ := os.ReadFile(c.report)
	if code != 0 || strings.Contains(string(b), "## Comparison") || !strings.Contains(string(b), "loadrun merge compares the whole run") || strings.Contains(out, "not compared") {
		t.Fatalf("exit %d\n%s\n%s\n%s", code, out, errs, b)
	}
	if code, _, errs := c.run(c.args("-shard", "-previous", c.report)...); code != 2 || !strings.Contains(errs, "exclude each other") {
		t.Errorf("-shard with -previous: exit %d, %q", code, errs)
	}
}
