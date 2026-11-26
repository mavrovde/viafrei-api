package conformance_test

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// snykScanStep is the scan step whole (name, env and run, nothing between)
// and the head of the upload after it: no "|| true", no lower threshold, and
// the SARIF uploaded only when it belongs to the commit code scanning files
// it under.
const snykScanStep = "\n      - name: Snyk test\n        env:\n          SNYK_TOKEN: ${{ secrets.SNYK_TOKEN }}\n        run: |\n          mkdir -p sarif\n          ./snyk test --severity-threshold=high --sarif-file-output=sarif/snyk.sarif\n      - name: Upload SARIF to code scanning\n" +
	"        # A called run scans another commit than the caller's github.sha; its findings are\n        # uploaded only when they belong to the commit code scanning files them under.\n" +
	"        if: always() && hashFiles('sarif/snyk.sarif') != '' && (inputs.ref == '' || inputs.ref == github.sha)\n"

// snykCheckout: the scan checks out the commit it was handed, or the run's own.
const snykCheckout = "\n        with:\n          ref: ${{ inputs.ref || github.sha }}\n          persist-credentials: false\n"

// publishHeader: publish waits for the scan and carries no condition that
// could run it past a failed one.
const publishHeader = "\n  publish:\n    name: Build, sign, attest, publish\n    needs: [commit, snyk, mutation]\n    runs-on: ubuntu-24.04\n"

// publishBinding: the commit built is the commit Snyk scanned.
const publishBinding = `[[ $(git rev-parse "$TAG^{commit}") == "$SCANNED" ]] || {`

const (
	snykGuardStep = "The run scans main or a release's commit"
	snykSecret    = "${{ secrets.SNYK_TOKEN }}"
	snykCall      = "./.github/workflows/snyk.yml"
)

type snykStep struct {
	ID   string            `yaml:"id"`
	Name string            `yaml:"name"`
	Env  map[string]string `yaml:"env"`
	Run  string            `yaml:"run"`
}

type snykJob struct {
	Uses    string            `yaml:"uses"`
	If      string            `yaml:"if"`
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

const (
	tagObject = "1111111111111111111111111111111111111111"
	tagCommit = "2222222222222222222222222222222222222222"
)

// commitJobCases: what release.yml's commit job resolves, against a stub gh
// that answers the two API reads. An annotated tag is dereferenced to its
// commit; anything that is not a commit in the end is refused.
var commitJobCases = []struct {
	name, tag, ref, deref string
	want                  int
	sha                   string
}{
	{"an annotated tag", "v1.2.3", "tag " + tagObject, "commit " + tagCommit, 0, tagCommit},
	{"a lightweight tag", "v1.2.3", "commit " + tagCommit, "", 0, tagCommit},
	{"an rc tag", "v1.2.3-rc.1", "tag " + tagObject, "commit " + tagCommit, 1, ""},
	{"a tag of a tree", "v1.2.3", "tag " + tagObject, "tree " + tagCommit, 1, ""},
	{"a tag that does not resolve", "v1.2.3", "", "", 1, ""},
	{"not a release tag", "v1.2", "commit " + tagCommit, "", 1, ""},
}

func bashExit(err error) (int, error) {
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0, nil
	case errors.As(err, &exit):
		return exit.ExitCode(), nil
	}
	return -1, err
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
	return bashExit(cmd.Run())
}

// runCommitJob runs the commit job's step against a stub gh and returns its
// exit status and the sha it wrote to GITHUB_OUTPUT.
func runCommitJob(run, tag, ref, deref string) (int, string, error) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		return -1, "", err
	}
	dir, err := os.MkdirTemp("", "commit-job")
	if err != nil {
		return -1, "", err
	}
	defer func() { _ = os.RemoveAll(dir) }()
	stub := "#!/bin/sh\ncase \"$*\" in\n  *git/ref/tags/*) [ -n \"$STUB_REF\" ] && echo \"$STUB_REF\" ;;\n  *git/tags/*) [ -n \"$STUB_TAG\" ] && echo \"$STUB_TAG\" ;;\nesac\n"
	if err := os.WriteFile(filepath.Join(dir, "gh"), []byte(stub), 0o700); err != nil { // #nosec G306 -- an executable stub in a private temp dir
		return -1, "", err
	}
	out := filepath.Join(dir, "output")
	cmd := exec.Command(bash, "--noprofile", "--norc", "-eo", "pipefail", "-c", run) // #nosec G204 -- the workflow's own step
	cmd.Env = []string{"PATH=" + dir + ":" + os.Getenv("PATH"), "TAG=" + tag, "GH_REPO=o/r", "GITHUB_OUTPUT=" + out, "STUB_REF=" + ref, "STUB_TAG=" + deref}
	code, err := bashExit(cmd.Run())
	b, _ := os.ReadFile(out) // #nosec G304 -- the temp file this function made
	return code, strings.TrimPrefix(strings.TrimSpace(string(b)), "sha="), err
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

func stepRun(j snykJob, match func(snykStep) bool) (snykStep, bool) {
	i := slices.IndexFunc(j.Steps, match)
	if i < 0 {
		return snykStep{}, false
	}
	return j.Steps[i], true
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
	if !strings.Contains(sn, snykScanStep) {
		out = append(out, "snyk.yml's scan step and SARIF upload are not exactly the high-threshold scan and the own-commit upload")
	}
	if !strings.Contains(sn, snykCheckout) {
		out = append(out, "snyk.yml does not check out ${{ inputs.ref || github.sha }}")
	}
	if strings.Contains(sn, "continue-on-error") {
		out = append(out, "snyk.yml carries continue-on-error")
	}
	guard, ok := stepRun(parsed["snyk.yml"].Jobs["snyk"], func(s snykStep) bool { return s.Name == snykGuardStep })
	if !ok {
		out = append(out, "snyk.yml has no step "+snykGuardStep)
	}
	for _, c := range snykGuardCases {
		if !ok {
			break
		}
		got, err := runSnykGuard(guard.Run, c.ref, c.gitRef)
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
		if name != "release.yml" && strings.Contains(string(src), "workflows/snyk.yml") {
			out = append(out, name+" references workflows/snyk.yml; only release.yml calls it")
		}
		if strings.Contains(string(src), "secrets: inherit") {
			out = append(out, name+" passes secrets: inherit")
		}
	}
	rel := parsed["release.yml"]
	if s := rel.Jobs["snyk"]; s.Uses != snykCall || s.With["ref"] != "${{ needs.commit.outputs.sha }}" || !slices.Contains(needs(s.Needs), "commit") {
		out = append(out, "release.yml's snyk job does not call snyk.yml on the tagged commit")
	}
	out = append(out, secretsFindings("release.yml job snyk", rel.Jobs["snyk"].Secrets)...)
	// release.yml runs on its own (a tag push or a dispatch), so the token it
	// hands snyk.yml is the repository secret itself; as a called workflow it
	// would hold only what a caller passed it.
	if _, called := rel.On["workflow_call"]; called {
		out = append(out, "release.yml is a called workflow (workflow_call); it runs on its own and hands snyk.yml the repository's SNYK_TOKEN")
	}
	if !slices.Contains(needs(rel.Jobs["publish"].Needs), "snyk") {
		out = append(out, "release.yml's publish does not need snyk")
	}
	if !slices.Contains(needs(rel.Jobs["publish"].Needs), "mutation") {
		out = append(out, "release.yml's publish does not need the mutation run")
	}
	if !strings.Contains(string(files["release.yml"]), publishHeader) {
		out = append(out, "release.yml's publish header is not exactly needs: [commit, snyk, mutation] with no condition")
	}
	if rel.Jobs["publish"].If != "" {
		out = append(out, "release.yml's publish carries a condition, which could let it run past a failed scan")
	}
	if tag, ok := stepRun(rel.Jobs["publish"], func(s snykStep) bool { return s.ID == "tag" }); !ok || tag.Env["SCANNED"] != "${{ needs.commit.outputs.sha }}" || !strings.Contains(tag.Run, publishBinding) {
		out = append(out, "release.yml's publish does not refuse a tag that names another commit than the one Snyk scanned")
	}
	commit, ok := stepRun(rel.Jobs["commit"], func(s snykStep) bool { return s.ID == "commit" })
	if !ok {
		out = append(out, "release.yml has no commit job")
	}
	for _, c := range commitJobCases {
		if !ok {
			break
		}
		got, sha, err := runCommitJob(commit.Run, c.tag, c.ref, c.deref)
		if err != nil {
			return append(out, "the commit job could not be run: "+err.Error())
		}
		if got != c.want || (c.want == 0 && sha != c.sha) {
			out = append(out, fmt.Sprintf("the commit job exits %d with sha %q for %s, not %d with %q", got, sha, c.name, c.want, c.sha))
		}
	}
	if strings.Contains(string(files["release-please.yml"]), "SNYK_TOKEN") {
		out = append(out, "release-please.yml names SNYK_TOKEN; only release.yml hands it to snyk.yml")
	}
	sort.Strings(out)
	return out
}

func TestSnykRunsWeeklyAndOncePerRelease(t *testing.T) {
	req.Covers(t, "SSC-014")
	files := readWorkflows(t)
	for _, f := range []string{"snyk.yml", "release.yml", "release-please.yml", "security.yml", "ci.yml"} {
		if files[f] == nil {
			t.Fatalf("no %s", f)
		}
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
	const deref = "          if [[ $type == tag ]]; then read -r type sha"
	for want, m := range map[string]map[string][]byte{
		"the guard exits 0 for REF_IN=\"\" on refs/heads/feature":    plant("snyk.yml", `not on $GITHUB_REF"; exit 2`, `not on $GITHUB_REF"; exit 0`),
		"the guard exits 2 for REF_IN=\"\" on refs/heads/main":       plant("snyk.yml", "if [[ -n $REF_IN ]]; then", "if [[ -n x ]]; then"),
		"the guard exits 0 for REF_IN=\"v1.2.3\"":                    plant("snyk.yml", "            [[ $REF_IN =~ ^[0-9a-f]{40}$ ]] || {", "            true || {"),
		"a dispatch with no input":                                   plant("snyk.yml", "  workflow_dispatch:\n", "  workflow_dispatch:\n    inputs:\n      ref:\n        type: string\n"),
		"the weekly schedule, a dispatch and the release call alone": plant("snyk.yml", "  workflow_dispatch:\n", "  pull_request:\n  workflow_dispatch:\n"),
		"snyk.yml's scan step and SARIF upload are not exactly":      plant("snyk.yml", "--sarif-file-output=sarif/snyk.sarif\n", "--sarif-file-output=sarif/snyk.sarif || true\n"),
		"the own-commit upload":                                      plant("snyk.yml", " && (inputs.ref == '' || inputs.ref == github.sha)", ""),
		"snyk.yml does not check out":                                plant("snyk.yml", "ref: ${{ inputs.ref || github.sha }}", "ref: ${{ github.sha }}"),
		"snyk.yml carries continue-on-error":                         plant("snyk.yml", "    timeout-minutes: 30\n", "    timeout-minutes: 30\n    continue-on-error: true\n"),
		"ci.yml references workflows/snyk.yml":                       plant("ci.yml", "\njobs:\n", "\njobs:\n  s:\n    uses: ./.github/workflows/snyk.yml\n"),
		"release.yml passes secrets: inherit":                        plant("release.yml", "    secrets:\n      SNYK_TOKEN: ${{ secrets.SNYK_TOKEN }}\n\n", "    secrets: inherit\n\n"),
		"release-please.yml names SNYK_TOKEN":                        plant("release-please.yml", "\n  approve:\n", "\n  scan:\n    env:\n      SNYK_TOKEN: ${{ secrets.SNYK_TOKEN }}\n\n  approve:\n"),
		"release.yml is a called workflow":                           plant("release.yml", "on:\n  push:", "on:\n  workflow_call:\n    secrets:\n      SNYK_TOKEN:\n        required: true\n  push:"),
		"release.yml job snyk does not pass exactly":                 plant("release.yml", "      SNYK_TOKEN: ${{ secrets.SNYK_TOKEN }}\n\n", "      SNYK_TOKEN: ${{ secrets.OTHER }}\n\n"),
		"release.yml's publish does not need snyk":                   plant("release.yml", "    needs: [commit, snyk, mutation]\n", "    needs: [commit, mutation]\n"),
		"release.yml's publish does not need the mutation run":       plant("release.yml", "    needs: [commit, snyk, mutation]\n", "    needs: [commit, snyk]\n"),
		"release.yml's publish header is not exactly":                plant("release.yml", "    needs: [commit, snyk, mutation]\n", "    needs: [commit, snyk, mutation]\n    if: always()\n"),
		"another commit than the one Snyk scanned":                   plant("release.yml", publishBinding, "true || {"),
		"for an annotated tag":                                       plant("release.yml", deref, "          if false; then read -r type sha"),
		"for a tag of a tree":                                        plant("release.yml", "          [[ $type == commit ]] || {", "          true || {"),
		"for an rc tag": plant("release.yml", `[[ $TAG =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo "::error::'$TAG' is not a confirmed release vX.Y.Z; an rc or any other suffix publishes no image"; exit 1; }
          read -r type sha`, "read -r type sha"),
		"security.yml runs the Snyk CLI": plant("security.yml", "jobs:\n", "jobs:\n  x:\n    runs-on: ubuntu-24.04\n    steps:\n      - run: ./snyk test\n"),
	} {
		t.Run(want, func(t *testing.T) {
			if got := strings.Join(snykFindings(m), "; "); !strings.Contains(got, want) {
				t.Errorf("planted %q, the rules said %q", want, got)
			}
		})
	}
}
