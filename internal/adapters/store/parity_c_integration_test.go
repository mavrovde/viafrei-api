//go:build integration

package store_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// The parity cases of the location, lookup, nearby, state and road reads,
// beside namedCases (merged into it when the package loads).
func init() {
	for name, cases := range map[string]func(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any{
		"api_location_place":         locationPlaceCases,
		"api_location_sectors":       locationSectorCases,
		"api_location_abroad_family": locationFamilyCases,
		"api_location_state":         locationStateCases,
		"api_location_address":       locationAddressCases,
		"api_lookup_anchor":          lookupAnchorCases,
		"api_lookup_eva":             lookupEVACases,
		"api_lookup_ags":             lookupAGSCases,
	} {
		namedCases[name] = cases
	}
	for need := range places.NeedKinds {
		namedCases["api_nearby_need_"+need] = nearbyNeedCases
	}
	namedCases["api_nearby_junctions"] = nearbyJunctionCases
	namedCases["api_state_warnings"] = stateWarningCases
	namedCases["api_state_alerts"] = stateAlertCases
	namedCases["api_road_shapes"] = func(*testing.T, context.Context, *pgx.Conn) [][]any { return [][]any{{}} }
	namedCases["api_road_junction_lands"] = roadJunctionLandCases
	namedCases["api_road"] = roadCases
	namedCases["api_road_coverage"] = roadCoverageCases
	namedCases["api_motorway_exits"] = roadJunctionLandCases
	namedCases["api_motorway_exits_warm"] = roadJunctionLandCases
	namedCases["api_road_states"] = roadStateCases
	namedCases["api_lamp_warnings_road"] = lampRoadCases
	namedCases["api_lamp_warnings_near"] = lampNearCases
	namedCases["api_lamp_warnings_state"] = lampStateCases
	namedCases["api_road_lamp_warnings"] = roadLampCases
	namedCases["api_states_road"] = statesRoadCases
	namedCases["api_states_near"] = statesNearCases
	namedCases["api_states_state"] = statesStateCases
}

// positions are the seeded points of sql (lat, lon rows), each also moved
// off by about a kilometre, and points far from any row (abroad and at sea).
func positions(t *testing.T, ctx context.Context, c *pgx.Conn, sql string) [][2]float64 {
	t.Helper()
	rows, _ := c.Query(ctx, sql)
	pts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) ([2]float64, error) {
		var p [2]float64
		return p, r.Scan(&p[0], &p[1])
	})
	if err != nil {
		t.Fatal(err)
	}
	out := [][2]float64{{0, 0}, {46.2, 6.1}, {55.6, 15.8}, {51.0, 10.0}}
	for _, p := range pts {
		out = append(out, p, [2]float64{p[0] + 0.009, p[1] - 0.007})
	}
	return out
}

func placeFamilies(t *testing.T, ctx context.Context, c *pgx.Conn) [][]string {
	all := column(t, ctx, c, `select distinct source_id from public.gazetteer_places order by 1`)
	return [][]string{all, all[:1], {}}
}

func locationPlaceCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	pts := positions(t, ctx, c, `select st_y(geom::geometry), st_x(geom::geometry) from public.gazetteer_places where kind in ('city', 'admin') order by id`)
	var out [][]any
	for _, fams := range placeFamilies(t, ctx, c) {
		for _, p := range pts {
			out = append(out,
				[]any{p[1], p[0], "municipality", places.MunicipalityReachM, fams},
				[]any{p[1], p[0], "district", places.DistrictReachM, fams},
				[]any{p[1], p[0], "state", places.StateReachM, fams})
		}
	}
	return out
}

func addressFamilies(t *testing.T, ctx context.Context, c *pgx.Conn) [][]string {
	all := column(t, ctx, c, `select distinct source_id from public.osm_addresses order by 1`)
	return [][]string{all, {"no_such_family"}, {}}
}

func addressPositions(t *testing.T, ctx context.Context, c *pgx.Conn) [][2]float64 {
	return positions(t, ctx, c, `select st_y(geom::geometry), st_x(geom::geometry) from public.osm_addresses order by id`)
}

func locationSectorCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	var out [][]any
	for _, fams := range addressFamilies(t, ctx, c) {
		for _, p := range addressPositions(t, ctx, c) {
			out = append(out, []any{places.SectorWedges(geo.Point{Lat: p[0], Lon: p[1]}, places.AbroadScanM, places.AbroadSectors), fams})
		}
	}
	return out
}

func locationFamilyCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	var out [][]any
	for _, fams := range addressFamilies(t, ctx, c) {
		for _, p := range addressPositions(t, ctx, c) {
			out = append(out, []any{p[1], p[0], fams})
		}
	}
	return out
}

func locationAddressCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	var out [][]any
	for _, fams := range addressFamilies(t, ctx, c) {
		for _, p := range addressPositions(t, ctx, c) {
			out = append(out, []any{p[1], p[0], places.AddressReachM, fams}, []any{p[1], p[0], 50, fams})
		}
	}
	return out
}

func locationStateCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	codes := append(column(t, ctx, c, `select ags from public.ags_regions order by 1`), "99001", "0", "")
	var out [][]any
	for _, fams := range placeFamilies(t, ctx, c) {
		for _, code := range codes {
			out = append(out, []any{code, fams})
		}
	}
	return out
}

func lookupAnchorCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	var out [][]any
	for _, p := range positions(t, ctx, c, `select st_y(geom::geometry), st_x(geom::geometry) from public.tmc_locations where geom is not null order by cid, tabcd, lcd`) {
		out = append(out, []any{p[1], p[0], store.AnchorMatchM}, []any{p[1], p[0], 5000})
	}
	return out
}

func lookupEVACases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	out := [][]any{{""}, {"zz no such"}, {"%"}, {"_"}}
	for _, n := range column(t, ctx, c, `select contract_search_fold(name) from public.stops where name is not null order by 1`) {
		out = append(out, []any{n})
		if len(n) > 2 {
			out = append(out, []any{n[:2]})
		}
	}
	return out
}

func lookupAGSCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	var out [][]any
	for _, p := range positions(t, ctx, c, `select st_y(geom::geometry), st_x(geom::geometry) from public.gazetteer_places order by id`) {
		out = append(out, []any{p[1], p[0], places.MunicipalityReachM}, []any{p[1], p[0], 50})
	}
	return out
}

// plantNeedPlaces plants, once, open places of every need's kind 300 m from
// the first fixture place of interest, two of them at one point (a distance
// tie), a post office named after a parcel service and a closed place; they
// are removed when the test ends.
func plantNeedPlaces(t *testing.T, ctx context.Context) {
	t.Helper()
	o, err := pgx.Connect(ctx, db.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = o.Close(context.WithoutCancel(ctx)) }()
	var n int
	if err := o.QueryRow(ctx, `select count(*) from public.osm_pois where osm_type = 'node' and osm_id > 9900000000`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n > 0 {
		return
	}
	var kinds []string
	for _, ks := range places.NeedKinds {
		kinds = append(kinds, ks...)
	}
	if _, err := o.Exec(ctx, `insert into public.osm_pois (osm_type, osm_id, name, name_norm, category, category_value, geom, source_id,
    extract_date, first_seen_at, last_seen_at, closed_at)
  select 'node', 9900000000 + k.i * 10 + v.j, v.prefix || ' ' || k.kind, lower(v.prefix || ' ' || k.kind),
    split_part(k.kind, '=', 1), split_part(k.kind, '=', 2),
    st_project(f.geom, 300 + v.off, radians(45))::geography, 'osm', current_date, now(), now(), v.gone
  from unnest($1::text[]) with ordinality k(kind, i),
       (select p.geom from public.osm_pois p where p.osm_id < 9900000000 order by p.id limit 1) f,
       (values (1, 'Erste', 0, null::timestamptz), (2, 'Zweite', 0, null), (3, 'DPD Paketshop', 40, null),
               (4, 'Alte', -100, now())) v(j, prefix, off, gone)`, kinds); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		o, err := pgx.Connect(context.WithoutCancel(ctx), db.OwnerURL)
		if err == nil {
			_, err = o.Exec(context.WithoutCancel(ctx), `delete from public.osm_pois where osm_type = 'node' and osm_id > 9900000000`)
			_ = o.Close(context.WithoutCancel(ctx))
		}
		if err != nil {
			t.Error(err)
		}
	})
}

func nearbyNeedCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	plantNeedPlaces(t, ctx)
	all := column(t, ctx, c, `select distinct source_id from public.osm_pois order by 1`)
	var out [][]any
	for _, fams := range [][]string{all, {"no_such_family"}} {
		for _, p := range positions(t, ctx, c, `select st_y(geom::geometry), st_x(geom::geometry) from public.osm_pois order by id`) {
			out = append(out, []any{p[1], p[0], places.NeedMaxM, fams, places.NeedRows}, []any{p[1], p[0], 300, fams, 1})
		}
	}
	return out
}

func nearbyJunctionCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	var out [][]any
	for _, fams := range [][]string{{"bast_lcl"}, {"osm"}, {}} {
		for _, p := range positions(t, ctx, c, `select st_y(geom::geometry), st_x(geom::geometry) from public.tmc_locations where geom is not null order by cid, tabcd, lcd`) {
			out = append(out, []any{p[1], p[0], 25000, fams, 40}, []any{p[1], p[0], 2000, fams, 1})
		}
	}
	return out
}

// stateCases are the Länder of the fixture, one that is none, at the
// instants of the fixture's rows (from times) and now, of every family of
// fams and of none.
func stateCases(t *testing.T, ctx context.Context, c *pgx.Conn, fams, times string) [][]any {
	lands := append(column(t, ctx, c, `select ags from public.ags_regions where length(ags) = 2 order by 1`), "99")
	all := column(t, ctx, c, fams)
	var at []time.Time
	rows, _ := c.Query(ctx, times)
	at, err := pgx.CollectRows(rows, pgx.RowTo[time.Time])
	if err != nil {
		t.Fatal(err)
	}
	at = append(at, time.Now())
	var out [][]any
	for _, f := range [][]string{all, {}} {
		for _, a := range at {
			for _, l := range lands {
				out = append(out, []any{f, a, l})
			}
		}
	}
	return out
}

func stateWarningCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	return stateCases(t, ctx, c, `select distinct source_feed from public.weather_warnings order by 1`,
		`select distinct t from public.weather_warnings, lateral (values (valid_from), (valid_from + interval '1 minute'), (valid_to)) v(t)
		 where t is not null order by 1 limit 12`)
}

func stateAlertCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	return stateCases(t, ctx, c, `select distinct source_feed from public.delfi_service_alerts order by 1`,
		`select distinct t from public.delfi_service_alerts, lateral (values (last_seen_at), (active_from), (active_to)) v(t)
		 where t is not null order by 1 limit 12`)
}

func roadJunctionLandCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	var out [][]any
	for _, r := range append(column(t, ctx, c, `select distinct road_number from public.tmc_locations where road_number is not null order by 1`), "A0", "") {
		out = append(out, []any{r})
	}
	return out
}

func roadCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	out := [][]any{{"A0"}, {""}}
	for _, r := range column(t, ctx, c, `select road from public.autobahn_roads order by 1`) {
		out = append(out, []any{r}, []any{strings.ToUpper(strings.ReplaceAll(r, " ", ""))})
	}
	return out
}

func roadCoverageCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	states := append(column(t, ctx, c, `select distinct state_code from public.message_coverage_sources order by 1`), "99")
	fams := column(t, ctx, c, `select distinct source_ref from public.message_coverage_sources order by 1`)
	return [][]any{{states, fams}, {states, fams[:1]}, {states[:1], fams}, {[]string{}, fams}, {states, []string{}}}
}

func roadStateCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	refs := append(column(t, ctx, c, `select distinct upper(replace(road_number, ' ', '')) from public.tmc_locations where road_number is not null order by 1`), "A0", "")
	return [][]any{{refs}, {refs[:1]}, {[]string{}}}
}

// lampTimes are the instants of the fixture's warnings (their starts, a
// minute later and their ends) and now.
func lampTimes(t *testing.T, ctx context.Context, c *pgx.Conn) []time.Time {
	rows, _ := c.Query(ctx, `select distinct t from public.weather_warnings,
		lateral (values (valid_from), (valid_from + interval '1 minute'), (valid_to)) v(t) where t is not null order by 1 limit 8`)
	at, err := pgx.CollectRows(rows, pgx.RowTo[time.Time])
	if err != nil {
		t.Fatal(err)
	}
	return append(at, time.Now())
}

func lampFamilies(t *testing.T, ctx context.Context, c *pgx.Conn) [][]string {
	return [][]string{column(t, ctx, c, `select distinct source_feed from public.weather_warnings order by 1`), {}}
}

func lampRoadCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	refs := append(column(t, ctx, c, `select distinct upper(replace(road_number, ' ', '')) from public.tmc_locations where road_number is not null order by 1`), "A0")
	var out [][]any
	for _, f := range lampFamilies(t, ctx, c) {
		for _, at := range lampTimes(t, ctx, c) {
			for _, r := range refs {
				out = append(out, []any{f, at, r})
			}
		}
	}
	return out
}

func lampNearCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	pts := positions(t, ctx, c, `select st_y(st_pointonsurface(area::geometry)), st_x(st_pointonsurface(area::geometry)) from public.weather_warnings where area is not null order by id limit 6`)
	var out [][]any
	for _, f := range lampFamilies(t, ctx, c) {
		for _, at := range lampTimes(t, ctx, c) {
			for _, p := range pts {
				out = append(out, []any{f, at, p[0], p[1], 5000}, []any{f, at, p[0], p[1], 0})
			}
		}
	}
	return out
}

func lampStateCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	lands := append(column(t, ctx, c, `select ags from public.ags_regions where length(ags) = 2 order by 1`), "de", "99")
	var out [][]any
	for _, f := range lampFamilies(t, ctx, c) {
		for _, at := range lampTimes(t, ctx, c) {
			for _, l := range lands {
				out = append(out, []any{f, at, l})
			}
		}
	}
	return out
}

func roadLampCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	refs := append(column(t, ctx, c, `select distinct upper(replace(road_number, ' ', '')) from public.tmc_locations where road_number is not null order by 1`), "A0")
	var out [][]any
	for _, f := range lampFamilies(t, ctx, c) {
		rows, _ := c.Query(ctx, `select distinct w.valid_from + interval '1 minute' from public.weather_warnings w
			join public.tmc_locations t on st_intersects(w.area::geometry, t.geom::geometry(Point,4326))
			where w.valid_from is not null order by 1 limit 4`)
		on, err := pgx.CollectRows(rows, pgx.RowTo[time.Time])
		if err != nil {
			t.Fatal(err)
		}
		for _, at := range append(lampTimes(t, ctx, c), on...) {
			out = append(out, []any{f, at, refs}, []any{f, at, refs[:1]})
		}
	}
	return out
}

func statesRoadCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	var out [][]any
	for _, r := range append(column(t, ctx, c, `select distinct upper(replace(road_number, ' ', '')) from public.tmc_locations where road_number is not null order by 1`), "A0") {
		out = append(out, []any{r})
	}
	return out
}

func statesNearCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	pts := positions(t, ctx, c, `select st_y(geom::geometry), st_x(geom::geometry) from public.tmc_locations where geom is not null order by lcd limit 6`)
	out := [][]any{{50.0, 10.0, 1000.0}, {54.9, 6.0, 0.0}}
	for _, p := range pts {
		out = append(out, []any{p[0], p[1], 20000.0}, []any{p[0] + 0.01, p[1], 10.0})
	}
	return out
}

func statesStateCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	return [][]any{{"de"}, {"09"}, {"zz"}}
}
