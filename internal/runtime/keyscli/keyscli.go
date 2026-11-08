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
	"github.com/mavrovde/viafrei-api/internal/domain/access"
)

// Usage lists the commands.
const Usage = `usage: viafrei-api keys COMMAND [flags]
  plan    -name N -use-class consumer|non_commercial|commercial -tier critical|standard|bulk -scopes a,b -sources x,y -actor A
  issue   -plan N -label L -contact C [-expires 8760h] -actor A     prints the key once
  rotate  -id ID -actor A                                           prints the new key once
  revoke  -id ID -reason R -actor A
  move    -id ID -plan N -actor A
  accept  -id ID -source FAMILY -version V -actor A
  prune   -actor A                                                  removes audit records past audit.retention_days`

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
	)
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(stderr, Usage)
		return 2
	}
	var (
		key access.Key
		n   int64
		err error
	)
	switch cmd {
	case "plan":
		p := access.Plan{Name: *name, UseClass: access.UseClass(*use), Tier: *tier, Sources: list(*sources)}
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
		n, err = keys.Prune(ctx, *actor)
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
		fmt.Fprintf(stdout, "%d audit records removed\n", n)
	default:
		fmt.Fprintf(stdout, "%s done at %s\n", cmd, time.Now().UTC().Format(time.RFC3339))
	}
	return 0
}
