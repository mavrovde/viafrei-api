// Command fakelive runs the fake live refresher (internal/testkit/fakelive)
// against a local database, for manual runs of the on-demand refreshes and
// for the parity producer: it claims and settles every refresh the API
// records, by a script, until interrupted.
//
//	fakelive -db <owner URL> [-registry test/live/registry.json] [-script test/live/fakelive.json]
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/mavrovde/viafrei-api/internal/testkit/fakelive"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stderr))
}

// run runs the fake until ctx ends: 0 on a clean stop, 1 on an error, 2 on
// a usage error.
func run(ctx context.Context, args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("fakelive", flag.ContinueOnError)
	fs.SetOutput(stderr)
	db := fs.String("db", "", "the database's owner URL (required)")
	registry := fs.String("registry", "test/live/registry.json", "the live sources registry the API reads")
	script := fs.String("script", "test/live/fakelive.json", "the script the fake settles by")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *db == "" {
		_, _ = fmt.Fprintln(stderr, "fakelive: -db is required")
		return 2
	}
	sc, err := fakelive.Load(*registry, *script)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	f, err := fakelive.Start(ctx, *db, sc)
	if err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	_, _ = fmt.Fprintln(stderr, "fakelive: settling refreshes; interrupt to stop")
	<-ctx.Done()
	if err := f.Stop(); err != nil {
		_, _ = fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
