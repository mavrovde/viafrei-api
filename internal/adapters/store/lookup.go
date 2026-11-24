package store

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/number"
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
	// LookupAnchor resolves a motorway page's anchor, "lat,lon", to the BASt
	// LCL location code of the motorway point it lies on.
	LookupAnchor = "anchor"
	// AnchorMatchM is how near a motorway point an anchor must lie, as the
	// platform tests it (ANCHOR_MATCH_M): a page rounds its anchor to four
	// decimals, under 7 m; 15 m leaves room for the view's own rounding.
	AnchorMatchM = 15
)

// motorwayAnchor reads the BASt LCL location code of the motorway point of
// the LCL nearest the position ($1 lon, $2 lat) within $3 m, a tie broken by
// the junction id (country:table:code): the public id a motorway page's
// anchor is stored as (lead ruling 2026-11-17). The points are the LCL's
// German motorway points with a name, read through their geography GiST.
var motorwayAnchor = named("api_lookup_anchor", `
select t.lcd::text from tmc_locations t
where t.cid = 58 and t.tabcd = 1 and t.tcd = 1 and t.road_number ~ '^A[1-9][0-9]{0,2}$'
  and t.geom is not null and t.name is not null and t.lcd::text ~ '^[0-9]{1,6}$'
  and st_dwithin(t.geom::geometry(Point,4326)::geography, `+atPosition+`, (select $3::float8))
order by t.geom::geometry(Point,4326)::geography <-> `+atPosition+`, t.cid || ':' || t.tabcd || ':' || t.lcd
limit 1`, "float8", "float8", "float8")

// stationEVA reads the EVA number of the railway station whose folded name
// is $1, else the shortest whose folded name starts with it: a stop place
// (a station, or a stop with no parent) of a live feed, its EVA number that
// of the open DB station with its IFOPT, DB StaDa's first.
var stationEVA = named("api_lookup_eva", `
select d.eva_no from stops s
join feeds f on f.gtfs_feed_key = s.gtfs_feed_key
cross join lateral (
  select x.eva_no from db_stations x where x.ifopt = s.id and x.closed_at is null
  order by (x.source_id = 'db_stada') desc, x.station_no limit 1) d
where s.deleted_at is null and s.geom is not null and s.name is not null
  and (s.location_type = 1 or (coalesce(s.location_type::int, 0) = 0 and s.parent_id is null))
  and contract_search_fold(s.name) like (select $1::text) || '%'
  and d.eva_no is not null
order by contract_search_fold(s.name) = (select $1::text) desc, length(s.name), s.name, s.gtfs_feed_key || ':' || s.id
limit 1`, "text")

// gemeindeAGS reads the AGS of the Gemeinde of the position ($1 lon, $2
// lat): the nearest open BKG municipality point with its eight-digit key
// within the municipality reach ($3 m), a distance tie broken by key, as the
// platform names a position's Gemeinde.
var gemeindeAGS = named("api_lookup_ags", `
select g.admin_code from gazetteer_places g
cross join lateral (select st_pointonsurface(g.geom::geometry)::geometry(Point,4326) p) l
where g.source_id = 'bkg_gvisys' and g.kind = 'city' and g.closed_at is null and g.geom is not null
  and g.admin_code ~ '^[0-9]{8}$'
  and st_dwithin(g.geom, `+atPosition+`, (select $3::float8))
  and st_dwithin(l.p::geography, `+atPosition+`, (select $3::float8))
order by l.p::geography <-> `+atPosition+`, g.admin_code
limit 1`, "float8", "float8", "float8")

// Lookup resolves value by the named lookup (refresh.Lookup); "" when
// nothing answers.
func (s *Store) Lookup(ctx context.Context, name, value string) (string, error) {
	var (
		stmt stmt
		args []any
	)
	switch name {
	case LookupStationEVA:
		folded := transit.Fold(value)
		if folded == "" {
			return "", nil
		}
		stmt, args = stationEVA, []any{folded}
	case LookupAGS:
		lat, lon, ok := position(value)
		if !ok {
			return "", nil
		}
		stmt, args = gemeindeAGS, []any{lon, lat, places.MunicipalityReachM}
	case LookupAnchor:
		lat, lon, ok := position(value)
		if !ok {
			return "", nil
		}
		stmt, args = motorwayAnchor, []any{lon, lat, AnchorMatchM}
	default:
		return "", fmt.Errorf("store: no lookup %q", name)
	}
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (string, error) {
		rows, _ := run(ctx, tx, stmt, args...)
		got, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil || len(got) == 0 {
			return "", err
		}
		return got[0], nil
	})
}

// position reads "lat,lon" inside the coordinate ranges.
func position(v string) (lat, lon float64, ok bool) {
	a, b, found := strings.Cut(v, ",")
	lat, errA := number.ParseFinite(a)
	lon, errB := number.ParseFinite(b)
	if !found || errA != nil || errB != nil || lat < -90 || lat > 90 || lon < -180 || lon > 180 {
		return 0, 0, false
	}
	return lat, lon, true
}
