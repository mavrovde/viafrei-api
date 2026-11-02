package config_test

import (
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// TestFileValuesOfEveryShape loads YAML values of each type the file can
// carry, the accepted and the refused ones.
func TestFileValuesOfEveryShape(t *testing.T) {
	req.Covers(t, "CFG-002", "CFG-003")
	ok := writeFile(t, "db:\n  pool_max: 5.0\n  pool_min: 1\ndocs:\n  enabled: false\nendpoints:\n  disabled: [getMeta]\ncontract:\n  expected: \"0.1\"\n")
	cfg, err := config.Load(config.Options{File: ok})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DB.PoolMax != 5 || cfg.Docs.Enabled || len(cfg.Endpoints.Disabled) != 1 || cfg.Contract.Expected != "0.1" {
		t.Fatalf("loaded %+v", cfg)
	}

	bad := writeFile(t, "server:\n  read_timeout: 5\n  max_header_bytes: 4096.5\n  max_uri_bytes: [1]\n  listen_addr: [a]\ndocs:\n  enabled: 7\nendpoints:\n  disabled: [1]\n")
	_, err = config.Load(config.Options{File: bad})
	assertRefused(t, err,
		"server.read_timeout: 5 is not a duration",
		"server.max_header_bytes: 4096.5 is not a whole number",
		"server.max_uri_bytes: [1] is not a whole number",
		"server.listen_addr: [a] is not text",
		"docs.enabled: 7 is not true or false",
		"endpoints.disabled: list item 1 is not text")

	list := writeFile(t, "endpoints:\n  disabled: 3\n")
	_, err = config.Load(config.Options{File: list})
	assertRefused(t, err, "endpoints.disabled: 3 is not a list")
}

func TestUnreadableFilesNamed(t *testing.T) {
	req.Covers(t, "CFG-003", "CFG-007")
	missing := t.TempDir() + "/absent.yaml"
	_, err := config.Load(config.Options{File: missing, Environ: []string{"VFAPI_DB_URL_FILE=" + missing}})
	assertRefused(t, err, "config file: cannot read", "db.url: cannot read the secret file named by VFAPI_DB_URL_FILE")
}

func TestRuleDescriptions(t *testing.T) {
	req.Covers(t, "CFG-003")
	_, err := config.Load(config.Options{Environ: []string{
		"VFAPI_SERVER_LISTEN_ADDR=",
		"VFAPI_SERVER_PUBLIC_BASE_URL=https://",
		"VFAPI_CONTRACT_EXPECTED=one",
		"VFAPI_ENDPOINTS_DISABLED=getMeta, ,",
	}})
	assertRefused(t, err, "server.listen_addr: required", "server.public_base_url: fails the url rule", "contract.expected: fails the contract_version rule")
}

func TestShowHidesSecrets(t *testing.T) {
	req.Covers(t, "CFG-007")
	cfg, err := config.Load(config.Options{Environ: []string{"VFAPI_DB_URL=postgres://u:hunter2@h/db", "VFAPI_ENDPOINTS_DISABLED=getMeta"}})
	if err != nil {
		t.Fatal(err)
	}
	shown := strings.Join(config.Show(cfg), "\n")
	for _, want := range []string{"db.url = <set>", "endpoints.disabled = getMeta", "server.read_timeout = 5s"} {
		if !strings.Contains(shown, want) {
			t.Errorf("show lacks %q:\n%s", want, shown)
		}
	}
	if strings.Contains(shown, "hunter2") {
		t.Fatal("show printed the secret")
	}
	if !strings.Contains(strings.Join(config.Show(config.Defaults()), "\n"), "db.url = <unset>") {
		t.Error("an unset secret is not shown as <unset>")
	}
}

func TestBoundsRendering(t *testing.T) {
	req.Covers(t, "CFG-005")
	cases := map[string]config.Field{
		"3 to 63 characters; lower": {Type: "string", Validate: "min=3,max=63,lower"},
		"1 to 16":                   {Type: "int", Validate: "min=1,max=16"},
		"at most 63 characters":     {Type: "string", Validate: "max=63"},
		"at most 16":                {Type: "int", Validate: "max=16"},
		"at least 1":                {Type: "int", Validate: "min=1"},
		"—":                         {Type: "string", Validate: "required"},
	}
	for want, f := range cases {
		if got := config.Bounds(f); got != want {
			t.Errorf("Bounds(%q) = %q, want %q", f.Validate, got, want)
		}
	}
}
