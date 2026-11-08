package auth_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// fakeLedger records every lifecycle write.
type fakeLedger struct {
	fakeStore
	changes []auth.Change
	plans   []access.Plan
	issued  []access.Record
	rotated []time.Time
	moved   []string
	accepts []access.Acceptance
	before  []time.Time
	err     error
}

func (l *fakeLedger) note(c auth.Change) error {
	l.changes = append(l.changes, c)
	return l.err
}

func (l *fakeLedger) PutPlan(_ context.Context, c auth.Change, p access.Plan) error {
	l.plans = append(l.plans, p)
	return l.note(c)
}

func (l *fakeLedger) Issue(_ context.Context, c auth.Change, r access.Record, _, _ string) error {
	l.issued = append(l.issued, r)
	return l.note(c)
}

func (l *fakeLedger) Rotate(_ context.Context, c auth.Change, _ []byte, until time.Time) error {
	l.rotated = append(l.rotated, until)
	return l.note(c)
}

func (l *fakeLedger) Revoke(_ context.Context, c auth.Change) error { return l.note(c) }

func (l *fakeLedger) Move(_ context.Context, c auth.Change, plan string) error {
	l.moved = append(l.moved, plan)
	return l.note(c)
}

func (l *fakeLedger) Accept(_ context.Context, c auth.Change, a access.Acceptance) error {
	l.accepts = append(l.accepts, a)
	return l.note(c)
}

func (l *fakeLedger) PruneAudit(_ context.Context, c auth.Change, before time.Time) (int64, error) {
	l.before = append(l.before, before)
	return 3, l.note(c)
}

func newKeys(l *fakeLedger) *auth.Keys {
	return auth.NewKeys(settings(), l, nil, func() time.Time { return t0 })
}

func TestIssue(t *testing.T) {
	req.Covers(t, "SEC-040", "SEC-010", "SEC-012")
	l := &fakeLedger{}
	k := newKeys(l)
	ctx := context.Background()
	key, err := k.Issue(ctx, "standard", "label", "ops@example.test", 0, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if len(l.issued) != 1 || !l.issued[0].ExpiresAt.Equal(t0.Add(365*24*time.Hour)) {
		t.Fatalf("default expiry not applied: %+v", l.issued)
	}
	r := l.issued[0]
	if !bytes.Equal(r.MAC, access.MAC(pepper, key.ID, key.Secret)) || bytes.Contains(r.MAC, []byte(key.Secret)) {
		t.Fatal("the stored value is not the MAC of the secret")
	}
	if c := l.changes[0]; c.Action != auth.ActionIssue || c.KeyID != key.ID || c.Actor != "operator" || !c.At.Equal(t0) {
		t.Fatalf("audit %+v", c)
	}
	if _, err := k.Issue(ctx, "standard", "l", "c", 48*time.Hour, "o"); err != nil || !l.issued[1].ExpiresAt.Equal(t0.Add(48*time.Hour)) {
		t.Fatalf("explicit expiry: %v", err)
	}
	for name, exp := range map[string]time.Duration{"too long": 1096 * 24 * time.Hour, "too short": time.Hour, "negative": -time.Hour} {
		if _, err := k.Issue(ctx, "standard", "l", "c", exp, "o"); !errors.Is(err, auth.ErrInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, args := range map[string][4]string{
		"no plan": {"", "l", "c", "o"}, "no label": {"p", "", "c", "o"}, "no contact": {"p", "l", " ", "o"},
		"no actor": {"p", "l", "c", ""}, "long label": {"p", strings.Repeat("x", 201), "c", "o"},
	} {
		if _, err := k.Issue(ctx, args[0], args[1], args[2], 0, args[3]); !errors.Is(err, auth.ErrInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// The bounds are inclusive: the shortest and longest expiry and a text
	// of exactly the limit are accepted.
	for name, exp := range map[string]time.Duration{"shortest": 24 * time.Hour, "longest": 1095 * 24 * time.Hour} {
		if _, err := k.Issue(ctx, "standard", "l", "c", exp, "o"); err != nil {
			t.Errorf("%s expiry refused: %v", name, err)
		}
	}
	if _, err := k.Issue(ctx, "standard", strings.Repeat("x", 200), "c", 0, "o"); err != nil {
		t.Errorf("a label of exactly 200 characters refused: %v", err)
	}
	l.err = errors.New("refused")
	if _, err := k.Issue(ctx, "p", "l", "c", 0, "o"); err == nil {
		t.Error("a store refusal is lost")
	}
	broken := auth.NewKeys(settings(), l, iotest.ErrReader(errors.New("no entropy")), nil)
	if _, err := broken.Issue(ctx, "p", "l", "c", 0, "o"); err == nil {
		t.Error("a random-source fault is lost")
	}
}

func TestRotate(t *testing.T) {
	req.Covers(t, "SEC-041", "SEC-044")
	l := &fakeLedger{}
	k := newKeys(l)
	ctx := context.Background()
	old := issue(t, &l.fakeStore, t0.Add(time.Hour))
	key, err := k.Rotate(ctx, old.ID, "operator")
	if err != nil {
		t.Fatal(err)
	}
	if key.ID != old.ID || key.Secret == old.Secret || !l.rotated[0].Equal(t0.Add(7*24*time.Hour)) {
		t.Fatalf("rotation %+v until %v", key, l.rotated)
	}
	if l.changes[0].Action != auth.ActionRotate {
		t.Fatal("no audit record")
	}
	if _, err := k.Rotate(ctx, "nosuchkey0", "o"); !errors.Is(err, auth.ErrUnknown) {
		t.Errorf("unknown key: %v", err)
	}
	if _, err := k.Rotate(ctx, old.ID, ""); !errors.Is(err, auth.ErrInput) {
		t.Errorf("no actor: %v", err)
	}
	expired := issue(t, &l.fakeStore, t0)
	if _, err := k.Rotate(ctx, expired.ID, "o"); !errors.Is(err, auth.ErrInput) {
		t.Errorf("expired key: %v", err)
	}
	revoked := issue(t, &l.fakeStore, t0.Add(time.Hour))
	r := l.keys[revoked.ID]
	r.RevokedAt = &t0
	l.keys[revoked.ID] = r
	if _, err := k.Rotate(ctx, revoked.ID, "o"); !errors.Is(err, auth.ErrInput) {
		t.Errorf("revoked key: %v", err)
	}
	l.err = errors.New("refused")
	if _, err := k.Rotate(ctx, old.ID, "o"); err == nil {
		t.Error("a store refusal is lost")
	}
	broken := auth.NewKeys(settings(), l, iotest.ErrReader(errors.New("no entropy")), func() time.Time { return t0 })
	if _, err := broken.Rotate(ctx, old.ID, "o"); err == nil {
		t.Error("a random-source fault is lost")
	}
}

func TestLifecycleValidatesAndAudits(t *testing.T) {
	req.Covers(t, "SEC-044", "SEC-034", "SEC-021", "SEC-022")
	l := &fakeLedger{}
	k := newKeys(l)
	ctx := context.Background()
	good := access.Plan{Name: "pro", UseClass: "commercial", Tier: "standard", Scopes: []access.Scope{"roads"}, Sources: []string{"alpha"}}
	if err := k.PutPlan(ctx, good, "o"); err != nil {
		t.Fatal(err)
	}
	if err := k.Revoke(ctx, "abcdefghij", "leaked", "o"); err != nil {
		t.Fatal(err)
	}
	if err := k.Move(ctx, "abcdefghij", "pro", "o"); err != nil || l.moved[0] != "pro" {
		t.Fatal(err)
	}
	if err := k.Accept(ctx, "abcdefghij", "delta", "2026-03-01", "o"); err != nil || l.accepts[0] != (access.Acceptance{Source: "delta", Version: "2026-03-01"}) {
		t.Fatal(err)
	}
	if n, err := k.Prune(ctx, "o"); err != nil || n != 3 || !l.before[0].Equal(t0.Add(-395*24*time.Hour)) {
		t.Fatalf("prune %d %v %v", n, err, l.before)
	}
	want := []string{auth.ActionPlan, auth.ActionRevoke, auth.ActionMove, auth.ActionAccept, auth.ActionPruned}
	for i, c := range l.changes {
		if c.Action != want[i] || c.Actor != "o" {
			t.Errorf("change %d: %+v", i, c)
		}
	}
	if l.changes[1].Reason != "leaked" {
		t.Error("revocation reason not audited")
	}
	bad := []struct {
		name string
		err  error
	}{
		{"plan no actor", k.PutPlan(ctx, good, "")},
		{"plan no name", k.PutPlan(ctx, access.Plan{UseClass: "consumer", Tier: "bulk"}, "o")},
		{"plan use class", k.PutPlan(ctx, access.Plan{Name: "x", UseClass: "everyone", Tier: "bulk"}, "o")},
		{"plan tier", k.PutPlan(ctx, access.Plan{Name: "x", UseClass: "consumer", Tier: "gold"}, "o")},
		{"plan scope", k.PutPlan(ctx, access.Plan{Name: "x", UseClass: "consumer", Tier: "bulk", Scopes: []access.Scope{"all"}}, "o")},
		{"revoke no reason", k.Revoke(ctx, "abcdefghij", "", "o")},
		{"move no plan", k.Move(ctx, "abcdefghij", "", "o")},
		{"accept no version", k.Accept(ctx, "abcdefghij", "delta", "", "o")},
		{"prune no actor", func() error { _, err := k.Prune(ctx, ""); return err }()},
	}
	for _, b := range bad {
		if !errors.Is(b.err, auth.ErrInput) {
			t.Errorf("%s: %v", b.name, b.err)
		}
	}
}
