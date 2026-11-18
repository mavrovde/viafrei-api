package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// speedNear is the circle of a speed read: $1 lon, $2 lat, $3 radius (m).
const speedNear = `st_dwithin(c.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography, $3)`

// speedColumns are a location's columns, $4 the Berlin day asked; the
// count is every row the statement matched before its limit.
const speedColumns = `c.camera_id, c.camera_kind, c.enforces, c.street, c.location_text, c.name, c.direction,
       c.max_speed_kmh, c.max_speed_hgv_kmh, c.valid_from, c.valid_to, c.data_as_of,
       case when c.data_as_of < ($4::date - interval '12 months') then extract(year from c.data_as_of)::int end,
       st_y(c.location), st_x(c.location), c.source_ref, count(*) over ()
from contract.speed_camera c`

// speedOfficial reads a town's official list as the platform's speed read
// does: in the circle, an announced mobile check only while it is not over,
// by kind (fixed, section control, announced, measuring site), then its
// first day, street and id. $5 the families, $6 the limit.
var speedOfficial = `select ` + speedColumns + `
where c.camera_id not like 'osm:%' and ` + speedNear + `
  and (c.camera_kind <> 'mobile_announced' or coalesce(c.valid_to, c.valid_from) >= $4::date)
  and c.source_ref = any($5) and ` + permitted("c") + `
order by array_position(array['fixed', 'section_control', 'mobile_announced', 'measuring_site'], c.camera_kind),
         c.valid_from nulls last, coalesce(c.street, c.location_text, ''), c.camera_id
limit $6`

// speedCellOf is the Gemeinde of a point (a geography expression) as the
// platform's speed cut keys it: the nearest BKG municipality point with its
// eight-digit key, a distance tie broken by key, among the cut's Gemeinden.
func speedCellOf(point string) string {
	return `(select g.cell from cut_gemeinde g order by g.at <-> ` + point + `, g.cell limit 1)`
}

// speedOSM reads OpenStreetMap's cameras in the circle by name and id, cut
// as the platform cuts them: a camera is left out when its Gemeinde (the
// nearest BKG municipality point) is one an official list covers, the
// Gemeinde of an official fixed camera or section control, or the key of
// one with no point; a camera no Gemeinde is named for is left out too. The
// Gemeinden are those within 80 km of the circle and the official cameras
// those within 50 km: no Gemeinde point lies farther than that from a camera
// whose nearest it is.
//
//nolint:misspell // materialized is the SQL keyword
var speedOSM = `
with cut_gemeinde as materialized (
  select p.official_key as cell, p.location::geography as at
  from contract.place p
  where p.source_ref = 'bkg_gvisys' and p.place_kind = 'municipality' and p.withdrawn_at is null
    and p.official_key ~ '^[0-9]{8}$' and ` + permitted("p") + `
    and st_dwithin(p.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography, $3 + 80000)
), official as (
  select ` + speedCellOf("o.location::geography") + ` as cell
  from contract.speed_camera o
  where o.camera_id not like 'osm:%' and o.camera_kind in ('fixed', 'section_control') and o.location is not null
    and st_dwithin(o.location::geography, st_setsrid(st_makepoint($1, $2), 4326)::geography, $3 + 50000)
    and o.source_ref = any($5) and ` + permitted("o") + `
  union
  select o.admin_code
  from contract.speed_camera o
  where o.camera_id not like 'osm:%' and o.camera_kind in ('fixed', 'section_control') and o.location is null
    and o.admin_code ~ '^[0-9]{8}$' and o.source_ref = any($5) and ` + permitted("o") + `
), kept as (
  select c.camera_id
  from contract.speed_camera c
  where c.camera_id like 'osm:%' and ` + speedNear + `
    and c.source_ref = any($5) and ` + permitted("c") + `
    and ` + speedCellOf("c.location::geography") + ` not in (select o.cell from official o where o.cell is not null)
)
select ` + speedColumns + `
where c.camera_id in (select k.camera_id from kept k) and ` + speedNear + `
order by coalesce(c.name, ''), c.camera_id
limit $6`

func scanSpeed(osm bool) func(pgx.CollectableRow) (speedRow, error) {
	return func(r pgx.CollectableRow) (speedRow, error) {
		var v speedRow
		var name *string
		var lat, lon *float64
		err := r.Scan(&v.c.ID, &v.c.Kind, &v.c.Enforces, &v.c.Street, &v.c.LocationText, &name, &v.c.Direction,
			&v.c.MaxSpeedKmh, &v.c.MaxSpeedHGVKm, &v.c.ValidFrom, &v.c.ValidTo, &v.c.DataAsOf, &v.c.StaleYear,
			&lat, &lon, &v.c.Family, &v.total)
		if osm {
			v.c.LocationText = name // OpenStreetMap's node name is its location text
		} else {
			v.c.Municipality = name
		}
		if lat != nil && lon != nil {
			v.c.Location = &geo.Point{Lat: *lat, Lon: *lon}
		}
		return v, err
	}
}

type speedRow struct {
	c     roads.SpeedCamera
	total int
}

func speedBlock(rows []speedRow) ([]roads.SpeedCamera, int) {
	out := make([]roads.SpeedCamera, len(rows))
	total := 0
	for i, r := range rows {
		out[i], total = r.c, r.total
	}
	return out, total
}

// SpeedCameras reads both blocks q asks for (features.SpeedCameras).
func (s *Store) SpeedCameras(ctx context.Context, q roads.SpeedQuery, families []string) (roads.SpeedCameras, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (roads.SpeedCameras, error) {
		var out roads.SpeedCameras
		today := q.Today.Format("2006-01-02")
		rows, _ := tx.Query(ctx, speedOfficial, q.At.Lon, q.At.Lat, q.RadiusM, today, families, q.Limit)
		official, err := pgx.CollectRows(rows, scanSpeed(false))
		if err != nil {
			return out, err
		}
		rows, _ = tx.Query(ctx, speedOSM, q.At.Lon, q.At.Lat, q.RadiusM, today, families, q.Limit)
		osm, err := pgx.CollectRows(rows, scanSpeed(true))
		if err != nil {
			return out, err
		}
		out.Official, out.OfficialTotal = speedBlock(official)
		out.OSM, out.OSMTotal = speedBlock(osm)
		return out, nil
	})
}
