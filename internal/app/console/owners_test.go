package console_test

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/console"
	"github.com/mavrovde/viafrei-api/internal/testkit/consolemem"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

var ownerSettings = console.OwnerSettings{Pepper: []byte(strings.Repeat("p", 32)), EnrolmentTTL: 30 * time.Minute}

func TestAnEnrolmentCodeWorksOnceAndBeforeItExpires(t *testing.T) {
	req.Covers(t, "THR-018", "SEC-010", "SEC-044")
	ctx := context.Background()
	c := newClock()
	st := consolemem.New()
	o := console.NewOwners(ownerSettings, st, nil, c.now)
	code, err := o.Offer(ctx, "alice", "ops")
	if err != nil || len(code) != 43 {
		t.Fatalf("offer: %q %v", code, err)
	}
	e, err := o.Enrolment(ctx, code)
	if err != nil || e.Owner != "alice" || e.UserHandle != nil || !e.ExpiresAt.Equal(c.t.Add(30*time.Minute)) {
		t.Fatalf("enrolment %+v %v", e, err)
	}
	for _, bad := range []string{"", strings.Repeat("x", 65), "unknown"} {
		if _, err := o.Enrolment(ctx, bad); !errors.Is(err, console.ErrCode) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
	p := console.Passkey{ID: []byte("credential-id-01"), Owner: "alice", UserHandle: []byte("handle"), PublicKey: []byte("pk")}
	if err := o.Enrol(ctx, code, p); err != nil {
		t.Fatal(err)
	}
	if _, err := o.Enrolment(ctx, code); !errors.Is(err, console.ErrCode) {
		t.Fatalf("a used code: %v", err)
	}
	if err := o.Enrol(ctx, code, p); !errors.Is(err, console.ErrCode) {
		t.Fatalf("a used code enrols nothing: %v", err)
	}
	// A second code for the same owner carries the owner's user handle.
	second, _ := o.Offer(ctx, "alice", "ops")
	if e, _ := o.Enrolment(ctx, second); string(e.UserHandle) != "handle" {
		t.Fatalf("handle %q", e.UserHandle)
	}
	c.t = c.t.Add(30 * time.Minute)
	if _, err := o.Enrolment(ctx, second); !errors.Is(err, console.ErrCode) {
		t.Fatalf("an expired code: %v", err)
	}
	changes := st.Changes()
	if len(changes) != 3 || changes[0].Action != console.ActionEnrolment || changes[0].Actor != "ops" || changes[0].Reason != "owner alice" ||
		changes[1].Action != console.ActionPasskeyAdded || changes[1].Actor != "console:alice" || changes[1].Reason != "passkey "+console.Fingerprint(p.ID) {
		t.Fatalf("audit %+v", changes)
	}
	for _, ch := range changes {
		if strings.Contains(ch.Reason, code) || strings.Contains(ch.Reason, second) {
			t.Fatal("an enrolment code reached the audit log")
		}
	}
}

func TestTheOwnersRefuseBadInput(t *testing.T) {
	req.Covers(t, "THR-018")
	ctx := context.Background()
	o := console.NewOwners(ownerSettings, consolemem.New(), nil, nil)
	for _, tc := range []struct{ owner, actor string }{
		{"", "ops"}, {"Alice", "ops"}, {"1alice", "ops"}, {strings.Repeat("a", 33), "ops"}, {"alice", ""}, {"alice", strings.Repeat("a", 201)},
	} {
		if _, err := o.Offer(ctx, tc.owner, tc.actor); !errors.Is(err, console.ErrInput) {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
	for _, tc := range []struct{ id, actor string }{{"", "ops"}, {"!!", "ops"}, {"YWJj", ""}, {"YWJj", strings.Repeat("a", 201)}} {
		if err := o.Remove(ctx, tc.id, tc.actor); !errors.Is(err, console.ErrInput) {
			t.Fatalf("%+v: %v", tc, err)
		}
	}
	if err := o.Remove(ctx, "YWJj", "ops"); !errors.Is(err, console.ErrUnknownPasskey) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := console.NewOwners(ownerSettings, consolemem.New(), failingReader{}, nil).Offer(ctx, "alice", "ops"); err == nil {
		t.Fatal("an offer without entropy")
	}
}

func TestSignInsAndRemovalsAreAudited(t *testing.T) {
	req.Covers(t, "THR-018", "SEC-044")
	ctx := context.Background()
	c := newClock()
	st := consolemem.New()
	o := console.NewOwners(ownerSettings, st, nil, c.now)
	code, _ := o.Offer(ctx, "alice", "ops")
	p := console.Passkey{ID: []byte("credential-id-01"), Owner: "alice", UserHandle: []byte("h"), PublicKey: []byte("pk")}
	if err := o.Enrol(ctx, code, p); err != nil {
		t.Fatal(err)
	}
	got, err := o.Passkey(ctx, p.ID)
	if err != nil || !got.CreatedAt.Equal(c.t) || got.Name() != base64.RawURLEncoding.EncodeToString(p.ID) {
		t.Fatalf("passkey %+v %v", got, err)
	}
	c.t = c.t.Add(time.Minute)
	if err := o.SignedIn(ctx, got, 7, 0x45); err != nil {
		t.Fatal(err)
	}
	list, err := o.Passkeys(ctx)
	if err != nil || len(list) != 1 || list[0].SignCount != 7 || list[0].Flags != 0x45 || !list[0].LastUsedAt.Equal(c.t) {
		t.Fatalf("passkeys %+v %v", list, err)
	}
	if err := o.Remove(ctx, got.Name(), "ops"); err != nil {
		t.Fatal(err)
	}
	ch := st.Changes()
	if len(ch) != 4 || ch[2].Action != console.ActionSignIn || ch[2].Actor != "console:alice" || ch[3].Action != console.ActionPasskeyRemoved || ch[3].Actor != "ops" {
		t.Fatalf("audit %+v", ch)
	}
	if len(console.Fingerprint(p.ID)) != 12 {
		t.Fatal("fingerprint length")
	}
}

func TestStoreFailuresReachTheCaller(t *testing.T) {
	req.Covers(t, "THR-018")
	ctx := context.Background()
	st := consolemem.New()
	o := console.NewOwners(ownerSettings, st, nil, nil)
	code, _ := o.Offer(ctx, "alice", "ops")
	st.Err = errors.New("down")
	if _, err := o.Offer(ctx, "alice", "ops"); !errors.Is(err, st.Err) {
		t.Fatal(err)
	}
	if _, err := o.Enrolment(ctx, code); !errors.Is(err, st.Err) {
		t.Fatal(err)
	}
	if err := o.Enrol(ctx, code, console.Passkey{Owner: "alice"}); !errors.Is(err, st.Err) {
		t.Fatal(err)
	}
	if _, err := o.Passkey(ctx, []byte("x")); !errors.Is(err, st.Err) {
		t.Fatal(err)
	}
	if err := o.SignedIn(ctx, console.Passkey{ID: []byte("x")}, 1, 0); !errors.Is(err, st.Err) {
		t.Fatal(err)
	}
	if _, err := o.Passkeys(ctx); !errors.Is(err, st.Err) {
		t.Fatal(err)
	}
	if err := o.Remove(ctx, "YWJj", "ops"); !errors.Is(err, st.Err) {
		t.Fatal(err)
	}
	st.Err = nil
	if _, err := o.Passkey(ctx, []byte("x")); !errors.Is(err, console.ErrUnknownPasskey) {
		t.Fatal(err)
	}
	if err := o.SignedIn(ctx, console.Passkey{ID: []byte("x")}, 1, 0); !errors.Is(err, console.ErrUnknownPasskey) {
		t.Fatal(err)
	}
}
