//go:build integration

package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/pgtest"
)

// fixtureRow reads one row the fixtures hold into dest; false when they hold none.
func fixtureRow(t *testing.T, sql string, dest ...any) bool {
	t.Helper()
	ctx := bounded(t)
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	err = conn.QueryRow(ctx, sql).Scan(dest...)
	if errors.Is(err, pgx.ErrNoRows) {
		return false
	}
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return true
}

func every(t *testing.T) []string {
	t.Helper()
	reg, err := seeded(t).Register(bounded(t))
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, len(reg))
	for i, r := range reg {
		out[i] = r.Family
	}
	return out
}

// At an address the location names it, near enough that the position is
// not abroad; under a district with a region code, the Land is the one that
// code begins.
func TestLocationAtAnAddress(t *testing.T) {
	st, fams := seeded(t), every(t)
	var at geo.Point
	if !fixtureRow(t, `select st_y(a.location), st_x(a.location) from contract.address a
		where a.location is not null order by a.address_id limit 1`, &at.Lat, &at.Lon) {
		t.Fatal("the fixtures hold no address")
	}
	l, err := st.Location(bounded(t), places.LocationQuery{At: at}, fams)
	if err != nil || l.Address == nil || *l.Address.DistanceM != 0 || l.MayLieAbroad {
		t.Fatalf("%+v %v", l, err)
	}
	if !fixtureRow(t, `select st_y(p.location), st_x(p.location) from contract.place p
		where p.place_kind = 'district' and p.region_code is not null and p.location is not null and p.withdrawn_at is null
		order by p.place_id limit 1`, &at.Lat, &at.Lon) {
		dbAsk(t, "contract.place holds no district with a region code: the Land of a location is never read by its code")
	}
	if l, err = st.Location(bounded(t), places.LocationQuery{At: at}, fams); err != nil || l.District == nil {
		t.Fatalf("at a district: %+v %v", l, err)
	}
}

// At a junction the junctions read lists it, at most the limit.
func TestJunctionsAtAJunction(t *testing.T) {
	var at geo.Point
	if !fixtureRow(t, `select st_y(location), st_x(location) from contract.junction
		where name is not null and location is not null order by junction_id limit 1`, &at.Lat, &at.Lon) {
		dbAsk(t, "contract.junction holds no named junction: no junction is near anything")
	}
	js, err := seeded(t).Junctions(bounded(t), places.NearbyQuery{At: at, RadiusM: 1000, Limit: 1}, every(t))
	if err != nil || len(js) != 1 {
		t.Fatalf("%+v %v", js, err)
	}
}

// A station's board lists a call it departs in the window; an unknown
// station is not found.
func TestDeparturesAndAnUnknownStation(t *testing.T) {
	st, fams := seeded(t), every(t)
	if _, err := st.Station(bounded(t), "no-such-station", fams); !errors.Is(err, transit.ErrNotFound) {
		t.Fatalf("an unknown station: %v", err)
	}
	var (
		station string
		at      time.Time
	)
	if !fixtureRow(t, `select d.stop_place_id, d.planned_departure_at from contract.departure d
		where d.boarding and exists (select 1 from contract.scheduled_call nx join contract.stop_point np on np.stop_point_id = nx.stop_point_id
		  where nx.journey_id = d.journey_id and nx.call_order > d.call_order and np.stop_place_id <> d.stop_place_id)
		order by d.planned_departure_at, d.journey_id limit 1`, &station, &at) {
		dbAsk(t, "contract.departure holds no boarding call with a later stop: no board lists a call")
	}
	ds, err := st.Departures(bounded(t), transit.DepartureQuery{StationID: station, From: at, Window: time.Minute, Limit: 5}, fams)
	if err != nil || len(ds) == 0 {
		t.Fatalf("%+v %v", ds, err)
	}
}

// A hydrogen read for a lorry asks for the truck refill codes.
func TestHydrogenForALorry(t *testing.T) {
	ctx := context.Background()
	if _, err := seeded(t).HydrogenNear(ctx, energy.HydrogenQuery{At: geo.Point{Lat: 51, Lon: 10}, RadiusM: 50000, Vehicle: "truck", Limit: 5}, every(t)); err != nil {
		t.Fatal(err)
	}
}

// private is a store on a copy of the package's database of its own, which
// the test may add rows to (as owner) without another test seeing them.
func private(t *testing.T, rows ...string) (*store.Store, *pgtest.DB) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), pgtest.SetupBound)
	defer cancel()
	seed, err := seedRows()
	if err != nil {
		t.Fatal(err)
	}
	cp, err := pgtest.Seeded(ctx, seed)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cp.Stop)
	conn, err := cp.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	for _, r := range rows {
		if _, err := conn.Exec(ctx, r); err != nil {
			t.Fatalf("%s: %v", r, err)
		}
	}
	cfg, err := config.Load(config.Options{Environ: []string{"VFAPI_DB_URL=" + cp.ReaderURL}, RequireSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return st, cp
}

// A Land's coverage names its own families, those current only, and the
// families it carries.
func TestStateCoverageReadsEveryRole(t *testing.T) {
	st, _ := private(t, `insert into contract.message_coverage (state_code, source_ref, coverage_role, current_only)
		select '09', s.source_ref, case when s.n = 1 then 'own' else 'carried' end, s.n = 1
		from (select source_ref, row_number() over (order by source_ref) n from contract.source
		      where source_ref not in (select source_ref from contract.message_coverage where state_code = '09')) s
		where s.n <= 2`)
	var fams []string
	owner(t, `select array_agg(source_ref order by source_ref) from contract.source`, &fams)
	p, err := st.SituationsNear(bounded(t), roads.AreaQuery{State: "09", Limit: 5}, roads.Families{Roads: fams}, time.Now())
	if err != nil || len(p.Coverage["09"].CurrentOnly) == 0 || len(p.Coverage["09"].Carried) == 0 {
		t.Fatalf("%+v %v", p.Coverage, err)
	}
}

// An alert placed on a station's stops counts against that station.
func TestAffectedStationsCountAnAlertOnTheirStops(t *testing.T) {
	st, cp := private(t, `insert into contract.service_alert (alert_id, header, source_ref, recorded_at, changed_at, active_periods, placed_by, stop_ids)
		select a.alert_id || '-stop', a.header, a.source_ref, now(), now(), '[]', 'stop',
		       array[(select stop_place_id from contract.stop_place where withdrawn_at is null order by stop_place_id limit 1)]
		from contract.service_alert a order by a.alert_id limit 1`)
	var fams []string
	owner(t, `select array_agg(distinct source_ref order by source_ref) from contract.source`, &fams)
	// The alert is live while its source was checked within three cadences.
	conn, err := cp.Owner(bounded(t))
	if err != nil {
		t.Fatal(err)
	}
	var at time.Time
	err = conn.QueryRow(bounded(t), `select checked_at from contract.service_alert where alert_id like '%-stop'`).Scan(&at)
	_ = conn.Close(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.AffectedStations(access.WithAudience(bounded(t), access.AudienceOwn), fams, at, 500)
	if err != nil {
		t.Fatal(err)
	}
	alerted := false
	for _, a := range got {
		alerted = alerted || a.Alerts > 0
	}
	if !alerted {
		t.Fatalf("no station counts the alert: %+v", got)
	}
}
