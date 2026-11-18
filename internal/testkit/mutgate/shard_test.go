package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// The count the full run of .github/workflows/mutation.yml is cut into
// (its matrix shard: [1, 2, 3, 4] and -shard "$SHARD/4").
const releaseShards = 4

func TestParseShardTakesKOfNAndNothingElse(t *testing.T) {
	for raw, want := range map[string]shard{"1/4": {1, 4}, "4/4": {4, 4}, "1/1": {1, 1}, "12/12": {12, 12}} {
		if got, ok := parseShard(raw); !ok || got != want {
			t.Errorf("%q: %v %v", raw, got, ok)
		}
	}
	for _, bad := range []string{"0/4", "5/4", "1/0", "-1/4", "1/4/2", "1.5/4", "01/4", "a/4", "1", "", " 1/4", "99999999999999999999/99999999999999999999"} {
		if got, ok := parseShard(bad); ok {
			t.Errorf("%q parsed as %v", bad, got)
		}
	}
}

func TestShardsAreDisjointCompleteAndEven(t *testing.T) {
	if got := shardOf([]int{0, 1, 2, 3, 4, 5, 6, 7, 8}, shard{2, 4}); !slices.Equal(got, []int{1, 5}) {
		t.Fatalf("shard 2/4 of 0..8: %v", got)
	}
	for _, length := range []int{0, 1, 3, 4, 5, 6, 9, 31} {
		list := make([]int, length)
		for i := range list {
			list[i] = i
		}
		for _, count := range []int{1, 2, 4, 7} {
			var all []int
			lo, hi := length, 0
			for k := 1; k <= count; k++ {
				s := shardOf(list, shard{k, count})
				all = append(all, s...)
				lo, hi = min(lo, len(s)), max(hi, len(s))
			}
			slices.Sort(all)
			if !slices.Equal(all, list) {
				t.Errorf("%d/%d: the shards together are %v", length, count, all)
			}
			if hi-lo > 1 {
				t.Errorf("%d/%d: shard sizes from %d to %d", length, count, lo, hi)
			}
		}
	}
}

func TestTheReleaseShardsOfTheRealScope(t *testing.T) {
	req.Covers(t, "TST-002")
	roots, err := readScope(filepath.Join("..", "..", "..", "test", "mutation", "scope.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]int{}
	for k := 1; k <= releaseShards; k++ {
		s := shardOf(roots, shard{k, releaseShards})
		if len(s) == 0 {
			t.Errorf("shard %d/%d of %d scope roots is empty", k, releaseShards, len(roots))
		}
		for _, r := range s {
			seen[r.dir]++
		}
	}
	for _, r := range roots {
		if seen[r.dir] != 1 {
			t.Errorf("scope root %s is in %d shard(s), not one", r.dir, seen[r.dir])
		}
	}
	if len(seen) != len(roots) {
		t.Errorf("the shards hold %d roots, the scope %d", len(seen), len(roots))
	}
}

func TestPlanPrintsAShardOfTheScopeInFullMode(t *testing.T) {
	dir := files(t, base())
	for k, want := range map[int]string{1: "d\n", 2: "h\n"} {
		code, out, errs := runGate(dir, "-mode", "full", "-min", "70", "-plan", "-shard", fmt.Sprintf("%d/2", k))
		if code != 0 || out != want {
			t.Errorf("shard %d/2: exit %d %q %s", k, code, out, errs)
		}
	}
	if code, out, _ := runGate(dir, "-mode", "full", "-min", "70", "-plan", "-shard", "1/1"); code != 0 || out != "d\nh\n" {
		t.Errorf("shard 1/1: exit %d %q", code, out)
	}
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"an empty shard":     {[]string{"-mode", "full", "-min", "70", "-plan", "-shard", "3/3"}, "selects no scope root"},
		"a bad shard":        {[]string{"-mode", "full", "-min", "70", "-plan", "-shard", "0/4"}, "-shard must be K/N"},
		"an empty value":     {[]string{"-mode", "full", "-min", "70", "-plan", "-shard", ""}, "-shard must be K/N"},
		"a shard in pr mode": {[]string{"-mode", "pr", "-min", "80", "-changed", "chg.tsv", "-plan", "-shard", "1/2"}, "-shard needs full mode and -plan"},
		"a shard of a gate":  {[]string{"-mode", "full", "-min", "70", "-shard", "1/2", "d=d.json", "h=h.json"}, "-shard needs full mode and -plan"},
		"no shard":           {[]string{"-mode", "full", "-min", "70", "-plan"}, "full mode with -shard K/N"},
	} {
		code, out, errs := runGate(dir, c.args...)
		if code != 2 || out != "" || !strings.Contains(errs, c.want) {
			t.Errorf("%s: exit %d %q %q", name, code, out, errs)
		}
	}
}
