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
func TestEveryOpaqueReadDeclaresTheTypesPostgresInfers(t *testing.T) {
	ctx := bounded(t)
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	reads := store.OpaqueReads()
	if len(reads) == 0 {
		t.Fatal("no opaque read: the check reads nothing")
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
