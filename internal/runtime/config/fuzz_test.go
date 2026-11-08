package config_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

// plantedDBURL is planted as the database URL of every fuzzed load; it must
// never appear in an error (CFG-007).
const plantedDBURL = "postgres://reader:fuzz-planted-secret-7f3a@db.invalid/api"

// FuzzLoad (TEST-006, TST-016): the configuration loader reads a YAML file
// and the environment, both operator input. One fuzzed variable (chosen
// among the declared keys, never a *_FILE name, so no input names a file to
// read) and one fuzzed YAML file either load or are refused with a list of
// violations, never a crash; a refusal never carries the secret; and a
// load that succeeds loads again to the same values.
// Seed corpus: testdata/fuzz/FuzzLoad (test/fuzz/targets.tsv).
func FuzzLoad(f *testing.F) {
	fields := config.Fields()
	f.Add(uint8(0), "", []byte(""))
	f.Add(uint8(1), "2s", []byte("server:\n  listen_addr: \"localhost:8080\"\n"))
	f.Add(uint8(2), "-1", []byte("db:\n  url: x\n"))
	f.Add(uint8(3), "999999999999999999999", []byte("nope: 1\n"))
	f.Add(uint8(4), "a,b,,c", []byte("server: [1, 2]\n"))
	f.Add(uint8(5), "true", []byte("\t: : :\n"))
	f.Fuzz(func(t *testing.T, which uint8, value string, yaml []byte) {
		field := fields[int(which)%len(fields)]
		path := filepath.Join(t.TempDir(), "config.yaml")
		if err := os.WriteFile(path, yaml, 0o600); err != nil {
			t.Fatal(err)
		}
		environ := []string{"VFAPI_DB_URL=" + plantedDBURL}
		if !field.Secret {
			environ = append(environ, field.Env+"="+value)
		}
		opts := config.Options{Environ: environ, File: path, RequireSecrets: true}
		cfg, err := config.Load(opts)
		if err != nil {
			var list config.Errors
			if !errors.As(err, &list) || len(list) == 0 {
				t.Fatalf("refused without a list of violations: %v", err)
			}
			if strings.Contains(err.Error(), "fuzz-planted-secret") {
				t.Fatalf("a refusal carries the secret: %v", err)
			}
			return
		}
		if cfg == nil {
			t.Fatal("loaded without a configuration")
		}
		again, err := config.Load(opts)
		if err != nil || again.Server != cfg.Server {
			t.Fatalf("the same input loaded twice differs: %v", err)
		}
	})
}
