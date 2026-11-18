package livepin_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/release/livepin"
)

const digest = "98f0e01b8bb2f34afac8b482bd701ee61fa7bf142cc03d413e83c3b8b605ec9f"

var published = livepin.Published + "0.1.0@sha256:" + digest

func TestParseReadsBothLines(t *testing.T) {
	p, err := livepin.Parse("# comment\n\n" + published + "\ndatabase 0.3.0\n")
	if err != nil || p.Ref != published || p.Version != "0.1.0" || p.Database != "0.3.0" {
		t.Fatalf("%+v %v", p, err)
	}
	p, err = livepin.Parse(livepin.Unreleased + "\ndatabase 0.3.0\n")
	if err != nil || p.Version != "" || p.Database != "0.3.0" {
		t.Fatalf("an unreleased pin: %+v %v", p, err)
	}
	for name, src := range map[string]string{
		"one line":         published + "\n",
		"three lines":      published + "\ndatabase 0.3.0\nextra\n",
		"a tag, no digest": livepin.Published + "0.1.0\ndatabase 0.3.0\n",
		"no database":      published + "\nschema-head 315\n",
	} {
		if _, err := livepin.Parse(src); err == nil {
			t.Errorf("%s: read as a pin", name)
		}
	}
}

func module(t *testing.T, pin string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if pin != "" {
		if err := os.WriteFile(filepath.Join(dir, livepin.File), []byte(pin), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sub := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	return sub
}

func TestReadFindsTheModulesPin(t *testing.T) {
	if p, err := livepin.Read(module(t, published+"\ndatabase 0.3.0\n")); err != nil || p.Ref != published {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := livepin.Read(module(t, "")); err == nil || !strings.Contains(err.Error(), "read "+livepin.File) {
		t.Errorf("a module without a pin: %v", err)
	}
	if _, err := livepin.Read(string(filepath.Separator)); err == nil || !strings.Contains(err.Error(), "no go.mod") {
		t.Errorf("no module: %v", err)
	}
}

func TestRefusal(t *testing.T) {
	for name, c := range map[string]struct {
		pin  livepin.Pin
		want string
	}{
		"a published release":  {livepin.Pin{Ref: published, Version: "0.1.0", Database: "0.3.0"}, ""},
		"unreleased":           {livepin.Pin{Ref: livepin.Unreleased, Database: "0.3.0"}, "unreleased"},
		"another repository":   {livepin.Pin{Ref: strings.Replace(livepin.Published, "mavrovde", "someone", 1) + "0.1.0@sha256:" + digest, Database: "0.3.0"}, "not the published"},
		"another database":     {livepin.Pin{Ref: published, Database: "0.2.0"}, "while database.pin names 0.3.0"},
		"a placeholder digest": {livepin.Pin{Ref: livepin.Published + "0.1.0@sha256:" + strings.Repeat("0", 64), Database: "0.3.0"}, "a placeholder digest"},
	} {
		got := livepin.Refusal(c.pin, "0.3.0")
		if (c.want == "") != (got == "") || !strings.Contains(got, c.want) {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
}
