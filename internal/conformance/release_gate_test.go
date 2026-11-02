package conformance_test

import (
	"fmt"
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
	Uses string            `yaml:"uses"`
	Run  string            `yaml:"run"`
	With map[string]string `yaml:"with"`
}

type gatedJob struct {
	Name        string            `yaml:"name"`
	If          string            `yaml:"if"`
	Needs       any               `yaml:"needs"`
	Permissions map[string]string `yaml:"permissions"`
	Steps       []gatedStep       `yaml:"steps"`
}

type gatedWorkflow struct {
	On   map[string]any      `yaml:"on"`
	Jobs map[string]gatedJob `yaml:"jobs"`
}

var requireFlag = regexp.MustCompile(`-require "([^"]+)"`)

// releaseRequired are the ci.yml jobs a commit must have passed before a tag
// or an image exists for it (REL-015): the tests, the coverage gate, the image
// checks, the vulnerability scan and the linters.
var releaseRequired = []string{
	"Unit and handler tests",
	"Integration tests (PostGIS)",
	"Coverage gate (100 % per package)",
	"Image (build, lint, scan, healthz)",
	"govulncheck",
	"Lint",
}

// gateRule: in workflow file, job runs scripts/ci-gate.sh before the first
// step that matches publishes (a tag push, an image push, a signature).
type gateRule struct{ file, job, publishes string }

var gateRules = []gateRule{
	{"release-please.yml", "tag", "git push origin"},
	{"release.yml", "publish", "docker/build-push-action"},
	{"release.yml", "publish", "cosign sign"},
	{"image.yml", "publish", "docker/build-push-action"},
}

// gateGrants: the jobs that run the gate, and the caller job that grants
// release.yml its token, can read check runs and workflow runs.
var gateGrants = [][2]string{
	{"release-please.yml", "tag"},
	{"release-please.yml", "release"},
	{"release.yml", "publish"},
	{"image.yml", "publish"},
}

// The conditions a workflow_run job must carry: CI succeeded, and the run
// came from this repository, not a fork's branch named main.
const (
	ciSucceeded = "github.event.workflow_run.conclusion == 'success'"
	sameRepo    = "github.event.workflow_run.head_repository.full_name == github.repository"
)

// The strings the shell rules look for.
const (
	onMain        = `git merge-base --is-ancestor "$TAG^{commit}" origin/main`
	hotfixRules   = `gh api "repos/$GH_REPO/rules/branches/$on"`
	hotfixNeeds   = `for rule in pull_request non_fast_forward; do`
	hotfixReviews = `(( reviews >= 1 )) || {`
	reviewCount   = `select(.type == "pull_request") | .parameters.required_approving_review_count // 0] | max // 0`
	gateRepo      = `-repo "$GH_REPO"`
	minorGuard    = `if [[ $FINAL == true && $HIGHEST_IN_MINOR == true ]]; then echo "$IMAGE:$MINOR"; fi`
	dispatchGuard = `if [[ $EVENT == workflow_dispatch && $REF != refs/heads/main ]]; then`
	tokenFallback = "secrets.RELEASE_PLEASE_TOKEN || secrets.GITHUB_TOKEN"
	publishedRef  = `gh api "repos/$GH_REPO/releases/tags/$TAG"`
	imageExists   = `docker buildx imagetools inspect "$IMAGE:$VERSION"`
	notesOnce     = `if grep -q '^## Container image$' /tmp/notes.md; then`
)

// releaseGateFindings (REL-015): nothing is tagged or pushed for a commit CI
// has not passed, and nothing is built from code this repository's main did
// not hold. Every publishing job runs scripts/ci-gate.sh before it
// publishes, the gate requires checks that are real ci.yml job names, the
// image and the release pull request follow a successful CI run of this
// repository instead of a bare push and never check out a ref the event
// names, a dispatched image comes from main only, a release tag must lie on
// main or a ruleset-protected hotfix branch before it is signed, and :X.Y
// moves only for the newest patch of its minor.
func releaseGateFindings(files map[string][]byte, gateScript string) []string {
	parsed := map[string]gatedWorkflow{}
	for name, src := range files {
		var w gatedWorkflow
		if err := yaml.Unmarshal(src, &w); err != nil {
			return []string{name + " does not parse: " + err.Error()}
		}
		parsed[name] = w
	}
	out := gateScriptFindings(parsed["ci.yml"], gateScript)
	out = append(out, ciPushFindings(parsed["ci.yml"])...)
	out = append(out, gateOrderFindings(parsed)...)
	out = append(out, gateGrantFindings(parsed)...)
	for _, name := range []string{"image.yml", "release-please.yml"} {
		out = append(out, workflowRunFindings(name, parsed[name])...)
	}
	out = append(out, shellRuleFindings(files)...)
	slices.Sort(out)
	return out
}

// gateScriptFindings: the gate requires at least three checks, each a job
// name in ci.yml.
func gateScriptFindings(ci gatedWorkflow, gateScript string) []string {
	var out []string
	jobNames := map[string]bool{}
	for _, j := range ci.Jobs {
		jobNames[j.Name] = true
	}
	if !strings.Contains(gateScript, gateRepo) {
		out = append(out, "ci-gate.sh does not name the repository whose pushes may carry the checks")
	}
	required := requireFlag.FindAllStringSubmatch(gateScript, -1)
	named := map[string]bool{}
	for _, m := range required {
		named[m[1]] = true
	}
	for _, name := range releaseRequired {
		if !named[name] {
			out = append(out, "ci-gate.sh does not require "+name)
		}
	}
	for _, m := range required {
		if !jobNames[m[1]] {
			out = append(out, "ci-gate.sh requires "+m[1]+", which is no job name in ci.yml")
		}
	}
	return out
}

// ciPushFindings: ci.yml runs on pushes to named branches and never on a
// pushed tag. cigate trusts a push run whose head_branch is main or starts
// with hotfix/, and a tag push reports the tag's name there, so a tag named
// hotfix/x must never start a CI run.
func ciPushFindings(ci gatedWorkflow) []string {
	push, ok := ci.On["push"].(map[string]any)
	if !ok || push["branches"] == nil {
		return []string{"ci.yml's push trigger does not name its branches, so a pushed tag runs CI"}
	}
	var out []string
	for _, k := range []string{"tags", "tags-ignore"} {
		if _, has := push[k]; has {
			out = append(out, "ci.yml's push trigger carries "+k+", so a tag named hotfix/x could pass as a hotfix branch run")
		}
	}
	return out
}

// gateOrderFindings: each gated job runs the gate before it publishes.
func gateOrderFindings(parsed map[string]gatedWorkflow) []string {
	var out []string
	for _, r := range gateRules {
		steps := parsed[r.file].Jobs[r.job].Steps
		gate := slices.IndexFunc(steps, func(s gatedStep) bool { return strings.Contains(s.Run, "scripts/ci-gate.sh") })
		pub := slices.IndexFunc(steps, func(s gatedStep) bool { return strings.Contains(s.Uses+"\n"+s.Run, r.publishes) })
		switch {
		case pub < 0:
			out = append(out, r.file+" job "+r.job+" no longer has a step with "+r.publishes)
		case gate < 0 || gate > pub:
			out = append(out, r.file+" job "+r.job+" reaches "+r.publishes+" without scripts/ci-gate.sh before it")
		}
	}
	return out
}

// gateGrantFindings: the gate's token can read check runs and workflow runs.
func gateGrantFindings(parsed map[string]gatedWorkflow) []string {
	var out []string
	for _, g := range gateGrants {
		perms := parsed[g[0]].Jobs[g[1]].Permissions
		for _, scope := range []string{"checks", "actions"} {
			if perms[scope] != "read" {
				out = append(out, fmt.Sprintf("%s job %s does not grant %s: read, which the gate needs", g[0], g[1], scope))
			}
		}
	}
	return out
}

// guarded: the job's own condition requires a successful CI run of this
// repository.
func guarded(j gatedJob) bool {
	return strings.Contains(j.If, ciSucceeded) && strings.Contains(j.If, sameRepo)
}

// workflowRunFindings: the workflow starts on a completed CI run, not a
// push; every job either carries both conditions or needs a job that does;
// and no checkout takes a ref from the event.
func workflowRunFindings(name string, w gatedWorkflow) []string {
	var out []string
	if _, onPush := w.On["push"]; onPush || w.On["workflow_run"] == nil {
		out = append(out, name+" must follow a completed CI run (workflow_run), not a push")
	}
	for id, j := range w.Jobs {
		if !guarded(j) && !slices.ContainsFunc(needs(j.Needs), func(n string) bool { return guarded(w.Jobs[n]) }) {
			out = append(out, name+" job "+id+" does not require a successful CI run of this repository")
		}
		for _, s := range j.Steps {
			if strings.HasPrefix(s.Uses, "actions/checkout@") && strings.Contains(s.With["ref"], "github.event") {
				out = append(out, name+" job "+id+" checks out a ref taken from the event")
			}
		}
	}
	return out
}

// shellRuleFindings: the rules written as shell inside the workflows.
func shellRuleFindings(files map[string][]byte) []string {
	var out []string
	rel := string(files["release.yml"])
	sign := strings.Index(rel, "cosign sign")
	before := func(s string) bool { i := strings.Index(rel, s); return i >= 0 && i < sign }
	if !before(onMain) {
		out = append(out, "release.yml signs without first checking that the tag lies on main")
	}
	if !before(hotfixRules) || !before(hotfixNeeds) {
		out = append(out, "release.yml signs a hotfix tag without checking that the hotfix ruleset protects its branch")
	}
	if !before(reviewCount) || !before(hotfixReviews) {
		out = append(out, "release.yml signs a hotfix tag without requiring at least one approving review")
	}
	build := strings.Index(rel, "docker/build-push-action")
	beforeBuild := func(s string) bool { i := strings.Index(rel, s); return i >= 0 && i < build }
	if !beforeBuild(publishedRef) {
		out = append(out, "release.yml rebuilds a release without first checking that it is not published")
	}
	if !beforeBuild(imageExists) {
		out = append(out, "release.yml rebuilds a release without first checking that :X.Y.Z does not exist")
	}
	if notes := strings.Index(rel, "--json body --jq .body > /tmp/notes.md"); notes < 0 || !strings.Contains(rel[notes:], notesOnce) {
		out = append(out, "release.yml appends the image section to notes that may already carry it")
	}
	if !strings.Contains(rel, minorGuard) {
		out = append(out, "release.yml moves :X.Y without the highest-in-minor guard")
	}
	if !strings.Contains(string(files["image.yml"]), dispatchGuard) {
		out = append(out, "image.yml publishes a dispatch from a branch other than main")
	}
	if !strings.Contains(string(files["release-please.yml"]), tokenFallback) {
		out = append(out, "release-please.yml does not use RELEASE_PLEASE_TOKEN when it is set")
	}
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
	const pushBranches = "    branches: [main, \"hotfix/**\"]\n"
	checkout := "      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1\n        with:\n          persist-credentials: false\n"
	s := string(script)
	for want, c := range map[string]struct {
		files  map[string][]byte
		script string
	}{
		"release-please.yml job tag reaches git push origin without":                                  {plant("release-please.yml", gateStep, "        run: echo skipped\n"), s},
		"release.yml job publish reaches docker/build-push-action without":                            {plant("release.yml", gateStep, "        run: echo skipped\n"), s},
		"image.yml job publish reaches docker/build-push-action without":                              {plant("image.yml", gateStep, "        run: echo skipped\n"), s},
		"image.yml job publish no longer has a step":                                                  {plant("image.yml", "docker/build-push-action", "docker/build-action"), s},
		"image.yml job publish reaches docker/build-push-action without scripts/ci-gate.sh before it": {late, s},
		"release-please.yml must follow a completed CI run":                                           {plant("release-please.yml", "  workflow_run:\n    workflows: [CI]\n    types: [completed]\n    branches: [main]\n", ""), s},
		"image.yml must follow a completed CI run":                                                    {plant("image.yml", "  workflow_run:\n", "  push:\n    branches: [main]\n  workflow_run:\n"), s},
		"release-please.yml job release-please does not require":                                      {plant("release-please.yml", "(github.event.workflow_run.conclusion == 'success' && ", "("), s},
		"image.yml job head does not require a successful CI run of this repository":                  {plant("image.yml", " && "+sameRepo, ""), s},
		"release-please.yml job release-please does not require a successful CI run of this":          {plant("release-please.yml", " && "+sameRepo, ""), s},
		"image.yml job publish does not require":                                                      {plant("image.yml", "    needs: head\n", ""), s},
		"release-please.yml job tag does not require":                                                 {plant("release-please.yml", "    needs: release-please\n", "    needs: [other]\n"), s},
		"image.yml job publish checks out a ref taken from the event":                                 {plant("image.yml", checkout, strings.Replace(checkout, "        with:\n", "        with:\n          ref: ${{ github.event.workflow_run.head_sha }}\n", 1)), s},
		"image.yml job publish does not grant actions: read":                                          {plant("image.yml", "      actions: read\n      packages: write\n", "      packages: write\n"), s},
		"release-please.yml job release does not grant checks: read":                                  {plant("release-please.yml", "      checks: read\n      actions: read\n      packages: write\n", "      actions: read\n      packages: write\n"), s},
		"signs without first checking that the tag lies on main":                                      {plant("release.yml", onMain, "true"), s},
		"signs a hotfix tag without checking that the hotfix ruleset (rules API)":                     {plant("release.yml", hotfixRules, "echo"), s},
		"signs a hotfix tag without checking that the hotfix ruleset (rule list)":                     {plant("release.yml", hotfixNeeds, "for rule in; do"), s},
		"signs a hotfix tag without requiring at least one approving review (no count)":               {plant("release.yml", reviewCount, "length"), s},
		"signs a hotfix tag without requiring at least one approving review (no test)":                {plant("release.yml", hotfixReviews, "true || {"), s},
		"signs a hotfix tag without requiring at least one approving review (zero)":                   {plant("release.yml", hotfixReviews, "(( reviews >= 0 )) || {"), s},
		"ci-gate.sh does not name the repository":                                                     {files, strings.Replace(s, gateRepo, "", 1)},
		"moves :X.Y without the highest-in-minor guard":                                               {plant("release.yml", "$FINAL == true && $HIGHEST_IN_MINOR == true ]]; then echo \"$IMAGE:$MINOR\"", "$FINAL == true ]]; then echo \"$IMAGE:$MINOR\""), s},
		"image.yml publishes a dispatch from a branch other than main":                                {plant("image.yml", dispatchGuard, "if false; then"), s},
		"does not use RELEASE_PLEASE_TOKEN":                                                           {plant("release-please.yml", tokenFallback, "secrets.GITHUB_TOKEN"), s},
		"rebuilds a release without first checking that it is not published":                          {plant("release.yml", publishedRef, `echo "repos/$GH_REPO"`), s},
		"rebuilds a release without first checking that :X.Y.Z does not exist":                        {plant("release.yml", imageExists, `echo "$IMAGE:$VERSION"`), s},
		"appends the image section to notes that may already carry it":                                {plant("release.yml", notesOnce, "if false; then"), s},
		"which is no job name in ci.yml":                                                              {files, strings.Replace(s, `"Coverage gate (100 % per package)"`, `"Coverage"`, 1)},
		"does not require Unit and handler tests":                                                     {files, strings.Replace(s, `-require "Unit and handler tests"`, "", 1)},
		"does not require Image (build, lint, scan, healthz)":                                         {files, strings.Replace(s, `-require "Image (build, lint, scan, healthz)"`, "", 1)},
		"does not require govulncheck":                                                                {files, strings.Replace(s, `-require "govulncheck"`, "", 1)},
		"does not require Lint":                                                                       {files, strings.Replace(s, `-require "Lint"`, "", 1)},
		"push trigger carries tags,":                                                                  {plant("ci.yml", pushBranches, pushBranches+"    tags: [\"hotfix/**\"]\n"), s},
		"push trigger carries tags-ignore":                                                            {plant("ci.yml", pushBranches, pushBranches+"    tags-ignore: [\"v*\"]\n"), s},
		"push trigger does not name its branches (bare)":                                              {plant("ci.yml", pushBranches, ""), s},
		"push trigger does not name its branches (ignore list)":                                       {plant("ci.yml", pushBranches, "    branches-ignore: [docs]\n"), s},
		"ci.yml does not parse":                                                                       {plant("ci.yml", "jobs:\n", "jobs: [\n"), s},
	} {
		w := strings.Split(want, " (")[0]
		if got := strings.Join(releaseGateFindings(c.files, c.script), "; "); !strings.Contains(got, w) {
			t.Errorf("planted %q, the rule said %q", want, got)
		}
	}
}
