package store

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// speedNear is the circle of a speed read: $1 lon, $2 lat, $3 radius (m),
// on the camera's own geography.
const speedNear = `st_dwithin(c.geom, st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography, $3::float8)`

// speedOfficialRows are the open cameras of the official lists (speed
// enforcement, Hamburg's cameras), in one shape; speedOSMRows
// OpenStreetMap's.
const speedOfficialRows = `(
  select 'se:' || s.id camera_id, s.kind, s.enforces, s.geom, s.max_speed_kmh, s.max_speed_hgv_kmh, s.direction, s.municipality name,
         s.street, s.location_text, s.ags admin_code, s.data_as_of, s.valid_from, s.valid_to, s.source_feed source_ref
  from speed_enforcement s where s.closed_at is null
  union all
  select 'hh:' || h.camera_id, 'fixed', null, h.geom::geography, null, null, null, h.location_label, null, h.note, '02000000',
         null, null, null, h.source_feed
  from hh_cameras h where h.closed_at is null)`

const speedOSMRows = `(
  select 'osm:' || o.osm_type || o.osm_id camera_id, 'fixed' kind, o.enforces, o.geom, o.max_speed_kmh, null::smallint max_speed_hgv_kmh,
         o.direction, o.name, null::text street, null::text location_text, null::text admin_code, o.extract_date data_as_of,
         null::date valid_from, null::date valid_to, o.source_id source_ref
  from osm_speed_cameras o where o.closed_at is null)`

// speedColumns are a location's columns, $4 the Berlin day asked; the
// count is every row the statement matched before its limit.
const speedColumns = `c.camera_id, c.kind, c.enforces, c.street, c.location_text, c.name, c.direction,
       c.max_speed_kmh::int, c.max_speed_hgv_kmh::int, c.valid_from, c.valid_to, c.data_as_of,
       case when c.data_as_of < ($4::date - interval '12 months') then extract(year from c.data_as_of)::int end,
       st_y(c.geom::geometry), st_x(c.geom::geometry), c.source_ref, count(*) over ()`

// speedFamilies are the families $5 their source rule gives the key.
const speedFamilies = `any((` + "select coalesce(array_agg(x.f), '{}') f from unnest($5::text[]) x(f) where " + `
  coalesce((select not coalesce(k.excluded, false) and coalesce(k.display_class, 'open') <> 'withhold'
      and (coalesce(k.audience, 'public') = 'public' or ` + ownKeyOn + `)
    from contract_source_rules k where x.f ~ k.pattern order by k.priority limit 1), true))::text[])`

// speedOfficial reads a town's official list as the platform's speed read
// does: in the circle, an announced mobile check only while it is not over,
// by kind (fixed, section control, announced, measuring site), then its
// first day, street and id. $5 the families, $6 the limit. The point and
// the radius decide the plan, so the statement is sent as written.
var speedOfficial = named("api_speed_official", `select `+speedColumns+`
from `+speedOfficialRows+` c
where `+speedNear+`
  and (c.kind <> 'mobile_announced' or coalesce(c.valid_to, c.valid_from) >= $4::date)
  and c.source_ref = `+speedFamilies+`
order by array_position(array['fixed', 'section_control', 'mobile_announced', 'measuring_site'], c.kind),
         c.valid_from nulls last, coalesce(c.street, c.location_text, ''), c.camera_id
limit $6::int`, "float8", "float8", "float8", "date", "text[]", "int")

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
var speedOSM = named("api_speed_osm", `
with cut_gemeinde as materialized (
  select g.admin_code as cell, st_pointonsurface(g.geom::geometry)::geography as at
  from gazetteer_places g
  where g.source_id = 'bkg_gvisys' and g.kind = 'city' and g.closed_at is null and g.geom is not null
    and g.admin_code ~ '^[0-9]{8}$'
    and st_dwithin(st_pointonsurface(g.geom::geometry)::geography, st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography, $3::float8 + 80000)
), official as (
  select `+speedCellOf("o.geom")+` as cell
  from `+speedOfficialRows+` o
  where o.kind in ('fixed', 'section_control') and o.geom is not null
    and st_dwithin(o.geom, st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography, $3::float8 + 50000)
    and o.source_ref = `+speedFamilies+`
  union
  select o.admin_code
  from `+speedOfficialRows+` o
  where o.kind in ('fixed', 'section_control') and o.geom is null
    and o.admin_code ~ '^[0-9]{8}$' and o.source_ref = `+speedFamilies+`
)
select `+speedColumns+`
from `+speedOSMRows+` c
where `+speedNear+`
  and c.source_ref = `+speedFamilies+`
  and `+speedCellOf("c.geom")+` not in (select o.cell from official o where o.cell is not null)
order by coalesce(c.name, ''), c.camera_id
limit $6::int`, "float8", "float8", "float8", "date", "text[]", "int")

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
		rows, _ := run(ctx, tx, speedOfficial, q.At.Lon, q.At.Lat, float64(q.RadiusM), today, families, q.Limit)
		official, err := pgx.CollectRows(rows, scanSpeed(false))
		if err != nil {
			return out, err
		}
		rows, _ = run(ctx, tx, speedOSM, q.At.Lon, q.At.Lat, float64(q.RadiusM), today, families, q.Limit)
		osm, err := pgx.CollectRows(rows, scanSpeed(true))
		if err != nil {
			return out, err
		}
		out.Official, out.OfficialTotal = speedBlock(official)
		out.OSM, out.OSMTotal = speedBlock(osm)
		return out, nil
	})
}
