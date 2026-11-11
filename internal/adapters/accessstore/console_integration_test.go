//go:build integration

package accessstore_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/console"
	"github.com/mavrovde/viafrei-api/internal/runtime/wire"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// ended returns a context that has already ended.
func ended() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func credentialID(b byte) []byte { return bytes.Repeat([]byte{b}, 16) }

func TestTheDirectoryReadsKeysPlansAndAudit(t *testing.T) {
	req.Covers(t, "THR-018")
	s := open(t)
	k := auth.NewKeys(wire.AuthSettings(cfg(t)), s, nil, nil)
	ctx := bounded(t)
	plan := newPlan(t, k, "consumer", "src-alpha-roads")
	key, err := k.Issue(ctx, plan, "directory", "ops@example.test", 0, "tester")
	if err != nil {
		t.Fatal(err)
	}
	if err := k.Revoke(ctx, key.ID, "listed", "tester"); err != nil {
		t.Fatal(err)
	}
	keys, err := k.KeyList(ctx)
	var found *auth.KeyView
	for i := range keys {
		if keys[i].ID == key.ID {
			found = &keys[i]
		}
	}
	if err != nil || found == nil || found.Plan != plan || found.Label != "directory" || found.RevokedAt == nil || found.RevokeReason != "listed" || found.Environment == "" {
		t.Fatalf("keys %+v %v", found, err)
	}
	plans, err := k.PlanList(ctx)
	var p *int
	for i := range plans {
		if plans[i].Name == plan {
			p = &i
		}
	}
	if err != nil || p == nil || plans[*p].UseClass != "consumer" || plans[*p].Rate != 1000 || len(plans[*p].Scopes) == 0 {
		t.Fatalf("plans %v", err)
	}
	audit, err := k.AuditList(ctx, 2)
	if err != nil || len(audit) != 2 || audit[0].Action != auth.ActionRevoke || audit[0].KeyID != key.ID || audit[0].Reason != "listed" || audit[1].Action != auth.ActionIssue {
		t.Fatalf("audit %+v %v", audit, err)
	}
	if _, err := s.KeyList(ended()); err == nil {
		t.Error("keys on an ended context")
	}
	if _, err := s.PlanList(ended()); err == nil {
		t.Error("plans on an ended context")
	}
	if _, err := s.AuditList(ended(), 10); err == nil {
		t.Error("audit on an ended context")
	}
}

func TestThePasskeyLifecycleAgainstTheStore(t *testing.T) {
	req.Covers(t, "THR-018", "SEC-010", "SEC-044")
	s := open(t)
	ctx := bounded(t)
	o := console.NewOwners(wire.ConsoleOwners(cfg(t)), s, nil, nil)
	code, err := o.Offer(ctx, "alice", "tester")
	if err != nil {
		t.Fatal(err)
	}
	e, err := o.Enrolment(ctx, code)
	if err != nil || e.Owner != "alice" || e.UserHandle != nil || e.Used {
		t.Fatalf("enrolment %+v %v", e, err)
	}
	handle := bytes.Repeat([]byte{7}, 32)
	p := console.Passkey{ID: credentialID(1), Owner: "alice", UserHandle: handle, PublicKey: []byte("public key"), SignCount: 1, Flags: 0x45}
	if err := o.Enrol(ctx, code, p); err != nil {
		t.Fatal(err)
	}
	if err := o.Enrol(ctx, code, console.Passkey{ID: credentialID(2), Owner: "alice", UserHandle: handle, PublicKey: []byte("k")}); !errors.Is(err, console.ErrCode) {
		t.Fatalf("a used code: %v", err)
	}
	if _, err := o.Enrolment(ctx, code); !errors.Is(err, console.ErrCode) {
		t.Fatalf("a used code reads as used: %v", err)
	}
	// A second code carries the owner's handle; the same credential id twice is refused.
	second, _ := o.Offer(ctx, "alice", "tester")
	if e, err := o.Enrolment(ctx, second); err != nil || !bytes.Equal(e.UserHandle, handle) {
		t.Fatalf("second enrolment %+v %v", e, err)
	}
	if err := o.Enrol(ctx, second, p); err == nil || errors.Is(err, console.ErrCode) {
		t.Fatalf("a duplicate credential: %v", err)
	}
	// The duplicate rolled back: the code is still open.
	if _, err := o.Enrolment(ctx, second); err != nil {
		t.Fatalf("rolled back: %v", err)
	}
	got, err := o.Passkey(ctx, p.ID)
	if err != nil || got.Owner != "alice" || got.SignCount != 1 || got.Flags != 0x45 || got.LastUsedAt != nil {
		t.Fatalf("passkey %+v %v", got, err)
	}
	if err := o.SignedIn(ctx, got, 9, 0x05); err != nil {
		t.Fatal(err)
	}
	list, err := o.Passkeys(ctx)
	if err != nil || len(list) == 0 {
		t.Fatalf("passkeys %v", err)
	}
	for _, l := range list {
		if bytes.Equal(l.ID, p.ID) && (l.SignCount != 9 || l.Flags != 0x05 || l.LastUsedAt == nil) {
			t.Fatalf("after sign-in %+v", l)
		}
	}
	if err := o.Remove(ctx, got.Name(), "tester"); err != nil {
		t.Fatal(err)
	}
	if err := o.Remove(ctx, got.Name(), "tester"); !errors.Is(err, console.ErrUnknownPasskey) {
		t.Fatalf("removed twice: %v", err)
	}
	if _, err := o.Passkey(ctx, p.ID); !errors.Is(err, console.ErrUnknownPasskey) {
		t.Fatalf("removed: %v", err)
	}
	if err := o.SignedIn(ctx, got, 10, 0); !errors.Is(err, console.ErrUnknownPasskey) {
		t.Fatalf("sign-in of a removed passkey: %v", err)
	}

	// Every action was audited; no audit row holds the code, its MAC or a key.
	conn := owner(t)
	rows, _ := conn.Query(ctx, `select action, actor, coalesce(reason, ''), row_to_json(a)::text from access.audit a
		where action like 'console_%' and (actor = 'tester' or actor = 'console:alice') order by audit_id`)
	type row struct{ action, actor, reason, all string }
	audit, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var v row
		err := r.Scan(&v.action, &v.actor, &v.reason, &v.all)
		return v, err
	})
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, r := range audit {
		actions = append(actions, r.action+" "+r.actor)
		if strings.Contains(r.all, code) || strings.Contains(r.all, second) || strings.Contains(r.all, "\\x") {
			t.Fatalf("an audit row holds secret material: %s", r.all)
		}
	}
	want := []string{
		"console_enrolment tester", "console_passkey_added console:alice", "console_enrolment tester",
		"console_sign_in console:alice", "console_passkey_removed tester",
	}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("audit %v, want %v", actions, want)
	}
	// Only the MAC of a code is stored.
	var stored int
	if err := conn.QueryRow(ctx, `select count(*) from access.console_enrolment where encode(code_mac, 'escape') like '%' || $1 || '%'`, code).Scan(&stored); err != nil || stored != 0 {
		t.Fatalf("a code stored in clear: %d %v", stored, err)
	}
}

func TestConsoleStoreRefusals(t *testing.T) {
	req.Covers(t, "THR-018")
	s := open(t)
	ctx := bounded(t)
	now := time.Now()
	c := auth.Change{Action: console.ActionEnrolment, Actor: "tester", At: now}
	mac := bytes.Repeat([]byte{9}, 32)
	bad := "\xff" // not valid UTF-8: the database refuses it as text
	if err := s.OfferEnrolment(ctx, c, mac, "Not An Owner", now.Add(time.Hour)); err == nil {
		t.Error("an owner name the table refuses")
	}
	if _, err := s.Enrolment(ctx, mac); !errors.Is(err, console.ErrCode) {
		t.Errorf("an unknown code: %v", err)
	}
	if _, err := s.Enrolment(ended(), mac); err == nil || errors.Is(err, console.ErrCode) {
		t.Errorf("an ended context: %v", err)
	}
	if err := s.OfferEnrolment(ctx, c, mac, "carol", now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	added := auth.Change{Action: console.ActionPasskeyAdded, Actor: "console:carol", At: now}
	p := console.Passkey{ID: credentialID(3), Owner: "carol", UserHandle: bytes.Repeat([]byte{3}, 32), PublicKey: []byte("k"), CreatedAt: now}
	if err := s.AddPasskey(ctx, added, mac, console.Passkey{Owner: bad}); err == nil || errors.Is(err, console.ErrCode) {
		t.Errorf("a bad owner: %v", err)
	}
	other := p
	other.Owner = "dave"
	if err := s.AddPasskey(ctx, added, mac, other); !errors.Is(err, console.ErrCode) {
		t.Errorf("another owner's code: %v", err)
	}
	late := added
	late.At = now.Add(2 * time.Hour)
	if err := s.AddPasskey(ctx, late, mac, p); !errors.Is(err, console.ErrCode) {
		t.Errorf("an expired code: %v", err)
	}
	if _, err := s.Passkey(ended(), p.ID); err == nil || errors.Is(err, console.ErrUnknownPasskey) {
		t.Errorf("passkey on an ended context: %v", err)
	}
	if _, err := s.Passkeys(ended()); err == nil {
		t.Error("passkeys on an ended context")
	}
	if err := s.AddPasskey(ctx, added, mac, p); err != nil {
		t.Fatal(err)
	}
	// A timestamp the database cannot hold fails the statement itself.
	far := auth.Change{Action: console.ActionSignIn, Actor: "console:carol", At: time.Date(300000, 1, 1, 0, 0, 0, 0, time.UTC)}
	if err := s.SignedIn(ctx, far, p.ID, 2, 0); err == nil || errors.Is(err, console.ErrUnknownPasskey) {
		t.Errorf("an out-of-range sign-in: %v", err)
	}
	if err := s.RemovePasskey(ctx, auth.Change{Action: "bogus", Actor: "t", At: now}, p.ID); err == nil {
		t.Error("an action the audit refuses")
	}
	if err := s.RemovePasskey(ctx, auth.Change{Action: console.ActionPasskeyRemoved, Actor: "t", At: now}, p.ID); err != nil {
		t.Fatal(err)
	}
}
