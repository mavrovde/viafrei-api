package fakelive

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/livesources"
)

var registryPath = filepath.Join("..", "..", "..", "test", "live", "registry.json")

func registry(t *testing.T) *livesources.Registry {
	t.Helper()
	reg, err := livesources.Load(registryPath, "")
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// A script that does not parse, names a source the registry does not hold,
// or an outcome no settle has, is refused; a known one is read.
func TestParseScriptRefusesWhatTheRegistryDoesNotHold(t *testing.T) {
	reg := registry(t)
	for name, c := range map[string]struct{ src, says string }{
		"not JSON":          {`{`, "script"},
		"an unknown field":  {`{"nope":1}`, "unknown field"},
		"an unknown source": {`{"sources":{"no-such-source":{"default":{"state":"refreshed"}}}}`, "not in the registry"},
		"an unknown state":  {`{"sources":{"fuel-prices-de":{"default":{"state":"done"}}}}`, `no outcome "done"`},
		"a negative delay":  {`{"sources":{"fuel-prices-de":{"default":{"state":"refreshed","after_ms":-1}}}}`, "after -1 ms"},
	} {
		if _, err := ParseScript([]byte(c.src), reg); err == nil || !strings.Contains(err.Error(), c.says) {
			t.Errorf("%s: %v", name, err)
		}
	}
	sc, err := ParseScript([]byte(`{"sources":{"fuel-prices-de":{"default":{"state":"never"},"subjects":{"ags:1":{"state":"failed","reason":"r"}}}}}`), reg)
	if err != nil {
		t.Fatal(err)
	}
	if o := sc.outcome("fuel-prices-de", "ags:1"); o.State != "failed" || o.Reason != "r" {
		t.Errorf("a subject's outcome: %+v", o)
	}
	if o := sc.outcome("fuel-prices-de", "ags:2"); o.State != Never {
		t.Errorf("the default: %+v", o)
	}
	if o := sc.outcome("train-board-de", "8000207"); o.State != "refused" || o.Reason != "no script" {
		t.Errorf("an unscripted source: %+v", o)
	}
}

// Load refuses a registry or a script it cannot read.
func TestLoadRefusesWhatItCannotRead(t *testing.T) {
	script := filepath.Join(t.TempDir(), "script.json")
	if err := os.WriteFile(script, []byte(`{"sources":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(filepath.Join(t.TempDir(), "none.json"), script); err == nil {
		t.Error("a missing registry was read")
	}
	if _, err := Load(registryPath, filepath.Join(t.TempDir(), "none.json")); err == nil || !strings.Contains(err.Error(), "fakelive") {
		t.Errorf("a missing script: %v", err)
	}
	if _, err := Load(registryPath, script); err != nil {
		t.Errorf("an empty script: %v", err)
	}
}

// A database the fake cannot reach is Start's error.
func TestStartRefusesAnUnreachableDatabase(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Start(ctx, "postgres://x:y@127.0.0.1:1/none?connect_timeout=1&sslmode=disable", Script{}); err == nil || !strings.Contains(err.Error(), "fakelive") {
		t.Fatalf("%v", err)
	}
}

// The first fault is kept; a nil or a cancelled one is none.
func TestFailKeepsTheFirstFault(t *testing.T) {
	f := &Fake{}
	first := errors.New("first")
	f.fail(nil)
	f.fail(context.Canceled)
	f.fail(first)
	f.fail(errors.New("second"))
	if !errors.Is(f.err, first) {
		t.Fatalf("%v", f.err)
	}
}
