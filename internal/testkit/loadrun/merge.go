package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// runMerge is loadrun merge: it gathers the shards of a split run
// (load-nightly.yml runs the catalogue as parallel jobs, each on a stack of
// its own, each within 15 minutes) into one report and one figures file,
// and compares the whole run with the previous main run, as a run of the
// whole catalogue on one stack does (TST-007, TST-021).
//
//	loadrun merge -profile nightly -report load-report.md \
//	    [-previous previous/load-report.json] shard-a/load-report.md ...
//
// Each argument is a shard's report (written with -shard); its figures are
// read from beside it. Every scenario of the catalogue must be run by
// exactly one shard: a scenario no shard ran, one run twice, a shard whose
// figures are missing, unreadable or of another budget, and a failed
// scenario each fail the merge (exit 1), named in the report. Exit 2 on a
// usage error.
func runMerge(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet(prog+" merge", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		prof     = fs.String("profile", "", "pr or nightly, the budget every shard ran")
		report   = fs.String("report", "load-report.md", "the merged Markdown report; the merged figures go beside it, .json for .md")
		previous = fs.String("previous", "", "the previous main run's figures to compare with; empty compares with nothing, and the report says so (TST-021)")
		tol      = fs.Float64("regression-tolerance", 0.10, "the growth of a class's p95 or p99 over the previous run that fails it, a fraction (TST-021)")
		floor    = fs.Duration("regression-floor", 5*time.Millisecond, "a p95 or p99 growth no larger than this is never a regression (TST-021)")
	)
	if err := fs.Parse(args); err != nil {
		return 2
	}
	fail := func(format string, a ...any) int {
		fmt.Fprintf(stderr, prog+" merge: "+format+"\n", a...)
		return 2
	}
	if _, ok := profiles[*prof]; !ok {
		return fail("-profile must be pr or nightly")
	}
	switch {
	case fs.NArg() == 0:
		return fail("name at least one shard report; a merge of nothing is not a run")
	case !(*tol > 0 && *tol <= 10):
		return fail("-regression-tolerance must be above 0 and at most 10")
	case *floor < 0:
		return fail("-regression-floor must not be negative")
	case !strings.HasSuffix(*report, ".md"):
		return fail("-report must end in .md, so that the figures can go beside it")
	}
	for _, a := range fs.Args() {
		if !strings.HasSuffix(a, ".md") {
			return fail("%s is not a shard report (.md)", a)
		}
	}
	m := mergeShards(*prof, fs.Args())
	cmp := compare(*previous, m.figs, *tol, *floor)
	if err := os.WriteFile(*report, []byte(m.render(*prof)+cmp.render(*tol, *floor)), 0o600); err != nil {
		return fail("%v", err)
	}
	if err := writeFigures(figuresPath(*report), m.figs); err != nil {
		return fail("%v", err)
	}
	failed := 0
	for _, s := range m.figs.Scenarios {
		if !s.Passed {
			failed++
		}
	}
	for _, p := range m.problems {
		fmt.Fprintf(stdout, "%s merge: %s\n", prog, p)
	}
	for _, r := range cmp.regressions {
		fmt.Fprintf(stdout, "%s merge: regression: %s\n", prog, r)
	}
	if cmp.state != compared {
		fmt.Fprintf(stdout, "%s merge: not compared with the previous main run: %s\n", prog, cmp.reason)
	}
	fmt.Fprintf(stdout, "%s merge: %d shard(s), %d scenario(s), %d failed, %d problem(s); report in %s, figures in %s\n",
		prog, len(m.shards), len(m.figs.Scenarios), failed, len(m.problems), *report, figuresPath(*report))
	if failed > 0 || len(m.problems) > 0 || cmp.failed() {
		return 1
	}
	return 0
}

// merged is the shards of a split run put together.
type merged struct {
	figs     figures
	shards   []shardReport
	problems []string
	from     map[string]string // scenario → the shard that ran it
}

type shardReport struct{ name, text string }

// mergeShards reads every shard's report and figures and puts the
// scenarios in catalogue order. It never turns a missing or unreadable
// shard into fewer scenarios silently: each is a problem, by name.
func mergeShards(prof string, paths []string) merged {
	m := merged{figs: figures{Version: figuresVersion, Profile: prof, Scenarios: []scenarioFigures{}}, from: map[string]string{}}
	byName := map[string]scenarioFigures{}
	for _, p := range paths {
		name := filepath.Base(filepath.Dir(p))
		text, err := os.ReadFile(p) //nolint:gosec // G304: the shard reports the gather job downloaded
		if err != nil {
			m.problems = append(m.problems, fmt.Sprintf("shard %s: its report could not be read: %v", name, err))
			continue
		}
		m.shards = append(m.shards, shardReport{name: name, text: string(text)})
		b, err := os.ReadFile(figuresPath(p))
		if err != nil {
			m.problems = append(m.problems, fmt.Sprintf("shard %s: its figures could not be read: %v", name, err))
			continue
		}
		var f figures
		switch err := json.Unmarshal(b, &f); {
		case err != nil:
			m.problems = append(m.problems, fmt.Sprintf("shard %s: its figures are not a figures file: %v", name, err))
			continue
		case f.Version != figuresVersion:
			m.problems = append(m.problems, fmt.Sprintf("shard %s: figures version %d, want %d", name, f.Version, figuresVersion))
			continue
		case f.Profile != prof:
			m.problems = append(m.problems, fmt.Sprintf("shard %s: ran the %s budget, not %s", name, f.Profile, prof))
			continue
		case len(f.Scenarios) == 0:
			m.problems = append(m.problems, fmt.Sprintf("shard %s: its figures hold no scenario", name))
			continue
		}
		for _, s := range f.Scenarios {
			if other, dup := m.from[s.Name]; dup {
				m.problems = append(m.problems, fmt.Sprintf("scenario %s: run by shard %s and by shard %s", s.Name, other, name))
				continue
			}
			if !slices.ContainsFunc(catalogue, func(c scenario) bool { return c.name == s.Name }) {
				m.problems = append(m.problems, fmt.Sprintf("shard %s: scenario %s is not in the catalogue", name, s.Name))
				continue
			}
			m.from[s.Name] = name
			byName[s.Name] = s
		}
	}
	for _, c := range catalogue {
		s, ok := byName[c.name]
		if !ok {
			m.problems = append(m.problems, fmt.Sprintf("scenario %s: no shard ran it", c.name))
			continue
		}
		m.figs.Scenarios = append(m.figs.Scenarios, s)
	}
	return m
}

// render is the merged report before its comparison section: one row per
// scenario with the shard that ran it, the problems, then every shard's own
// report with its headings one level down.
func (m merged) render(prof string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## Load and fault scenarios, %s budget, in %d shards\n\n", prof, len(m.shards))
	b.WriteString("Each shard ran its scenarios on a stack of its own, on a runner of its own, in parallel (load-nightly.yml).\n\n")
	b.WriteString("| Scenario | Shard | Verdict |\n|---|---|---|\n")
	failed := 0
	for _, s := range m.figs.Scenarios {
		v := "pass"
		if !s.Passed {
			v, failed = "**FAIL**", failed+1
		}
		fmt.Fprintf(&b, "| %s | %s | %s |\n", s.Name, m.from[s.Name], v)
	}
	fmt.Fprintf(&b, "\n%d scenario(s), %d failed.\n", len(m.figs.Scenarios), failed)
	if len(m.problems) > 0 {
		b.WriteString("\n")
		for _, p := range m.problems {
			fmt.Fprintf(&b, "- **FAIL**: %s\n", p)
		}
	}
	for _, s := range m.shards {
		fmt.Fprintf(&b, "\n## Shard %s\n\n", s.name)
		for line := range strings.SplitSeq(strings.TrimRight(s.text, "\n"), "\n") {
			if strings.HasPrefix(line, "#") {
				line = "#" + line
			}
			b.WriteString(line + "\n")
		}
	}
	return b.String()
}
