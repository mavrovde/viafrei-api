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
// digest. The PostGIS image of pgtest is the platform's postgis/postgis:16-3.4
// and compose.yaml's db image, digest and all, and CI pulls exactly it
// and the viafrei-database release database.pin names (pinRef); the reaper
// testcontainers-go starts by tag is pulled by digest and tagged with the
// name that version asks for.
func testImageFindings(image, pinRef, reaper, compose, ci string) []string {
	var out []string
	if !pinnedFrom.MatchString(image) {
		out = append(out, "pgtest.Image is not pinned by digest")
	}
	if !strings.HasPrefix(image, "postgis/postgis:16-3.4@") {
		out = append(out, "pgtest.Image is not the platform's postgis/postgis:16-3.4")
	}
	if !regexp.MustCompile(`(?m)^\s+database=\$\(go run \./internal/release/pinguard\)\n\s+docker pull "\$database"$`).MatchString(ci) {
		out = append(out, "ci.yml does not pull the release database.pin names, as pinguard prints it")
	}
	if !strings.Contains(compose, "  db:\n    image: "+image+"\n") {
		out = append(out, "pgtest.Image is not compose.yaml's db image")
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
	// "$database" is what pinguard printed: a digest-pinned release or nothing.
	if regexp.MustCompile(`(?m)docker pull [^\s@"$][^\s@]*\s*$`).MatchString(ci) {
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
	composeSrc := readText(t, "compose.yaml")
	ci := readText(t, ".github", "workflows", "ci.yml")
	// The deprecated alias is the only exported name of the image the
	// reaper is started from; it equals the internal value it was replaced by.
	reaper := testcontainers.ReaperDefaultImage //nolint:staticcheck // see above
	pin := readPin(t).Ref
	for _, f := range testImageFindings(pgtest.Image, pin, reaper, composeSrc, ci) {
		t.Error(f)
	}
	unpinned := strings.SplitN(pgtest.Image, "@", 2)[0]
	pull := "docker pull " + pgtest.Image + "\n"
	// Each plant must produce the finding named for it, not just any finding.
	// The pulls stand in each job that starts a test database (integration,
	// parity): a plant changes every one of them.
	for want, c := range map[string][3]string{
		"pgtest.Image is not pinned by digest":         {unpinned, composeSrc, ci},
		"is not the platform's postgis/postgis:16-3.4": {strings.Replace(pgtest.Image, ":16-3.4@", ":18-3.6@", 1), strings.Replace(composeSrc, ":16-3.4@", ":18-3.6@", 1), strings.Replace(ci, ":16-3.4@", ":18-3.6@", 1)},
		"does not pull the release database.pin names": {pgtest.Image, composeSrc, strings.ReplaceAll(ci, `docker pull "$database"`, "docker pull other/database:9.9.9@sha256:"+strings.Repeat("0", 64))},
		"is not compose.yaml's db image":               {pgtest.Image, strings.Replace(composeSrc, "  db:\n    image: postgis/", "  db:\n    image: other/", 1), ci},
		"does not pull pgtest.Image by digest":         {pgtest.Image, composeSrc, strings.ReplaceAll(ci, pull, "docker pull "+unpinned+"@sha256:"+strings.Repeat("0", 64)+"\n")},
		"does not pull the reaper":                     {pgtest.Image, composeSrc, strings.ReplaceAll(ci, "docker pull "+reaper+"@", "docker pull other/ryuk:1@")},
		"does not tag the pinned reaper":               {pgtest.Image, composeSrc, strings.ReplaceAll(ci, "docker tag ", "echo ")},
		"ci.yml pulls an image by tag alone":           {pgtest.Image, composeSrc, strings.Replace(ci, pull, pull+"          docker pull busybox:1\n", 1)},
	} {
		if c == [3]string{pgtest.Image, composeSrc, ci} {
			t.Fatalf("plant for %q changed nothing", want)
		}
		if got := strings.Join(testImageFindings(c[0], pin, reaper, c[1], c[2]), "; "); !strings.Contains(got, want) {
			t.Errorf("planted %q, the rules said %q", want, got)
		}
	}
}
