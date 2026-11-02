package conformance_test

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

var pinnedAction = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+@[0-9a-f]{40}$`)

type workflowFile struct {
	Permissions any `yaml:"permissions"`
	Jobs        map[string]struct {
		Uses           string `yaml:"uses"`
		TimeoutMinutes any    `yaml:"timeout-minutes"`
		Steps          []struct {
			Uses string         `yaml:"uses"`
			With map[string]any `yaml:"with"`
		} `yaml:"steps"`
	} `yaml:"jobs"`
}

// workflowFindings lists every rule a workflow breaks (SSC-005, TST-017): a
// third-party action not pinned to a full commit SHA, no top-level
// least-privilege permissions block, or a job without a timeout. A job that
// calls a reusable workflow cannot carry a timeout; the called workflow's
// jobs carry their own.
func workflowFindings(name string, src []byte) []string {
	var w workflowFile
	if err := yaml.Unmarshal(src, &w); err != nil {
		return []string{name + ": does not parse: " + err.Error()}
	}
	if len(w.Jobs) == 0 {
		return []string{name + ": no job read"}
	}
	out := permissionFindings(name, w.Permissions)
	check := func(job, uses string) {
		if uses == "" || strings.HasPrefix(uses, "./") {
			return
		}
		if !pinnedAction.MatchString(uses) {
			out = append(out, name+": job "+job+" uses an action not pinned to a commit SHA: "+uses)
		}
	}
	for id, j := range w.Jobs {
		check(id, j.Uses)
		if j.Uses == "" && j.TimeoutMinutes == nil {
			out = append(out, name+": job "+id+" has no timeout-minutes")
		}
		for _, s := range j.Steps {
			check(id, s.Uses)
		}
	}
	sort.Strings(out)
	return out
}

// permissionFindings: the top level must set permissions, and never a write-all.
func permissionFindings(name string, perms any) []string {
	if perms == nil {
		return []string{name + ": no top-level permissions block"}
	}
	if s, ok := perms.(string); ok && strings.Contains(s, "write") {
		return []string{name + ": top-level permissions grant " + s}
	}
	return nil
}

func readWorkflows(t *testing.T) map[string][]byte {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(root, ".github", "workflows", "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) < 4 {
		t.Fatalf("read %d workflow file(s); a check over too few files proves nothing", len(paths))
	}
	out := map[string][]byte{}
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(p)] = b
	}
	return out
}

func TestWorkflowsArePinnedBoundedAndLeastPrivilege(t *testing.T) {
	req.Covers(t, "SSC-005", "TST-017")
	for name, src := range readWorkflows(t) {
		for _, f := range workflowFindings(name, src) {
			t.Error(f)
		}
	}
}

func TestWorkflowRulesCanSayNo(t *testing.T) {
	req.Covers(t, "SSC-005", "TST-017")
	good := readWorkflows(t)["ci.yml"]
	if good == nil {
		t.Fatal("ci.yml not read")
	}
	if f := workflowFindings("ci.yml", good); len(f) > 0 {
		t.Fatalf("the unplanted file already has findings: %v", f)
	}
	sha := regexp.MustCompile(`(actions/checkout)@[0-9a-f]{40}`)
	for want, bad := range map[string]string{
		"not pinned to a commit SHA: actions/checkout@v7": sha.ReplaceAllString(string(good), "${1}@v7"),
		"no top-level permissions block":                  strings.Replace(string(good), "permissions:\n  contents: read\n", "", 1),
		"top-level permissions grant write-all":           strings.Replace(string(good), "permissions:\n  contents: read\n", "permissions: write-all\n", 1),
		"job lint has no timeout-minutes":                 strings.Replace(string(good), "    name: Lint\n    runs-on: ubuntu-24.04\n    timeout-minutes: 10\n", "    name: Lint\n    runs-on: ubuntu-24.04\n", 1),
	} {
		if bad == string(good) {
			t.Fatalf("plant for %q changed nothing", want)
		}
		if got := strings.Join(workflowFindings("ci.yml", []byte(bad)), "; "); !strings.Contains(got, want) {
			t.Errorf("planted %q, the rules said %q", want, got)
		}
	}
}

// TestCosignIdentityNamesRealWorkflows: the identity users verify against
// (docs/container.md, the release notes) names the two publishing workflows,
// so renaming either would make every published signature unverifiable.
func TestCosignIdentityNamesRealWorkflows(t *testing.T) {
	req.Covers(t, "SSC-005")
	wf := readWorkflows(t)
	doc, err := os.ReadFile(filepath.Join(root, "docs", "container.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"image.yml", "release.yml"} {
		if wf[name] == nil {
			t.Errorf("the cosign identity names %s, which does not exist", name)
		}
		if !strings.Contains(string(wf[name]), `(image|release)\.yml@refs/(heads/main|tags/v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?)$`) {
			t.Errorf("%s does not state the identity regexp of docs/container.md", name)
		}
	}
	if !strings.Contains(string(doc), `(image|release)\.yml@refs/(heads/main|tags/v[0-9]+\.[0-9]+\.[0-9]+(-rc\.[0-9]+)?)$`) {
		t.Error("docs/container.md no longer carries the identity regexp")
	}
}

// setupGoFindings: every actions/setup-go step names the exact toolchain of
// go.mod. setup-go reads only the go line from go-version-file, so a job
// using it built with an older patch release than the one pinned (and
// govulncheck reported that release's standard-library vulnerabilities).
func setupGoFindings(name string, src []byte, toolchain string) []string {
	var w workflowFile
	if err := yaml.Unmarshal(src, &w); err != nil {
		return []string{name + ": does not parse"}
	}
	var out []string
	for id, j := range w.Jobs {
		for _, s := range j.Steps {
			if !strings.HasPrefix(s.Uses, "actions/setup-go@") {
				continue
			}
			if got := fmt.Sprint(s.With["go-version"]); got != toolchain {
				out = append(out, fmt.Sprintf("%s: job %s sets up Go %q, go.mod pins %s", name, id, got, toolchain))
			}
		}
	}
	return out
}

func TestWorkflowsUseTheGoModToolchain(t *testing.T) {
	req.Covers(t, "SSC-005")
	mod, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^toolchain go([0-9]+\.[0-9]+\.[0-9]+)$`).FindSubmatch(mod)
	if m == nil {
		t.Fatal("go.mod has no toolchain line")
	}
	toolchain := string(m[1])
	steps := 0
	for name, src := range readWorkflows(t) {
		steps += strings.Count(string(src), "actions/setup-go@")
		for _, f := range setupGoFindings(name, src, toolchain) {
			t.Error(f)
		}
	}
	if steps == 0 {
		t.Fatal("no setup-go step read")
	}
	bad := strings.Replace(string(readWorkflows(t)["ci.yml"]), `go-version: "`+toolchain+`"`, "go-version-file: go.mod", 1)
	if len(setupGoFindings("ci.yml", []byte(bad), toolchain)) == 0 {
		t.Error("a planted go-version-file step was not reported")
	}
}

// securityJobs are the checks of the Secure SDLC page, one job each (SSC-003, SSC-014).
var securityJobs = []string{"codeql", "semgrep", "gitleaks", "osv", "trivy-fs", "snyk", "dependency-review", "licenses", "checkov", "zap"}

func securityFindings(src []byte, zap string) []string {
	var w workflowFile
	if err := yaml.Unmarshal(src, &w); err != nil {
		return []string{"security.yml does not parse"}
	}
	var out []string
	for _, id := range securityJobs {
		if _, ok := w.Jobs[id]; !ok {
			out = append(out, "security.yml has no job "+id)
		}
	}
	// DAST may only ever scan the compose container (TST-010).
	if !strings.Contains(zap, "-t http://api:8080/v1/openapi.json") || !strings.Contains(zap, "-O http://api:8080") {
		out = append(out, "zap-scan.sh does not pin its target to the compose API container")
	}
	return out
}

func TestSecurityWorkflowRunsEveryCheck(t *testing.T) {
	req.Covers(t, "SSC-003", "SSC-012", "SSC-014", "TST-010")
	wf := readWorkflows(t)
	zap, err := os.ReadFile(filepath.Join(root, "scripts", "zap-scan.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if wf["scorecard.yml"] == nil {
		t.Error("no scorecard.yml")
	}
	for _, f := range securityFindings(wf["security.yml"], string(zap)) {
		t.Error(f)
	}
	for want, bad := range map[string][2]string{
		"no job gitleaks":         {strings.Replace(string(wf["security.yml"]), "\n  gitleaks:\n", "\n  leaks:\n", 1), string(zap)},
		"does not pin its target": {string(wf["security.yml"]), strings.Replace(string(zap), "-O http://api:8080", "", 1)},
	} {
		if got := strings.Join(securityFindings([]byte(bad[0]), bad[1]), "; "); !strings.Contains(got, want) {
			t.Errorf("planted %q, the rules said %q", want, got)
		}
	}
}
