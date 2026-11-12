// Command viafrei-api is the ViaFrei API service.
//
//	viafrei-api [serve]            run the service (the default)
//	viafrei-api healthcheck        exit 0 when /healthz answers ok (for container healthchecks)
//	viafrei-api meta-check [-platform X.Y.Z] [-contract M.m]
//	                               exit 0 when /v1/meta reports these versions, 1 on a mismatch
//	viafrei-api config check       validate the configuration and exit
//	viafrei-api config show        print the effective configuration, secrets hidden
//	viafrei-api config docs        print the configuration reference (docs/configuration.md)
//	viafrei-api openapi            print the OpenAPI document (api/openapi.json)
//	viafrei-api version            print the build's version facts
//	viafrei-api keys COMMAND       the operator's key tool (plan, issue, rotate, revoke, move, accept, prune)
//	viafrei-api console COMMAND    the owner console's passkeys (enrol, passkeys, remove)
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mavrovde/viafrei-api/db/contract"
	"github.com/mavrovde/viafrei-api/internal/adapters/accessstore"
	consoleweb "github.com/mavrovde/viafrei-api/internal/adapters/console"
	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/admission"
	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/console"
	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/app/health"
	"github.com/mavrovde/viafrei-api/internal/app/metering"
	"github.com/mavrovde/viafrei-api/internal/app/ratelimit"
	"github.com/mavrovde/viafrei-api/internal/buildinfo"
	"github.com/mavrovde/viafrei-api/internal/invariant"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/runtime/consolecli"
	"github.com/mavrovde/viafrei-api/internal/runtime/keyscli"
	"github.com/mavrovde/viafrei-api/internal/runtime/logging"
	"github.com/mavrovde/viafrei-api/internal/runtime/wire"
)

func main() {
	os.Exit(run(os.Args[1:], os.Environ(), os.Stdout, os.Stderr))
}

func run(args, environ []string, stdout, stderr io.Writer) int {
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		return serve(environ, stderr)
	case "healthcheck":
		return healthcheck(environ, stderr)
	case "meta-check":
		return metaCheck(args, environ, stderr)
	case "config":
		return configCmd(args, environ, stdout, stderr)
	case "openapi":
		return openapiCmd(environ, stdout, stderr)
	case "keys":
		return keysCmd(args, environ, stdout, stderr)
	case "console":
		return consoleCmd(args, environ, stdout, stderr)
	case "version":
		_ = json.NewEncoder(stdout).Encode(buildinfo.Get())
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q; known: serve, healthcheck, meta-check, config {check,show,docs}, openapi, keys, console, version\n", cmd)
		return 2
	}
}

func load(environ []string, requireSecrets bool, stderr io.Writer) (*config.Config, bool) {
	cfg, err := config.Load(config.Options{Environ: environ, RequireSecrets: requireSecrets})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return nil, false
	}
	return cfg, true
}

func serve(environ []string, stderr io.Writer) int {
	cfg, ok := load(environ, true, stderr)
	if !ok {
		return config.ExitCode
	}
	log := logging.New(cfg.Log, os.Stdout)
	expected, err := health.ExpectedVersion(cfg.Contract.Expected, contract.Version())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return config.ExitCode
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	st, err := store.Open(ctx, cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return config.ExitCode
	}
	defer st.Close()
	st.WithAdmission(admission.New(wire.AdmissionSettings(cfg), nil)).WithSharedBudget(wire.SharedBudget(cfg))
	addresses := ratelimit.NewIPLimiter(wire.AddressSettings(cfg), nil)
	go addresses.Run(ctx)

	deps := httpapi.Deps{
		Config:          cfg,
		Readiness:       health.NewChecker(st, expected, cfg.Health.ReadyTimeout),
		Logger:          log,
		Build:           buildinfo.Get(),
		ContractVersion: contract.Version(),
		Sources:         st,
		Features: features.New(features.Repositories{Register: st, Charging: st, Parking: st, Places: st},
			wire.FeatureSettings(cfg), time.Now),
		Addresses: addresses,
	}

	if cfg.Access.URL.IsSet() {
		as, err := accessstore.Open(ctx, cfg)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return config.ExitCode
		}
		defer as.Close()
		authn := auth.New(wire.AuthSettings(cfg), as, nil)
		go authn.Run(ctx)
		deps.Auth = authn
		deps.Keys = ratelimit.NewKeyLimiter(wire.QuotaZone(cfg), as, nil)
		meter := metering.New(wire.MeteringSettings(cfg), as, nil)
		deps.Meter = meter
		// The meter outlives the server: it is stopped after the shutdown,
		// so the requests answered during the shutdown are counted, and its
		// last flush is waited for before the access store closes.
		mctx, stopMeter := context.WithCancel(context.WithoutCancel(ctx))
		metered := make(chan struct{})
		go func() { meter.Run(mctx, reportUsage(log)); close(metered) }()
		defer func() { stopMeter(); <-metered }()
		if addr, on := wire.ConsoleListener(cfg); on {
			stopConsole, err := startConsole(ctx, cfg, addr, as, log)
			if err != nil {
				fmt.Fprintln(stderr, err)
				return config.ExitCode
			}
			defer stopConsole()
		}
	} else {
		log.Warn("no access store configured: keyed operations are switched off")
	}
	srv, err := httpapi.NewServer(deps)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return config.ExitCode
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.ListenAndServe() }()
	log.Info("listening", "addr", cfg.Server.ListenAddr, "version", buildinfo.Get().Version)

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Error("server stopped", "error", err)
			return 1
		}
	case <-ctx.Done():
		log.Info("shutting down")
		sctx, cancel := httpapi.ShutdownContext(cfg)
		defer cancel()
		if err := srv.Shutdown(sctx); err != nil {
			log.Error("shutdown incomplete", "error", err)
			return 1
		}
	}
	return 0
}

// healthcheck lets a distroless image (no shell, no curl) run a container
// healthcheck: it asks the service's own /healthz on the loopback.
func healthcheck(environ []string, stderr io.Writer) int {
	cfg, ok := load(environ, false, stderr)
	if !ok {
		return config.ExitCode
	}
	if err := httpapi.Probe(context.Background(), cfg); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

// metaCheck is the platform deployment check's half inside the container:
// it asks the running service's own /v1/meta and compares the platform
// release and the read-contract version with the ones the deployment
// expects. Exit 0 on a match, 1 on a mismatch (both values named), 2 when the
// answer cannot be read or the flags are wrong, never a pass.
func metaCheck(args, environ []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("meta-check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var want httpapi.Versions
	fs.StringVar(&want.Platform, "platform", "", "platform release X.Y.Z the service must report")
	fs.StringVar(&want.Contract, "contract", "", "read-contract version major.minor the service must report")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if want == (httpapi.Versions{}) {
		fmt.Fprintln(stderr, "meta-check: name -platform, -contract or both; a check of nothing is not a pass")
		return 2
	}
	cfg, ok := load(environ, false, stderr)
	if !ok {
		return config.ExitCode
	}
	err := httpapi.CheckVersions(context.Background(), cfg, want)
	switch {
	case errors.Is(err, httpapi.ErrVersionMismatch):
		fmt.Fprintln(stderr, err)
		return 1
	case err != nil:
		fmt.Fprintln(stderr, err)
		return 2
	}
	return 0
}

func configCmd(args, environ []string, stdout, stderr io.Writer) int {
	if len(args) != 1 {
		fmt.Fprintln(stderr, "usage: viafrei-api config {check|show|docs}")
		return 2
	}
	switch args[0] {
	case "docs":
		if err := config.WriteReference(stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	case "check":
		if _, ok := load(environ, true, stderr); !ok {
			return config.ExitCode
		}
		fmt.Fprintf(stdout, "configuration valid: %d keys checked\n", len(config.Fields()))
		return 0
	case "show":
		cfg, ok := load(environ, false, stderr)
		if !ok {
			return config.ExitCode
		}
		for _, line := range config.Show(cfg) {
			fmt.Fprintln(stdout, line)
		}
		return 0
	default:
		fmt.Fprintf(stderr, "unknown config command %q\n", args[0])
		return 2
	}
}

func openapiCmd(environ []string, stdout, stderr io.Writer) int {
	cfg, ok := load(environ, false, stderr)
	if !ok {
		return config.ExitCode
	}
	doc, err := httpapi.OpenAPI(cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = stdout.Write(doc)
	return 0
}

// keysCmd runs the operator's key tool against the access store.
func keysCmd(args, environ []string, stdout, stderr io.Writer) int {
	cfg, ok := load(environ, false, stderr)
	if !ok {
		return config.ExitCode
	}
	if !cfg.Access.URL.IsSet() {
		fmt.Fprintln(stderr, "keys: access.url is not set; set VFAPI_ACCESS_URL or VFAPI_ACCESS_URL_FILE")
		return config.ExitCode
	}
	ctx := context.Background()
	as, err := accessstore.Open(ctx, cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return config.ExitCode
	}
	defer as.Close()
	return keyscli.Run(ctx, args, auth.NewKeys(wire.AuthSettings(cfg), as, nil, nil), stdout, stderr)
}

// consoleCmd runs the owner console's passkey tool against the access store.
func consoleCmd(args, environ []string, stdout, stderr io.Writer) int {
	cfg, ok := load(environ, false, stderr)
	if !ok {
		return config.ExitCode
	}
	if !cfg.Access.URL.IsSet() || !cfg.Auth.Pepper.IsSet() {
		fmt.Fprintln(stderr, "console: access.url and auth.pepper must be set; the enrolment code is stored as its MAC")
		return config.ExitCode
	}
	ctx := context.Background()
	as, err := accessstore.Open(ctx, cfg)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return config.ExitCode
	}
	defer as.Close()
	return consolecli.Run(ctx, args, console.NewOwners(wire.ConsoleOwners(cfg), as, nil, nil), stdout, stderr)
}

// startConsole serves the owner console on admin.listen_addr, which must be
// a loopback address, and returns the function that stops it.
func startConsole(ctx context.Context, cfg *config.Config, addr string, as *accessstore.Store, log *slog.Logger) (func(), error) {
	clog := log.With("listener", "console")
	// The settings New checks are the ones config.Load has validated.
	h := invariant.Must(consoleweb.New(consoleweb.Deps{
		Settings: wire.ConsoleWeb(cfg),
		Guard:    console.NewGuard(wire.ConsoleGuard(cfg), nil, nil),
		Owners:   console.NewOwners(wire.ConsoleOwners(cfg), as, nil, nil),
		Keys:     auth.NewKeys(wire.AuthSettings(cfg), as, nil, nil),
		Logger:   clog,
	}))
	l, err := consoleweb.Listen(ctx, addr)
	if err != nil {
		return nil, fmt.Errorf("admin.listen_addr: %w", err)
	}
	// The console's connections are held to the public listener's timeouts.
	srv := &http.Server{
		Handler: h, ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout, ReadTimeout: cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout, IdleTimeout: cfg.Server.IdleTimeout, MaxHeaderBytes: cfg.Server.MaxHeaderBytes,
		ErrorLog: consoleweb.ErrorLog(clog),
	}
	go func() { _ = srv.Serve(l) }()
	log.Info("console listening", "addr", l.Addr().String())
	return func() { _ = srv.Close() }, nil
}

// reportUsage logs a usage flush that failed or dropped counts; a clean
// flush is silent.
func reportUsage(log *slog.Logger) func(metering.Report) {
	return func(r metering.Report) {
		if r.Err != nil {
			log.Error("usage write failed; the counts are kept for the next flush", "error", r.Err)
		}
		if r.Dropped > 0 {
			log.Warn("usage counts dropped: metering.max_keys_buffered reached", "dropped", r.Dropped)
		}
	}
}
