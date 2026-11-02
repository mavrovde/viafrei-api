package conformance_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

var (
	fromLine   = regexp.MustCompile(`(?m)^FROM\s+(?:--platform=\S+\s+)?(\S+)(?:\s+AS\s+(\S+))?`)
	pinnedFrom = regexp.MustCompile(`^[a-z0-9./-]+(:[A-Za-z0-9._-]+)?@sha256:[0-9a-f]{64}$`)
	secretWord = regexp.MustCompile(`(?i)(password|secret|token|api_?key|credential)`)
)

// dockerfileFindings lists every rule the Dockerfile breaks: an external base
// image not pinned by digest, a runtime stage that is not distroless,
// non-root and last, or an ENV/ARG/LABEL naming a secret.
func dockerfileFindings(src string) []string {
	var out []string
	stages := map[string]bool{}
	matches := fromLine.FindAllStringSubmatch(src, -1)
	if len(matches) == 0 {
		return []string{"no FROM line read"}
	}
	for _, m := range matches {
		image, name := m[1], m[2]
		if !stages[image] && !pinnedFrom.MatchString(image) {
			out = append(out, "base image not pinned by digest: "+image)
		}
		if name != "" {
			stages[name] = true
		}
	}
	last := matches[len(matches)-1]
	if last[2] != "runtime" || !strings.HasPrefix(last[1], "gcr.io/distroless/static") || !strings.Contains(last[1], ":nonroot@") {
		out = append(out, "the last stage must be runtime, on distroless static nonroot")
	}
	runtime := src[strings.LastIndex(src, last[0]):]
	if !regexp.MustCompile(`(?m)^USER\s+nonroot(:nonroot)?\s*$`).MatchString(runtime) {
		out = append(out, "the runtime stage does not run as nonroot")
	}
	if !strings.Contains(runtime, `HEALTHCHECK`) || !strings.Contains(runtime, `"/app", "healthcheck"`) {
		out = append(out, "the runtime stage has no /app healthcheck")
	}
	if !strings.Contains(runtime, `org.opencontainers.image.source="https://github.com/mavrovde/viafrei-api"`) {
		out = append(out, "the runtime stage has no OCI source label")
	}
	for _, line := range strings.Split(src, "\n") {
		f := strings.Fields(line)
		if len(f) > 1 && (f[0] == "ENV" || f[0] == "ARG") && secretWord.MatchString(line) {
			out = append(out, "a secret-like build value: "+line)
		}
	}
	return out
}

func TestDockerfileRules(t *testing.T) {
	req.Covers(t, "SSC-005", "SSC-017")
	src, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range dockerfileFindings(string(src)) {
		t.Error(f)
	}
}

func TestDockerfileRulesCanSayNo(t *testing.T) {
	req.Covers(t, "SSC-005", "SSC-017")
	good, err := os.ReadFile(filepath.Join(root, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	// Each plant must produce the finding named for it, not just any finding.
	builder := regexp.MustCompile(`(golang:[0-9.a-z-]+)@sha256:[0-9a-f]{64}`)
	for want, bad := range map[string]string{
		"not pinned by digest: golang": builder.ReplaceAllString(string(good), "$1"),
		"does not run as nonroot":      strings.Replace(string(good), "USER nonroot:nonroot", "USER root", 1),
		"secret-like build value":      strings.Replace(string(good), "ENV VFAPI_SERVER_LISTEN_ADDR", "ENV VFAPI_DB_PASSWORD=x\nENV VFAPI_SERVER_LISTEN_ADDR", 1),
		"no /app healthcheck":          regexp.MustCompile(`(?m)^HEALTHCHECK.*$`).ReplaceAllString(string(good), ""),
		"last stage must be runtime":   string(good) + "\nFROM runtime AS debug\n",
		"no OCI source label":          strings.Replace(string(good), "image.source=", "image.origin=", 1),
	} {
		if bad == string(good) {
			t.Fatalf("plant for %q changed nothing", want)
		}
		if got := strings.Join(dockerfileFindings(bad), "; "); !strings.Contains(got, want) {
			t.Errorf("planted %q, the rules said %q", want, got)
		}
	}
}

func TestDockerignoreKeepsSecretsOut(t *testing.T) {
	req.Covers(t, "SSC-017")
	src, err := os.ReadFile(filepath.Join(root, ".dockerignore"))
	if err != nil {
		t.Fatal(err)
	}
	lines := map[string]bool{}
	for _, l := range strings.Split(string(src), "\n") {
		lines[strings.TrimSpace(l)] = true
	}
	for _, want := range []string{".git", ".env", ".env.*", "*.local.yaml", "testdata/fixtures"} {
		if !lines[want] {
			t.Errorf(".dockerignore does not exclude %s", want)
		}
	}
}

type composeFile struct {
	Services map[string]struct {
		Ports       []string          `yaml:"ports"`
		ReadOnly    bool              `yaml:"read_only"`
		CapDrop     []string          `yaml:"cap_drop"`
		Environment map[string]string `yaml:"environment"`
		Healthcheck map[string]any    `yaml:"healthcheck"`
		DependsOn   map[string]struct {
			Condition string `yaml:"condition"`
		} `yaml:"depends_on"`
		Networks []string `yaml:"networks"`
	} `yaml:"services"`
	Networks map[string]struct {
		Internal bool `yaml:"internal"`
	} `yaml:"networks"`
}

// composeFindings: the database is never published, the API only on a
// loopback address, both have healthchecks, the API waits for a healthy
// database, runs read-only without capabilities, and no secret is written
// into the file (every one is interpolated from .env).
func composeFindings(src []byte) []string {
	var c composeFile
	if err := yaml.Unmarshal(src, &c); err != nil {
		return []string{"compose.yaml does not parse: " + err.Error()}
	}
	db, okDB := c.Services["db"]
	api, okAPI := c.Services["api"]
	if !okDB || !okAPI {
		return []string{"compose.yaml needs the services db and api"}
	}
	var out []string
	if len(db.Ports) > 0 {
		out = append(out, "the database publishes a port")
	}
	if len(api.Ports) != 1 || !regexp.MustCompile(`^\$\{API_PUBLISH:-\[::1\]:8080\}:8080$`).MatchString(api.Ports[0]) {
		out = append(out, "the API must be published once, on the loopback default: "+strings.Join(api.Ports, ", "))
	}
	if db.Healthcheck == nil || api.Healthcheck == nil {
		out = append(out, "a service has no healthcheck")
	}
	if api.DependsOn["db"].Condition != "service_healthy" {
		out = append(out, "the API does not wait for a healthy database")
	}
	if !api.ReadOnly || len(api.CapDrop) != 1 || api.CapDrop[0] != "ALL" {
		out = append(out, "the API must run read-only with every capability dropped")
	}
	for _, n := range db.Networks {
		if !c.Networks[n].Internal {
			out = append(out, "the database is on a non-internal network: "+n)
		}
	}
	for svc, s := range map[string]map[string]string{"db": db.Environment, "api": api.Environment} {
		for k, v := range s {
			if secretWord.MatchString(k) && !strings.HasPrefix(v, "${") {
				out = append(out, svc+": "+k+" is written into compose.yaml")
			}
			if strings.Contains(v, ":") && strings.Contains(v, "@") && !strings.Contains(v, "${") {
				out = append(out, svc+": "+k+" holds a literal credential")
			}
		}
	}
	return out
}

func TestComposeRules(t *testing.T) {
	req.Covers(t, "SSC-017", "SSC-019")
	src, err := os.ReadFile(filepath.Join(root, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range composeFindings(src) {
		t.Error(f)
	}
}

func TestComposeRulesCanSayNo(t *testing.T) {
	req.Covers(t, "SSC-017", "SSC-019")
	good, err := os.ReadFile(filepath.Join(root, "compose.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// Each plant must produce the finding named for it, not just any finding.
	for want, edit := range map[string][2]string{
		"database publishes a port":       {"    networks: [backend]\n\n  api:", "    networks: [backend]\n    ports: [\"5432:5432\"]\n\n  api:"},
		"on the loopback default":         {`"${API_PUBLISH:-[::1]:8080}:8080"`, `"8080:8080"`},
		"does not wait for a healthy":     {"condition: service_healthy", "condition: service_started"},
		"written into compose.yaml":       {"POSTGRES_PASSWORD: ${POSTGRES_PASSWORD:?set POSTGRES_PASSWORD in .env}", "POSTGRES_PASSWORD: hunter2"},
		"read-only with every capability": {"read_only: true", "read_only: false"},
		"non-internal network":            {"    internal: true\n", ""},
	} {
		bad := strings.Replace(string(good), edit[0], edit[1], 1)
		if bad == string(good) {
			t.Fatalf("plant for %q did not change the file", want)
		}
		if got := strings.Join(composeFindings([]byte(bad)), "; "); !strings.Contains(got, want) {
			t.Errorf("planted %q, the rules said %q", want, got)
		}
	}
}

// TestEnvExampleHoldsPlaceholdersOnly: every value in .env.example is a
// placeholder, and .env itself is ignored by git.
func TestEnvExampleHoldsPlaceholdersOnly(t *testing.T) {
	req.Covers(t, "SSC-017")
	src, err := os.ReadFile(filepath.Join(root, ".env.example"))
	if err != nil {
		t.Fatal(err)
	}
	read := 0
	for _, l := range strings.Split(string(src), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		read++
		if secretWord.MatchString(k) && !strings.HasPrefix(v, "change-me") {
			t.Errorf(".env.example sets %s to a value that is not a change-me placeholder", k)
		}
	}
	if read == 0 {
		t.Fatal("no variable read from .env.example")
	}
	ignore, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`(?m)^\.env$`).Match(ignore) {
		t.Error(".gitignore does not ignore .env")
	}
}
