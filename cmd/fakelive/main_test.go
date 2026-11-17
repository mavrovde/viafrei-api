package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// The fake's command: a usage error is 2 and names it; a registry or script
// it cannot read, or a database it cannot reach, is 1.
func TestRunRefusesWhatItCannotUse(t *testing.T) {
	ctx := context.Background()
	reg := filepath.Join("..", "..", "test", "live", "registry.json")
	script := filepath.Join("..", "..", "test", "live", "fakelive.json")
	for _, c := range []struct {
		args []string
		code int
		says string
	}{
		{[]string{"-no-such-flag"}, 2, "flag provided but not defined"},
		{nil, 2, "-db is required"},
		{[]string{"-db", "postgres://x", "-registry", "no-such-registry.json", "-script", script}, 1, "no-such-registry.json"},
		{[]string{"-db", "postgres://127.0.0.1:1/none?connect_timeout=1", "-registry", reg, "-script", script}, 1, ""},
	} {
		var stderr bytes.Buffer
		if got := run(ctx, c.args, &stderr); got != c.code || !strings.Contains(stderr.String(), c.says) {
			t.Errorf("%v: %d %q", c.args, got, stderr.String())
		}
	}
}
