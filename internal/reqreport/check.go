package reqreport

import (
	"fmt"
	"sort"
)

// Report is the outcome of one check: the catalogue, the scope, every test
// read, which tests cover which requirement, and every finding that fails
// the build.
type Report struct {
	Catalogue map[string]Requirement
	Scope     []string
	Tests     []TestResult
	Files     int
	Covering  map[string][]TestResult // requirement ID -> tests carrying it
	Findings  []string
}

// Failed reports whether any finding fails the build.
func (r *Report) Failed() bool { return len(r.Findings) > 0 }

// Check maps the tests to the catalogue and lists every finding:
//   - a test carrying an ID the catalogue does not hold (typo guard);
//   - a scope entry that is not in the catalogue, not a MUST row, or not
//     verified by an automated method;
//   - an in-scope requirement without a passing test;
//   - a leaf test that ran longer than TestBound;
//   - a test that failed.
func Check(cat map[string]Requirement, scope []string, files int, tests []TestResult) *Report {
	r := &Report{Catalogue: cat, Scope: scope, Tests: tests, Files: files, Covering: map[string][]TestResult{}}
	for _, t := range tests {
		r.checkTest(t)
	}
	for _, id := range scope {
		r.checkScope(id)
	}
	sort.Strings(r.Findings)
	return r
}

func (r *Report) checkTest(t TestResult) {
	for _, id := range t.IDs {
		if _, ok := r.Catalogue[id]; !ok {
			r.Findings = append(r.Findings, fmt.Sprintf("unknown requirement %s carried by %s", id, t.Key()))
			continue
		}
		r.Covering[id] = append(r.Covering[id], t)
	}
	if t.Outcome == "fail" {
		r.Findings = append(r.Findings, "failed: "+t.Key())
	}
	if t.leaf && t.Elapsed > TestBound {
		r.Findings = append(r.Findings, fmt.Sprintf("%s took %s, over the %s bound (TST-012)", t.Key(), t.Elapsed, TestBound))
	}
}

func (r *Report) checkScope(id string) {
	req, ok := r.Catalogue[id]
	switch {
	case !ok:
		r.Findings = append(r.Findings, "scope lists "+id+", which the catalogue does not hold")
	case req.Keyword != "MUST" || !req.Automated():
		r.Findings = append(r.Findings, fmt.Sprintf("scope lists %s, a %s row verified by %s; only automated MUST rows belong in scope", id, req.Keyword, req.Method))
	case !r.passing(id):
		r.Findings = append(r.Findings, "in-scope requirement "+id+" has no passing test")
	}
}

func (r *Report) passing(id string) bool {
	for _, t := range r.Covering[id] {
		if t.Outcome == "pass" {
			return true
		}
	}
	return false
}

// Summary is the one line the step prints: what it read and its verdict.
func (r *Report) Summary() string {
	verdict := "ok"
	if r.Failed() {
		verdict = fmt.Sprintf("FAILED with %d finding(s)", len(r.Findings))
	}
	return fmt.Sprintf("reqreport: %d requirements in the catalogue, %d in scope, %d tests from %d result file(s), %d requirement(s) covered: %s",
		len(r.Catalogue), len(r.Scope), len(r.Tests), r.Files, len(r.Covering), verdict)
}
