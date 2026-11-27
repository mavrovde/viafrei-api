package store

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// speedNear is the circle of a speed read: $1 lon, $2 lat, $3 radius (m),
// on the camera's own geography.
const speedNear = `st_dwithin(c.geom, st_setsrid(st_makepoint($1::float8, $2::float8), 4326)::geography, $3::float8)`

// speedOfficialRows are the open cameras of the official lists of speed
// enforcement, in one shape; speedOSMRows OpenStreetMap's. Hamburg's
// traffic cameras (hh_cameras) are traffic-view webcams of the transport
// authority, no enforcement point, and never read here (#301); the read
// contract's view had listed them as fixed speed cameras.
const speedOfficialRows = `(
  select 'se:' || s.id camera_id, s.kind, s.enforces, s.geom, s.max_speed_kmh, s.max_speed_hgv_kmh, s.direction, s.municipality name,
         s.street, s.location_text, s.ags admin_code, s.data_as_of, s.valid_from, s.valid_to, s.source_feed source_ref
  from speed_enforcement s where s.closed_at is null)`

const speedOSMRows = `(
  select 'osm:' || o.osm_type || o.osm_id camera_id, 'fixed' kind, o.enforces, o.geom, o.max_speed_kmh, null::smallint max_speed_hgv_kmh,
         o.direction, o.name, null::text street, null::text location_text, null::text admin_code, o.extract_date data_as_of,
         null::date valid_from, null::date valid_to, o.source_id source_ref, o.osm_id
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
// platform's speed cut keys it: the nearest open BKG municipality point with
// its eight-digit key, a distance tie broken by key. As today's statement
// reads it: a nearest-neighbour scan of the municipality points' partial
// index takes the four nearest, and only they are sorted by distance and key.
func speedCellOf(point string) string {
	return `(select k.admin_code from (
    select g.admin_code, g.geom <-> ` + point + ` d from gazetteer_places g
    where g.source_id = 'bkg_gvisys' and g.kind = 'city' and g.closed_at is null and g.geom is not null
      and g.admin_code ~ '^[0-9]{8}$'
    order by g.geom <-> ` + point + ` limit 4) k
  order by k.d, k.admin_code limit 1)`
}

// speedOfficialCells reads the Gemeinden an official list of the families
// $1 covers, as today's statement keys them (SPEED_OFFICIAL_CELLS): the
// Gemeinde of every open official fixed camera or section control, and the
// key of every such camera with no point. They name no place: read once and
// kept while the families and the gazetteer are unchanged (officialCells).
var speedOfficialCells = named("api_speed_official_cells", `
select `+speedCellOf("o.geom")+`
from `+speedOfficialRows+` o
where o.kind in ('fixed', 'section_control') and o.geom is not null and o.source_ref = any((select $1::text[])::text[])
union
select o.admin_code
from `+speedOfficialRows+` o
where o.kind in ('fixed', 'section_control') and o.geom is null
  and o.admin_code ~ '^[0-9]{8}$' and o.source_ref = any((select $1::text[])::text[])`, "text[]")

// speedOSM reads OpenStreetMap's cameras in the circle by name and then
// the numeric OSM id (an empty name first, as today's statement orders them;
// the text id would put node12230389900 before node1277236977), cut
// as the platform cuts them: a camera is left out when its Gemeinde (the
// nearest BKG municipality point) is one an official list covers ($7, the
// Gemeinden speedOfficialCells reads, as today's SPEED_CUT_GIVEN takes
// them); a camera no Gemeinde is named for is left out too. The cameras in
// the circle are read first and each is located by itself, as today's
// statement does (Hamburg, 10 km: 382 ms when every camera was matched
// against the Gemeinden within 80 km, on prod 2026-11-25).
//
//nolint:misspell // materialized is the SQL keyword
var speedOSM = named("api_speed_osm", `
with cand as materialized (
  select c.camera_id, c.kind, c.enforces, c.geom, c.max_speed_kmh, c.max_speed_hgv_kmh, c.direction, c.name, c.street,
         c.location_text, c.admin_code, c.data_as_of, c.valid_from, c.valid_to, c.source_ref, c.osm_id
  from `+speedOSMRows+` c
  where `+speedNear+`
    and c.source_ref = `+speedFamilies+`
)
select `+speedColumns+`
from cand c
where `+speedCellOf("c.geom")+` <> all((select $7::text[])::text[])
order by coalesce(c.name, ''), c.osm_id, c.camera_id
limit $6::int`, "float8", "float8", "float8", "date", "text[]", "int", "text[]")

// gazetteerFamily is the gazetteer's family in the versions read: a write
// of the gazetteer moves the Gemeinde a point lies in.
const gazetteerFamily = "gazetteer"

// officialCells keeps the Gemeinden the official lists cover, per set of
// families, under the versions of those families and of the gazetteer as
// the last versions read (FreshnessVersions, once a second) stated them, as
// today's speed card keeps them under its sources' versions: the cut no
// longer reads every official camera's Gemeinde on each request. Without a
// versions read stating the gazetteer, the cells are read for the request.
type officialCells struct {
	mu       sync.Mutex
	versions map[string]string
	kept     map[string][]string
}

// noteVersions records the versions the last versions read stated.
func (o *officialCells) noteVersions(families map[string]string) {
	o.mu.Lock()
	o.versions = families
	o.mu.Unlock()
}

// key is the families and their versions (and the gazetteer's), "" while no
// versions read has stated the gazetteer's.
func (o *officialCells) key(families []string) string {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.versions[gazetteerFamily]; !ok {
		return ""
	}
	fams := append(slices.Clone(families), gazetteerFamily)
	slices.Sort(fams)
	var b strings.Builder
	for _, f := range slices.Compact(fams) {
		b.WriteString(f + "=" + o.versions[f] + ";")
	}
	return b.String()
}

// officialCellsOf answers the kept cells of the families, or reads them and
// keeps them; with no key they are read every time.
func (s *Store) officialCellsOf(ctx context.Context, tx pgx.Tx, families []string) ([]string, error) {
	o := &s.cells
	key := o.key(families)
	if key != "" {
		o.mu.Lock()
		c, ok := o.kept[key]
		o.mu.Unlock()
		if ok {
			return c, nil
		}
	}
	rows, _ := run(ctx, tx, speedOfficialCells, families)
	cells, err := pgx.CollectRows(rows, pgx.RowTo[*string])
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, c := range cells {
		if c != nil {
			out = append(out, *c)
		}
	}
	if key != "" {
		o.keep(key, out)
	}
	return out, nil
}

// keep keeps the cells of key; the kept sets are dropped first once there
// are 64.
func (o *officialCells) keep(key string, cells []string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.kept == nil || len(o.kept) >= 64 {
		o.kept = map[string][]string{}
	}
	o.kept[key] = cells
}

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
		cells, err := s.officialCellsOf(ctx, tx, families)
		if err != nil {
			return out, err
		}
		rows, _ = run(ctx, tx, speedOSM, q.At.Lon, q.At.Lat, float64(q.RadiusM), today, families, q.Limit, cells)
		osm, err := pgx.CollectRows(rows, scanSpeed(true))
		if err != nil {
			return out, err
		}
		out.Official, out.OfficialTotal = speedBlock(official)
		out.OSM, out.OSMTotal = speedBlock(osm)
		return out, nil
	})
}
