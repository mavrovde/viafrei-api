package conformance_test

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

type gatedStep struct {
	Uses string `yaml:"uses"`
	Run  string `yaml:"run"`
}

type gatedWorkflow struct {
	On   map[string]any `yaml:"on"`
	Jobs map[string]struct {
		Name  string      `yaml:"name"`
		If    string      `yaml:"if"`
		Steps []gatedStep `yaml:"steps"`
	} `yaml:"jobs"`
}

var requireFlag = regexp.MustCompile(`-require "([^"]+)"`)

// gateRule: in workflow file, job runs scripts/ci-gate.sh before the first
// step that matches publishes (a tag push, an image push, a signature).
type gateRule struct{ file, job, publishes string }

var gateRules = []gateRule{
	{"release-please.yml", "tag", "git push origin"},
	{"release.yml", "publish", "docker/build-push-action"},
	{"release.yml", "publish", "cosign sign"},
	{"image.yml", "publish", "docker/build-push-action"},
}

// releaseGateFindings (REL-015): nothing is tagged or pushed for a commit CI
// has not passed. Every publishing job runs scripts/ci-gate.sh before it
// publishes, the gate requires checks that are real ci.yml job names, the
// image and the release pull request follow a successful CI run instead of a
// bare push, a release tag must lie on main or a hotfix branch before it is
// signed, and :X.Y moves only for the newest patch of its minor.
func releaseGateFindings(files map[string][]byte, gateScript string) []string {
	var out []string
	parsed := map[string]gatedWorkflow{}
	for name, src := range files {
		var w gatedWorkflow
		if err := yaml.Unmarshal(src, &w); err != nil {
			return []string{name + " does not parse: " + err.Error()}
		}
		parsed[name] = w
	}
	jobNames := map[string]bool{}
	for _, j := range parsed["ci.yml"].Jobs {
		jobNames[j.Name] = true
	}
	required := requireFlag.FindAllStringSubmatch(gateScript, -1)
	if len(required) < 3 {
		out = append(out, "ci-gate.sh requires fewer than three checks")
	}
	for _, m := range required {
		if !jobNames[m[1]] {
			out = append(out, "ci-gate.sh requires "+m[1]+", which is no job name in ci.yml")
		}
	}
	for _, r := range gateRules {
		steps := parsed[r.file].Jobs[r.job].Steps
		gate := slices.IndexFunc(steps, func(s gatedStep) bool { return strings.Contains(s.Run, "scripts/ci-gate.sh") })
		pub := -1
		for i, s := range steps {
			if strings.Contains(s.Uses+"\n"+s.Run, r.publishes) {
				pub = i
				break
			}
		}
		switch {
		case pub < 0:
			out = append(out, r.file+" job "+r.job+" no longer has a step with "+r.publishes)
		case gate < 0 || gate > pub:
			out = append(out, r.file+" job "+r.job+" reaches "+r.publishes+" without scripts/ci-gate.sh before it")
		}
	}
	for _, name := range []string{"image.yml", "release-please.yml"} {
		w := parsed[name]
		if _, onPush := w.On["push"]; onPush || w.On["workflow_run"] == nil {
			out = append(out, name+" must follow a completed CI run (workflow_run), not a push")
		}
		for id, j := range w.Jobs {
			if id != "tag" && id != "release" && !strings.Contains(j.If, "github.event.workflow_run.conclusion == 'success'") {
				out = append(out, name+" job "+id+" does not require a successful CI run")
			}
		}
	}
	rel := string(files["release.yml"])
	ancestor, sign := strings.Index(rel, `git merge-base --is-ancestor "$TAG^{commit}" origin/main`), strings.Index(rel, "cosign sign")
	if ancestor < 0 || ancestor > sign {
		out = append(out, "release.yml signs without first checking that the tag lies on main")
	}
	if !strings.Contains(rel, `if [[ $FINAL == true && $HIGHEST_IN_MINOR == true ]]; then echo "$IMAGE:$MINOR"; fi`) {
		out = append(out, "release.yml moves :X.Y without the highest-in-minor guard")
	}
	if !strings.Contains(string(files["release-please.yml"]), "secrets.RELEASE_PLEASE_TOKEN || secrets.GITHUB_TOKEN") {
		out = append(out, "release-please.yml does not use RELEASE_PLEASE_TOKEN when it is set")
	}
	slices.Sort(out)
	return out
}

func TestReleaseGateIsWired(t *testing.T) {
	req.Covers(t, "REL-015")
	files := readWorkflows(t)
	script, err := os.ReadFile(filepath.Join(root, "scripts", "ci-gate.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if f := releaseGateFindings(files, string(script)); len(f) > 0 {
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
	gateStep := "        run: scripts/ci-gate.sh \"$SHA\"\n"
	// The gate moved after the push: the image step runs it once the image is
	// already published.
	late := plant("image.yml", gateStep, "        run: echo skipped\n")
	late["image.yml"] = []byte(strings.Replace(string(late["image.yml"]), "        run: cosign sign --yes \"${IMAGE}@${DIGEST}\"\n", "        run: scripts/ci-gate.sh \"$SHA\" && cosign sign --yes \"${IMAGE}@${DIGEST}\"\n", 1))
	for want, c := range map[string]struct {
		files  map[string][]byte
		script string
	}{
		"release-please.yml job tag reaches git push origin without":                                  {plant("release-please.yml", gateStep, "        run: echo skipped\n"), string(script)},
		"release.yml job publish reaches docker/build-push-action without":                            {plant("release.yml", gateStep, "        run: echo skipped\n"), string(script)},
		"image.yml job publish reaches docker/build-push-action without":                              {plant("image.yml", gateStep, "        run: echo skipped\n"), string(script)},
		"image.yml job publish no longer has a step":                                                  {plant("image.yml", "docker/build-push-action", "docker/build-action"), string(script)},
		"image.yml job publish reaches docker/build-push-action without scripts/ci-gate.sh before it": {late, string(script)},
		"release-please.yml must follow a completed CI run":                                           {plant("release-please.yml", "  workflow_run:\n    workflows: [CI]\n    types: [completed]\n    branches: [main]\n", ""), string(script)},
		"image.yml must follow a completed CI run":                                                    {plant("image.yml", "  workflow_run:\n", "  push:\n    branches: [main]\n  workflow_run:\n"), string(script)},
		"release-please.yml job release-please does not require":                                      {plant("release-please.yml", "(github.event.workflow_run.conclusion == 'success' && ", "("), string(script)},
		"signs without first checking that the tag lies on main":                                      {plant("release.yml", "git merge-base --is-ancestor \"$TAG^{commit}\" origin/main", "true"), string(script)},
		"moves :X.Y without the highest-in-minor guard":                                               {plant("release.yml", "$FINAL == true && $HIGHEST_IN_MINOR == true ]]; then echo \"$IMAGE:$MINOR\"", "$FINAL == true ]]; then echo \"$IMAGE:$MINOR\""), string(script)},
		"does not use RELEASE_PLEASE_TOKEN":                                                           {plant("release-please.yml", "secrets.RELEASE_PLEASE_TOKEN || secrets.GITHUB_TOKEN", "secrets.GITHUB_TOKEN"), string(script)},
		"which is no job name in ci.yml":                                                              {files, strings.Replace(string(script), `"Coverage gate (100 % per package)"`, `"Coverage"`, 1)},
		"requires fewer than three checks":                                                            {files, strings.Replace(string(script), `-require "Unit and handler tests"`, "", 1)},
		"ci.yml does not parse":                                                                       {plant("ci.yml", "jobs:\n", "jobs: [\n"), string(script)},
	} {
		if got := strings.Join(releaseGateFindings(c.files, c.script), "; "); !strings.Contains(got, want) {
			t.Errorf("planted %q, the rule said %q", want, got)
		}
	}
}
