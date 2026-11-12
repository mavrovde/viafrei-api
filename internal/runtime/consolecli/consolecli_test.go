package consolecli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/console"
	"github.com/mavrovde/viafrei-api/internal/runtime/consolecli"
	"github.com/mavrovde/viafrei-api/internal/testkit/consolemem"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func run(o consolecli.Owners, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := consolecli.Run(context.Background(), args, o, &out, &errb)
	return code, out.String(), errb.String()
}

func TestTheConsoleCommands(t *testing.T) {
	req.Covers(t, "THR-018")
	st := consolemem.New()
	o := console.NewOwners(console.OwnerSettings{Pepper: []byte(strings.Repeat("p", 32)), EnrolmentTTL: time.Hour}, st, nil, nil)
	code, out, _ := run(o, "enrol", "-owner", "alice", "-actor", "ops")
	lines := strings.Split(out, "\n")
	if code != 0 || len(lines) < 3 || len(lines[1]) != 43 || !strings.Contains(out, "only time") {
		t.Fatalf("enrol: %d %q", code, out)
	}
	ctx := context.Background()
	if err := o.Enrol(ctx, lines[1], console.Passkey{ID: []byte("credential-01"), Owner: "alice"}); err != nil {
		t.Fatal(err)
	}
	p, _ := o.Passkey(ctx, []byte("credential-01"))
	_ = o.SignedIn(ctx, p, 2, 5)
	if err := o.Enrol(ctx, "x", console.Passkey{}); err == nil {
		t.Fatal("unknown code")
	}
	code, out, _ = run(o, "passkeys")
	if code != 0 || !strings.HasPrefix(out, "owner\tid\tfingerprint") || !strings.Contains(out, "alice\t"+p.Name()+"\t"+console.Fingerprint(p.ID)) {
		t.Fatalf("passkeys: %d %q", code, out)
	}
	// A passkey never used prints an empty last_used.
	code2, _ := o.Offer(ctx, "bob", "ops")
	_ = o.Enrol(ctx, code2, console.Passkey{ID: []byte("credential-02"), Owner: "bob"})
	if _, out, _ = run(o, "passkeys"); !strings.Contains(out, "Z\t\n") {
		t.Fatalf("an unused passkey: %q", out)
	}
	if code, out, _ = run(o, "remove", "-id", p.Name(), "-actor", "ops"); code != 0 || out != "passkey removed\n" {
		t.Fatalf("remove: %d %q", code, out)
	}
	if code, _, errs := run(o, "remove", "-id", p.Name(), "-actor", "ops"); code != 1 || !strings.Contains(errs, "refused") {
		t.Fatalf("remove again: %d %q", code, errs)
	}
	for _, args := range [][]string{nil, {"drop"}, {"enrol", "-bogus"}, {"passkeys", "extra"}, {"enrol", "-owner", "Alice", "-actor", "ops"}, {"remove", "-id", "!", "-actor", "ops"}} {
		if code, _, _ := run(o, args...); code != 2 {
			t.Fatalf("%v: %d", args, code)
		}
	}
	st.Err = errors.New("down")
	for _, args := range [][]string{{"enrol", "-owner", "alice", "-actor", "ops"}, {"passkeys"}} {
		if code, _, _ := run(o, args...); code != 1 {
			t.Fatalf("%v: %d", args, code)
		}
	}
}
