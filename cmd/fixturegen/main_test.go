package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/fixtures"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestWritesTheSetAndItsManifest(t *testing.T) {
	req.Covers(t, "TST-011", "TST-014")
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-profile", "tiny", "-seed", "5", "-out", dir}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit %d: %s", code, stderr.String())
	}
	sql, err := os.ReadFile(filepath.Join(dir, "seed.sql"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m fixtures.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("sha256:%x", sha256.Sum256(sql)); m.Checksum != want {
		t.Fatalf("manifest checksum %s, seed.sql hashes to %s", m.Checksum, want)
	}
	if m.Seed != 5 || m.Profile != "tiny" || len(m.Inputs) == 0 {
		t.Fatalf("manifest does not record the run: %+v", m)
	}
	// The success line prints counts (TST-011), not just "ok".
	if !strings.Contains(stdout.String(), "rows in 30 tables, 16 Länder, 7 families") {
		t.Fatalf("success line: %q", stdout.String())
	}
}

func TestUsageErrorsExit2(t *testing.T) {
	req.Covers(t, "TST-011")
	dir := t.TempDir()
	for _, args := range [][]string{
		{"-profile", "huge", "-out", dir},
		{"-profile", "tiny"},
		{"-out", dir, "extra"},
		{"-seed", "minus-one", "-out", dir},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr); code != 2 || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Errorf("%v: exit %d, stdout %q, stderr %q", args, code, stdout.String(), stderr.String())
		}
	}
}

// TestOutputFailuresExit1: a destination that cannot hold the set is exit 1
// with the reason, never a usage error and never a partial success.
func TestOutputFailuresExit1(t *testing.T) {
	req.Covers(t, "TST-011")
	file := filepath.Join(t.TempDir(), "plain")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	seedDir := t.TempDir()
	manifestDir := t.TempDir()
	for _, d := range []string{filepath.Join(seedDir, "seed.sql"), filepath.Join(manifestDir, "manifest.json")} {
		if err := os.Mkdir(d, 0o750); err != nil {
			t.Fatal(err)
		}
	}
	for name, out := range map[string]string{
		"out under a file":       filepath.Join(file, "sub"),
		"seed.sql is a dir":      seedDir,
		"manifest.json is a dir": manifestDir,
	} {
		var stdout, stderr bytes.Buffer
		if code := run([]string{"-out", out}, &stdout, &stderr); code != 1 || stdout.Len() != 0 || stderr.Len() == 0 {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", name, code, stdout.String(), stderr.String())
		}
	}
}

// TestACoverageGapExits1: a set with a gap is written for diagnosis but the
// run fails, so CI never loads a set that misses a family.
func TestACoverageGapExits1(t *testing.T) {
	req.Covers(t, "TST-014")
	saved := fixtures.Families
	t.Cleanup(func() { fixtures.Families = saved })
	fixtures.Families = append(append(fixtures.Families[:0:0], saved...), struct{ Name, Category string }{"omega", "attribution"})
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-out", t.TempDir()}, &stdout, &stderr); code != 1 || !strings.Contains(stderr.String(), "omega") {
		t.Fatalf("exit %d, stderr %q", code, stderr.String())
	}
}

// recorded returns the committed checksum lines for the given profiles.
func recorded(t *testing.T, profiles ...string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "test", "fixtures", "checksums.tsv"))
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	seen := map[string]bool{}
	for _, l := range strings.Split(string(raw), "\n") {
		p, _, _ := strings.Cut(l, "\t")
		seen[p] = true
		if slices.Contains(profiles, p) {
			out = append(out, l)
		}
	}
	for _, p := range fixtures.Profiles() {
		if !seen[p] {
			t.Fatalf("test/fixtures/checksums.tsv records no %s set", p)
		}
	}
	return strings.Join(out, "\n") + "\n"
}

func verifyFile(t *testing.T, content string) (int, string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "checksums.tsv")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := run([]string{"-verify", path}, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// TestRecordedChecksumsHoldOnThisMachine: the checksums in
// test/fixtures/checksums.tsv were recorded on another machine and
// architecture; regenerating the tiny and ci sets here gives the same bytes
// (TST-014). The benchmark set takes longer than a test may (TST-012); CI
// verifies it with the same command as a step of its own.
func TestRecordedChecksumsHoldOnThisMachine(t *testing.T) {
	req.Covers(t, "TST-014")
	for _, p := range []string{"tiny", "ci"} {
		t.Run(p, func(t *testing.T) {
			code, stdout, stderr := verifyFile(t, "# recorded\n"+recorded(t, p))
			if code != 0 || !strings.Contains(stdout, "verified 1 of 1 recorded sets") {
				t.Fatalf("exit %d, stdout %q, stderr %q", code, stdout, stderr)
			}
		})
	}
}

func TestVerifyFailsOnAMismatch(t *testing.T) {
	req.Covers(t, "TST-014", "TST-011")
	line := recorded(t, "tiny")
	other := strings.Replace(line, "\t"+fixtures.GeneratorVersion+"\t", "\t0.0.0-other\t", 1)
	wrong := line[:strings.Index(line, "sha256:")] + "sha256:00\n"
	for name, content := range map[string]string{
		"another checksum":         wrong,
		"another generator":        other,
		"one good, one mismatched": line + wrong,
	} {
		code, stdout, stderr := verifyFile(t, content)
		if code != 1 || stderr == "" || !strings.Contains(stdout, "recorded sets") {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", name, code, stdout, stderr)
		}
	}

	saved := fixtures.Families
	t.Cleanup(func() { fixtures.Families = saved })
	fixtures.Families = append(append(fixtures.Families[:0:0], saved...), struct{ Name, Category string }{"omega", "attribution"})
	if code, _, stderr := verifyFile(t, line); code != 1 || !strings.Contains(stderr, "omega") {
		t.Fatalf("a set with a gap: exit %d, stderr %q", code, stderr)
	}
}

func TestVerifyRefusesWhatItCannotRead(t *testing.T) {
	req.Covers(t, "TST-011")
	for name, content := range map[string]string{
		"empty":           "",
		"comments only":   "# nothing\n\n",
		"three fields":    "tiny\t1\tsha256:00\n",
		"unknown profile": "huge\t1\t0.1.0\tsha256:00\n",
		"seed not number": "tiny\tone\t0.1.0\tsha256:00\n",
		"no algorithm":    "tiny\t1\t0.1.0\t00\n",
	} {
		code, stdout, stderr := verifyFile(t, content)
		if code != 2 || stdout != "" || stderr == "" {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", name, code, stdout, stderr)
		}
	}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-verify", filepath.Join(t.TempDir(), "missing.tsv")}, &stdout, &stderr); code != 2 {
		t.Fatalf("a missing file: exit %d", code)
	}
}
