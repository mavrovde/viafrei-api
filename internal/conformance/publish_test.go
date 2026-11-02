package conformance_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

var (
	publishFlag  = regexp.MustCompile(`(?:^|\s)(?:-p|--publish)[ =]"?([^"\s]+)`)
	singleQuoted = regexp.MustCompile(`'[^']*'`)
)

// publishFindings: a script that runs a container publishes its ports on the
// loopback address only, as compose.yaml does, never on every interface of
// the machine it runs on.
func publishFindings(name, src string) []string {
	var out []string
	for _, m := range publishFlags(src) {
		if !strings.HasPrefix(m[1], "[::1]:") {
			out = append(out, name+" publishes "+m[1]+" beyond the loopback address")
		}
	}
	return out
}

// publishFlags returns the -p/--publish values of every docker run command,
// continuation lines joined; a mkdir -p is not a published port, and neither
// is anything in a single-quoted command the container runs (sh -c '...').
func publishFlags(src string) [][]string {
	var out [][]string
	for _, line := range strings.Split(strings.ReplaceAll(src, "\\\n", " "), "\n") {
		if strings.Contains(line, "docker run") {
			out = append(out, publishFlag.FindAllStringSubmatch(singleQuoted.ReplaceAllString(line, "''"), -1)...)
		}
	}
	return out
}

func TestScriptsPublishOnLoopbackOnly(t *testing.T) {
	req.Covers(t, "SSC-017")
	files, err := filepath.Glob(filepath.Join(root, "scripts", "*.sh"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no scripts read (%v)", err)
	}
	published := 0
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		published += len(publishFlags(string(src)))
		for _, finding := range publishFindings(filepath.Base(f), string(src)) {
			t.Error(finding)
		}
	}
	if n := len(publishFlags("docker run img sh -c 'mkdir -p /w && run'")); n != 0 {
		t.Errorf("a quoted container command read as %d published port(s)", n)
	}
	if published == 0 {
		t.Fatal("no script publishes a port; the rule read nothing (image-smoke.sh does)")
	}
	for _, bad := range []string{"docker run -d \\\n  -p \"$port:8080\" \\\n  img", `docker run --publish=8080:8080 x`, "docker run -p [::]:1:1 x\nmkdir -p dir", "docker run -p 9:9 img sh -c 'mkdir -p /w'"} {
		if len(publishFindings("plant", bad)) != 1 {
			t.Errorf("the rule passed %q", bad)
		}
	}
}
