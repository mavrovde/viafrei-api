package store

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/weather"
)

// warnCellAt is the warn cell a position lies in, as the platform keys a
// weather watch on a place (WARNCELL_AT_POINT): of the Gemeinde and coast
// cells (ids 8… and 7…) held with an area, the newest load of each area; of
// those holding the position, the newest. $1 the families, $2 latitude, $3
// longitude.
var warnCellAt = `
with here as (select st_setsrid(st_makepoint($3::float8, $2::float8), 4326) g)
select c.warn_cell_id, c.area_name
from (select distinct on (md5(st_asbinary(w.area))) w.warn_cell_id, w.area_name, w.area, w.seen_at
        from contract.weather_warning w, here
       where w.area is not null and left(w.warn_cell_id, 1) in ('7', '8')
         and w.source_ref = any($1) and ` + permitted("w") + `
         and w.area && here.g
       order by md5(st_asbinary(w.area)), w.seen_at desc) c, here
where st_intersects(c.area::geography, here.g::geography)
order by c.seen_at desc
limit 1`

// warnCellNamed is whether a warn cell of the families ($1) is held under the
// DWD's name $2, the other key the platform's weather watch matches.
var warnCellNamed = `select exists (select 1 from contract.weather_warning w
  where w.area_name = $2 and w.source_ref = any($1) and ` + permitted("w") + `)`

// cellWarnings are the warnings of the families ($1) of the cell $2 (its id
// or its name) in force at $3, at level $4 or above, worst first, then the
// newest start: the platform's weather watch read.
var cellWarnings = `
select w.warning_id, w.source_ref, w.hazard, w.warning_level, w.level_label, w.issuer_name, w.headline,
       w.text_lang, w.valid_from, w.valid_to, coalesce(w.area_name, '')
from contract.weather_warning w
where (w.warn_cell_id = $2 or w.area_name = $2)
  and w.source_ref = any($1) and ` + permitted("w") + `
  and w.withdrawn_at is null and w.message_status <> 'cancelled'
  and w.valid_from <= $3 and (w.valid_to is null or w.valid_to > $3)
  and w.warning_level >= $4
order by w.warning_level desc, w.valid_from desc, w.warning_id`

// CellWarnings resolves q to its warn cell and reads the cell's warnings in
// force at now (features.Weather). A position no cell of the families holds,
// and a name no cell is held under, are weather.ErrNoCell; any other key is
// taken as given, as the platform takes it.
func (s *Store) CellWarnings(ctx context.Context, q weather.CellQuery, families []string, now time.Time) (weather.Cell, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (weather.Cell, error) {
		out := weather.Cell{Key: q.Cell}
		switch {
		case q.At != nil:
			if err := tx.QueryRow(ctx, warnCellAt, families, q.At.Lat, q.At.Lon).Scan(&out.Key, &out.Name); err != nil {
				return out, noCell(err)
			}
		case !weather.LooksLikeCell(q.Cell):
			var known bool
			if err := tx.QueryRow(ctx, warnCellNamed, families, q.Cell).Scan(&known); err != nil {
				return out, err
			}
			if !known {
				return out, weather.ErrNoCell
			}
			out.Name = &q.Cell
		}
		rows, _ := tx.Query(ctx, cellWarnings, families, out.Key, now, q.MinLevel)
		var err error
		out.Warnings, err = pgx.CollectRows(rows, scanWarning)
		return out, err
	})
}

// noCell names no row the absence of a cell.
func noCell(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return weather.ErrNoCell
	}
	return err
}
