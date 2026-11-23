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

var (
	ghRelease = regexp.MustCompile(`\bgh\s+release\s+(edit|create)\b`)
	latestSet = regexp.MustCompile(`(?:^|\s)--latest(?:[=\s]|$)`)
	draftOff  = regexp.MustCompile(`(?:^|\s)--draft=false(?:\s|$)`)
	draftOn   = regexp.MustCompile(`(?:^|\s)--draft(?:=true)?(?:\s|$)`)
)

// latestOnDraftFindings (REL-003): GitHub refuses to mark a draft release
// latest (HTTP 422, "Latest release cannot be draft or prerelease"), and the
// refusal stops the publishing step before the draft is ever published; the
// v1.0.1 page stayed a draft that way. So no gh release edit may set --latest
// on a release that is still a draft: the same call carries --draft=false, or
// an earlier gh release edit of the same script already published it. The
// publishing call (--draft=false) itself carries --latest, because GitHub's
// make_latest defaults to true for a newly published release and would make
// any backport latest. A gh release create that makes a draft never sets
// --latest. script is one run block or one shell file, continuation lines
// joined.
func latestOnDraftFindings(name, script string) []string {
	var out []string
	published := false
	for _, line := range strings.Split(strings.ReplaceAll(script, "\\\n", " "), "\n") {
		m := ghRelease.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		latest := latestSet.MatchString(line)
		if m[1] == "create" {
			if latest && draftOn.MatchString(line) {
				out = append(out, name+" creates a draft release and marks it latest in one call")
			}
			continue
		}
		if draftOff.MatchString(line) {
			published = true
			if !latest {
				out = append(out, name+" publishes a release without --latest (GitHub then makes it latest by default)")
			}
		}
		if latest && !published {
			out = append(out, name+" marks a release latest while it is still a draft (no --draft=false in or before the call)")
		}
	}
	return out
}

// releaseScripts returns every run block of the workflows, named file/job#step,
// and every shell script under scripts/, each as its own script.
func releaseScripts(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for name, src := range readWorkflows(t) {
		var w gatedWorkflow
		if err := yaml.Unmarshal(src, &w); err != nil {
			t.Fatalf("%s does not parse: %v", name, err)
		}
		for job, j := range w.Jobs {
			for i, s := range j.Steps {
				if s.Run != "" {
					out[fmt.Sprintf("%s/%s#%d", name, job, i+1)] = s.Run
				}
			}
		}
	}
	files, err := filepath.Glob(filepath.Join(root, "scripts", "*.sh"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no scripts read (%v)", err)
	}
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out["scripts/"+filepath.Base(f)] = string(src)
	}
	return out
}

func TestReleaseIsPublishedBeforeItIsLatest(t *testing.T) {
	req.Covers(t, "REL-003")
	scripts := releaseScripts(t)
	names := make([]string, 0, len(scripts))
	for name := range scripts {
		names = append(names, name)
	}
	slices.Sort(names)
	marks := 0
	for _, name := range names {
		for _, line := range strings.Split(scripts[name], "\n") {
			if ghRelease.MatchString(line) && latestSet.MatchString(line) {
				marks++
			}
		}
		for _, f := range latestOnDraftFindings(name, scripts[name]) {
			t.Error(f)
		}
	}
	if marks == 0 {
		t.Fatal("no gh release call sets --latest; the rule read nothing (release.yml does)")
	}
	const latest = `--latest="$([[ $PRERELEASE == false && $HIGHEST == true ]] && echo true || echo false)"`
	create := `gh release create "$TAG" --verify-tag --draft --title "$TAG"` + "\n"
	const (
		onDraft   = "while it is still a draft"
		noLatest  = "without --latest"
		createdAs = "creates a draft release and marks it latest"
	)
	for name, c := range map[string]struct{ want, script string }{
		"latest first, published after (v1.0.1)": {onDraft, create + `gh release edit "$TAG" --notes-file n.md --prerelease=false ` + latest + "\n" + `gh release edit "$TAG" --draft=false`},
		"latest without publishing":              {onDraft, create + `gh release edit "$TAG" --notes-file n.md ` + latest},
		"latest on an explicit draft":            {onDraft, create + `gh release edit "$TAG" --draft=true --latest=true`},
		"latest on a bare draft flag":            {onDraft, `gh release edit "$TAG" --draft --latest`},
		"latest over a continuation line":        {onDraft, create + "gh release edit \"$TAG\" \\\n  --latest=true \\\n  --notes-file n.md\ngh release edit \"$TAG\" --draft=false --latest=true"},
		"publish flag only in a later call":      {onDraft, `gh release edit v1 --latest` + "\n" + `gh release edit v1 --draft=false --latest`},
		"published, then latest (split)":         {noLatest, create + `gh release edit "$TAG" --notes-file n.md --draft=false` + "\n" + `gh release edit "$TAG" ` + latest},
		"published without latest (backport)":    {noLatest, create + `gh release edit "$TAG" --notes-file n.md --prerelease=false --draft=false`},
		"created as a draft and marked latest":   {createdAs, `gh release create v1 --draft --latest`},
	} {
		t.Run(name, func(t *testing.T) {
			if got := strings.Join(latestOnDraftFindings("plant", c.script), "; "); !strings.Contains(got, c.want) {
				t.Errorf("planted %q, the rule said %q", c.script, got)
			}
		})
	}
	for _, good := range []string{
		create + `gh release edit "$TAG" --notes-file n.md --prerelease=false --draft=false ` + latest,
		create + `gh release edit "$TAG" --draft=false --latest=false` + "\n" + `gh release edit "$TAG" --latest=true`,
		`gh release create v1 --latest --title v1`,
		`gh release edit v1 --notes-file n.md`,
	} {
		if f := latestOnDraftFindings("plant", good); len(f) != 0 {
			t.Errorf("the rule refused %q: %v", good, f)
		}
	}
}
