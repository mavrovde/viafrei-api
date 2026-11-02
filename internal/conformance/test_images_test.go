package conformance_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/testcontainers/testcontainers-go"

	"github.com/mavrovde/viafrei-api/internal/testkit/pgtest"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// testImageFindings (SSC-005): the images the tests run are pinned by
// digest. The PostGIS image of pgtest is the Dockerfile's devdb image, digest
// and all, and CI pulls exactly it; the reaper testcontainers-go starts by
// tag is pulled by digest and tagged with the name that version asks for.
func testImageFindings(image, reaper, dockerfile, ci string) []string {
	var out []string
	if !pinnedFrom.MatchString(image) {
		out = append(out, "pgtest.Image is not pinned by digest")
	}
	if !strings.Contains(dockerfile, "FROM "+image+" AS devdb") {
		out = append(out, "pgtest.Image is not the Dockerfile's devdb image")
	}
	if !regexp.MustCompile(`(?m)^\s+docker pull ` + regexp.QuoteMeta(image) + `$`).MatchString(ci) {
		out = append(out, "ci.yml does not pull pgtest.Image by digest")
	}
	m := regexp.MustCompile(`(?m)^\s+docker pull (` + regexp.QuoteMeta(reaper) + `@sha256:[0-9a-f]{64})$`).FindStringSubmatch(ci)
	if m == nil {
		out = append(out, "ci.yml does not pull the reaper "+reaper+" by digest")
	} else if !strings.Contains(ci, "docker tag "+m[1]+" "+reaper+"\n") {
		out = append(out, "ci.yml does not tag the pinned reaper as "+reaper)
	}
	if regexp.MustCompile(`(?m)docker pull [^\s@]+\s*$`).MatchString(ci) {
		out = append(out, "ci.yml pulls an image by tag alone")
	}
	return out
}

func readText(t *testing.T, path ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{root}, path...)...))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTestImagesArePinned(t *testing.T) {
	req.Covers(t, "SSC-005")
	dockerfile := readText(t, "Dockerfile")
	ci := readText(t, ".github", "workflows", "ci.yml")
	// The deprecated alias is the only exported name of the image the
	// reaper is started from; it equals the internal value it was replaced by.
	reaper := testcontainers.ReaperDefaultImage //nolint:staticcheck // see above
	for _, f := range testImageFindings(pgtest.Image, reaper, dockerfile, ci) {
		t.Error(f)
	}
	unpinned := strings.SplitN(pgtest.Image, "@", 2)[0]
	pull := "docker pull " + pgtest.Image + "\n"
	// Each plant must produce the finding named for it, not just any finding.
	for want, c := range map[string][3]string{
		"pgtest.Image is not pinned by digest": {unpinned, dockerfile, ci},
		"is not the Dockerfile's devdb image":  {pgtest.Image, strings.Replace(dockerfile, " AS devdb", " AS other", 1), ci},
		"does not pull pgtest.Image by digest": {pgtest.Image, dockerfile, strings.Replace(ci, pull, "docker pull "+unpinned+"@sha256:"+strings.Repeat("0", 64)+"\n", 1)},
		"does not pull the reaper":             {pgtest.Image, dockerfile, strings.Replace(ci, "docker pull "+reaper+"@", "docker pull other/ryuk:1@", 1)},
		"does not tag the pinned reaper":       {pgtest.Image, dockerfile, strings.Replace(ci, "docker tag ", "echo ", 1)},
		"ci.yml pulls an image by tag alone":   {pgtest.Image, dockerfile, strings.Replace(ci, pull, pull+"          docker pull busybox:1\n", 1)},
	} {
		if c == [3]string{pgtest.Image, dockerfile, ci} {
			t.Fatalf("plant for %q changed nothing", want)
		}
		if got := strings.Join(testImageFindings(c[0], reaper, c[1], c[2]), "; "); !strings.Contains(got, want) {
			t.Errorf("planted %q, the rules said %q", want, got)
		}
	}
}
