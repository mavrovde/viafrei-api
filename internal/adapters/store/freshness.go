package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// FreshnessVersions reads the freshness version of every source family. It
// is one small table, never an aggregate over observations, so the HTTP
// cache can read it while requests arrive and answer If-None-Match before
// any feature read (Performance-and-Caching).
func (s *Store) FreshnessVersions(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	err := s.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		var family, version string
		// A failed query surfaces through ForEachRow.
		rows, _ := tx.Query(ctx, `select source_ref, freshness_version from contract.source_freshness`)
		_, err := pgx.ForEachRow(rows, []any{&family, &version}, func() error {
			out[family] = version
			return nil
		})
		return err
	})
	return out, err
}
