package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// source serves one fixed answer on the path a source is asked on. Tests
// never reach the public registry or API: -base names this server.
func source(t *testing.T, path string, status int, body string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != path {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func runOut(args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb)
	return code, out.String(), errb.String()
}

func TestUsage(t *testing.T) {
	req.Covers(t, "REL-001")
	for _, args := range [][]string{nil, {"sync"}, {"read", "-nope"}, {"read"}, {"read", "-timeout", "1s", "-source", "pypi"}, {"apply", "-nope"}, {"apply"}, {"apply", "-version", "v2.5.21"}} {
		if code, _, _ := runOut(args...); code != 2 {
			t.Errorf("%q exited %d, want 2", args, code)
		}
	}
}

func TestReadFromNPM(t *testing.T) {
	req.Covers(t, "REL-001")
	base := source(t, "/viafrei", 200, `{"name":"viafrei","dist-tags":{"latest":"2.5.21","next":"2.6.0"}}`)
	code, out, errs := runOut("read", "-timeout", "5s", "-base", base+"/")
	if code != 0 || out != "2.5.21\n" {
		t.Fatalf("exit %d, out %q, err %q", code, out, errs)
	}
}

func TestReadFromGitHub(t *testing.T) {
	req.Covers(t, "REL-001")
	base := source(t, "/repos/mavrovde/viafrei-mcp/releases/latest", 200, `{"tag_name":"v2.5.21","draft":false,"prerelease":false}`)
	code, out, errs := runOut("read", "-timeout", "5s", "-source", "github", "-base", base)
	if code != 0 || out != "2.5.21\n" {
		t.Fatalf("exit %d, out %q, err %q", code, out, errs)
	}
}

func TestReadDefaultsToThePublicBase(t *testing.T) {
	req.Covers(t, "REL-001")
	saved := defaultBase["npm"]
	t.Cleanup(func() { defaultBase["npm"] = saved })
	defaultBase["npm"] = source(t, "/viafrei", 200, `{"dist-tags":{"latest":"3.0.0"}}`)
	if code, out, _ := runOut("read", "-timeout", "5s"); code != 0 || out != "3.0.0\n" {
		t.Fatalf("exit %d, out %q", code, out)
	}
}

// Every unreadable answer is exit 2: the sync fails closed.
func TestReadFailsClosed(t *testing.T) {
	req.Covers(t, "REL-001")
	npm := func(status int, body string) []string {
		return []string{"read", "-timeout", "5s", "-base", source(t, "/viafrei", status, body)}
	}
	gh := func(body string) []string {
		return []string{"read", "-timeout", "5s", "-source", "github", "-base", source(t, "/repos/mavrovde/viafrei-mcp/releases/latest", 200, body)}
	}
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	short := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte(`{"dist-tags":`))
	}))
	t.Cleanup(short.Close)
	cases := map[string][]string{
		"server error":         npm(500, `{"dist-tags":{"latest":"2.5.21"}}`),
		"not found":            npm(404, ``),
		"not JSON":             npm(200, `<html>`),
		"no latest tag":        npm(200, `{"dist-tags":{}}`),
		"pre-release":          npm(200, `{"dist-tags":{"latest":"2.6.0-rc.1"}}`),
		"too large":            npm(200, strings.Repeat(" ", maxBody+1)),
		"no answer":            {"read", "-timeout", "5s", "-base", closed.URL},
		"cut short":            {"read", "-timeout", "5s", "-base", short.URL},
		"bad URL":              {"read", "-timeout", "5s", "-base", "http://bad\x7f"},
		"github not JSON":      gh(`[`),
		"github draft":         gh(`{"tag_name":"v2.5.21","draft":true}`),
		"github pre-release":   gh(`{"tag_name":"v2.6.0","prerelease":true}`),
		"github tag without v": gh(`{"tag_name":"2.5.21"}`),
		"github not a version": gh(`{"tag_name":"vnext"}`),
	}
	for name, args := range cases {
		code, out, errs := runOut(args...)
		if code != 2 || out != "" || !strings.Contains(errs, "cannot read the platform version") {
			t.Errorf("%s: exit %d, out %q, err %q; want exit 2 and nothing printed", name, code, out, errs)
		}
	}
}

// repo is a repository root holding the two files apply reads.
func repo(t *testing.T, platform, api string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "internal", "buildinfo"), 0o755); err != nil {
		t.Fatal(err)
	}
	for path, s := range map[string]string{platformFile: platform, versionFile: api} {
		if s == "" {
			continue
		}
		if err := os.WriteFile(filepath.Join(dir, path), []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func platformOf(t *testing.T, dir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, platformFile))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// The sync against a fixture version: the platform file holds it, and the
// commit message carries the CHANGELOG line and the Release-As footer that
// makes release-please cut that version.
func TestApplyStartsAndAdvancesTheSync(t *testing.T) {
	req.Covers(t, "REL-001")
	dir := repo(t, "unsynced\n", "0.2.1\n")
	msg := filepath.Join(t.TempDir(), "msg")
	code, out, errs := runOut("apply", "-dir", dir, "-version", "2.5.21", "-start", "-message", msg)
	if code != 0 || out != "changed 2.5.21\n" {
		t.Fatalf("exit %d, out %q, err %q", code, out, errs)
	}
	if got := platformOf(t, dir); got != "2.5.21\n" {
		t.Fatalf("platform file %q", got)
	}
	b, _ := os.ReadFile(msg)
	lines := strings.Split(string(b), "\n")
	if lines[0] != "deps: keep in step with ViaFrei platform 2.5.21" || !strings.Contains(string(b), "\n\nRelease-As: 2.5.21\n") {
		t.Fatalf("commit message %q", b)
	}
	if code, out, _ := runOut("apply", "-dir", dir, "-version", "2.5.22", "-message", msg); code != 0 || out != "changed 2.5.22\n" {
		t.Fatalf("advance: exit %d, out %q", code, out)
	}
	if code, out, _ := runOut("apply", "-dir", dir, "-version", "2.5.22"); code != 0 || out != "unchanged 2.5.22\n" {
		t.Fatalf("same version: exit %d, out %q", code, out)
	}
}

func TestApplyAcceptsThePlatformEqualToTheAPI(t *testing.T) {
	req.Covers(t, "REL-001")
	dir := repo(t, "unsynced\n", "2.5.21\n")
	if code, out, _ := runOut("apply", "-dir", dir, "-version", "2.5.21", "-start", "-message", filepath.Join(t.TempDir(), "m")); code != 0 || out != "changed 2.5.21\n" {
		t.Fatalf("exit %d, out %q", code, out)
	}
}

func TestApplyIsIdleUntilStarted(t *testing.T) {
	req.Covers(t, "REL-001")
	dir := repo(t, "unsynced\n", "0.2.1\n")
	if code, out, _ := runOut("apply", "-dir", dir, "-version", "2.5.21"); code != 0 || out != "not-started 2.5.21\n" {
		t.Fatalf("exit %d, out %q", code, out)
	}
	if got := platformOf(t, dir); got != "unsynced\n" {
		t.Fatalf("an unstarted sync wrote %q", got)
	}
}

func TestApplyNeverGoesBack(t *testing.T) {
	req.Covers(t, "REL-001")
	for name, c := range map[string][2]string{
		"older than the synced platform": {"2.5.21\n", "0.2.1\n"},
		"older than the API":             {"unsynced\n", "3.0.0\n"},
	} {
		dir := repo(t, c[0], c[1])
		code, _, errs := runOut("apply", "-dir", dir, "-version", "2.5.20", "-start", "-message", filepath.Join(t.TempDir(), "m"))
		if code != 1 || !strings.Contains(errs, "refused") {
			t.Errorf("%s: exit %d, err %q", name, code, errs)
		}
		if got := platformOf(t, dir); got != c[0] {
			t.Errorf("%s: a refused sync wrote %q", name, got)
		}
	}
}

func TestApplyCannotReadOrWrite(t *testing.T) {
	req.Covers(t, "REL-001")
	msg := filepath.Join(t.TempDir(), "m")
	readOnly := repo(t, "2.5.0\n", "0.2.1\n")
	if err := os.Chmod(filepath.Join(readOnly, platformFile), 0o444); err != nil {
		t.Fatal(err)
	}
	cases := map[string]struct{ dir, msg string }{
		"no platform file":         {repo(t, "", "0.2.1\n"), msg},
		"malformed platform file":  {repo(t, "2.5\n", "0.2.1\n"), msg},
		"no version file":          {repo(t, "2.5.0\n", ""), msg},
		"malformed version file":   {repo(t, "2.5.0\n", "0.2\n"), msg},
		"no message file named":    {repo(t, "2.5.0\n", "0.2.1\n"), ""},
		"message cannot be saved":  {repo(t, "2.5.0\n", "0.2.1\n"), filepath.Join(t.TempDir(), "no", "m")},
		"platform cannot be saved": {readOnly, msg},
	}
	for name, c := range cases {
		if code, out, _ := runOut("apply", "-dir", c.dir, "-version", "2.5.21", "-message", c.msg); code != 2 || out != "" {
			t.Errorf("%s: exit %d, out %q; want exit 2", name, code, out)
		}
	}
}
