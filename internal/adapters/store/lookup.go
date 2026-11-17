package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// The lookups a live source's key case or the refreshes name.
const (
	// LookupStationEVA resolves a station's name to its EVA number (the
	// live refresher's registry names it eva).
	LookupStationEVA = "eva"
	// LookupAGS resolves a position, "lat,lon", to the AGS of its Gemeinde.
	LookupAGS = "ags"
)

// stationEVA reads the EVA number of the railway station whose folded name
// is $1, else the shortest whose folded name starts with it.
var stationEVA = `select s.eva_no from contract.stop_place s
where s.withdrawn_at is null and s.eva_no is not null
  and ` + permitted("s") + `
  and s.search_fold like $1::text || '%'
order by s.search_fold = $1::text desc, length(s.name), s.name, s.stop_place_id
limit 1`

// gemeindeAGS reads the AGS of the Gemeinde of the position ($1 lon, $2
// lat): the nearest BKG municipality point with its eight-digit key within
// the municipality reach ($3 m), a distance tie broken by key, as the
// platform names a position's Gemeinde.
var gemeindeAGS = `select p.official_key from contract.place p
where p.source_ref = 'bkg_gvisys' and p.place_kind = 'municipality' and p.withdrawn_at is null
  and p.official_key ~ '^[0-9]{8}$' and ` + permitted("p") + `
  and st_dwithin(p.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography, $3)
order by p.location::geography <-> st_setsrid(st_makepoint($1, $2), 4326)::geography, p.official_key
limit 1`

// Lookup resolves value by the named lookup (refresh.Lookup); "" when
// nothing answers.
func (s *Store) Lookup(ctx context.Context, name, value string) (string, error) {
	var (
		sql  string
		args []any
	)
	switch name {
	case LookupStationEVA:
		folded := transit.Fold(value)
		if folded == "" {
			return "", nil
		}
		sql, args = stationEVA, []any{folded}
	case LookupAGS:
		lat, lon, ok := position(value)
		if !ok {
			return "", nil
		}
		sql, args = gemeindeAGS, []any{lon, lat, places.MunicipalityReachM}
	default:
		return "", fmt.Errorf("store: no lookup %q", name)
	}
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (string, error) {
		var got string
		err := tx.QueryRow(ctx, sql, args...).Scan(&got)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return got, err
	})
}

// position reads "lat,lon" inside the coordinate ranges.
func position(v string) (lat, lon float64, ok bool) {
	a, b, found := strings.Cut(v, ",")
	lat, errA := strconv.ParseFloat(a, 64)
	lon, errB := strconv.ParseFloat(b, 64)
	if !found || errA != nil || errB != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return 0, 0, false
	}
	return lat, lon, true
}
