//go:build !integration

// The compose guard needs no database, so it runs in the unit build only;
// in the integration build it shared the runner with every container test
// and its cases crossed the 2 s bound (TST-012).

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

// composeSecrets names the two values compose.yaml requires before
// docker compose config answers at all; every test env file sets each of
// them to a placeholder.
var composeSecrets = []string{"POSTGRES_PASSWORD", "API_READER_PASSWORD"}

// placeholderSecrets is the env-file text that sets every compose secret to
// a test-only placeholder.
func placeholderSecrets() string {
	var b strings.Builder
	for _, name := range composeSecrets {
		b.WriteString(name + "=test-only-placeholder\n")
	}
	return b.String()
}

// runComposeGuard runs scripts/compose-guard.sh with only the given
// environment, the given PATH and an env file holding the compose secrets
// and envFile, and returns its exit status. The guard asks the real
// docker compose for the resolved configuration, so the cases are judged by
// Compose's own reading of the env file.
func runComposeGuard(t *testing.T, path string, env []string, envFile string) int {
	t.Helper()
	file := filepath.Join(t.TempDir(), "env")
	if err := os.WriteFile(file, []byte(placeholderSecrets()+envFile), 0o600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs(filepath.Join(root, "scripts", "compose-guard.sh"))
	if err != nil {
		t.Fatal(err)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bash, script) // #nosec G204 -- the repository's own script
	cmd.Env = append([]string{"PATH=" + path, "HOME=" + os.Getenv("HOME"), "API_ENV_FILE=" + file}, env...)
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
// API_PUBLISH that is not a loopback address, from the environment or .env,
// in every spelling Compose itself accepts.
func TestComposeGuardRefusesNonLoopback(t *testing.T) {
	req.Covers(t, "SSC-019")
	path := os.Getenv("PATH")
	for _, tool := range []string{"docker", "jq"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("the compose guard is judged by the real %s, which is not on PATH: %v", tool, err)
		}
	}
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
		"loopback in .env, exported":   {nil, "export API_PUBLISH=[::1]:8080\n", 0},
		"non-loopback in .env, export": {nil, "export API_PUBLISH=0.0.0.0:8080\n", 2},
		"the environment wins over it": {[]string{"API_PUBLISH=0.0.0.0:8080"}, "API_PUBLISH=[::1]:8080\n", 2},
		"loopback env wins over .env":  {[]string{"API_PUBLISH=[::1]:8080"}, "API_PUBLISH=0.0.0.0:8080\n", 0},
		"colon separator in .env":      {nil, "API_PUBLISH: 0.0.0.0:9999\n", 2},
		"space before = in .env":       {nil, "API_PUBLISH =0.0.0.0:9996\n", 2},
		"spaces around the name":       {nil, "  API_PUBLISH  =0.0.0.0:9997\n", 2},
	} {
		// Sequential on purpose: each case starts a real docker compose, and
		// seventeen at once on a CI runner took 3 s each (TST-012).
		t.Run(name, func(t *testing.T) {
			if got := runComposeGuard(t, path, c.env, c.envFile); got != c.want {
				t.Errorf("exit %d, want %d", got, c.want)
			}
		})
	}
	for _, file := range []string{"Makefile", "scripts/compose-smoke.sh"} {
		src, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatal(err)
		}
		guard := strings.Index(string(src), "scripts/compose-guard.sh")
		up := strings.Index(string(src), "docker compose up")
		if guard < 0 || up < 0 || guard > up {
			t.Errorf("%s does not run the compose guard before docker compose up", file)
		}
	}
}

// TestComposeGuardRefusesWhenComposeCannotAnswer: the guard is a refusal,
// never a pass, when it cannot learn what Compose would publish. Each case
// runs the real script with a PATH that lacks a tool or holds a docker stub.
func TestComposeGuardRefusesWhenComposeCannotAnswer(t *testing.T) {
	req.Covers(t, "SSC-019")
	link := func(t *testing.T, dir, tool string) {
		t.Helper()
		real, err := exec.LookPath(tool)
		if err != nil {
			t.Fatalf("the case needs the real %s: %v", tool, err)
		}
		if err := os.Symlink(real, filepath.Join(dir, tool)); err != nil {
			t.Fatal(err)
		}
	}
	stub := func(t *testing.T, dir, body string) {
		t.Helper()
		// #nosec G306 -- an executable test stub in a temporary directory
		if err := os.WriteFile(filepath.Join(dir, "docker"), []byte("#!/bin/sh\n"+body+"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for name, setup := range map[string]func(t *testing.T, dir string){
		"no docker": func(t *testing.T, dir string) { link(t, dir, "jq") },
		"no jq":     func(t *testing.T, dir string) { link(t, dir, "docker") },
		"compose config fails": func(t *testing.T, dir string) {
			link(t, dir, "jq")
			stub(t, dir, "echo planted failure >&2; exit 1")
		},
		"compose answers non-JSON": func(t *testing.T, dir string) {
			link(t, dir, "jq")
			stub(t, dir, "echo not json")
		},
		"the api publishes nothing": func(t *testing.T, dir string) {
			link(t, dir, "jq")
			stub(t, dir, `echo '{"services":{"api":{}}}'`)
		},
		"another service off loopback": func(t *testing.T, dir string) {
			link(t, dir, "jq")
			stub(t, dir, `echo '{"services":{"api":{"ports":[{"host_ip":"::1","published":"8080"}]},"db":{"ports":[{"host_ip":"0.0.0.0","published":"5432"}]}}}'`)
		},
		"loopback without a fixed port": func(t *testing.T, dir string) {
			link(t, dir, "jq")
			stub(t, dir, `echo '{"services":{"api":{"ports":[{"host_ip":"127.0.0.1","target":8080}]}}}'`)
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			setup(t, dir)
			if got := runComposeGuard(t, dir, nil, ""); got != 2 {
				t.Errorf("exit %d, want 2", got)
			}
		})
	}
}
