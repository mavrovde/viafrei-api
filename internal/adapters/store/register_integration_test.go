//go:build integration

package store_test

import (
	"bytes"
	"context"
	"slices"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/db/contract"
	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/fixtures"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

var seedOnce sync.Once

// seeded loads the tiny fixture set once into the shared test database.
func seeded(t *testing.T) *store.Store {
	t.Helper()
	seedOnce.Do(func() {
		var buf bytes.Buffer
		if _, err := fixtures.Generate(&buf, "tiny", 1, contract.Version()); err != nil {
			t.Fatal(err)
		}
		ctx := context.Background()
		conn, err := db.Owner(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close(ctx) }()
		if err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
			_, err := tx.Exec(ctx, buf.String())
			return err
		}); err != nil {
			t.Fatal(err)
		}
	})
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
