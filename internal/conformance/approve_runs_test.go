package conformance_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// approveStub answers the gh calls of approve-release-runs.sh: the branch's
// head commit, the runs on it (none until the STUB_LISTED_FROM-th read, and a
// failed read at the STUB_FAIL_ON-th), and the approvals, which it records.
// It applies the --jq filter itself, as gh does. A script that reads more
// than approveMaxReads times does not end: the stub marks it and ends the
// script's process group, so no test waits on a wall clock (TST-012).
const approveStub = `#!/bin/bash
jqf=""
prev=""
for a in "$@"; do [[ $prev == --jq ]] && jqf=$a; prev=$a; done
case "$*" in
  *"-X POST"*) echo "$4" >> "$STUB_DIR/approved"; exit 0 ;;
  *branches/*) echo 3333333333333333333333333333333333333333; exit 0 ;;
  *actions/runs*)
    n=$(( $(cat "$STUB_DIR/reads" 2>/dev/null || echo 0) + 1 ))
    echo "$n" > "$STUB_DIR/reads"
    if (( n > STUB_MAX_READS )); then : > "$STUB_DIR/runaway"; kill -TERM -- "-$(ps -o pgid= $$ | tr -d ' ')"; exit 1; fi
    [[ $n == "$STUB_FAIL_ON" ]] && { echo "HTTP 502" >&2; exit 1; }
    if (( n < STUB_LISTED_FROM )); then body='{"workflow_runs":[]}'; else body=$STUB_RUNS; fi
    jq "$jqf" <<<"$body"; exit 0 ;;
esac
echo "unexpected gh call: $*" >&2
exit 3
`

// approveMaxReads bounds the reads of one run: the slowest case needs 4.
const approveMaxReads = 20

// approveRuns: on the head commit, CI and Security each have a pull_request
// run that waits for approval (1, 3). The push run of CI (2), a run of
// another workflow (4) and a queued run (5) are not approved.
const approveRuns = `{"workflow_runs":[
 {"id":1,"name":"CI","event":"pull_request","status":"completed","conclusion":"action_required"},
 {"id":2,"name":"CI","event":"push","status":"completed","conclusion":"action_required"},
 {"id":3,"name":"Security","event":"pull_request","status":"completed","conclusion":"action_required"},
 {"id":4,"name":"CodeQL","event":"pull_request","status":"completed","conclusion":"action_required"},
 {"id":5,"name":"Security","event":"pull_request","status":"queued","conclusion":null}]}`

// approveCases: what the script does against the stub. Runs that GitHub lists
// only after a few reads are found, because every round asks again; a failed
// read is asked again; no run by the deadline is a failure (TST-011).
var approveCases = []struct {
	name               string
	listedFrom, failOn int
	waitS              string
	want               int
	approved           []string
}{
	{"runs listed at once", 1, 0, "30", 0, []string{"1", "3"}},
	{"runs listed on the third read", 3, 0, "30", 0, []string{"1", "3"}},
	{"a failed read, then the runs", 1, 1, "30", 0, []string{"1", "3"}},
	{"no run by the deadline", 1 << 30, 0, "0", 1, nil},
}

// runApprove runs the script on a release-please branch against the stub and
// returns its exit status and the ids it approved. A run that reads past
// approveMaxReads, or (a backstop) does not end within 5 s, is reported as
// not ending.
func runApprove(script string, listedFrom, failOn int, waitS string) (int, []string, error) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		return -1, nil, err
	}
	dir, err := os.MkdirTemp("", "approve-runs")
	if err != nil {
		return -1, nil, err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(approveStub), 0o700); err != nil { // #nosec G306 -- an executable stub in a private temp dir
		return -1, nil, err
	}
	path := filepath.Join(dir, "approve.sh")
	if err := os.WriteFile(path, []byte(script), 0o600); err != nil {
		return -1, nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bash, "--noprofile", "--norc", path, "release-please--branches--main") // #nosec G204 -- the repository's own script
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}                                                  // the stub ends this group alone, never the test's
	cmd.Env = []string{
		"PATH=" + dir + ":" + os.Getenv("PATH"), "GH_REPO=o/r", "STUB_DIR=" + dir, "STUB_RUNS=" + approveRuns,
		"STUB_MAX_READS=" + fmt.Sprint(approveMaxReads), "STUB_LISTED_FROM=" + fmt.Sprint(listedFrom), "STUB_FAIL_ON=" + fmt.Sprint(failOn), "APPROVE_WAIT_S=" + waitS, "APPROVE_POLL_S=0",
	}
	runErr := cmd.Run()
	if _, serr := os.Stat(filepath.Join(dir, "runaway")); serr == nil || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return -1, nil, errors.New("the script did not end")
	}
	code, err := bashExit(runErr)
	b, _ := os.ReadFile(filepath.Join(dir, "approved")) // #nosec G304 -- the temp file the stub wrote
	var ids []string
	for _, l := range strings.Fields(string(b)) {
		ids = append(ids, strings.TrimSuffix(strings.TrimPrefix(l, "repos/o/r/actions/runs/"), "/approve"))
	}
	return code, ids, err
}

// approveFindings: the script approves exactly the pull_request runs of CI
// and Security that wait for approval, finds runs listed late, survives a
// failed read, and fails when no run is there by the deadline.
func approveFindings(script string) []string {
	var out []string
	for _, c := range approveCases {
		got, ids, err := runApprove(script, c.listedFrom, c.failOn, c.waitS)
		switch {
		case err != nil:
			out = append(out, c.name+": "+err.Error())
		case got != c.want || !slices.Equal(ids, c.approved):
			out = append(out, fmt.Sprintf("%s: the script exits %d and approves %v, not %d and %v", c.name, got, ids, c.want, c.approved))
		}
	}
	return out
}

func TestApproveReleaseRunsFindsAndApprovesThePullRequestRuns(t *testing.T) {
	req.Covers(t, "REL-015")
	b, err := os.ReadFile(filepath.Join(root, "scripts", approveScript))
	if err != nil {
		t.Fatal(err)
	}
	src := string(b)
	if f := approveFindings(src); len(f) > 0 {
		t.Fatal(strings.Join(f, "; "))
	}
	plant := func(from, to string) string {
		bad := strings.Replace(src, from, to, 1)
		if bad == src {
			t.Fatalf("plant changed nothing: %q", from)
		}
		return bad
	}
	for want, bad := range map[string]string{
		"runs listed on the third read: the script exits 0 and approves []":  plant(`    sleep "$poll"`+"\n", `    sleep "$poll"; break`+"\n"),
		"runs listed at once: the script exits 0 and approves [1 2 3]":       plant(` and .event == \"pull_request\"`, ""),
		"runs listed at once: the script exits 0 and approves [1 3 4 1 3 4]": plant(`select(.name == \"$wf\" and `, `select(`),
		"runs listed at once: the script exits 0 and approves [1 3 5]":       plant(`select(.conclusion == "action_required")`, `select(.status != "")`),
		"a failed read, then the runs: the script exits 1":                   plant(`could not be read ($last_err); asking again" >&2`, `could not be read" >&2; exit 1`),
		"no run by the deadline: the script did not end":                     plant("    (( SECONDS < deadline )) ||", "    true ||"),
		"no run by the deadline: the script exits 0":                         plant(`$last_err}" >&2; exit 1; }`, `$last_err}" >&2; exit 0; }`),
	} {
		t.Run(want, func(t *testing.T) {
			t.Parallel() // each run has its own temp dir and process group
			if got := strings.Join(approveFindings(bad), "; "); !strings.Contains(got, want) {
				t.Errorf("planted %q, the rules said %q", want, got)
			}
		})
	}
}
