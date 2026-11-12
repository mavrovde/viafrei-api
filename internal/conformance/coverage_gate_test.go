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

type ciStep struct {
	Name            string `yaml:"name"`
	Run             string `yaml:"run"`
	ContinueOnError any    `yaml:"continue-on-error"`
}

type ciJob struct {
	Needs           any      `yaml:"needs"`
	ContinueOnError any      `yaml:"continue-on-error"`
	Steps           []ciStep `yaml:"steps"`
}

type ciJobs struct {
	Jobs map[string]ciJob `yaml:"jobs"`
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
	out := measureFindings(w)
	for _, job := range []string{"test", "integration", "coverage"} {
		out = append(out, swallowFindings(job, w.Jobs[job])...)
	}
	out = append(out, needsFindings(w)...)
	slices.Sort(out)
	return out
}

// runOf joins the run scripts of a job's steps.
func runOf(j ciJob) string {
	var b strings.Builder
	for _, s := range j.Steps {
		b.WriteString(s.Run + "\n")
	}
	return b.String()
}

// measureFindings: both test jobs measure every package, and the coverage
// job merges their profiles with the boot smoke at -min 100.
func measureFindings(w ciJobs) []string {
	var out []string
	for job, profile := range map[string]string{"test": "coverage-unit.out", "integration": "coverage-integration.out"} {
		if r := runOf(w.Jobs[job]); !strings.Contains(r, "-coverpkg=./...") || !strings.Contains(r, "-coverprofile="+profile) {
			out = append(out, "job "+job+" does not measure every package into "+profile)
		}
	}
	gate := runOf(w.Jobs["coverage"])
	for _, want := range []string{
		"scripts/boot-coverage.sh coverage-boot.out",
		"covergate -min 100 -exclusions test/coverage-exclusions.txt -o coverage.out",
		"coverage-unit.out coverage-integration.out coverage-boot.out",
	} {
		if !strings.Contains(gate, want) {
			out = append(out, "job coverage does not run "+want)
		}
	}
	return out
}

// set: a continue-on-error value that is present and not false.
func set(v any) bool { return v != nil && fmt.Sprint(v) != "false" }

// swallowFindings: a job that may not swallow a failure, neither by
// continue-on-error on the job or a step nor by || true in a step.
func swallowFindings(job string, j ciJob) []string {
	var out []string
	if set(j.ContinueOnError) {
		out = append(out, "job "+job+" sets continue-on-error")
	}
	for i, s := range j.Steps {
		if set(s.ContinueOnError) {
			out = append(out, fmt.Sprintf("job %s step %d (%s) sets continue-on-error", job, i+1, s.Name))
		}
		if orTrue.MatchString(s.Run) {
			out = append(out, fmt.Sprintf("job %s step %d (%s) swallows a failure with || true", job, i+1, s.Name))
		}
	}
	return out
}

// needsFindings: the coverage job waits for both test jobs, Sonar for the
// merged profile and for the docs-portal job, whose LCOV of the
// accessibility runner it reads.
func needsFindings(w ciJobs) []string {
	var out []string
	for job, want := range map[string][]string{"coverage": {"test", "integration"}, "sonar": {"coverage", "docs-portal"}} {
		for _, n := range want {
			if !slices.Contains(needs(w.Jobs[job].Needs), n) {
				out = append(out, "job "+job+" does not need "+n)
			}
		}
	}
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
		"job sonar does not need coverage":             {"needs: [test, coverage, docs-portal]", "needs: [test, docs-portal]"},
		"job sonar does not need docs-portal":          {"needs: [test, coverage, docs-portal]", "needs: [test, coverage]"},
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
	explicit := strings.Replace(string(ci), "      - name: Merge and gate\n", "      - name: Merge and gate\n        continue-on-error: false\n", 1)
	if f := coverageGateFindings([]byte(explicit)); explicit == string(ci) || len(f) > 0 {
		t.Errorf("continue-on-error: false swallows nothing, yet the rule said %q", f)
	}
	if got := needs(map[string]any{}); got != nil {
		t.Errorf("needs of a map = %v, want nil", got)
	}
}
