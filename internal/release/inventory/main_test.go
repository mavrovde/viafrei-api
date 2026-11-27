package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

const commit = "566552fb65c8c5b3ec7e3019e559387f2baf249b"

func write(t *testing.T, dir, name, src string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func call(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestUsage(t *testing.T) {
	req.Covers(t, "REL-015")
	for name, args := range map[string][]string{"no command": nil, "an unknown command": {"nope"}, "no version": {"web"}, "a bad flag": {"age", "-x"}} {
		if code, out, _ := call(args...); code != 2 || out != "" {
			t.Errorf("%s: exit %d, stdout %q", name, code, out)
		}
	}
}

func TestRoutes(t *testing.T) {
	req.Covers(t, "REL-015")
	dir := t.TempDir()
	a := write(t, dir, "app.ts", "export const P_ROUTE_PREFIX = \"/internal/p/\";\nexport const C_CARD = \"departures\";\n"+
		"app.get(`${P_ROUTE_PREFIX}:slug/${C_CARD}`, (req, res) => {});\napp.get(\"/healthz\", h);\napp.post(S_ROUTE, h);\napp.get(\"/healthz\", h);\n")
	b := write(t, dir, "spot.ts", "export const S_ROUTE = `/internal/spot`;\n")
	code, out, errs := call("routes", "-version", "2.6.18", "-commit", commit, a, b)
	if code != 0 || out != "# source routes 2.6.18 "+commit+"\n# method\tpath\nGET\t/internal/p/:slug/departures\nGET\t/healthz\nPOST\t/internal/spot\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errs)
	}
	for name, c := range map[string]struct{ src, want string }{
		"an unknown constant":      {"app.get(NOPE_ROUTE, h);\n", "unknown constant NOPE_ROUTE"},
		"an unknown interpolation": {"app.get(`${NOPE}/x`, h);\n", "unknown constant ${NOPE}"},
		"no route":                 {"const x = 1;\n", "no route read"},
	} {
		p := write(t, dir, "bad.ts", c.src)
		if code, _, errs := call("routes", "-version", "2.6.18", "-commit", commit, p); code != 2 || !strings.Contains(errs, c.want) {
			t.Errorf("%s: exit %d, %q", name, code, errs)
		}
	}
	if code, _, _ := call("routes", "-version", "2.6.18", "-commit", commit, filepath.Join(dir, "none.ts")); code != 2 {
		t.Errorf("a missing file: exit %d", code)
	}
	code, out, errs = call("routes", "-version", "2.6.18", "-commit", commit, "-opaque", "own-internal="+a, a, b)
	if code != 0 || !strings.HasSuffix(out, "# method\tpath\nGET\town-internal-01\nGET\town-internal-02\nPOST\town-internal-03\n") || strings.Contains(out, "/internal/") {
		t.Fatalf("opaque: exit %d, stdout %q, stderr %q", code, out, errs)
	}
	if code, _, errs := call("routes", "-version", "2.6.18", "-commit", commit, "-opaque", "x="+filepath.Join(dir, "other.ts"), a, b); code != 2 || !strings.Contains(errs, "not a FILE argument") {
		t.Errorf("an opaque file that is no argument: exit %d, %q", code, errs)
	}
	if code, _, errs := call("routes", "-version", "2.6.18", "-commit", commit, "-opaque", "X=y", a); code != 2 || !strings.Contains(errs, "want PREFIX=FILE") {
		t.Errorf("a bad opaque flag: exit %d, %q", code, errs)
	}
	if got := (opaques{"f": "p"}).String(); got != "map[f:p]" {
		t.Errorf("String() = %q", got)
	}
	if code, _, _ := call("routes", "-x"); code != 2 {
		t.Errorf("a bad flag: exit %d", code)
	}
}

func TestFragmentPaths(t *testing.T) {
	req.Covers(t, "REL-015")
	for line, want := range map[string]string{
		"get(`${o.baseUrl}/internal/place/${slug}/card/${card}?lang=${lang}`)":   "/internal/place/{}/card/{}",
		"f(`${o.baseUrl}/internal/${areaPathOf(target, { a: 1 })}?x`)":           "/internal/{}",
		`const P = "/internal/news";`:                                            "/internal/news",
		"x('/internal/a/', y) + `${b}/internal/c/${encodeURIComponent(road)}/d`": "/internal/a /internal/c/{}/d",
		"broken `${o.baseUrl}/internal/x/${unclosed":                             "/internal/x/{}",
		"nothing here": "",
	} {
		if got := strings.Join(fragmentPaths(line), " "); got != want {
			t.Errorf("%s: %q, want %q", line, got, want)
		}
	}
}

func TestWeb(t *testing.T) {
	req.Covers(t, "REL-015")
	dir := t.TempDir()
	write(t, dir, "lib/a.ts", "/** `/internal/comment` */\n// `/internal/comment2`\nget(`${u}/internal/tiles/${id}`);\nget(`${u}/internal/tiles/${id}?v`);\nget(`${u}/internal/news`);\n")
	write(t, dir, "lib/a.test.ts", "get(`${u}/internal/test-only`);\n")
	write(t, dir, "layouts/P.astro", "fetch(\"/internal/news\")\n")
	write(t, dir, "lib/notes.md", "/internal/doc\n")
	code, out, errs := call("web", "-version", "0.1.1", "-commit", commit, "-expand", "/internal/tiles/{}=fuel,lifts", dir)
	want := "# source web 0.1.1 " + commit + "\n# expanded /internal/tiles/{} = fuel,lifts\n# path\tplaces asked from\n/internal/news\t2\n/internal/tiles/fuel\t2\n/internal/tiles/lifts\t2\n"
	if code != 0 || out != want {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errs)
	}
	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"an expansion of nothing": {[]string{"-expand", "/internal/none/{}=a", dir}, "asks for no such path"},
		"a bad expansion":         {[]string{"-expand", "/internal/none=a", dir}, "want TEMPLATE=a,b"},
		"no directory":            {nil, "one source directory"},
		"a missing directory":     {[]string{filepath.Join(dir, "none")}, "no such file"},
		"no fragment path":        {[]string{filepath.Join(dir, "layouts", "none")}, "no such file"},
	} {
		if code, _, errs := call(append([]string{"web", "-version", "0.1.1", "-commit", commit}, c.args...)...); code != 2 || !strings.Contains(errs, c.want) {
			t.Errorf("%s: exit %d, %q", name, code, errs)
		}
	}
	quiet := t.TempDir()
	write(t, quiet, "a.ts", "no fragments\n")
	if code, _, errs := call("web", "-version", "0.1.1", "-commit", commit, quiet); code != 2 || !strings.Contains(errs, "no fragment path read") {
		t.Errorf("no fragment path: exit %d, %q", code, errs)
	}
	if got := (expansions{"a/{}": {"b"}}).String(); got != "map[a/{}:[b]]" {
		t.Errorf("String() = %q", got)
	}
}

func TestWebUnreadableFile(t *testing.T) {
	req.Covers(t, "REL-015")
	dir := t.TempDir()
	p := write(t, dir, "a.ts", "x")
	if err := os.Chmod(p, 0); err != nil {
		t.Fatal(err)
	}
	if code, _, _ := call("web", "-version", "0.1.1", "-commit", commit, dir); code != 2 {
		t.Errorf("an unreadable file: exit %d", code)
	}
}

func inventory(t *testing.T, routes, web string) string {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "routes.tsv", "# source routes "+routes+" "+commit+"\n")
	write(t, dir, "web.tsv", "# source web "+web+" "+commit+"\n")
	return dir
}

func TestAge(t *testing.T) {
	req.Covers(t, "REL-015")
	dir := inventory(t, "2.6.18", "0.1.1")
	if code, out, errs := call("age", "-dir", dir, "-web", "0.1.1"); code != 0 ||
		out != "routes.tsv: routes 2.6.18, skipped (frozen: the site moves to the API)\nweb.tsv: web 0.1.1\n" {
		t.Fatalf("current: exit %d, %q, %q", code, out, errs)
	}
	// The frozen file is never compared: older than every release, it passes,
	// and it is named as skipped.
	frozen := inventory(t, "2.7.2", "0.1.1")
	write(t, frozen, "routes.tsv", "# source routes 2.6.44 "+commit+"\n")
	if code, out, errs := call("age", "-dir", frozen, "-web", "0.1.1"); code != 0 || !strings.Contains(out, "routes.tsv: routes 2.6.44, skipped (frozen: the site moves to the API)\n") {
		t.Fatalf("frozen: exit %d, %q, %q", code, out, errs)
	}
	if code, _, _ := call("age", "-dir", dir, "-web", "0.1.0"); code != 0 {
		t.Fatalf("newer than the newest release: exit %d", code)
	}
	for name, web := range map[string]string{"a newer site patch": "0.1.2", "a newer site minor": "0.2.0", "a newer site major": "1.0.0"} {
		code, _, errs := call("age", "-dir", dir, "-web", web)
		if code != 1 || !strings.Contains(errs, "export it again") {
			t.Errorf("%s: exit %d, %q", name, code, errs)
		}
	}
	if code, _, errs := call("age", "-dir", dir, "-web", "0.1"); code != 2 || !strings.Contains(errs, "-web X.Y.Z") {
		t.Errorf("a bad version: exit %d, %q", code, errs)
	}
	if code, _, _ := call("age", "-dir", t.TempDir(), "-web", "0.1.1"); code != 2 {
		t.Errorf("no files: exit %d", code)
	}
	// The MCP server's release is no input any more: -platform is a usage error.
	if code, _, _ := call("age", "-dir", dir, "-platform", "2.7.2", "-web", "0.1.1"); code != 2 {
		t.Errorf("-platform: exit %d, want 2", code)
	}
	bad := inventory(t, "2.6.18", "0.1.1")
	write(t, bad, "routes.tsv", "routes without a header\n")
	if code, _, errs := call("age", "-dir", bad, "-web", "0.1.1"); code != 2 || !strings.Contains(errs, "routes.tsv: the first line") {
		t.Errorf("no header: exit %d, %q", code, errs)
	}
}

// TestOnlyRoutesIsFrozen: age skips routes.tsv and nothing else. Every other
// file is compared with its consumer's newest release, so freezing another
// one is a change this test must be made to accept.
func TestOnlyRoutesIsFrozen(t *testing.T) {
	req.Covers(t, "REL-015")
	if len(Frozen) != 1 || Frozen["routes.tsv"] != "frozen: the site moves to the API" {
		t.Fatalf("Frozen = %v, want routes.tsv alone", Frozen)
	}
	for n, consumer := range Files {
		_, frozen := Frozen[n]
		if frozen != (consumer == "frozen") {
			t.Errorf("%s: consumer %q, frozen %v", n, consumer, frozen)
		}
		if !frozen && consumer != "web" {
			t.Errorf("%s: consumer %q has no -flag age compares with", n, consumer)
		}
	}
	// Every file but routes.tsv refuses when it is behind.
	for _, n := range []string{"web.tsv"} {
		dir := inventory(t, "2.7.2", "0.1.1")
		write(t, dir, n, "# source x 0.0.1 "+commit+"\n")
		if code, out, errs := call("age", "-dir", dir, "-web", "0.1.1"); code != 1 || strings.Contains(out, n) || !strings.Contains(errs, n+" records") {
			t.Errorf("%s behind: exit %d, %q, %q", n, code, out, errs)
		}
	}
}

// TestTheRepositorysInventoryHasHeaders: each pinned file names its release.
func TestTheRepositorysInventoryHasHeaders(t *testing.T) {
	req.Covers(t, "REL-015")
	for name := range Files {
		b, err := os.ReadFile(filepath.Join("..", "..", "..", "test", "inventory", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, err := Header(string(b)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
