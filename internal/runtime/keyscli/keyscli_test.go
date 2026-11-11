package keyscli_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/metering"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/runtime/keyscli"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

type ledger struct {
	plans []access.Plan
	since time.Time
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
func (l *ledger) PruneUsage(context.Context, auth.Change, int) (int64, error)       { return 4, l.err }
func (l *ledger) Usage(_ context.Context, id string, since time.Time) ([]metering.Row, error) {
	l.since = since
	return []metering.Row{{Key: id, Hour: time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC), Operation: "getKey",
		Counts: metering.Counts{Requests: 3, NotModified: 1, RateLimited: 2, Storage: 1500 * time.Microsecond}}}, l.err
}

func (l *ledger) KeyList(context.Context) ([]auth.KeyView, error)           { return nil, l.err }
func (l *ledger) PlanList(context.Context) ([]access.Plan, error)           { return nil, l.err }
func (l *ledger) AuditList(context.Context, int) ([]auth.AuditEntry, error) { return nil, l.err }

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
		{[]string{"plan", "-name", "pro", "-use-class", "commercial", "-tier", "standard", "-scopes", "roads, energy,", "-sources", "alpha",
			"-rate", "20", "-burst", "40", "-quota-day", "1000", "-quota-month", "20000", "-actor", "o"}, 0, "plan done"},
		{[]string{"plan", "-name", "pro", "-use-class", "commercial", "-tier", "standard", "-actor", "o"}, 2, ""},
		{[]string{"usage", "-id", "abcdefghij", "-hours", "3"}, 0, "2026-05-01T09:00:00Z\tabcdefghij\tgetKey\t3\t1\t2\t0\t0\t0\t1\n"},
		{[]string{"usage", "-hours", "0"}, 2, ""},
		{[]string{"issue", "-plan", "pro", "-label", "l", "-contact", "c", "-actor", "o"}, 0, "only time the key is shown"},
		{[]string{"issue", "-plan", "pro", "-label", "l", "-contact", "c"}, 2, ""},
		{[]string{"rotate", "-id", "abcdefghij", "-actor", "o"}, 0, "vf_live_abcdefghij_"},
		{[]string{"revoke", "-id", "abcdefghij", "-reason", "leaked", "-actor", "o"}, 0, "revoke done"},
		{[]string{"move", "-id", "abcdefghij", "-plan", "dev", "-actor", "o"}, 0, "move done"},
		{[]string{"accept", "-id", "abcdefghij", "-source", "delta", "-version", "v1", "-actor", "o"}, 0, "accept done"},
		{[]string{"prune", "-actor", "o"}, 0, "7 audit records removed\n4 usage records removed"},
	}
	for _, c := range cases {
		code, out, _ := run(l, c.args...)
		if code != c.code || !strings.Contains(out, c.out) {
			t.Errorf("%v: %d %q", c.args, code, out)
		}
	}
	if p := l.plans[0]; len(p.Scopes) != 2 || p.Scopes[1] != "energy" || len(p.Sources) != 1 ||
		p.Rate != 20 || p.Burst != 40 || p.QuotaDay != 1000 || p.QuotaMonth != 20000 {
		t.Errorf("flags not parsed: %+v", p)
	}
	if _, out, _ := run(l, "usage"); !strings.HasPrefix(out, "hour\tkey\toperation\trequests") {
		t.Errorf("no header: %q", out)
	}
	if d := time.Since(l.since); d < 24*time.Hour || d > 25*time.Hour {
		t.Errorf("the default window is not 24 hours: %v", d)
	}
	l.err = errors.New("store down")
	if code, _, errOut := run(l, "revoke", "-id", "x", "-reason", "r", "-actor", "o"); code != 1 || !strings.Contains(errOut, "refused") {
		t.Errorf("refusal: %d %q", code, errOut)
	}
}
