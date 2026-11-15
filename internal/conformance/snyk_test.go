package conformance_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// Owner 2026-11-15: Snyk runs once a week on main and once per release,
// before anything is published, and never on a pull request or a push.

// snykTriggers is the whole trigger block of snyk.yml: the dispatch takes no
// input and is followed directly by the call, so a dispatch on a branch cannot
// name a commit and pass the guard the way release.yml's call does.
const snykTriggers = "on:\n  schedule:\n    - cron: \"17 5 * * 1\" # Mondays 05:17 UTC\n  workflow_dispatch:\n  workflow_call:\n    inputs:\n      ref:\n"

// snykScan is the scan step's run block, exactly: no "|| true", no lower
// threshold, the SARIF written where the upload reads it.
const snykScan = "mkdir -p sarif\n./snyk test --severity-threshold=high --sarif-file-output=sarif/snyk.sarif\n"

const (
	snykGuardStep = "The run scans main or a release's commit"
	snykSecret    = "${{ secrets.SNYK_TOKEN }}"
	snykCall      = "./.github/workflows/snyk.yml"
)

type snykStep struct {
	Name string `yaml:"name"`
	Run  string `yaml:"run"`
}

type snykJob struct {
	Uses    string            `yaml:"uses"`
	Needs   any               `yaml:"needs"`
	With    map[string]string `yaml:"with"`
	Secrets any               `yaml:"secrets"`
	Steps   []snykStep        `yaml:"steps"`
}

type snykWorkflow struct {
	On   map[string]any     `yaml:"on"`
	Jobs map[string]snykJob `yaml:"jobs"`
}

// snykGuardCases: what the guard step must answer. A skipped job would be a
// green run about a scan that never ran, so a wrong ref is exit 2.
var snykGuardCases = []struct {
	ref, gitRef string
	want        int
}{
	{"", "refs/heads/feature", 2},
	{"", "refs/pull/1/merge", 2},
	{"", "refs/heads/main", 0},
	{"0123456789abcdef0123456789abcdef01234567", "refs/heads/feature", 0},
	{"v1.2.3", "refs/heads/main", 2},
	{"0123456789ABCDEF0123456789ABCDEF01234567", "refs/heads/main", 2},
	{"0123456789abcdef0123456789abcdef0123456", "refs/heads/main", 2},
}

// runSnykGuard runs the guard's run block the way the runner does (bash
// --noprofile --norc -eo pipefail) and returns its exit status.
func runSnykGuard(run, ref, gitRef string) (int, error) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		return -1, err
	}
	cmd := exec.Command(bash, "--noprofile", "--norc", "-eo", "pipefail", "-c", run) // #nosec G204 -- the workflow's own step
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "REF_IN=" + ref, "GITHUB_REF=" + gitRef}
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exit):
		return exit.ExitCode(), nil
	}
	return -1, err
}

// secretsFindings: a caller hands snyk's token on by name and nothing else,
// never "secrets: inherit".
func secretsFindings(where string, v any) []string {
	m, ok := v.(map[string]any)
	if !ok || len(m) != 1 || fmt.Sprint(m["SNYK_TOKEN"]) != snykSecret {
		return []string{where + " does not pass exactly SNYK_TOKEN: " + snykSecret}
	}
	return nil
}

func snykFindings(files map[string][]byte) []string {
	parsed := map[string]snykWorkflow{}
	for name, src := range files {
		var w snykWorkflow
		if err := yaml.Unmarshal(src, &w); err != nil {
			return []string{name + " does not parse: " + err.Error()}
		}
		parsed[name] = w
	}
	var out []string
	sn := string(files["snyk.yml"])
	var triggers []string
	for k := range parsed["snyk.yml"].On {
		triggers = append(triggers, k)
	}
	sort.Strings(triggers)
	if !slices.Equal(triggers, []string{"schedule", "workflow_call", "workflow_dispatch"}) {
		out = append(out, fmt.Sprintf("snyk.yml runs on %v, not the weekly schedule, a dispatch and the release call alone", triggers))
	}
	if !strings.Contains(sn, snykTriggers) {
		out = append(out, "snyk.yml's trigger block is not the weekly schedule, a dispatch with no input and the release call")
	}
	job := parsed["snyk.yml"].Jobs["snyk"]
	scan, guard := false, ""
	for _, s := range job.Steps {
		if s.Run == snykScan {
			scan = true
		}
		if s.Name == snykGuardStep {
			guard = s.Run
		}
	}
	if !scan {
		out = append(out, "snyk.yml's scan step is not exactly: "+strings.TrimSpace(strings.ReplaceAll(snykScan, "\n", "; ")))
	}
	if guard == "" {
		out = append(out, "snyk.yml has no step "+snykGuardStep)
	}
	for _, c := range snykGuardCases {
		if guard == "" {
			break
		}
		got, err := runSnykGuard(guard, c.ref, c.gitRef)
		if err != nil {
			return append(out, "the guard could not be run: "+err.Error())
		}
		if got != c.want {
			out = append(out, fmt.Sprintf("the guard exits %d for REF_IN=%q on %s, not %d", got, c.ref, c.gitRef, c.want))
		}
	}
	for name, src := range files {
		if name != "snyk.yml" && strings.Contains(string(src), "snyk test") {
			out = append(out, name+" runs the Snyk CLI; only snyk.yml does")
		}
		if strings.Contains(string(src), "secrets: inherit") {
			out = append(out, name+" passes secrets: inherit")
		}
	}
	rel := parsed["release.yml"].Jobs
	if s := rel["snyk"]; s.Uses != snykCall || s.With["ref"] != "${{ needs.commit.outputs.sha }}" || !slices.Contains(needs(s.Needs), "commit") {
		out = append(out, "release.yml's snyk job does not call snyk.yml on the tagged commit")
	}
	out = append(out, secretsFindings("release.yml job snyk", rel["snyk"].Secrets)...)
	if !slices.Contains(needs(rel["publish"].Needs), "snyk") {
		out = append(out, "release.yml's publish does not need snyk")
	}
	out = append(out, secretsFindings("release-please.yml job release", parsed["release-please.yml"].Jobs["release"].Secrets)...)
	sort.Strings(out)
	return out
}

func TestSnykRunsWeeklyAndOncePerRelease(t *testing.T) {
	req.Covers(t, "SSC-014")
	wf := readWorkflows(t)
	files := map[string][]byte{}
	for _, f := range []string{"snyk.yml", "release.yml", "release-please.yml", "security.yml", "ci.yml"} {
		if wf[f] == nil {
			t.Fatalf("no %s", f)
		}
		files[f] = wf[f]
	}
	if f := snykFindings(files); len(f) > 0 {
		t.Fatal(strings.Join(f, "; "))
	}
	plant := func(file, from, to string) map[string][]byte {
		m := map[string][]byte{}
		for k, v := range files {
			m[k] = v
		}
		bad := strings.Replace(string(m[file]), from, to, 1)
		if bad == string(m[file]) {
			t.Fatalf("plant in %s changed nothing: %q", file, from)
		}
		m[file] = []byte(bad)
		return m
	}
	for want, m := range map[string]map[string][]byte{
		"the guard exits 0 for REF_IN=\"\" on refs/heads/feature":    plant("snyk.yml", `not on $GITHUB_REF"; exit 2`, `not on $GITHUB_REF"; exit 0`),
		"the guard exits 2 for REF_IN=\"\" on refs/heads/main":       plant("snyk.yml", "if [[ -n $REF_IN ]]; then", "if [[ -n x ]]; then"),
		"the guard exits 0 for REF_IN=\"v1.2.3\"":                    plant("snyk.yml", "            [[ $REF_IN =~ ^[0-9a-f]{40}$ ]] || {", "            true || {"),
		"a dispatch with no input":                                   plant("snyk.yml", "  workflow_dispatch:\n", "  workflow_dispatch:\n    inputs:\n      ref:\n        type: string\n"),
		"the weekly schedule, a dispatch and the release call alone": plant("snyk.yml", "  workflow_dispatch:\n", "  pull_request:\n  workflow_dispatch:\n"),
		"snyk.yml's scan step is not exactly":                        plant("snyk.yml", "--sarif-file-output=sarif/snyk.sarif\n", "--sarif-file-output=sarif/snyk.sarif || true\n"),
		"release.yml passes secrets: inherit":                        plant("release.yml", "    secrets:\n      SNYK_TOKEN: ${{ secrets.SNYK_TOKEN }}\n", "    secrets: inherit\n"),
		"release-please.yml job release does not pass exactly":       plant("release-please.yml", "    secrets:\n      SNYK_TOKEN: ${{ secrets.SNYK_TOKEN }}\n", "    secrets: inherit\n"),
		"release.yml's publish does not need snyk":                   plant("release.yml", "    needs: [commit, snyk]\n", "    needs: [commit]\n"),
		"security.yml runs the Snyk CLI":                             plant("security.yml", "jobs:\n", "jobs:\n  x:\n    runs-on: ubuntu-24.04\n    steps:\n      - run: ./snyk test\n"),
	} {
		t.Run(want, func(t *testing.T) {
			if got := strings.Join(snykFindings(m), "; "); !strings.Contains(got, want) {
				t.Errorf("planted %q, the rules said %q", want, got)
			}
		})
	}
}
