// Package consolecli is the operator's side of the owner console: it offers
// an owner an enrolment code, lists the enrolled passkeys and removes one.
package consolecli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/mavrovde/viafrei-api/internal/app/console"
)

// Usage lists the commands.
const Usage = `usage: viafrei-api console COMMAND [flags]
  enrol     -owner NAME -actor A     prints a one-time enrolment code; open /enrol on the console and enter it
  passkeys                           lists the enrolled passkeys, tab-separated
  remove    -id ID -actor A          removes a passkey (ID as passkeys prints it)`

// Owners is the owners' passkey lifecycle the commands run.
type Owners interface {
	Offer(ctx context.Context, owner, actor string) (string, error)
	Passkeys(ctx context.Context) ([]console.Passkey, error)
	Remove(ctx context.Context, name, actor string) error
}

// Run runs one console command and returns the exit status: 0 done, 1
// refused by the store, 2 a usage error.
func Run(ctx context.Context, args []string, owners Owners, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, Usage)
		return 2
	}
	cmd, args := args[0], args[1:]
	fs := flag.NewFlagSet("console "+cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	var (
		owner = fs.String("owner", "", "the owner's name: 1 to 32 of a-z, 0-9, '.', '_' and '-'")
		actor = fs.String("actor", "", "who performs the action (audited)")
		id    = fs.String("id", "", "passkey id")
	)
	if err := fs.Parse(args); err != nil || fs.NArg() > 0 {
		fmt.Fprintln(stderr, Usage)
		return 2
	}
	var err error
	switch cmd {
	case "enrol":
		var code string
		if code, err = owners.Offer(ctx, *owner, *actor); err == nil {
			fmt.Fprintf(stdout, "enrolment code for %s\n%s\nThis is the only time the code is shown. It works once, until admin.enrolment_ttl has passed.\n", *owner, code)
		}
	case "passkeys":
		var keys []console.Passkey
		if keys, err = owners.Passkeys(ctx); err == nil {
			fmt.Fprintln(stdout, "owner\tid\tfingerprint\tenrolled\tlast_used")
			for _, p := range keys {
				last := ""
				if p.LastUsedAt != nil {
					last = p.LastUsedAt.UTC().Format("2006-01-02T15:04:05Z")
				}
				fmt.Fprintf(stdout, "%s\t%s\t%s\t%s\t%s\n", p.Owner, p.Name(), console.Fingerprint(p.ID), p.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"), last)
			}
		}
	case "remove":
		if err = owners.Remove(ctx, *id, *actor); err == nil {
			fmt.Fprintln(stdout, "passkey removed")
		}
	default:
		fmt.Fprintln(stderr, Usage)
		return 2
	}
	switch {
	case err == nil:
		return 0
	case errors.Is(err, console.ErrInput):
		fmt.Fprintln(stderr, err)
		return 2
	default:
		fmt.Fprintln(stderr, "console "+cmd+": refused:", err)
		return 1
	}
}
