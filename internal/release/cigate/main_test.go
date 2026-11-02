package main

import (
	"bytes"
	"strings"
	"testing"
	"testing/iotest"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

const sha = "0123456789abcdef0123456789abcdef01234567"

func gate(stdin string, extra ...string) (int, string, string) {
	var out, errs bytes.Buffer
	args := append([]string{"-sha", sha, "-require", "Unit", "-require", "Coverage"}, extra...)
	code := run(args, strings.NewReader(stdin), &out, &errs)
	return code, out.String(), errs.String()
}

// cr is one check run; at is its completion minute, "" for none.
func cr(name, status, conclusion, at string) string {
	completed := "null"
	if at != "" {
		completed = `"2026-11-02T10:` + at + `:00Z"`
	}
	return `{"name":"` + name + `","head_sha":"` + sha + `","status":"` + status + `","conclusion":"` + conclusion + `","completed_at":` + completed + `}`
}

func listing(total int, runs ...string) string {
	return `{"total_count":` + string(rune('0'+total)) + `,"check_runs":[` + strings.Join(runs, ",") + `]}`
}

func TestEveryRequiredCheckPassed(t *testing.T) {
	req.Covers(t, "REL-015")
	ok := listing(3, cr("Unit", "completed", "success", "01"), cr("Coverage", "completed", "success", "02"), cr("Lint", "completed", "failure", "03"))
	for name, stdin := range map[string]string{
		"one page":         ok,
		"pages (slurp)":    "[" + listing(4, cr("Unit", "completed", "success", "01"), cr("Coverage", "completed", "success", "02")) + "," + listing(4, cr("x", "completed", "success", "01"), cr("y", "queued", "", "")) + "]",
		"re-run succeeded": listing(3, cr("Unit", "completed", "failure", "01"), cr("Unit", "completed", "success", "05"), cr("Coverage", "completed", "success", "02")),
	} {
		if code, out, errs := gate(stdin); code != 0 || !strings.Contains(out, "cigate: 0123456789ab passed Unit, Coverage") {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", name, code, out, errs)
		}
	}
}

func TestRefusals(t *testing.T) {
	req.Covers(t, "REL-015")
	other := strings.Replace(cr("Coverage", "completed", "success", "02"), sha, strings.Repeat("f", 40), 1)
	for want, stdin := range map[string]string{
		`"Coverage" has not run on this commit`:                                          listing(1, cr("Unit", "completed", "success", "01")),
		`"Coverage" has not run on this commit (another sha)`:                            listing(2, cr("Unit", "completed", "success", "01"), other),
		`"Coverage" is still in_progress`:                                                listing(2, cr("Unit", "completed", "success", "01"), cr("Coverage", "in_progress", "", "")),
		`"Coverage" is still queued`:                                                     listing(3, cr("Unit", "completed", "success", "01"), cr("Coverage", "completed", "success", "02"), cr("Coverage", "queued", "", "")),
		`"Coverage" is still in_progress (a re-run that carries an old completion time)`: listing(3, cr("Unit", "completed", "success", "01"), cr("Coverage", "completed", "success", "02"), cr("Coverage", "in_progress", "", "03")),
		`"Coverage" is still without a completion time`:                                  listing(2, cr("Unit", "completed", "success", "01"), cr("Coverage", "completed", "success", "")),
		`"Coverage" concluded "failure"`:                                                 listing(2, cr("Unit", "completed", "success", "01"), cr("Coverage", "completed", "failure", "02")),
		`"Unit" concluded "cancelled"`:                                                   listing(3, cr("Unit", "completed", "success", "01"), cr("Unit", "completed", "cancelled", "04"), cr("Coverage", "completed", "success", "02")),
		`"Coverage" concluded "skipped"`:                                                 listing(2, cr("Unit", "completed", "success", "01"), cr("Coverage", "completed", "skipped", "02")),
		`"Unit" has not run on this commit; "Coverage" has not run`:                      listing(0),
	} {
		code, out, errs := gate(stdin)
		for _, w := range strings.Split(strings.Split(want, " (")[0], "; ") {
			if code != 1 || !strings.Contains(errs, "REFUSED 0123456789ab: "+w) || out != "" {
				t.Errorf("want %q: exit %d, stdout %q, stderr %q", w, code, out, errs)
			}
		}
	}
}

func TestUnreadableAnswersAreNeverAPass(t *testing.T) {
	req.Covers(t, "REL-015")
	for want, stdin := range map[string]string{
		"is not JSON":             "<html>rate limited</html>",
		"carries no total_count":  `{"message":"Not Found"}`,
		"carries no total_count ": `[]`,
		"holds 1 of 5 check runs": listing(5, cr("Unit", "completed", "success", "01")),
	} {
		if code, _, errs := gate(stdin); code != 2 || !strings.Contains(errs, strings.TrimSpace(want)) {
			t.Errorf("want %q: exit %d, stderr %q", want, code, errs)
		}
	}
	var errs bytes.Buffer
	if code := run([]string{"-sha", sha, "-require", "x"}, iotest.ErrReader(iotest.ErrTimeout), &bytes.Buffer{}, &errs); code != 2 || !strings.Contains(errs.String(), "reading the check runs") {
		t.Errorf("an unreadable stdin: exit %d, stderr %q", code, errs.String())
	}
}

func TestUsage(t *testing.T) {
	req.Covers(t, "REL-015")
	for _, args := range [][]string{
		nil,
		{"-sha", sha},
		{"-require", "Unit"},
		{"-sha", "abc", "-require", "Unit"},
		{"-sha", strings.ToUpper(sha), "-require", "Unit"},
		{"-sha", sha, "-require", "Unit", "extra"},
		{"-unknown"},
	} {
		var errs bytes.Buffer
		if code := run(args, strings.NewReader(listing(0)), &bytes.Buffer{}, &errs); code != 2 {
			t.Errorf("%q: exit %d, want 2 (stderr %q)", args, code, errs.String())
		}
	}
}
