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
