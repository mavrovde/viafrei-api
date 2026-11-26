package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

const digest = "98f0e01b8bb2f34afac8b482bd701ee61fa7bf142cc03d413e83c3b8b605ec9f"

// module writes a module root holding pin, or no pin when pin is empty.
func module(t *testing.T, pin string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if pin != "" {
		if err := os.WriteFile(filepath.Join(dir, "database.pin"), []byte(pin), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// TestPinguard: a published release is printed; a local build, another
// repository and a placeholder digest are refused; a pin that cannot be
// read is a usage error, never a pass.
func TestPinguard(t *testing.T) {
	req.Covers(t, "REL-015")
	published := "ghcr.io/mavrovde/viafrei-database:0.3.0@sha256:" + digest
	for name, c := range map[string]struct {
		pin, out, err string
		code          int
	}{
		"a published release":  {pin: published + "\nschema-head 313\n", out: published + "\n"},
		"a local build":        {pin: "viafrei-database:0.3.0-local\nschema-head 313\n", code: 1, err: "a locally built image"},
		"another repository":   {pin: "ghcr.io/someone/viafrei-database:0.3.0@sha256:" + digest + "\nschema-head 313\n", code: 1, err: "not the published viafrei-database release"},
		"a placeholder digest": {pin: "ghcr.io/mavrovde/viafrei-database:0.3.0@sha256:" + strings.Repeat("0", 64) + "\nschema-head 313\n", code: 1, err: "a placeholder digest"},
		"an unreadable pin":    {pin: "nonsense\n", code: 2, err: "database.pin"},
		"a module with no pin": {code: 2, err: "read database.pin"},
	} {
		var stdout, stderr bytes.Buffer
		code := run([]string{"-dir", module(t, c.pin)}, &stdout, &stderr)
		if code != c.code || stdout.String() != c.out || !strings.Contains(stderr.String(), c.err) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q", name, code, stdout.String(), stderr.String())
		}
	}
}

func TestPinguardUsage(t *testing.T) {
	req.Covers(t, "REL-015")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-nope"}, &stdout, &stderr); code != 2 || stdout.Len() != 0 {
		t.Fatalf("an unknown flag: exit %d, stdout %q", code, stdout.String())
	}
}
