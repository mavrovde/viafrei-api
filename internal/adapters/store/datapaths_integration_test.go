//go:build integration

package store_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
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
// not abroad.
func TestLocationAtAnAddress(t *testing.T) {
	var at geo.Point
	if !fixtureRow(t, `select st_y(a.location), st_x(a.location) from contract.address a
		where a.location is not null order by a.address_id limit 1`, &at.Lat, &at.Lon) {
		t.Fatal("the fixtures hold no address")
	}
	l, err := seeded(t).Location(bounded(t), places.LocationQuery{At: at}, every(t))
	if err != nil || l.Address == nil || *l.Address.DistanceM != 0 || l.MayLieAbroad {
		t.Fatalf("%+v %v", l, err)
	}
}

// Under a district with a region code, the Land is the one that code
// begins: a district planted on a municipality's point and key.
func TestLocationUnderADistrictReadsItsLand(t *testing.T) {
	st, cp := private(t, plantCopy("contract.place", "admin_code",
		`w.id::text in (select place_id from contract.place where place_kind = 'municipality' and region_code is not null)`,
		`jsonb_build_object('kind', 'district', 'external_id', w.external_id || '-district', 'closed_at', null)`))
	var at geo.Point
	ownerOf(t, cp, `select st_y(location), st_x(location) from contract.place
		where place_kind = 'district' and region_code is not null limit 1`, &at.Lat, &at.Lon)
	l, err := st.Location(bounded(t), places.LocationQuery{At: at}, every(t))
	if err != nil || l.District == nil || l.District.RegionCode == nil || l.State == nil {
		t.Fatalf("%+v %v", l, err)
	}
}

// ownerOf reads one row of the copy cp as its owner into dest.
func ownerOf(t *testing.T, cp *pgtest.DB, sql string, dest ...any) {
	t.Helper()
	conn, err := cp.Owner(bounded(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if err := conn.QueryRow(bounded(t), sql).Scan(dest...); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
}

// plantCopy copies the first row, by id, of the platform's table under
// the contract view that has column, among those where holds, as one more
// row with the jsonb overrides: the table is read off the view, as it is
// the platform's to name.
func plantCopy(view, column, where, overrides string) string {
	insert := "insert into %1$s overriding system value select (jsonb_populate_record(null::%1$s, to_jsonb(w)" +
		" || jsonb_build_object('id', (select max(id) from %1$s) + 1) || " + overrides + ")).* from %1$s w where " +
		where + " order by w.id limit 1"
	return `do $plant$ declare t text; begin
		select d.refobjid::regclass::text into strict t from pg_rewrite r
		  join pg_depend d on d.classid = 'pg_rewrite'::regclass and d.objid = r.oid
		    and d.refclassid = 'pg_class'::regclass and d.refobjid <> r.ev_class
		  join pg_attribute a on a.attrelid = d.refobjid and a.attname = '` + column + `'
		 where r.ev_class = '` + view + `'::regclass group by d.refobjid;
		execute format('` + strings.ReplaceAll(insert, "'", "''") + `', t);
	end $plant$`
}

// plantWarning copies the first placed warning as one more, version n,
// of level, in force since an hour.
func plantWarning(n, level int) string {
	return plantCopy("contract.weather_warning", "warncell_id", "w.area is not null", fmt.Sprintf(`jsonb_build_object(
		'version', w.version + 1000 + %d, 'level', %d, 'valid_from', now() - interval '1 hour', 'valid_to', null,
		'closed_at', null, 'closed_reason', null, 'deleted_at', null)`, n, level))
}

// At a point more warnings answer than the limit: the page says so. A
// stored level outside 1 to 4 in force there is refused, not re-graded.
func TestWarningsAtMoreThanTheLimitAndABadLevel(t *testing.T) {
	var fams []string
	owner(t, `select array_agg(source_ref order by source_ref) from contract.source`, &fams)
	const where = `select st_y(st_pointonsurface(area)), st_x(st_pointonsurface(area)) from contract.weather_warning
		where area is not null and valid_to is null and valid_from > now() - interval '2 hours' limit 1`
	st, cp := private(t, plantWarning(1, 2), plantWarning(2, 3))
	var at geo.Point
	ownerOf(t, cp, where, &at.Lat, &at.Lon)
	got, err := st.WarningsAt(bounded(t), weather.Query{At: at, MinLevel: 1, Limit: 1}, fams, time.Now())
	if err != nil || len(got.Warnings) != 1 || !got.HasMore {
		t.Fatalf("%+v %v", got, err)
	}
	st, cp = private(t, plantWarning(1, 0))
	ownerOf(t, cp, where, &at.Lat, &at.Lon)
	if _, err := st.WarningsAt(bounded(t), weather.Query{At: at, MinLevel: 1, Limit: 5}, fams, time.Now()); err == nil || !strings.Contains(err.Error(), "level 0") {
		t.Fatalf("a level-0 warning in force: %v", err)
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

// A hydrogen read for a lorry or a car asks for that vehicle's refill codes.
func TestHydrogenForALorry(t *testing.T) {
	ctx := context.Background()
	for _, v := range []string{"truck", "car"} {
		if _, err := seeded(t).HydrogenNear(ctx, energy.HydrogenQuery{At: geo.Point{Lat: 51, Lon: 10}, RadiusM: 50000, Vehicle: v, Limit: 5}, every(t)); err != nil {
			t.Fatal(v, err)
		}
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

// A rule topic whose title, or a rule whose review date, the platform's
// schema holds but a copy has lost, fails the read rather than answering a
// topic with a blank title or a rule reviewed on no date.
func TestTopicRefusesARuleWithNoReviewDateOrNoTitle(t *testing.T) {
	var code string
	owner(t, `select topic_code from public.driving_rule_facts order by fact_key limit 1`, &code)
	for col, plant := range map[string]string{
		"title":    `alter table public.driving_rule_topics alter column title_de drop not null; update public.driving_rule_topics set title_de = null where topic_code = '` + code + `'`,
		"reviewed": `alter table public.driving_rule_facts alter column reviewed_on drop not null; update public.driving_rule_facts set reviewed_on = null where topic_code = '` + code + `'`,
	} {
		st, _ := private(t, plant)
		if _, err := st.Topic(bounded(t), code, "de"); err == nil {
			t.Errorf("a topic with no %s was read", col)
		}
	}
}
