package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

const (
	sha  = "0123456789abcdef0123456789abcdef01234567"
	ci   = ".github/workflows/ci.yml"
	repo = "owner/api"
)

// wr is one workflow run.
func wr(suite int, path, headSHA, event, branch, headRepo string) string {
	return `{"check_suite_id":` + strconv.Itoa(suite) + `,"path":"` + path + `","head_sha":"` + headSHA +
		`","event":"` + event + `","head_branch":"` + branch + `","head_repository":{"full_name":"` + headRepo + `"}}`
}

// trusted is a run of ci.yml on sha, pushed to main of repo.
func trusted(suite int) string { return wr(suite, ci, sha, "push", "main", repo) }

func runsAnswer(runs ...string) string {
	return `{"total_count":` + strconv.Itoa(len(runs)) + `,"workflow_runs":[` + strings.Join(runs, ",") + `]}`
}

// workflowRuns is the default answer. Suites 7 (main) and 13 (hotfix/*) are
// trusted push runs of ci.yml on sha; every other suite differs from a
// trusted run in exactly one field.
var workflowRuns = runsAnswer(
	trusted(7),
	wr(8, ".github/workflows/other.yml", sha, "push", "main", repo),
	wr(9, ci, strings.Repeat("f", 40), "push", "main", repo),
	wr(10, ci, sha, "pull_request", "main", repo),
	wr(11, ci, sha, "push", "main", "fork/api"),
	wr(12, ci, sha, "push", "feature", repo),
	wr(13, ci, sha, "push", "hotfix/1.2.3", repo),
)

func file(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "runs.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func gateWith(t *testing.T, runs, stdin string) (int, string, string) {
	t.Helper()
	var out, errs bytes.Buffer
	args := []string{"-sha", sha, "-repo", repo, "-workflow", ci, "-workflow-runs", file(t, runs), "-require", "Unit", "-require", "Coverage"}
	code := run(args, strings.NewReader(stdin), &out, &errs)
	return code, out.String(), errs.String()
}

func gate(t *testing.T, stdin string) (int, string, string) {
	t.Helper()
	return gateWith(t, workflowRuns, stdin)
}

// crIn is one check run from app in check suite suite; at is its completion
// minute, "" for none.
func crIn(app string, suite int, name, status, conclusion, at string) string {
	completed := "null"
	if at != "" {
		completed = `"2026-11-02T10:` + at + `:00Z"`
	}
	return `{"name":"` + name + `","head_sha":"` + sha + `","status":"` + status + `","conclusion":"` + conclusion +
		`","completed_at":` + completed + `,"app":{"slug":"` + app + `"},"check_suite":{"id":` + strconv.Itoa(suite) + `}}`
}

// cr is a check run GitHub Actions created in a ci.yml run on sha.
func cr(name, status, conclusion, at string) string {
	return crIn("github-actions", 7, name, status, conclusion, at)
}

func listing(total int, runs ...string) string {
	return `{"total_count":` + strconv.Itoa(total) + `,"check_runs":[` + strings.Join(runs, ",") + `]}`
}

func TestEveryRequiredCheckPassed(t *testing.T) {
	req.Covers(t, "REL-015")
	ok := listing(3, cr("Unit", "completed", "success", "01"), cr("Coverage", "completed", "success", "02"), cr("Lint", "completed", "failure", "03"))
	for name, stdin := range map[string]string{
		"one page":         ok,
		"pages (slurp)":    "[" + listing(4, cr("Unit", "completed", "success", "01"), cr("Coverage", "completed", "success", "02")) + "," + listing(4, cr("x", "completed", "success", "01"), cr("y", "queued", "", "")) + "]",
		"re-run succeeded": listing(3, cr("Unit", "completed", "failure", "01"), cr("Unit", "completed", "success", "05"), cr("Coverage", "completed", "success", "02")),
		"a foreign failure beside a trusted success": listing(3, cr("Unit", "completed", "success", "01"), cr("Coverage", "completed", "success", "02"),
			crIn("other-app", 7, "Coverage", "completed", "failure", "09")),
	} {
		if code, out, errs := gate(t, stdin); code != 0 || !strings.Contains(out, "cigate: 0123456789ab passed Unit, Coverage in "+ci) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", name, code, out, errs)
		}
	}
	slurped := "[" + workflowRuns + `,{"total_count":3,"workflow_runs":[]}]`
	if code, _, errs := gateWith(t, slurped, ok); code != 0 {
		t.Errorf("slurped workflow runs: exit %d, stderr %q", code, errs)
	}
}

func TestRefusals(t *testing.T) {
	req.Covers(t, "REL-015")
	other := strings.Replace(cr("Coverage", "completed", "success", "02"), sha, strings.Repeat("f", 40), 1)
	unit := cr("Unit", "completed", "success", "01")
	planted := `"Coverage" has not run in ` + ci + ` on this commit; `
	for want, stdin := range map[string]string{
		`"Coverage" has not run on this commit`:                                          listing(1, unit),
		`"Coverage" has not run on this commit (another sha)`:                            listing(2, unit, other),
		`"Coverage" is still in_progress`:                                                listing(2, unit, cr("Coverage", "in_progress", "", "")),
		`"Coverage" is still queued`:                                                     listing(3, unit, cr("Coverage", "completed", "success", "02"), cr("Coverage", "queued", "", "")),
		`"Coverage" is still in_progress (a re-run that carries an old completion time)`: listing(3, unit, cr("Coverage", "completed", "success", "02"), cr("Coverage", "in_progress", "", "03")),
		`"Coverage" is still without a completion time`:                                  listing(2, unit, cr("Coverage", "completed", "success", "")),
		`"Coverage" concluded "failure"`:                                                 listing(2, unit, cr("Coverage", "completed", "failure", "02")),
		`"Unit" concluded "cancelled"`:                                                   listing(3, unit, cr("Unit", "completed", "cancelled", "04"), cr("Coverage", "completed", "success", "02")),
		`"Coverage" concluded "skipped"`:                                                 listing(2, unit, cr("Coverage", "completed", "skipped", "02")),
		`"Unit" has not run on this commit; "Coverage" has not run`:                      listing(0),
		// The planted runs: same name, same sha, success, but not GitHub
		// Actions in a ci.yml run on this sha.
		planted + `1 same-named run(s) from another app, workflow or event are not evidence (another app)`: listing(2, unit, crIn("other-app", 7, "Coverage", "completed", "success", "02")),
		planted + `1 same-named run(s) (another workflow)`:                                                 listing(2, unit, crIn("github-actions", 8, "Coverage", "completed", "success", "02")),
		planted + `1 same-named run(s) (ci.yml on another sha)`:                                            listing(2, unit, crIn("github-actions", 9, "Coverage", "completed", "success", "02")),
		planted + `1 same-named run(s) (a pull-request run)`:                                               listing(2, unit, crIn("github-actions", 10, "Coverage", "completed", "success", "02")),
		planted + `1 same-named run(s) (a push from a fork)`:                                               listing(2, unit, crIn("github-actions", 11, "Coverage", "completed", "success", "02")),
		planted + `1 same-named run(s) (a push to another branch)`:                                         listing(2, unit, crIn("github-actions", 12, "Coverage", "completed", "success", "02")),
		// A pull-request run of the release commit whose ci.yml exits 0
		// finishes after the failing push run: the push run still decides.
		`"Coverage" concluded "failure" (a pull-request success after a failing push)`: listing(3, unit, cr("Coverage", "completed", "failure", "02"),
			crIn("github-actions", 10, "Coverage", "completed", "success", "05")),
		planted + `1 same-named run(s) (no suite)`: listing(2, unit, crIn("github-actions", 0, "Coverage", "completed", "success", "02")),
		planted + `2 same-named run(s) (two plants)`: listing(3, unit, crIn("other-app", 7, "Coverage", "completed", "success", "02"),
			crIn("", 7, "Coverage", "completed", "success", "02")),
	} {
		code, out, errs := gate(t, stdin)
		w := strings.Split(want, " (")[0]
		if code != 1 || !strings.Contains(errs, "REFUSED 0123456789ab: ") || out != "" {
			t.Errorf("want %q: exit %d, stdout %q, stderr %q", w, code, out, errs)
		}
		for _, part := range strings.Split(w, "; ") {
			if !strings.Contains(errs, part) {
				t.Errorf("want %q in stderr %q", part, errs)
			}
		}
	}
}

func TestOnlyATrustedPushRunCarriesTheChecks(t *testing.T) {
	req.Covers(t, "REL-015")
	ok := listing(2, cr("Unit", "completed", "success", "01"), cr("Coverage", "completed", "success", "02"))
	for name, runs := range map[string]string{
		"no workflow runs":           runsAnswer(),
		"only another path":          runsAnswer(wr(7, "ci.yml", sha, "push", "main", repo)),
		"ci.yml, another sha":        runsAnswer(wr(7, ci, strings.Repeat("e", 40), "push", "main", repo)),
		"a pull_request run":         runsAnswer(wr(7, ci, sha, "pull_request", "main", repo)),
		"a pull_request_target run":  runsAnswer(wr(7, ci, sha, "pull_request_target", "main", repo)),
		"a push from a fork":         runsAnswer(wr(7, ci, sha, "push", "main", "fork/api")),
		"a push to a feature branch": runsAnswer(wr(7, ci, sha, "push", "feature/hotfix/x", repo)),
		"a push to hotfix without /": runsAnswer(wr(7, ci, sha, "push", "hotfix", repo)),
	} {
		if code, _, errs := gateWith(t, runs, ok); code != 1 || !strings.Contains(errs, "not evidence") {
			t.Errorf("%s: exit %d, stderr %q", name, code, errs)
		}
	}
	if code, _, errs := gateWith(t, runsAnswer(wr(7, ci, sha, "push", "hotfix/1.2.3", repo)), ok); code != 0 {
		t.Errorf("a push to hotfix/1.2.3: exit %d, stderr %q", code, errs)
	}
	if code, _, errs := gateWith(t, ok, ok); code != 2 || !strings.Contains(errs, "holds 0 of 2 workflow runs") {
		t.Errorf("a check-runs listing given as workflow runs: exit %d, stderr %q", code, errs)
	}
}

func TestUnreadableAnswersAreNeverAPass(t *testing.T) {
	req.Covers(t, "REL-015")
	for want, stdin := range map[string]string{
		"check runs answer is not JSON": "<html>rate limited</html>",
		"carries no total_count":        `{"message":"Not Found"}`,
		"carries no total_count ":       `[]`,
		"holds 1 of 5 check runs":       listing(5, cr("Unit", "completed", "success", "01")),
		"holds 2 of 3 check runs":       listing(3, cr("Unit", "completed", "success", "01"), cr("Coverage", "completed", "success", "02")),
	} {
		if code, _, errs := gate(t, stdin); code != 2 || !strings.Contains(errs, strings.TrimSpace(want)) {
			t.Errorf("want %q: exit %d, stderr %q", want, code, errs)
		}
	}
	ok := listing(0)
	for want, runs := range map[string]string{
		"workflow runs answer is not JSON":            "<html>",
		"workflow runs answer carries no total_count": `{"message":"Not Found"}`,
		"holds 1 of 4 workflow runs":                  `{"total_count":4,"workflow_runs":[{"check_suite_id":7}]}`,
	} {
		if code, _, errs := gateWith(t, runs, ok); code != 2 || !strings.Contains(errs, want) {
			t.Errorf("want %q: exit %d, stderr %q", want, code, errs)
		}
	}
	var errs bytes.Buffer
	args := []string{"-sha", sha, "-repo", repo, "-workflow", ci, "-workflow-runs", file(t, workflowRuns), "-require", "x"}
	if code := run(args, iotest.ErrReader(iotest.ErrTimeout), &bytes.Buffer{}, &errs); code != 2 || !strings.Contains(errs.String(), "reading the check runs") {
		t.Errorf("an unreadable stdin: exit %d, stderr %q", code, errs.String())
	}
	errs.Reset()
	args = []string{"-sha", sha, "-repo", repo, "-workflow", ci, "-workflow-runs", filepath.Join(t.TempDir(), "absent"), "-require", "x"}
	if code := run(args, strings.NewReader(ok), &bytes.Buffer{}, &errs); code != 2 || !strings.Contains(errs.String(), "reading the workflow runs") {
		t.Errorf("an absent workflow-runs file: exit %d, stderr %q", code, errs.String())
	}
}

func TestUsage(t *testing.T) {
	req.Covers(t, "REL-015")
	runs := file(t, workflowRuns)
	full := []string{"-sha", sha, "-repo", repo, "-workflow", ci, "-workflow-runs", runs, "-require", "Unit"}
	without := func(flag string) []string {
		i := slices.Index(full, flag)
		return slices.Delete(slices.Clone(full), i, i+2)
	}
	with := func(flag, value string) []string {
		out := slices.Clone(full)
		out[slices.Index(out, flag)+1] = value
		return out
	}
	for _, args := range [][]string{
		nil,
		without("-sha"),
		without("-repo"),
		without("-workflow"),
		without("-workflow-runs"),
		without("-require"),
		with("-sha", "abc"),
		with("-sha", strings.ToUpper(sha)),
		with("-repo", "api"),
		append(slices.Clone(full), "extra"),
		{"-unknown"},
	} {
		var errs bytes.Buffer
		if code := run(args, strings.NewReader(listing(0)), &bytes.Buffer{}, &errs); code != 2 {
			t.Errorf("%q: exit %d, want 2 (stderr %q)", args, code, errs.String())
		}
	}
	if code := run(full, strings.NewReader(listing(1, cr("Unit", "completed", "success", "01"))), &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Errorf("the complete argument list: exit %d, want 0", code)
	}
}
