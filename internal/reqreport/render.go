package reqreport

import (
	"fmt"
	"io"
	"sort"
	"strings"
)

// Meta describes the run a report belongs to.
type Meta struct {
	Version   string // the API version or the milestone, e.g. 0.2.0
	Commit    string
	Generated string // RFC 3339 instant, UTC
}

// WriteRequirementReport writes the requirement report: the findings, the
// in-scope rows, and every requirement that carries at least one test.
func WriteRequirementReport(w io.Writer, r *Report, m Meta) error {
	var b strings.Builder
	fmt.Fprintf(&b, "# Requirement report: %s\n\n", m.Version)
	fmt.Fprintf(&b, "Commit `%s`, generated %s.\n\n%s\n\n", m.Commit, m.Generated, r.Summary())
	b.WriteString("## Findings\n\n")
	if len(r.Findings) == 0 {
		b.WriteString("None.\n\n")
	}
	for _, f := range r.Findings {
		fmt.Fprintf(&b, "- %s\n", f)
	}
	if len(r.Findings) > 0 {
		b.WriteString("\n")
	}
	b.WriteString("## In scope\n\n| Requirement | Method | Tests | Passing |\n|---|---|---|---|\n")
	for _, id := range r.Scope {
		req := r.Catalogue[id]
		fmt.Fprintf(&b, "| %s | %s | %d | %s |\n", id, req.Method, len(r.Covering[id]), yesNo(r.passing(id)))
	}
	b.WriteString("\n## Every covered requirement\n\n| Requirement | Keyword | Method | Test | Level | Result | Time |\n|---|---|---|---|---|---|---|\n")
	ids := make([]string, 0, len(r.Covering))
	for id := range r.Covering {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		ts := append([]TestResult(nil), r.Covering[id]...)
		sortTests(ts)
		req := r.Catalogue[id]
		for _, t := range ts {
			fmt.Fprintf(&b, "| %s | %s | %s | `%s.%s` | %s | %s | %.2f s |\n", id, req.Keyword, req.Method,
				shortPackage(t.Package), t.Name, t.Level, t.Outcome, t.Elapsed.Seconds())
		}
	}
	_, err := io.WriteString(w, b.String())
	return err
}

// WriteCompletionDraft writes the draft test completion report in the shape
// of the wiki's template (Test Documentation Templates, section 2). Every
// figure is a result of this run; what CI cannot know is left for the
// maintainers and says so.
func WriteCompletionDraft(w io.Writer, r *Report, m Meta) error {
	var b strings.Builder
	verdict := "ready"
	if r.Failed() {
		verdict = "not ready"
	}
	fmt.Fprintf(&b, "# Test completion report: %s\n\n", m.Version)
	fmt.Fprintf(&b, "Draft written by CI (`cmd/reqreport`) at %s for commit `%s`; the maintainers complete the sections marked *to complete*.\n\n", m.Generated, m.Commit)
	fmt.Fprintf(&b, "## 1. Summary\n- Release / milestone: %s\n- Specification revision: *to complete* (wiki commit the catalogue was generated from)\n- Image digest: *to complete*\n- Verdict from the automated gates: %s\n\n", m.Version, verdict)
	fmt.Fprintf(&b, "## 2. Scope\n- In-scope requirements: %d, listed in the requirement report\n- Out of scope, with reason: every other catalogue row (%d); the scope file of the milestone lists what this milestone covers\n\n",
		len(r.Scope), len(r.Catalogue)-len(r.Scope))
	b.WriteString("## 3. Results by level\n| Level | Executed | Passed | Failed | Skipped | Quarantined |\n|---|---|---|---|---|---|\n")
	for _, l := range levels(r.Tests) {
		c := countLevel(r.Tests, l)
		fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | 0 |\n", l, c["pass"]+c["fail"], c["pass"], c["fail"], c["skip"])
	}
	passing := 0
	var missing []string
	for _, id := range r.Scope {
		if r.passing(id) {
			passing++
		} else {
			missing = append(missing, id)
		}
	}
	fmt.Fprintf(&b, "\n## 4. Requirement report\n- Automated MUST rows in scope with a passing test: %d of %d\n- Rows without a passing test, with reason: %s\n\n", passing, len(r.Scope), listOrNone(missing))
	b.WriteString("## 5. Quality measures against targets\n*To complete* from the coverage and mutation results of the run.\n\n")
	b.WriteString("## 6. Non-functional results\n*To complete*: load, chaos, security scans and DAST, accessibility.\n\n")
	fmt.Fprintf(&b, "## 7. Deviations\n- Findings of the automated gates: %s\n- SHOULD rows not met, open defects, gates changed: *to complete*\n\n", listOrNone(r.Findings))
	b.WriteString("## 8. Escaped defects since the last report\n*To complete.*\n\n## 9. Lessons and actions\n*To complete.*\n\n## 10. Approval\n- Maintainer: *to complete*\n- Owner: *to complete*\n")
	_, err := io.WriteString(w, b.String())
	return err
}

func levels(ts []TestResult) []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range ts {
		if !seen[t.Level] {
			seen[t.Level] = true
			out = append(out, t.Level)
		}
	}
	sort.Strings(out)
	return out
}

func countLevel(ts []TestResult, level string) map[string]int {
	c := map[string]int{}
	for _, t := range ts {
		if t.Level == level {
			c[t.Outcome]++
		}
	}
	return c
}

func listOrNone(xs []string) string {
	if len(xs) == 0 {
		return "none"
	}
	return strings.Join(xs, "; ")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "**no**"
}
