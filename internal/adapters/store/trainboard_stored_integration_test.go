//go:build integration

package store_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// storedBoardSeed is a large station's board as the stored-boards worker
// writes it: two EVA numbers, a late train, a cancelled one, a moved
// platform and a row whose planned time cannot be read; and a second
// station whose degraded board holds none.
var storedBoardSeed = []string{
	`insert into public.db_station_boards (station_no, eva_nos, read_at, degraded, covers_until, departures) values
	   ('9901', '{8099701,8099702}', now() - interval '4 minutes', false, now() + interval '80 minutes',
	    jsonb_build_array(
	      jsonb_build_object('line', 'ICE 279', 'category', 'ICE', 'destination', 'Chur', 'plannedAt', to_char((now() + interval '5 minutes') at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
	                         'delayMin', 83, 'plannedPlatform', '7', 'platform', '8', 'platformChanged', true, 'cancelled', false),
	      jsonb_build_object('line', 'RE 1', 'category', 'RE', 'destination', null, 'plannedAt', to_char((now() + interval '9 minutes') at time zone 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.MS"Z"'),
	                         'delayMin', 0, 'plannedPlatform', '3', 'platform', '3', 'platformChanged', false, 'cancelled', true),
	      jsonb_build_object('line', 'S 8', 'plannedAt', 'soon'))),
	   ('9902', '{8099703}', now() - interval '1 minute', true, now() + interval '30 minutes', '[]'::jsonb)`,
}

func TestStoredTrainBoardReadsTheWorkersRow(t *testing.T) {
	st, pdb := private(t, storedBoardSeed...)
	fams := []string{transit.TrainBoardFamily}
	b, err := st.StoredTrainBoard(bounded(t), "8099702", fams)
	if err != nil || b == nil || !slices.Equal(b.EVAs, []string{"8099701", "8099702"}) || b.Degraded || len(b.Departures) != 3 {
		t.Fatalf("%+v %v", b, err)
	}
	tb := transit.TrainBoardOf("8099702", *b)
	if !tb.Degraded || len(tb.Departures) != 2 || tb.Departures[0].DelayMin() != 83 || *tb.Departures[0].ChangedPlatform != "8" || !tb.Departures[1].Cancelled {
		t.Errorf("board %+v", tb)
	}
	if b, err = st.StoredTrainBoard(bounded(t), "8099703", fams); err != nil || b == nil || !b.Degraded || len(b.Departures) != 0 {
		t.Errorf("none: %+v %v", b, err)
	}
	for _, c := range []struct{ eva, fam string }{{"8099704", transit.TrainBoardFamily}, {"8099701", "other"}} {
		if b, err = st.StoredTrainBoard(bounded(t), c.eva, []string{c.fam}); err != nil || b != nil {
			t.Errorf("%v: %+v %v", c, b, err)
		}
	}
	fst, f := faultyOn(t, pdb.ReaderURL)
	failEach(t, fst, f, "StoredTrainBoard", func(ctx context.Context, s *store.Store) error {
		_, err := s.StoredTrainBoard(ctx, "8099702", fams)
		return err
	})
}

// A station's interchange is the timetable stops the zHV pairs with its
// StaDa point: their families' lines, and the departing U-Bahn and tram
// calls of their stops.
func TestTrainInterchangeReadsThePairedStops(t *testing.T) {
	station, at := boardingCall(t, "and (d.route_type in (0, 1) or d.route_type between 400 and 405 or d.route_type between 900 and 906)")
	dhid := station[strings.Index(station, ":")+1:]
	st, pdb := private(t,
		fmt.Sprintf(`insert into public.gazetteer_places (kind, name, name_norm, source_id, external_id, geom)
		  select 'station', 'Hbf', 'hbf', 'db_stada', '8099777', s.geom from public.stops s where s.id = '%[1]s' and s.deleted_at is null limit 1`, dhid),
		fmt.Sprintf(`insert into public.gazetteer_places (kind, name, name_norm, source_id, external_id, geom)
		  select 'stop', 'Hbf', 'hbf', 'delfi_zhv', '%[1]s', st_project(s.geom, 100, 0) from public.stops s where s.id = '%[1]s' and s.deleted_at is null limit 1`, dhid),
		`refresh materialized view public.transit_stop_modes`, //nolint:misspell // the SQL keyword
		// The Trip Updates checked now, a trip-level row for each trip at the station.
		`insert into public.feed_catalog (feed_key, provider, display_name, licence_id, cadence_s)
		   select 'tripupdates', 'p', 'tripupdates', (select licence_id from public.feed_catalog limit 1), 60
		   on conflict (feed_key) do update set cadence_s = 60, closed_at = null`,
		`insert into control.feed_state (ledger, key, updated_at, last_success_at) values ('feed', 'tripupdates', now(), now())
		   on conflict (ledger, key) do update set last_success_at = excluded.last_success_at`,
		fmt.Sprintf(`insert into public.transit_delays (observed_at, feed_ts, trip_id, stop_id, stop_sequence, delay_s, schedule_relationship, source)
		   select distinct now() - interval '2 minutes', now() - interval '2 minutes', st.trip_id, null::text, null::int, null::int, 'CANCELED', 'delfi_rt_tu'
		   from public.stop_times st where st.stop_id like '%[1]s%%' and st.deleted_at is null`, dhid))
	// The fixture's register files no Trip Updates family; a board adds it
	// (boardFamilies).
	fams := append(every(t), transit.TripUpdatesFamily)
	r, err := st.TrainInterchange(bounded(t), []string{"8099777"}, at.Add(-time.Minute), at.Add(time.Minute), at, fams)
	if err != nil || !r.Stops || len(r.Plan) == 0 || len(r.Lines) == 0 || !r.TripUpdatesLive || len(r.TripUpdates) == 0 {
		t.Fatalf("%+v %v", r, err)
	}
	// An hour after the check it is stale: the timetable alone; a key without
	// the Trip Updates reads none.
	for _, c := range []struct {
		now  time.Time
		fams []string
	}{{time.Now().Add(time.Hour), fams}, {at, slices.DeleteFunc(slices.Clone(fams), func(f string) bool { return f == transit.TripUpdatesFamily })}} {
		if r, err = st.TrainInterchange(bounded(t), []string{"8099777"}, at.Add(-time.Minute), at.Add(time.Minute), c.now, c.fams); err != nil || r.TripUpdatesLive || r.TripUpdates != nil || len(r.Plan) == 0 {
			t.Errorf("%v: %+v %v", c.now, r, err)
		}
	}
	// No pairing, no stop; a key without the timetable reads none.
	for _, c := range []struct {
		eva  string
		fams []string
	}{{"8099778", fams}, {"8099777", []string{transit.TrainBoardFamily}}} {
		if r, err = st.TrainInterchange(bounded(t), []string{c.eva}, at, at.Add(time.Minute), at, c.fams); err != nil || r.Stops || r.Plan != nil || r.Lines != nil {
			t.Errorf("%s: %+v %v", c.eva, r, err)
		}
	}
	fst, f := faultyOn(t, pdb.ReaderURL)
	failEach(t, fst, f, "TrainInterchange", func(ctx context.Context, s *store.Store) error {
		_, err := s.TrainInterchange(ctx, []string{"8099777"}, at.Add(-time.Minute), at.Add(time.Minute), at, fams)
		return err
	})
}
