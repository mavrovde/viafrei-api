package access_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

var prefixes = access.Prefixes{Live: "vf_live_", Test: "vf_test_"}

func TestKeyFormat(t *testing.T) {
	req.Covers(t, "SEC-003", "SEC-004")
	for _, env := range []access.Environment{access.Live, access.Test} {
		k, err := access.Generate(rand.Reader, env, prefixes)
		if err != nil {
			t.Fatal(err)
		}
		want := "vf_" + string(env) + "_"
		if !strings.HasPrefix(k.Full, want) || len(k.Full) != len(want)+access.IDLength+1+access.SecretLength+access.ChecksumLength {
			t.Fatalf("%s: malformed %d characters", env, len(k.Full))
		}
		id, secret, err := access.Parse(k.Full, env, prefixes)
		if err != nil || id != k.ID || secret != k.Secret {
			t.Fatalf("%s: round trip failed: %v", env, err)
		}
		other := access.Live
		if env == access.Live {
			other = access.Test
		}
		if _, _, err := access.Parse(k.Full, other, prefixes); !errors.Is(err, access.ErrMalformed) {
			t.Errorf("%s key accepted as %s", env, other)
		}
		if !prefixes.Carries("x" + k.Full) {
			t.Errorf("%s key not recognised in a string", env)
		}
	}
	if prefixes.Carries("plain text") {
		t.Error("plain text recognised as a key")
	}
}

func TestParseRefusesEveryMalformation(t *testing.T) {
	req.Covers(t, "SEC-003")
	k, _ := access.Generate(rand.Reader, access.Live, prefixes)
	flip := func(s string, i int) string {
		b := []byte(s)
		if b[i] == 'A' {
			b[i] = 'B'
		} else {
			b[i] = 'A'
		}
		return string(b)
	}
	p := len("vf_live_")
	bad := map[string]string{
		"empty":           "",
		"no prefix":       k.Full[p:],
		"short":           k.Full[:len(k.Full)-1],
		"no separator":    k.Full[:p+access.IDLength] + "-" + k.Full[p+access.IDLength+1:],
		"id not base62":   k.Full[:p] + "!" + k.Full[p+1:],
		"secret mangled":  flip(k.Full, p+access.IDLength+5),
		"checksum wrong":  flip(k.Full, len(k.Full)-1),
		"secret not b62":  k.Full[:p+access.IDLength+1] + "-" + k.Full[p+access.IDLength+2:],
		"trailing junk":   k.Full + "x",
		"other prefix":    "vf_prod_" + k.Full[p:],
		"id changed only": flip(k.Full, p),
	}
	for name, s := range bad {
		if _, _, err := access.Parse(s, access.Live, prefixes); !errors.Is(err, access.ErrMalformed) {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestGenerateReportsARandomSourceFault(t *testing.T) {
	broken := iotest.ErrReader(errors.New("no entropy"))
	if _, err := access.Generate(broken, access.Live, prefixes); err == nil {
		t.Error("no error from a failing id source")
	}
	short := bytes.NewReader(make([]byte, access.IDLength))
	if _, err := access.Generate(short, access.Live, prefixes); err == nil {
		t.Error("no error from a failing secret source")
	}
}

func TestEncodingIsFixedWidth(t *testing.T) {
	zero, err := access.NewSecret(bytes.NewReader(make([]byte, 32)), access.Live, prefixes, "0000000000")
	if err != nil || zero.Secret != strings.Repeat("0", access.SecretLength) {
		t.Fatalf("zero secret %q %v", zero.Secret, err)
	}
	ff := bytes.Repeat([]byte{0xff}, 32)
	maxed, _ := access.NewSecret(bytes.NewReader(ff), access.Live, prefixes, "zzzzzzzzzz")
	if len(maxed.Secret) != access.SecretLength {
		t.Fatalf("largest secret is %d characters", len(maxed.Secret))
	}
	if _, _, err := access.Parse(maxed.Full, access.Live, prefixes); err != nil {
		t.Fatal(err)
	}
}

var (
	pepper  = bytes.Repeat([]byte("p"), 32)
	pepper2 = bytes.Repeat([]byte("q"), 32)
	now     = time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
)

func record(secret string, p []byte) access.Record {
	return access.Record{ID: "abcdefghij", MAC: access.MAC(p, "abcdefghij", secret), ExpiresAt: now.Add(time.Hour)}
}

func TestMACIsBoundToIDAndPepper(t *testing.T) {
	req.Covers(t, "SEC-010")
	a := access.MAC(pepper, "abcdefghij", "s")
	if len(a) != 32 || bytes.Equal(a, access.MAC(pepper2, "abcdefghij", "s")) || bytes.Equal(a, access.MAC(pepper, "abcdefghik", "s")) {
		t.Fatal("the MAC does not depend on the pepper and the id")
	}
}

func TestVerify(t *testing.T) {
	req.Covers(t, "SEC-003", "SEC-011", "SEC-041")
	cur := access.Peppers{Current: pepper}
	rec := record("right", pepper)
	if _, err := access.Verify(rec, "right", cur, now); err != nil {
		t.Fatal(err)
	}
	if _, err := access.Verify(rec, "wrong", cur, now); !errors.Is(err, access.ErrInvalid) {
		t.Errorf("wrong secret: %v", err)
	}
	if _, err := access.Verify(rec, "right", cur, now.Add(time.Hour)); !errors.Is(err, access.ErrExpired) {
		t.Errorf("expired: %v", err)
	}
	revoked := rec
	revoked.RevokedAt = &now
	if _, err := access.Verify(revoked, "right", cur, now); !errors.Is(err, access.ErrRevoked) {
		t.Errorf("revoked: %v", err)
	}
	if _, err := access.Verify(revoked, "wrong", cur, now); !errors.Is(err, access.ErrInvalid) {
		t.Errorf("a wrong secret on a revoked key must stay invalid: %v", err)
	}

	// Pepper rotation: a key stored under the old pepper matches and is
	// re-MACed under the new one.
	rotated := access.Peppers{Current: pepper2, Previous: pepper}
	m, err := access.Verify(rec, "right", rotated, now)
	if err != nil || !bytes.Equal(m.Remac, access.MAC(pepper2, rec.ID, "right")) || m.Previous {
		t.Fatalf("old pepper: %v %+v", err, m)
	}
	if _, err := access.Verify(rec, "right", access.Peppers{Current: pepper2}, now); !errors.Is(err, access.ErrInvalid) {
		t.Errorf("old pepper without pepper_previous: %v", err)
	}

	// Key rotation: the previous secret works during the overlap only.
	rot := record("new", pepper)
	rot.PreviousMAC, rot.PreviousUntil = access.MAC(pepper, rot.ID, "old"), now.Add(time.Minute)
	if m, err := access.Verify(rot, "old", cur, now); err != nil || !m.Previous || m.Remac != nil {
		t.Errorf("previous secret in the overlap: %v %+v", err, m)
	}
	if _, err := access.Verify(rot, "old", cur, now.Add(time.Minute)); !errors.Is(err, access.ErrInvalid) {
		t.Errorf("previous secret after the overlap: %v", err)
	}
	if m, err := access.Verify(rot, "new", cur, now); err != nil || m.Previous {
		t.Errorf("new secret: %v %+v", err, m)
	}
	rot.PreviousMAC = access.MAC(pepper, rot.ID, "old")
	if m, err := access.Verify(rot, "old", access.Peppers{Current: pepper2, Previous: pepper}, now); err != nil || !m.Previous || m.Remac == nil {
		t.Errorf("previous secret under the old pepper: %v %+v", err, m)
	}

	// An absent pepper is never a key: a MAC stored under the empty pepper
	// does not match while no previous pepper is set.
	bare := record("right", nil)
	if _, err := access.Verify(bare, "right", cur, now); !errors.Is(err, access.ErrInvalid) {
		t.Errorf("a MAC under the empty pepper matched: %v", err)
	}
}

func TestPlanHas(t *testing.T) {
	p := access.Plan{Scopes: []access.Scope{"roads"}}
	if !p.Has("roads") || p.Has("energy") {
		t.Fatal("Has")
	}
}
