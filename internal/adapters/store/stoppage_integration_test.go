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
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// walkKey reads every family the fixtures hold.
func walkKey(t *testing.T) access.Record {
	t.Helper()
	return access.Record{Plan: access.Plan{Name: "t", UseClass: "commercial", Tier: "standard", Sources: append(every(t), "db_stada"),
		Scopes: []access.Scope{"transit"}}}
}

// A stop's page is found as the platform's station page lists its stops:
// a bus stop one metre from a register station is listed under it, the
// same stop with the station gone is listed by none, and a stop the
// station's own DHIDs hold is never listed around it.
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
	refresh := `refresh materialized view public.transit_stop_modes` //nolint:misspell // the SQL keyword
	st, pdb := private(t, append([]string{refresh}, station("99020", "walk-nord", "db_stada", "null")...)...)
	var root string
	owner(t, `select coalesce(x.parent_id, x.id) from public.stops x where x.id = '`+id+`' and x.deleted_at is null limit 1`, &root)
	l, err := st.StopStation(bounded(t), stop)
	if err != nil || l == nil || *l != (transit.StopLink{Slug: "walk-nord", Key: transit.StopKey(root)}) {
		t.Fatalf("%s listed under %+v: %v", stop, l, err)
	}
	// The timetable station whose own DHID is the stop's level leaves it out.
	level := transit.StationDHID(id)
	if level == "" {
		t.Fatalf("the fixture's stop %s is no DHID", id)
	}
	own, _ := private(t, append([]string{refresh}, station(level, "walk-own", "delfi_static", "'"+level+"'")...)...)
	if l, err := own.StopStation(bounded(t), stop); err != nil || l != nil {
		t.Errorf("an own stop is listed under %+v: %v", l, err)
	}
	bare, _ := private(t, refresh)
	if l, err := bare.StopStation(bounded(t), stop); err != nil || l != nil {
		t.Errorf("no station near: %+v %v", l, err)
	}
	if l, err := st.StopStation(bounded(t), "1:no-such-stop"); err != nil || l != nil {
		t.Errorf("no stop: %+v %v", l, err)
	}
	fst, f := faultyOn(t, pdb.ReaderURL)
	failEach(t, fst, f, "StopStation", func(ctx context.Context, s *store.Store) error {
		_, err := s.StopStation(ctx, stop)
		return err
	})
	walk(t, st, stop, &transit.StopLink{Slug: "walk-nord", Key: transit.StopKey(root)})
	walk(t, bare, stop, nil)
}

// walk lists the stations near the stop and asks the page of the one that
// is the stop: a stop the register does not hold is paged from its own
// data, under the station that lists it (want) or none; a register station
// is its register's page.
func walk(t *testing.T, st *store.Store, stop string, want *transit.StopLink) {
	t.Helper()
	svc := features.New(features.Repositories{Register: st, Transit: st}, features.Settings{Language: "de"}, time.Now)
	k := walkKey(t)
	ctx := bounded(t)
	b, err := svc.Departures(ctx, k, transit.DepartureQuery{StationID: stop, From: time.Now(), Window: time.Minute, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	pt := b.Station.Location
	list, err := svc.Stations(ctx, k, transit.Query{At: &pt, RadiusM: 50, Limit: 50})
	if err != nil || !slices.ContainsFunc(list.Items, func(s transit.Station) bool { return s.ID == stop }) {
		t.Fatalf("listStations near %v does not list %s: %v", pt, stop, err)
	}
	p, err := svc.StationPage(ctx, k, features.StationPageQuery{StationID: stop, Departures: 10})
	if err != nil || !p.Stop || p.Record.Name != b.Station.Name || p.Record.At == nil || *p.Record.StopPlaceID != stop ||
		(want == nil) != (p.Link == nil) || (want != nil && *p.Link != *want) {
		t.Fatalf("stop page %+v: %v", p, err)
	}
	if want == nil {
		return
	}
	p, err = svc.StationPage(ctx, k, features.StationPageQuery{StationID: "99020", Departures: 10})
	if err != nil || p.Stop || p.Record.StationNo != "99020" || *p.Record.Slug != want.Slug {
		t.Fatalf("register page %+v: %v", p, err)
	}
}
