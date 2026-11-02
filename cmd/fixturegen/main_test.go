package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
