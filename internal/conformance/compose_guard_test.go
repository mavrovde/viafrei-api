package conformance_test

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// runComposeGuard runs scripts/compose-guard.sh with only the given
// environment (plus PATH) and an env file holding envFile, and returns its
// exit status.
func runComposeGuard(t *testing.T, env []string, envFile string) int {
	t.Helper()
	file := filepath.Join(t.TempDir(), "env")
	if err := os.WriteFile(file, []byte(envFile), 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join(root, "scripts", "compose-guard.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("bash", script) // #nosec G204 -- the repository's own script
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "API_ENV_FILE=" + file}, env...)
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
		return 0
	case errors.As(err, &exit):
		return exit.ExitCode()
	default:
		t.Fatal(err)
		return -1
	}
}

// TestComposeGuardRefusesNonLoopback holds SSC-019 where compose is driven:
// make up and the compose smoke run the guard first, and it refuses an
// API_PUBLISH that is not a loopback address, from the environment or .env.
func TestComposeGuardRefusesNonLoopback(t *testing.T) {
	req.Covers(t, "SSC-019")
	for name, c := range map[string]struct {
		env     []string
		envFile string
		want    int
	}{
		"unset is the default":         {nil, "", 0},
		"IPv6 loopback":                {[]string{"API_PUBLISH=[::1]:8080"}, "", 0},
		"IPv4 loopback":                {[]string{"API_PUBLISH=127.0.0.1:18080"}, "", 0},
		"loopback in .env, quoted":     {nil, "API_PUBLISH=\"127.0.0.1:8080\"\n", 0},
		"all addresses":                {[]string{"API_PUBLISH=0.0.0.0:8080"}, "", 2},
		"a bare port":                  {[]string{"API_PUBLISH=8080"}, "", 2},
		"IPv6 any":                     {[]string{"API_PUBLISH=[::]:8080"}, "", 2},
		"a routable address":           {[]string{"API_PUBLISH=192.0.2.10:8080"}, "", 2},
		"loopback without a port":      {[]string{"API_PUBLISH=127.0.0.1"}, "", 2},
		"non-loopback in .env":         {nil, "API_PUBLISH=0.0.0.0:8080\n", 2},
		"the environment wins over it": {[]string{"API_PUBLISH=0.0.0.0:8080"}, "API_PUBLISH=[::1]:8080\n", 2},
	} {
		if got := runComposeGuard(t, c.env, c.envFile); got != c.want {
			t.Errorf("%s: exit %d, want %d", name, got, c.want)
		}
	}
	for _, path := range []string{"Makefile", "scripts/compose-smoke.sh"} {
		src, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			t.Fatal(err)
		}
		guard := strings.Index(string(src), "scripts/compose-guard.sh")
		up := strings.Index(string(src), "docker compose up")
		if guard < 0 || up < 0 || guard > up {
			t.Errorf("%s does not run the compose guard before docker compose up", path)
		}
	}
}
