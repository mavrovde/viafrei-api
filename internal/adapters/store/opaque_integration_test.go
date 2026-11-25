//go:build integration

package store_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
)

// Each statement opaque rewrote declares the types Postgres infers for it as
// written, so the rewrite changes no parameter's type, and pgx encodes every
// value as before.
// wantOpaque is the count of statements the package rewrites opaquely.
const wantOpaque = 59

func TestEveryOpaqueReadDeclaresTheTypesPostgresInfers(t *testing.T) {
	ctx := bounded(t)
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	reads := store.OpaqueReads()
	if len(reads) != wantOpaque {
		t.Fatalf("%d opaque reads, want the %d the package rewrites", len(reads), wantOpaque)
	}
	for i, r := range reads {
		for j, text := range []string{r.SQL, r.Sent} {
			d, err := conn.Prepare(ctx, fmt.Sprintf("opaque_types_%d_%d", i, j), text)
			if err != nil {
				t.Fatalf("prepare %.80q: %v", text, err)
			}
			got := make([]string, len(d.ParamOIDs))
			for k, oid := range d.ParamOIDs {
				if err := conn.QueryRow(ctx, `select format_type($1, null)`, oid).Scan(&got[k]); err != nil {
					t.Fatal(err)
				}
			}
			if strings.Join(got, ",") != strings.Join(r.Types, ",") {
				t.Errorf("%.80q: Postgres infers %v, declared %v", text, got, r.Types)
			}
		}
	}
}
