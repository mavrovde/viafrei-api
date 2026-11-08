package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"

	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

// Probe asks the service's own /healthz on the loopback, at the port of
// server.listen_addr, within health.probe_timeout. A distroless image has no
// shell and no curl, so the binary's healthcheck subcommand calls this.
func Probe(ctx context.Context, cfg *config.Config) error {
	var body HealthBody
	status, err := askSelf(ctx, cfg, "/healthz", &body)
	if err != nil {
		return fmt.Errorf("healthcheck: %w", err)
	}
	if status != http.StatusOK || body.Status != "ok" {
		return fmt.Errorf("healthcheck: status %d", status)
	}
	return nil
}

// Versions is what a deployment expects the running service to report.
// An empty field is not compared.
type Versions struct {
	Platform string // the platform release, X.Y.Z
	Contract string // the read-contract version, major.minor
}

// ErrVersionMismatch marks an answer that was read and differs from the
// expected versions, as opposed to one that could not be read.
var ErrVersionMismatch = errors.New("version mismatch")

// CheckVersions asks the service's own /v1/meta on the loopback, like Probe,
// and compares the platform and read-contract versions it reports with want.
// A deployment check of the platform runs it in the container (the
// meta-check subcommand), so a deployed API that does not match the
// platform's release or schema is reported. A difference wraps
// ErrVersionMismatch and names both values; any other error means the answer
// could not be read, which is never a match.
func CheckVersions(ctx context.Context, cfg *config.Config, want Versions) error {
	var body MetaEnvelope
	status, err := askSelf(ctx, cfg, "/v1/meta", &body)
	if err != nil {
		return fmt.Errorf("meta-check: %w", err)
	}
	if status != http.StatusOK {
		return fmt.Errorf("meta-check: status %d", status)
	}
	var diffs []string
	got := "null"
	if body.Data.PlatformVersion != nil {
		got = *body.Data.PlatformVersion
	}
	if want.Platform != "" && got != want.Platform {
		diffs = append(diffs, fmt.Sprintf("platformVersion %s, want %s", got, want.Platform))
	}
	if want.Contract != "" && body.Data.ContractVersion != want.Contract {
		diffs = append(diffs, fmt.Sprintf("contractVersion %s, want %s", body.Data.ContractVersion, want.Contract))
	}
	if len(diffs) > 0 {
		return fmt.Errorf("meta-check: %w: %s", ErrVersionMismatch, strings.Join(diffs, "; "))
	}
	return nil
}

// loopback is the URL of path on this machine's loopback. The service
// listens without TLS behind its proxy; the request never leaves the host.
func loopback(port, path string) string {
	u := url.URL{Scheme: "http", Host: net.JoinHostPort("localhost", port), Path: path}
	return u.String()
}

// askSelf GETs path on the loopback at the port of server.listen_addr within
// health.probe_timeout and decodes the JSON answer into out.
func askSelf(ctx context.Context, cfg *config.Config, path string, out any) (int, error) {
	_, port, err := net.SplitHostPort(cfg.Server.ListenAddr)
	if err != nil {
		return 0, errors.New("server.listen_addr has no port")
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Health.ProbeTimeout)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, loopback(port, path), nil)
	if err != nil {
		return 0, err
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return 0, errors.New("no answer")
	}
	defer func() { _ = resp.Body.Close() }()
	if json.NewDecoder(resp.Body).Decode(out) != nil {
		return resp.StatusCode, fmt.Errorf("status %d, answer not readable", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

// ShutdownContext bounds a graceful shutdown by server.shutdown_grace.
func ShutdownContext(cfg *config.Config) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), cfg.Server.ShutdownGrace)
}
