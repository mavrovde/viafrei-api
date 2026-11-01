package conformance_test

import (
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
			Uses string `yaml:"uses"`
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
	var out []string
	if w.Permissions == nil {
		out = append(out, name+": no top-level permissions block")
	} else if s, ok := w.Permissions.(string); ok && strings.Contains(s, "write") {
		out = append(out, name+": top-level permissions grant "+s)
	}
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
