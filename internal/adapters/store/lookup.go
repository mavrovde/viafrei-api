package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// LookupStationEVA is the lookup a live source's key rule names to resolve
// a station's name to its EVA number.
const LookupStationEVA = "station_eva"

// stationEVA reads the EVA number of the railway station whose folded name
// is $1, else the shortest whose folded name starts with it.
var stationEVA = `select s.eva_no from contract.stop_place s
where s.withdrawn_at is null and s.eva_no is not null
  and ` + permitted("s") + `
  and s.search_fold like $1::text || '%'
order by s.search_fold = $1::text desc, length(s.name), s.name, s.stop_place_id
limit 1`

// Lookup resolves value by the named lookup (refresh.Lookup); "" when
// nothing answers.
func (s *Store) Lookup(ctx context.Context, name, value string) (string, error) {
	if name != LookupStationEVA {
		return "", fmt.Errorf("store: no lookup %q", name)
	}
	folded := transit.Fold(value)
	if folded == "" {
		return "", nil
	}
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (string, error) {
		var eva string
		err := tx.QueryRow(ctx, stationEVA, folded).Scan(&eva)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return eva, err
	})
}
