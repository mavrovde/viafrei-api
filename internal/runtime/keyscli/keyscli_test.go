package keyscli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/runtime/keyscli"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

type ledger struct {
	plans []access.Plan
	err   error
}

func (l *ledger) Lookup(context.Context, string) (access.Record, error) {
	return access.Record{ID: "abcdefghij", Environment: access.Live, ExpiresAt: time.Now().Add(time.Hour)}, l.err
}
func (l *ledger) Remac(context.Context, string, bool, []byte) error { return nil }
func (l *ledger) ChangedSince(context.Context, time.Time) ([]string, time.Time, error) {
	return nil, time.Time{}, nil
}
func (l *ledger) PutPlan(_ context.Context, _ auth.Change, p access.Plan) error {
	l.plans = append(l.plans, p)
	return l.err
}
func (l *ledger) Issue(context.Context, auth.Change, access.Record, string, string) error {
	return l.err
}
func (l *ledger) Rotate(context.Context, auth.Change, []byte, time.Time) error      { return l.err }
func (l *ledger) Revoke(context.Context, auth.Change) error                         { return l.err }
func (l *ledger) Move(context.Context, auth.Change, string) error                   { return l.err }
func (l *ledger) Accept(context.Context, auth.Change, access.Acceptance) error      { return l.err }
func (l *ledger) PruneAudit(context.Context, auth.Change, time.Time) (int64, error) { return 7, l.err }

func run(l *ledger, args ...string) (int, string, string) {
	s := auth.Settings{
		Environment: access.Live, Prefixes: access.Prefixes{Live: "vf_live_", Test: "vf_test_"},
		Peppers: access.Peppers{Current: bytes.Repeat([]byte("p"), 32)}, DefaultExpiry: 24 * time.Hour,
		MinExpiry: time.Hour, MaxExpiry: 48 * time.Hour, RotationOverlap: time.Hour,
	}
	var out, errb bytes.Buffer
	code := keyscli.Run(context.Background(), args, auth.NewKeys(s, l, nil, nil), &out, &errb)
	return code, out.String(), errb.String()
}

func TestCommands(t *testing.T) {
	req.Covers(t, "SEC-012", "SEC-040")
	l := &ledger{}
	cases := []struct {
		args []string
		code int
		out  string
	}{
		{nil, 2, ""},
		{[]string{"nope", "-actor", "o"}, 2, ""},
		{[]string{"issue", "-bogus"}, 2, ""},
		{[]string{"issue", "extra"}, 2, ""},
		{[]string{"plan", "-name", "pro", "-use-class", "commercial", "-tier", "standard", "-scopes", "roads, energy,", "-sources", "alpha", "-actor", "o"}, 0, "plan done"},
		{[]string{"issue", "-plan", "pro", "-label", "l", "-contact", "c", "-actor", "o"}, 0, "only time the key is shown"},
		{[]string{"issue", "-plan", "pro", "-label", "l", "-contact", "c"}, 2, ""},
		{[]string{"rotate", "-id", "abcdefghij", "-actor", "o"}, 0, "vf_live_abcdefghij_"},
		{[]string{"revoke", "-id", "abcdefghij", "-reason", "leaked", "-actor", "o"}, 0, "revoke done"},
		{[]string{"move", "-id", "abcdefghij", "-plan", "dev", "-actor", "o"}, 0, "move done"},
		{[]string{"accept", "-id", "abcdefghij", "-source", "delta", "-version", "v1", "-actor", "o"}, 0, "accept done"},
		{[]string{"prune", "-actor", "o"}, 0, "7 audit records removed"},
	}
	for _, c := range cases {
		code, out, _ := run(l, c.args...)
		if code != c.code || !strings.Contains(out, c.out) {
			t.Errorf("%v: %d %q", c.args, code, out)
		}
	}
	if p := l.plans[0]; len(p.Scopes) != 2 || p.Scopes[1] != "energy" || len(p.Sources) != 1 {
		t.Errorf("lists not parsed: %+v", p)
	}
	l.err = errors.New("store down")
	if code, _, errOut := run(l, "revoke", "-id", "x", "-reason", "r", "-actor", "o"); code != 1 || !strings.Contains(errOut, "refused") {
		t.Errorf("refusal: %d %q", code, errOut)
	}
}
