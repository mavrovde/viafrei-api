// Package keyscli is the operator's key tool: `viafrei-api keys …`. It runs on
// the host with the access-store role; every action writes its audit record
// in the same transaction (SEC-044). A key is printed in full exactly once,
// by issue or rotate (SEC-012); nothing else ever prints a secret.
package keyscli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/metering"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
)

// Usage lists the commands.
const Usage = `usage: viafrei-api keys COMMAND [flags]
  plan    -name N -use-class consumer|non_commercial|commercial -tier critical|standard|bulk -scopes a,b -sources x,y
          -rate R -burst B [-quota-day D] [-quota-month M] -actor A    a quota of 0 is none
  issue   -plan N -label L -contact C [-expires 8760h] -actor A     prints the key once
  rotate  -id ID -actor A                                           prints the new key once
  revoke  -id ID -reason R -actor A
  move    -id ID -plan N -actor A
  accept  -id ID -source FAMILY -version V -actor A
  prune   -actor A                                                  removes audit records past audit.retention_days
                                                                    and usage records past metering.retention_days
  usage   [-id ID] [-hours 24]                                      prints the usage of the last hours, tab-separated`

func list(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// Run runs one keys command and returns the exit status: 0 done, 1 refused
// by the store or the lifecycle, 2 a usage error.
func Run(ctx context.Context, args []string, keys *auth.Keys, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, Usage)
		return 2
	}
	cmd, args := args[0], args[1:]
	fs := flag.NewFlagSet("keys "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		actor   = fs.String("actor", "", "who performs the action (audited)")
		id      = fs.String("id", "", "key id")
		plan    = fs.String("plan", "", "plan name")
		name    = fs.String("name", "", "plan name")
		use     = fs.String("use-class", "", "use class")
		tier    = fs.String("tier", "", "priority tier")
		scopes  = fs.String("scopes", "", "comma-separated scopes")
		sources = fs.String("sources", "", "comma-separated source families")
		label   = fs.String("label", "", "key label")
		contact = fs.String("contact", "", "key holder contact")
		expires = fs.Duration("expires", 0, "key lifetime; 0 means auth.default_expiry")
		reason  = fs.String("reason", "", "revocation reason")
		source  = fs.String("source", "", "source family")
		version = fs.String("version", "", "terms version")
		rate    = fs.Int("rate", 0, "plan rate, requests a second")
		burst   = fs.Int("burst", 0, "plan burst, requests at once")
		day     = fs.Int64("quota-day", 0, "plan units a quota day; 0 is no quota")
		month   = fs.Int64("quota-month", 0, "plan units a quota month; 0 is no quota")
		hours   = fs.Int("hours", 24, "usage: the hours to read back")
	)
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(stderr, Usage)
		return 2
	}
	var (
		key  access.Key
		n, u int64
		rows []metering.Row
		err  error
	)
	switch cmd {
	case "plan":
		p := access.Plan{Name: *name, UseClass: access.UseClass(*use), Tier: *tier, Sources: list(*sources),
			Rate: *rate, Burst: *burst, QuotaDay: *day, QuotaMonth: *month}
		for _, s := range list(*scopes) {
			p.Scopes = append(p.Scopes, access.Scope(s))
		}
		err = keys.PutPlan(ctx, p, *actor)
	case "issue":
		key, err = keys.Issue(ctx, *plan, *label, *contact, *expires, *actor)
	case "rotate":
		key, err = keys.Rotate(ctx, *id, *actor)
	case "revoke":
		err = keys.Revoke(ctx, *id, *reason, *actor)
	case "move":
		err = keys.Move(ctx, *id, *plan, *actor)
	case "accept":
		err = keys.Accept(ctx, *id, *source, *version, *actor)
	case "prune":
		if n, err = keys.Prune(ctx, *actor); err == nil {
			u, err = keys.PruneUsage(ctx, *actor)
		}
	case "usage":
		rows, err = keys.Usage(ctx, *id, time.Duration(*hours)*time.Hour)
	default:
		fmt.Fprintln(stderr, Usage)
		return 2
	}
	if err != nil {
		if errors.Is(err, auth.ErrInput) {
			fmt.Fprintln(stderr, err)
			return 2
		}
		fmt.Fprintln(stderr, "keys "+cmd+": refused:", err)
		return 1
	}
	switch {
	case key.Full != "":
		fmt.Fprintf(stdout, "key id %s\n%s\nThis is the only time the key is shown. Store it now.\n", key.ID, key.Full)
	case cmd == "prune":
		fmt.Fprintf(stdout, "%d audit records removed\n%d usage records removed\n", n, u)
	case cmd == "usage":
		printUsage(stdout, rows)
	default:
		fmt.Fprintf(stdout, "%s done at %s\n", cmd, time.Now().UTC().Format(time.RFC3339))
	}
	return 0
}

// printUsage writes the usage records as tab-separated lines under a header:
// the hour in UTC, the key, the operation and the counters.
func printUsage(w io.Writer, rows []metering.Row) {
	fmt.Fprintln(w, "hour\tkey\toperation\trequests\tnot_modified\trate_limited\tquota_exceeded\toverloaded\tdeadline_exhausted\tstorage_ms")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\n", r.Hour.UTC().Format(time.RFC3339), r.Key, r.Operation,
			r.Requests, r.NotModified, r.RateLimited, r.QuotaExceeded, r.Overloaded, r.DeadlineExhausted, r.Storage.Milliseconds())
	}
}
