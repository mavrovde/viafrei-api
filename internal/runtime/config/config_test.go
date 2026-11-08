package config_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestDefaultsValidate(t *testing.T) {
	req.Covers(t, "CFG-001")
	cfg, err := config.Load(config.Options{})
	if err != nil {
		t.Fatalf("defaults refused: %v", err)
	}
	if cfg.Deadline.Request.String() != "2s" {
		t.Fatalf("deadline.request default = %s", cfg.Deadline.Request)
	}
}

func TestEveryKeyIsDeclaredCompletely(t *testing.T) {
	req.Covers(t, "CFG-001")
	fields := config.Fields()
	if len(fields) < 20 {
		t.Fatalf("only %d keys reflected; the walk did not read the declaration", len(fields))
	}
	for _, f := range fields {
		if f.Desc == "" {
			t.Errorf("%s: no description", f.Key)
		}
		if f.Secret {
			continue
		}
		if f.Type != "bool" && f.Type != "list" && f.Key != "contract.expected" && f.Validate == "" {
			t.Errorf("%s: no bounds", f.Key)
		}
	}
}

func TestEnvironmentOverridesFileOverridesDefault(t *testing.T) {
	req.Covers(t, "CFG-002")
	path := writeFile(t, "server:\n  max_uri_bytes: 4096\n  max_header_bytes: 8192\n")
	cfg, err := config.Load(config.Options{
		File:    path,
		Environ: []string{"VFAPI_SERVER_MAX_URI_BYTES=3000"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.MaxURIBytes != 3000 {
		t.Errorf("env did not win: %d", cfg.Server.MaxURIBytes)
	}
	if cfg.Server.MaxHeaderBytes != 8192 {
		t.Errorf("file did not win over default: %d", cfg.Server.MaxHeaderBytes)
	}
}

func TestFileNamedByEnvironment(t *testing.T) {
	req.Covers(t, "CFG-002")
	path := writeFile(t, "log:\n  level: debug\n")
	cfg, err := config.Load(config.Options{Environ: []string{config.FileEnv + "=" + path}})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Log.Level != "debug" {
		t.Fatalf("log.level = %q", cfg.Log.Level)
	}
}

func TestSecretFromFileVariant(t *testing.T) {
	req.Covers(t, "CFG-002")
	path := writeFile(t, "postgres://reader:planted-file-secret@db/x\n")
	cfg, err := config.Load(config.Options{Environ: []string{"VFAPI_DB_URL_FILE=" + path}, RequireSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DB.URL.Reveal() != "postgres://reader:planted-file-secret@db/x" {
		t.Fatalf("secret not read from file")
	}
}

func TestSecretSetTwiceRefused(t *testing.T) {
	req.Covers(t, "CFG-002", "CFG-003")
	path := writeFile(t, "postgres://reader:planted-file-secret@db/x\n")
	_, err := config.Load(config.Options{Environ: []string{
		"VFAPI_DB_URL=postgres://reader:planted-env-secret@db/x",
		"VFAPI_DB_URL_FILE=" + path,
	}, RequireSecrets: true})
	assertRefused(t, err, "db.url: set by both VFAPI_DB_URL and VFAPI_DB_URL_FILE")
	if strings.Contains(err.Error(), "planted-env-secret") || strings.Contains(err.Error(), "planted-file-secret") {
		t.Fatal("the refusal printed the secret")
	}
}

func TestSecretInConfigFileRefused(t *testing.T) {
	req.Covers(t, "CFG-002", "CFG-007")
	path := writeFile(t, "db:\n  url: postgres://reader:planted-yaml-secret@db/x\n")
	_, err := config.Load(config.Options{File: path})
	assertRefused(t, err, "db.url: is a secret")
	if strings.Contains(err.Error(), "planted-yaml-secret") {
		t.Fatal("the refusal printed the secret")
	}
}

func TestBootRefusesEveryViolationAtOnce(t *testing.T) {
	req.Covers(t, "CFG-003")
	path := writeFile(t, "server:\n  no_such_key: 1\n")
	_, err := config.Load(config.Options{
		File: path,
		Environ: []string{
			"VFAPI_DB_POOL_MAX=17",
			"VFAPI_DEADLINE_REQUEST=3s",
			"VFAPI_LOG_LEVEL=chatty",
			"VFAPI_NO_SUCH_VARIABLE=1",
		},
	})
	assertRefused(t, err,
		"server.no_such_key: unknown key in the config file",
		"VFAPI_NO_SUCH_VARIABLE: unknown environment variable",
		"db.pool_max: above the maximum 16",
		"deadline.request: above the maximum 2s",
		"log.level: not one of debug info warn error",
	)
}

func TestMalformedValuesNamed(t *testing.T) {
	req.Covers(t, "CFG-003")
	_, err := config.Load(config.Options{Environ: []string{
		"VFAPI_DB_POOL_MAX=four",
		"VFAPI_SERVER_READ_TIMEOUT=5",
		"VFAPI_DOCS_ENABLED=maybe",
	}})
	assertRefused(t, err, "db.pool_max:", "server.read_timeout:", "docs.enabled:")
}

func TestCrossFieldRules(t *testing.T) {
	req.Covers(t, "CFG-004")
	_, err := config.Load(config.Options{Environ: []string{
		"VFAPI_DB_POOL_MIN=5",
		"VFAPI_DB_POOL_MAX=4",
		"VFAPI_DEADLINE_REQUEST=300ms",
		"VFAPI_DEADLINE_RESERVE=300ms",
		"VFAPI_HEALTH_READY_TIMEOUT=500ms",
	}})
	assertRefused(t, err,
		"db.pool_min: 5 is larger than db.pool_max 4",
		"deadline.reserve:",
		"health.ready_timeout:",
		"db.statement_timeout_floor:",
	)
}

func TestMissingRequiredSecret(t *testing.T) {
	req.Covers(t, "CFG-003")
	_, err := config.Load(config.Options{RequireSecrets: true})
	assertRefused(t, err, "db.url: required")
}

func TestSecretNeverPrinted(t *testing.T) {
	req.Covers(t, "CFG-007")
	const planted = "planted-secret-value-7f3a"
	cfg, err := config.Load(config.Options{Environ: []string{"VFAPI_DB_URL=postgres://reader:" + planted + "@db/x"}})
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))
	logger.Info("config", "cfg", cfg, "url", cfg.DB.URL)
	slog.New(slog.NewTextHandler(&buf, nil)).Info("config", "cfg", cfg)
	j, _ := json.Marshal(cfg)
	buf.Write(j)
	fmt.Fprintf(&buf, "%v %+v %#v %s %q %x", cfg, cfg, cfg, cfg.DB.URL, cfg.DB.URL, cfg.DB.URL)
	// Each printable method on its own, since fmt prefers Format over them.
	text, _ := cfg.DB.URL.MarshalText()
	buf.WriteString(cfg.DB.URL.String() + cfg.DB.URL.GoString() + string(text) + cfg.DB.URL.LogValue().String())
	if strings.Contains(buf.String(), planted) {
		t.Fatalf("a printable form carried the secret:\n%s", buf.String())
	}
	if !strings.Contains(buf.String(), config.Redacted) {
		t.Fatal("the redaction marker is missing, so nothing was printed")
	}
}

func TestReferenceIsCurrent(t *testing.T) {
	req.Covers(t, "CFG-005")
	var buf bytes.Buffer
	if err := config.WriteReference(&buf); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("..", "..", "..", "docs", "configuration.md")
	if os.Getenv("UPDATE_GENERATED") == "1" {
		if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(path) // #nosec G304 -- fixed repository path
	if err != nil {
		t.Fatalf("docs/configuration.md unreadable: %v (run: make generate)", err)
	}
	if !bytes.Equal(got, buf.Bytes()) {
		t.Fatal("docs/configuration.md drifted from the declaration; run: make generate")
	}
	for _, f := range config.Fields() {
		if !bytes.Contains(got, []byte("`"+f.Key+"`")) {
			t.Errorf("reference lacks %s", f.Key)
		}
	}
}

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func assertRefused(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("configuration accepted; expected a refusal")
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal lacks %q:\n%v", want, err)
		}
	}
}

func TestCrossFieldAuthRules(t *testing.T) {
	req.Covers(t, "CFG-004")
	_, err := config.Load(config.Options{Environ: []string{
		"VFAPI_AUTH_KEY_PREFIX_TEST=vf_live_",
		"VFAPI_AUTH_REVOCATION_PROPAGATION=30s",
		"VFAPI_AUTH_KEY_CACHE_TTL=10s",
		"VFAPI_ACCESS_URL=postgres://access@db.invalid/x",
		"VFAPI_AUTH_PEPPER_PREVIOUS=short",
	}})
	assertRefused(t, err,
		"auth.key_prefix_test: equal to auth.key_prefix_live",
		"auth.revocation_propagation: 30s is longer than auth.key_cache_ttl 10s",
		"auth.pepper: required with access.url",
		"auth.pepper_previous: shorter than 32 bytes",
	)
	same := strings.Repeat("s", 32)
	_, err = config.Load(config.Options{Environ: []string{"VFAPI_AUTH_PEPPER=" + same, "VFAPI_AUTH_PEPPER_PREVIOUS=" + same, "VFAPI_AUTH_KEY_PREFIX_LIVE=Bad"}})
	assertRefused(t, err, "auth.pepper_previous: equal to auth.pepper", "auth.key_prefix_live: fails the key_prefix rule")
	if _, err := config.Load(config.Options{Environ: []string{"VFAPI_AUTH_PEPPER=" + same, "VFAPI_ACCESS_URL=postgres://a@b/c"}}); err != nil {
		t.Fatal(err)
	}
}
