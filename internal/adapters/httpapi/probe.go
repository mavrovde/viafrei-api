package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"

	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

// Probe asks the service's own /healthz on the loopback, at the port of
// server.listen_addr, within health.probe_timeout. A distroless image has no
// shell and no curl, so the binary's healthcheck subcommand calls this.
func Probe(ctx context.Context, cfg *config.Config) error {
	_, port, err := net.SplitHostPort(cfg.Server.ListenAddr)
	if err != nil {
		return errors.New("healthcheck: server.listen_addr has no port")
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Health.ProbeTimeout)
	defer cancel()
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+net.JoinHostPort("localhost", port)+"/healthz", nil)
	if err != nil {
		return fmt.Errorf("healthcheck: %w", err)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		return errors.New("healthcheck: no answer")
	}
	defer func() { _ = resp.Body.Close() }()
	var body HealthBody
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&body) != nil || body.Status != "ok" {
		return fmt.Errorf("healthcheck: status %d", resp.StatusCode)
	}
	return nil
}

// ShutdownContext bounds a graceful shutdown by server.shutdown_grace.
func ShutdownContext(cfg *config.Config) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), cfg.Server.ShutdownGrace)
}
