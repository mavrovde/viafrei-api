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
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/mavrovde/viafrei-api/db/contract"
	"github.com/mavrovde/viafrei-api/internal/adapters/accessstore"
	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/health"
	"github.com/mavrovde/viafrei-api/internal/buildinfo"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
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
	case "version":
		_ = json.NewEncoder(stdout).Encode(buildinfo.Get())
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q; known: serve, healthcheck, meta-check, config {check,show,docs}, openapi, keys, version\n", cmd)
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

	deps := httpapi.Deps{
		Config:          cfg,
		Readiness:       health.NewChecker(st, expected, cfg.Health.ReadyTimeout),
		Logger:          log,
		Build:           buildinfo.Get(),
		ContractVersion: contract.Version(),
		Sources:         st,
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
