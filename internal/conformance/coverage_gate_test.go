package conformance_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

type ciJobs struct {
	Jobs map[string]struct {
		Needs any `yaml:"needs"`
		Steps []struct {
			Run string `yaml:"run"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

func needs(v any) []string {
	switch n := v.(type) {
	case string:
		return []string{n}
	case []any:
		out := make([]string, len(n))
		for i, s := range n {
			out[i] = fmt.Sprint(s)
		}
		return out
	}
	return nil
}

// coverageGateFindings: the 100 % rule (TST-001) is wired so it cannot be
// weakened unnoticed. Both test jobs measure every package with -coverpkg, the
// coverage job merges them with the boot smoke at -min 100 through the named
// exclusion file, and SonarCloud waits for that merged profile.
func coverageGateFindings(src []byte) []string {
	var w ciJobs
	if err := yaml.Unmarshal(src, &w); err != nil {
		return []string{"ci.yml does not parse: " + err.Error()}
	}
	run := func(job string) string {
		var b strings.Builder
		for _, s := range w.Jobs[job].Steps {
			b.WriteString(s.Run + "\n")
		}
		return b.String()
	}
	var out []string
	for job, profile := range map[string]string{"test": "coverage-unit.out", "integration": "coverage-integration.out"} {
		if r := run(job); !strings.Contains(r, "-coverpkg=./...") || !strings.Contains(r, "-coverprofile="+profile) {
			out = append(out, "job "+job+" does not measure every package into "+profile)
		}
	}
	gate := run("coverage")
	for _, want := range []string{
		"scripts/boot-coverage.sh coverage-boot.out",
		"covergate -min 100 -exclusions test/coverage-exclusions.txt -o coverage.out",
		"coverage-unit.out coverage-integration.out coverage-boot.out",
	} {
		if !strings.Contains(gate, want) {
			out = append(out, "job coverage does not run "+want)
		}
	}
	for job, want := range map[string][]string{"coverage": {"test", "integration"}, "sonar": {"coverage"}} {
		for _, n := range want {
			if !slices.Contains(needs(w.Jobs[job].Needs), n) {
				out = append(out, "job "+job+" does not need "+n)
			}
		}
	}
	slices.Sort(out)
	return out
}

func TestCoverageGateIsWired(t *testing.T) {
	req.Covers(t, "TST-001")
	ci := readWorkflows(t)["ci.yml"]
	if ci == nil {
		t.Fatal("ci.yml not read")
	}
	if f := coverageGateFindings(ci); len(f) > 0 {
		t.Fatal(strings.Join(f, "; "))
	}
	for want, plant := range map[string][2]string{
		"job coverage does not run covergate -min 100": {"covergate -min 100", "covergate -min 95"},
		"job integration does not measure every":       {"-tags integration -covermode=atomic -coverpkg=./...", "-tags integration -covermode=atomic"},
		"job coverage does not run scripts/boot":       {"scripts/boot-coverage.sh coverage-boot.out", "true"},
		"job sonar does not need coverage":             {"needs: [test, coverage]", "needs: test"},
		"ci.yml does not parse":                        {"jobs:\n", "jobs: ["},
	} {
		bad := strings.Replace(string(ci), plant[0], plant[1], 1)
		if bad == string(ci) {
			t.Fatalf("plant for %q changed nothing", want)
		}
		if got := strings.Join(coverageGateFindings([]byte(bad)), "; "); !strings.Contains(got, want) {
			t.Errorf("planted %q, the rule said %q", want, got)
		}
	}
	if got := needs(map[string]any{}); got != nil {
		t.Errorf("needs of a map = %v, want nil", got)
	}
}
