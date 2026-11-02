package conformance_test

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

type ciJobs struct {
	Jobs map[string]struct {
		Needs           any `yaml:"needs"`
		ContinueOnError any `yaml:"continue-on-error"`
		Steps           []struct {
			Name            string `yaml:"name"`
			Run             string `yaml:"run"`
			ContinueOnError any    `yaml:"continue-on-error"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// orTrue matches a command whose failure is discarded: "|| true" or "|| :".
var orTrue = regexp.MustCompile(`\|\|\s*(true|:)(\s|;|$)`)

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
// exclusion file, and SonarCloud waits for that merged profile. None of the
// three jobs may swallow a failure: no continue-on-error on the job or a step,
// and no "|| true" in a step that runs.
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
	for _, job := range []string{"test", "integration", "coverage"} {
		j := w.Jobs[job]
		if j.ContinueOnError != nil && fmt.Sprint(j.ContinueOnError) != "false" {
			out = append(out, "job "+job+" sets continue-on-error")
		}
		for i, s := range j.Steps {
			if s.ContinueOnError != nil && fmt.Sprint(s.ContinueOnError) != "false" {
				out = append(out, fmt.Sprintf("job %s step %d (%s) sets continue-on-error", job, i+1, s.Name))
			}
			if orTrue.MatchString(s.Run) {
				out = append(out, fmt.Sprintf("job %s step %d (%s) swallows a failure with || true", job, i+1, s.Name))
			}
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
		"job coverage sets continue-on-error":          {"    name: Coverage gate (100 % per package)\n", "    name: Coverage gate (100 % per package)\n    continue-on-error: true\n"},
		"(Merge and gate) sets continue-on-error":      {"      - name: Merge and gate\n", "      - name: Merge and gate\n        continue-on-error: ${{ true }}\n"},
		"(Merge and gate) swallows a failure":          {"coverage-unit.out coverage-integration.out coverage-boot.out", "coverage-unit.out coverage-integration.out coverage-boot.out || true"},
		"swallows a failure with || true":              {"-coverprofile=coverage-integration.out", "-coverprofile=coverage-integration.out || :"},
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
