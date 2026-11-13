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

// permissionFindings: the top level must set permissions and grant no write,
// neither write-all nor a single scope such as contents: write. A job that
// writes names that scope in its own block.
func permissionFindings(name string, perms any) []string {
	switch p := perms.(type) {
	case nil:
		return []string{name + ": no top-level permissions block"}
	case string:
		if strings.Contains(p, "write") {
			return []string{name + ": top-level permissions grant " + p}
		}
	case map[string]any:
		var out []string
		for scope, level := range p {
			if fmt.Sprint(level) == "write" {
				out = append(out, name+": top-level permissions grant "+scope+": write")
			}
		}
		sort.Strings(out)
		return out
	}
	return nil
}

func readWorkflows(t *testing.T) map[string][]byte {
	t.Helper()
	return readWorkflowsIn(t, filepath.Join(root, ".github", "workflows"))
}

// readWorkflowsIn reads every workflow in dir under both extensions GitHub
// runs, so a .yaml file cannot escape the rules a .yml file is held to.
func readWorkflowsIn(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	var paths []string
	for _, ext := range []string{"*.yml", "*.yaml"} {
		m, err := filepath.Glob(filepath.Join(dir, ext))
		if err != nil {
			t.Fatal(err)
		}
		paths = append(paths, m...)
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
		"top-level permissions grant contents: write":     strings.Replace(string(good), "permissions:\n  contents: read\n", "permissions:\n  contents: write\n", 1),
		"top-level permissions grant packages: write":     strings.Replace(string(good), "permissions:\n  contents: read\n", "permissions:\n  contents: read\n  packages: write\n", 1),
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
// (docs/container.md, the release notes) names the one publishing workflow,
// so renaming it would make every published signature unverifiable, and it
// accepts release tags without a suffix only: images are published for
// confirmed releases alone.
func TestCosignIdentityNamesRealWorkflows(t *testing.T) {
	req.Covers(t, "SSC-005")
	wf := readWorkflows(t)
	doc, err := os.ReadFile(filepath.Join(root, "docs", "container.md"))
	if err != nil {
		t.Fatal(err)
	}
	const identity = `^https://github\.com/mavrovde/viafrei-api/\.github/workflows/release\.yml@refs/(heads/main|tags/v[0-9]+\.[0-9]+\.[0-9]+)$`
	if wf["release.yml"] == nil {
		t.Error("the cosign identity names release.yml, which does not exist")
	}
	if !strings.Contains(string(wf["release.yml"]), identity) {
		t.Error("release.yml does not state the identity regexp of docs/container.md")
	}
	if !strings.Contains(string(doc), identity) {
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
var securityJobs = []string{"codeql", "gitleaks", "snyk", "dependency-review", "licenses", "zap"}

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

// sarifUploads: every scanner that can write SARIF hands it to GitHub code
// scanning, so its findings are listed in the Security tab (SSC-014). The
// key is "workflow/job" (a "#n" suffix tells apart several uploads of one
// job), the value the upload's category. CodeQL and Scorecard upload through
// their own actions. It is also the whole set: one tool per kind of check
// (docs/security-checks.md), so an upload under any other category is a
// second tool doing a job one of these already does, and is reported.
var sarifUploads = map[string]string{
	"security.yml/gitleaks": "gitleaks",
	"security.yml/snyk":     "snyk-open-source",
	"security.yml/zap":      "zap",
	"ci.yml/openapi":        "spectral",
	"ci.yml/vulncheck":      "govulncheck",
	"ci.yml/image":          "trivy-image",
	"ci.yml/image#2":        "hadolint",
}

const uploadSarif = "github/codeql-action/upload-sarif@"

func sarifFindings(files map[string][]byte) []string {
	got := map[string]map[string]bool{}
	for name, src := range files {
		var w workflowFile
		if err := yaml.Unmarshal(src, &w); err != nil {
			return []string{name + " does not parse"}
		}
		for id, job := range w.Jobs {
			for _, st := range job.Steps {
				if !strings.HasPrefix(st.Uses, uploadSarif) {
					continue
				}
				key := name + "/" + id
				if got[key] == nil {
					got[key] = map[string]bool{}
				}
				c, _ := st.With["category"].(string)
				got[key][c] = true
			}
		}
	}
	var out []string
	want := map[string]map[string]bool{}
	for key, cat := range sarifUploads {
		job, _, _ := strings.Cut(key, "#")
		if want[job] == nil {
			want[job] = map[string]bool{}
		}
		want[job][cat] = true
		if !got[job][cat] {
			out = append(out, job+" does not upload SARIF with category "+cat)
		}
	}
	for job, cats := range got {
		for cat := range cats {
			if job == "scorecard.yml/analysis" && cat == "" {
				continue // Scorecard's own SARIF, uploaded without a category
			}
			if !want[job][cat] {
				out = append(out, job+" uploads SARIF with category "+cat+", which is not in the tool set")
			}
		}
	}
	sort.Strings(out)
	return out
}

func TestScannersUploadSARIF(t *testing.T) {
	req.Covers(t, "SSC-014")
	wf := readWorkflows(t)
	if f := sarifFindings(wf); len(f) > 0 {
		t.Fatal(strings.Join(f, "; "))
	}
	for want, plant := range map[string][3]string{
		"security.yml/gitleaks does not upload SARIF with category gitleaks":           {"security.yml", "category: gitleaks", "category: other"},
		"security.yml/zap does not upload SARIF with category zap":                     {"security.yml", "category: zap", "category: other"},
		"ci.yml/image does not upload SARIF with category trivy-image":                 {"ci.yml", "category: trivy-image", "category: other"},
		"ci.yml/image uploads SARIF with category other, which is not in the tool set": {"ci.yml", "category: hadolint", "category: other"},
		"ci.yml/openapi does not upload SARIF with category spectral":                  {"ci.yml", uploadSarif, "github/other-action@"},
	} {
		t.Run(want, func(t *testing.T) {
			m := map[string][]byte{}
			for k, v := range wf {
				m[k] = v
			}
			bad := strings.Replace(string(m[plant[0]]), plant[1], plant[2], 1)
			if bad == string(m[plant[0]]) {
				t.Fatalf("plant in %s changed nothing: %q", plant[0], plant[1])
			}
			m[plant[0]] = []byte(bad)
			if got := strings.Join(sarifFindings(m), "; "); !strings.Contains(got, want) {
				t.Errorf("planted %q, the rules said %q", want, got)
			}
		})
	}
}

func TestWorkflowsAreReadUnderBothExtensions(t *testing.T) {
	req.Covers(t, "SSC-005")
	dir := t.TempDir()
	for _, n := range []string{"a.yml", "b.yml", "c.yml", "d.yaml"} {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("on: push\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, ok := readWorkflowsIn(t, dir)["d.yaml"]; !ok {
		t.Error("a .yaml workflow was not read, so no rule is applied to it")
	}
}
