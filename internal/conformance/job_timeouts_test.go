package conformance_test

import (
	"fmt"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// maxJobMinutes is the owner's rule for every GitHub Actions job of this
// repository: it ends within 30 minutes (owner 2026-11-26, raised from 15),
// and longer work is split into
// parallel jobs (the fuzz targets, the load scenarios and the mutation
// shards are matrices for that reason).
const maxJobMinutes = 30

// jobTimeoutFindings lists every job of a workflow that has no
// timeout-minutes, one that is not a whole number of minutes (an expression
// could lift it at run time), or one above maxJobMinutes. A job that calls a
// reusable workflow cannot carry a timeout: it must call one of this
// repository's workflows, whose jobs are held to the same rule here.
func jobTimeoutFindings(name string, src []byte) []string {
	var w struct {
		Jobs map[string]struct {
			Uses    string `yaml:"uses"`
			Timeout any    `yaml:"timeout-minutes"`
		} `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(src, &w); err != nil {
		return []string{name + ": does not parse: " + err.Error()}
	}
	if len(w.Jobs) == 0 {
		return []string{name + ": no job read"}
	}
	var out []string
	for id, j := range w.Jobs {
		switch t := j.Timeout.(type) {
		case nil:
			if j.Uses != "" && strings.HasPrefix(j.Uses, "./.github/workflows/") {
				continue
			}
			out = append(out, fmt.Sprintf("%s: job %s has no timeout-minutes", name, id))
		case int:
			if t < 1 || t > maxJobMinutes {
				out = append(out, fmt.Sprintf("%s: job %s has timeout-minutes %d, above %d (or below 1)", name, id, t, maxJobMinutes))
			}
		default:
			out = append(out, fmt.Sprintf("%s: job %s has timeout-minutes %v, not a whole number of minutes", name, id, t))
		}
	}
	sort.Strings(out)
	return out
}

func TestEveryWorkflowJobEndsWithinThirtyMinutes(t *testing.T) {
	req.Covers(t, "TST-017")
	files := readWorkflows(t)
	jobs := 0
	for name, src := range files {
		for _, f := range jobTimeoutFindings(name, src) {
			t.Error(f)
		}
		jobs += strings.Count(string(src), "\n    timeout-minutes: ")
	}
	if jobs < 20 {
		t.Fatalf("read %d job timeouts over %d files; a check over too few proves nothing", jobs, len(files))
	}
}

func TestJobTimeoutRuleCanSayNo(t *testing.T) {
	req.Covers(t, "TST-017")
	good := string(readWorkflows(t)["fuzz-nightly.yml"])
	if f := jobTimeoutFindings("fuzz-nightly.yml", []byte(good)); len(f) > 0 {
		t.Fatalf("the unplanted file already has findings: %v", f)
	}
	native := "    runs-on: ubuntu-24.04\n    # Evidence (run of 2026-11-22, one job): a target took its 10 min budget\n    # plus 1 to 20 s of build; the job's setup 13 s. One target per job is\n    # about 11 min.\n    timeout-minutes: 15\n"
	for want, bad := range map[string]string{
		"job native has timeout-minutes 31, above 30":          strings.Replace(good, native, strings.Replace(native, ": 15\n", ": 31\n", 1), 1),
		"job native has timeout-minutes 150, above 30":         strings.Replace(good, native, strings.Replace(native, ": 15\n", ": 150\n", 1), 1),
		"job native has timeout-minutes 0":                     strings.Replace(good, native, strings.Replace(native, ": 15\n", ": 0\n", 1), 1),
		"job native has no timeout-minutes":                    strings.Replace(good, native, strings.Replace(native, "    timeout-minutes: 15\n", "", 1), 1),
		"job native has timeout-minutes ${{ inputs.minutes }}": strings.Replace(good, native, strings.Replace(native, ": 15\n", ": ${{ inputs.minutes }}\n", 1), 1),
		"job native has timeout-minutes 14.5, not a whole":     strings.Replace(good, native, strings.Replace(native, ": 15\n", ": 14.5\n", 1), 1),
		"job remote has no timeout-minutes":                    good + "  remote:\n    uses: other/repo/.github/workflows/x.yml@0123456789abcdef0123456789abcdef01234567\n",
		"fuzz-nightly.yml: does not parse":                     good + "\n\t:\n",
	} {
		if bad == good {
			t.Fatalf("plant for %q changed nothing", want)
		}
		if got := strings.Join(jobTimeoutFindings("fuzz-nightly.yml", []byte(bad)), "; "); !strings.Contains(got, want) {
			t.Errorf("planted %q, the rule said %q", want, got)
		}
	}
	local := good + "  local:\n    uses: ./.github/workflows/ci.yml\n"
	if f := jobTimeoutFindings("fuzz-nightly.yml", []byte(local)); len(f) > 0 {
		t.Errorf("a call of this repository's own workflow, whose jobs carry their own timeouts, was refused: %v", f)
	}
}
