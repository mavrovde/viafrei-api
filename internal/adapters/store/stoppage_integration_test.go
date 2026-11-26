//go:build integration

package store_test

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// walkKey reads every family the fixtures hold, and DB's register.
func walkKey(t *testing.T, st *store.Store) access.Record {
	t.Helper()
	reg, err := st.Register(bounded(t))
	if err != nil {
		t.Fatal(err)
	}
	fams := []string{"db_stada"}
	for _, r := range reg {
		fams = append(fams, r.Family)
	}
	return access.Record{Plan: access.Plan{Name: "t", UseClass: "commercial", Tier: "standard", Sources: fams, Scopes: []access.Scope{"transit"}}}
}

// A stop's page is found as the platform's station page lists its stops:
// a bus stop no station is near is listed by none, one metre from a
// register station it is listed under it, and a stop the station's own
// DHIDs hold is never listed around it. One copy of the fixture holds the
// three cases in turn, its stations changed between them.
func TestStopStationIsTheStationThatListsIt(t *testing.T) {
	stop, _ := boardingCall(t, "and (d.route_type = 3 or d.route_type between 700 and 716)")
	_, id, _ := strings.Cut(stop, ":")
	at := `(select t.geom from public.stops x join public.transit_stop_modes t on t.gtfs_feed_key = x.gtfs_feed_key
	          and t.root_id = coalesce(x.parent_id, x.id) where x.id = '` + id + `' and x.deleted_at is null limit 1)`
	station := func(no, slug, src, ifopt string) []string {
		return []string{`insert into public.db_stations (station_no, name, eva_nos, ifopt, geom, source_id, content_hash, first_seen_at, last_seen_at, updated_at)
			select '` + no + `', 'Walk ` + no + `', '{}', ` + ifopt + `, st_project(` + at + `, 1, 0), '` + src + `', 'h', now(), now(), now()`,
			`insert into public.station_slugs (slug, station_no, name, assigned_at) values ('` + slug + `', '` + no + `', 'x', now())`}
	}
	st, pdb := private(t, `refresh materialized view public.transit_stop_modes`) //nolint:misspell // the SQL keyword
	conn, err := pdb.Owner(bounded(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	exec := func(sqls ...string) {
		t.Helper()
		for _, q := range sqls {
			if _, err := conn.Exec(bounded(t), q); err != nil {
				t.Fatalf("%s: %v", q, err)
			}
		}
	}
	var root string
	var pt geo.Point
	if err := conn.QueryRow(bounded(t), `select coalesce(x.parent_id, x.id), st_y(x.geom::geometry), st_x(x.geom::geometry) from public.stops x
		where x.id = '`+id+`' and x.deleted_at is null limit 1`).Scan(&root, &pt.Lat, &pt.Lon); err != nil {
		t.Fatal(err)
	}
	if l, err := st.StopStation(bounded(t), stop); err != nil || l != nil {
		t.Errorf("no station near: %+v %v", l, err)
	}
	w := walkTo(t, st, stop, pt)
	w.page(t, nil)
	exec(station("99020", "walk-nord", "db_stada", "null")...)
	l, err := st.StopStation(bounded(t), stop)
	if err != nil || l == nil || *l != (transit.StopLink{Slug: "walk-nord", Key: transit.StopKey(root)}) {
		t.Fatalf("%s listed under %+v: %v", stop, l, err)
	}
	if l, err := st.StopStation(bounded(t), "1:no-such-stop"); err != nil || l != nil {
		t.Errorf("no stop: %+v %v", l, err)
	}
	w.page(t, &transit.StopLink{Slug: "walk-nord", Key: transit.StopKey(root)})
	// The timetable station whose own DHID is the stop's level leaves it out.
	level := transit.StationDHID(id)
	if level == "" {
		t.Fatalf("the fixture's stop %s is no DHID", id)
	}
	exec(append([]string{`update public.db_stations set closed_at = now() where station_no = '99020'`},
		station(level, "walk-own", "delfi_static", "'"+level+"'")...)...)
	if l, err := st.StopStation(bounded(t), stop); err != nil || l != nil {
		t.Errorf("an own stop is listed under %+v: %v", l, err)
	}
}

// A failure of the read of the stations around a stop is the read's error,
// whichever statement it struck.
func TestStopStationFailsWithItsStatement(t *testing.T) {
	stop, _ := boardingCall(t, "and (d.route_type = 3 or d.route_type between 700 and 716)")
	fst, f := faultyOn(t, db.ReaderURL)
	failEach(t, fst, f, "StopStation", func(ctx context.Context, s *store.Store) error {
		_, err := s.StopStation(ctx, stop)
		return err
	})
}

// walker asks a stop's pages: walkTo lists the stations near the stop and
// finds it among them, page asks the page of the stop. A stop the register
// does not hold is paged from its own data, under the station that lists it
// (want) or none; with want, the register station's page is its register's.
type walker struct {
	svc        *features.Service
	k          access.Record
	stop, name string
}

func walkTo(t *testing.T, st *store.Store, stop string, pt geo.Point) walker {
	t.Helper()
	w := walker{svc: features.New(features.Repositories{Register: st, Transit: st}, features.Settings{Language: "de"}, time.Now), k: walkKey(t, st), stop: stop}
	list, err := w.svc.Stations(bounded(t), w.k, transit.Query{At: &pt, RadiusM: 50, Limit: 50})
	i := slices.IndexFunc(list.Items, func(s transit.Station) bool { return s.ID == stop })
	if err != nil || i < 0 {
		t.Fatalf("listStations near %v does not list %s: %v", pt, stop, err)
	}
	w.name = list.Items[i].Name
	return w
}

func (w walker) page(t *testing.T, want *transit.StopLink) {
	t.Helper()
	ctx := bounded(t)
	p, err := w.svc.StationPage(ctx, w.k, features.StationPageQuery{StationID: w.stop, Departures: 10})
	if err != nil || !p.Stop || p.Record.Name != w.name || p.Record.At == nil || *p.Record.StopPlaceID != w.stop ||
		(want == nil) != (p.Link == nil) || (want != nil && *p.Link != *want) {
		t.Fatalf("stop page %+v: %v", p, err)
	}
	if want == nil {
		return
	}
	p, err = w.svc.StationPage(ctx, w.k, features.StationPageQuery{StationID: "99020", Departures: 10})
	if err != nil || p.Stop || p.Record.StationNo != "99020" || *p.Record.Slug != want.Slug {
		t.Fatalf("register page %+v: %v", p, err)
	}
}
