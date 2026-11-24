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
// those holding the position, the newest. A live row comes before a
// withdrawn one in both: the loader stamps an unchanged live warning only
// once an hour, so a withdrawn row closed since can carry the later
// seen_at. $1 the families, $2 latitude, $3 longitude. Withdrawn rows count,
// so the box test is on the area index of every row (geography); the
// read contract's view cast the area to geometry(MultiPolygon,4326), an
// expression no index holds, and read every warning's area on prod (about
// 1 s a call). The geometry box test is kept as well: the rows are the
// view statement's.
var warnCellAt = named("api_weather_cell_at", `
with here as (select st_setsrid(st_makepoint((select $3::float8), (select $2::float8)), 4326) g)
select c.warncell_id, c.area_desc
from (select distinct on (md5(st_asbinary(w.area::geometry))) w.warncell_id, w.area_desc, w.area, w.last_seen_at, w.closed_at is null live
        from weather_warnings w, here
       where w.deleted_at is null and w.valid_from is not null
         and w.area is not null and left(w.warncell_id, 1) in ('7', '8')
         and w.source_feed = any((select $1::text[])::text[])
         and w.area && here.g::geography and w.area::geometry && here.g
       order by md5(st_asbinary(w.area::geometry)), w.closed_at is null desc, w.last_seen_at desc) c, here
where st_intersects(c.area, here.g::geography)
order by c.live desc, c.last_seen_at desc
limit 1`, "text[]", "float8", "float8")

// warnCellNamed is whether a warn cell of the families ($1) is held under the
// DWD's name $2, the other key the platform's weather watch matches.
var warnCellNamed = named("api_weather_cell_named", `select exists (select from weather_warnings w
  where w.area_desc = (select $2::text) and w.deleted_at is null and w.valid_from is not null
    and w.source_feed = any((select $1::text[])::text[]))`, "text[]", "text")

// cellWarnings are the warnings of the families ($1) of the cell $2 (its id
// or its name) in force at $3, at level $4 or above, worst first, then the
// newest start: the platform's weather watch read.
var cellWarnings = named("api_weather_cell_warnings", warningColumns+`
from weather_warnings w
where (w.warncell_id = (select $2::text) or w.area_desc = (select $2::text))
  and w.source_feed = any((select $1::text[])::text[])
  and w.closed_at is null and w.deleted_at is null and w.closed_reason is distinct from 'withdrawn'
  and w.valid_from <= (select $3::timestamptz) and (w.valid_to is null or w.valid_to > (select $3::timestamptz))
  and w.level >= (select $4::int)
order by warning_level desc, w.valid_from desc, warning_id`, "text[]", "text", "timestamptz", "int")

// CellWarnings resolves q to its warn cell and reads the cell's warnings in
// force at now (features.Weather). A position no cell of the families holds,
// and a name no cell is held under, are weather.ErrNoCell; any other key is
// taken as given, as the platform takes it.
func (s *Store) CellWarnings(ctx context.Context, q weather.CellQuery, families []string, now time.Time) (weather.Cell, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (weather.Cell, error) {
		out := weather.Cell{Key: q.Cell}
		switch {
		case q.At != nil:
			if err := cellRow(ctx, tx, warnCellAt, []any{families, q.At.Lat, q.At.Lon}, &out.Key, &out.Name); err != nil {
				return out, noCell(err)
			}
		case !weather.LooksLikeCell(q.Cell):
			var known bool
			if err := cellRow(ctx, tx, warnCellNamed, []any{families, q.Cell}, &known); err != nil {
				return out, err
			}
			if !known {
				return out, weather.ErrNoCell
			}
			out.Name = &q.Cell
		}
		rows, _ := run(ctx, tx, cellWarnings, families, out.Key, now, q.MinLevel)
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

// cellRow runs s with args and scans its first row into dest; no row is
// pgx.ErrNoRows.
func cellRow(ctx context.Context, tx pgx.Tx, s stmt, args []any, dest ...any) error {
	rows, _ := run(ctx, tx, s, args...)
	_, err := pgx.CollectOneRow(rows, func(r pgx.CollectableRow) (struct{}, error) { return struct{}{}, r.Scan(dest...) })
	return err
}
