package conformance_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// mutationShardFindings holds mutation.yml to the sharded full run: a
// matrix of exactly shards 1 to 4, each mutating its shard of 4, fail-fast
// off, and a gate that runs unless cancelled, needs the shards, is red on
// any shard result but success and gathers every report. The shards of the
// scope are proved disjoint, complete and none empty by
// internal/testkit/mutgate/shard_test.go, at the same count.
func mutationShardFindings(wf string) []string {
	var out []string
	for _, want := range []struct{ text, finding string }{
		{"\n    strategy:\n      fail-fast: false\n      matrix:\n        shard: [1, 2, 3, 4]\n", "the matrix is not shards [1, 2, 3, 4] with fail-fast off"},
		{"\n        run: scripts/mutation.sh shard \"$SHARD/4\" mutation-shard\n", "a shard does not mutate its shard of 4"},
		{"\n    needs: [plan, shards]\n    if: ${{ !cancelled() }}\n", "the gate does not wait for the shards, or does not run when one failed"},
		{"[[ $SHARDS == success ]] ||", "the gate does not stop on a shard that did not succeed"},
		{"\n            scripts/mutation.sh gather mutation-shards mutation-report.md\n", "the full run's gate does not gather the shards' reports"},
	} {
		if !strings.Contains(wf, want.text) {
			out = append(out, want.finding)
		}
	}
	if strings.Contains(wf, "continue-on-error") {
		out = append(out, "a mutation job may fail without failing the run")
	}
	if strings.Contains(wf, "scripts/mutation.sh full") {
		out = append(out, "a job runs the whole full set serially")
	}
	return out
}

func TestMutationFullRunIsFourShardsAndAGate(t *testing.T) {
	req.Covers(t, "TST-002")
	b, err := os.ReadFile(filepath.Join(root, ".github", "workflows", "mutation.yml"))
	if err != nil {
		t.Fatal(err)
	}
	wf := string(b)
	if f := mutationShardFindings(wf); len(f) > 0 {
		t.Fatalf("mutation.yml: %v", f)
	}
	for planted, want := range map[string][2]string{
		"three shards":       {"        shard: [1, 2, 3, 4]\n", "        shard: [1, 2, 3]\n"},
		"fail-fast":          {"      fail-fast: false\n", "      fail-fast: true\n"},
		"a shard of 5":       {`shard "$SHARD/4"`, `shard "$SHARD/5"`},
		"a gate on success":  {"    if: ${{ !cancelled() }}\n", "    if: success()\n"},
		"a lenient fan-in":   {"[[ $SHARDS == success ]] ||", "[[ $SHARDS != failure ]] ||"},
		"no gather":          {"scripts/mutation.sh gather mutation-shards", "scripts/mutation.sh full"},
		"an optional shard":  {"    timeout-minutes: 30\n    strategy:", "    timeout-minutes: 30\n    continue-on-error: true\n    strategy:"},
		"no wait for shards": {"    needs: [plan, shards]\n", "    needs: plan\n"},
	} {
		bad := strings.Replace(wf, want[0], want[1], 1)
		if bad == wf {
			t.Fatalf("%s: the planted text is not in mutation.yml", planted)
		}
		if len(mutationShardFindings(bad)) == 0 {
			t.Errorf("%s: planted and not reported", planted)
		}
	}
}
