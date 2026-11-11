//go:build integration

package store_test

import (
	"bytes"
	"context"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/db/contract"
	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/fixtures"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// seed loads the tiny fixture set into the shared test database. TestMain
// runs it once, as setup: generating and loading the fixtures is outside
// every test body's bound, which it would otherwise spend in whichever test
// happened to ask first.
func seed(ctx context.Context) error {
	var buf bytes.Buffer
	if _, err := fixtures.Generate(&buf, "tiny", 1, contract.Version()); err != nil {
		return err
	}
	conn, err := db.Owner(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, buf.String())
		return err
	})
}

// seeded opens a store on the shared test database, which TestMain seeded.
func seeded(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), readerConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return st
}

func TestRegisterReadsEveryFamilysTerms(t *testing.T) {
	req.Covers(t, "SEC-030")
	reg, err := seeded(t).Register(bounded(t))
	if err != nil {
		t.Fatal(err)
	}
	byFamily := map[string]access.Terms{}
	for _, r := range reg {
		byFamily[r.Family] = r
	}
	// Expected from docs/test-data.md, not from the code under test.
	if g := byFamily["src-gamma-energy"]; !slices.Equal(g.UseClasses, []access.UseClass{"consumer"}) || !g.Reviewed || g.AcceptanceRequired {
		t.Errorf("restricted-purpose family: %+v", g)
	}
	if d := byFamily["src-delta-parking"]; !d.AcceptanceRequired || d.Version != "2026-03-01" {
		t.Errorf("terms family: %+v", d)
	}
	if e := byFamily["src-epsilon-roads"]; e.Reviewed || len(e.UseClasses) != 0 {
		t.Errorf("unreviewed family: %+v", e)
	}
	if len(reg) < 20 {
		t.Errorf("only %d families read", len(reg))
	}
}

func TestSourcesReadsTheAllowedFamiliesOnly(t *testing.T) {
	req.Covers(t, "SEC-031")
	st := seeded(t)
	got, err := st.Sources(bounded(t), []string{"src-beta-energy", "src-alpha-roads", "no-such-family"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Family != "src-alpha-roads" || got[1].Family != "src-beta-energy" {
		t.Fatalf("read %+v", got)
	}
	if b := got[1]; !b.ShareAlike || b.ShareAlikeOffer == nil || b.Publisher == "" || b.TermsURL == nil {
		t.Errorf("share-alike attribution: %+v", b)
	}
	none, err := st.Sources(bounded(t), nil)
	if err != nil || len(none) != 0 {
		t.Fatalf("an empty allowed set read %d families: %v", len(none), err)
	}
}

func TestRegisterAndSourcesReportStorageFaults(t *testing.T) {
	st := seeded(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := st.Register(ctx); err == nil {
		t.Error("Register on an ended context succeeded")
	}
	if _, err := st.Sources(ctx, []string{"x"}); err == nil {
		t.Error("Sources on an ended context succeeded")
	}
}

func TestTheRegisterFingerprintMovesWithEveryLicenceInput(t *testing.T) {
	req.Covers(t, "NFR-PERF-005", "SEC-030")
	st := seeded(t)
	ctx := bounded(t)
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	base, err := st.FreshnessVersions(ctx)
	if err != nil || base.Register == "" || len(base.Families) == 0 {
		t.Fatalf("versions %+v, %v", base, err)
	}
	for _, change := range []struct{ name, do, undo string }{
		{"register_version", `update contract.source set register_version = register_version + 1 where source_ref = 'src-delta-parking'`,
			`update contract.source set register_version = register_version - 1 where source_ref = 'src-delta-parking'`},
		{"use_classes", `update contract.source set use_classes = use_classes || '{probe}' where source_ref = 'src-gamma-energy'`,
			`update contract.source set use_classes = array_remove(use_classes, 'probe') where source_ref = 'src-gamma-energy'`},
		{"a note", `update contract.source_note set body = body || ' (probe)' where source_ref = 'src-gamma-energy'`,
			`update contract.source_note set body = left(body, -8) where source_ref = 'src-gamma-energy'`},
	} {
		if _, err := conn.Exec(ctx, change.do); err != nil {
			t.Fatal(err)
		}
		got, err := st.FreshnessVersions(ctx)
		if _, uerr := conn.Exec(ctx, change.undo); uerr != nil {
			t.Fatal(uerr)
		}
		if err != nil || got.Register == base.Register {
			t.Errorf("%s changed and the fingerprint did not: %v", change.name, err)
		}
		if back, err := st.FreshnessVersions(ctx); err != nil || back.Register != base.Register {
			t.Errorf("after undoing %s the fingerprint is %q, want %q (%v)", change.name, back.Register, base.Register, err)
		}
	}
}
