//go:build integration

package store_test

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
	"github.com/mavrovde/viafrei-api/internal/fixtures"
)

// A price a poll confirmed unchanged is restated in fuel_price_latest, and
// the ticks gain no row: the station's prices are read from it, as today's
// fuel read takes them, so a confirmed price is as current as its newest
// confirmation (current, under today's strict comparison, at 29 minutes,
// and not at 30 or 31). A retracted price (deleted_at) is not read.
func TestFuelPricesAreTheNewestConfirmation(t *testing.T) {
	var (
		id, fam, product string
		lat, lon         float64
		tick             time.Time
	)
	owner(t, `select l.station_id, l.source_feed, l.product, st_y(f.geom::geometry), st_x(f.geom::geometry), l.observed_at
from fuel_price_latest l join fuel_stations f on f.id = l.station_id
where l.deleted_at is null and l.price is not null and f.deleted_at is null and f.geom is not null
order by l.observed_at desc, 1, 3 limit 1`, &id, &fam, &product, &lat, &lon, &tick)
	restated := tick.Add(6 * time.Hour).Truncate(time.Second)
	st, _ := private(t,
		`update fuel_price_latest set observed_at = '`+restated.Format(time.RFC3339)+`' where station_id = '`+id+`' and product = '`+product+`'`,
		`update fuel_price_latest set deleted_at = now() where station_id = '`+id+`' and product <> '`+product+`'`,
		`update fuel_price_latest set is_open = false where station_id = '`+id+`' and product = '`+product+`'`,
		// hours never read from the provider are none: no 24 h claim
		`update fuel_stations set whole_day = true, detail_refreshed_at = null where id = '`+id+`'`)
	ctx := access.WithAudience(bounded(t), access.AudienceOwn)
	near, err := st.FuelNear(ctx, energy.FuelQuery{At: geo.Point{Lat: lat, Lon: lon}, RadiusM: 50, Limit: 10}, []string{fam})
	if err != nil {
		t.Fatal(err)
	}
	var got *energy.FuelStation
	for i := range near {
		if near[i].ID == id {
			got = &near[i]
		}
	}
	if got == nil || len(got.Prices) != 1 || got.Prices[0].Product != product || !got.Prices[0].ObservedAt.Equal(restated) {
		t.Fatalf("station %s: %+v", id, got)
	}
	// the state reported with the price, at its time
	if got.Open != "closed" || got.OpenObservedAt == nil || !got.OpenObservedAt.Equal(restated) || got.Hours.WholeDay != nil || got.Hours.AsOf != nil {
		t.Errorf("opening: %s %v, hours %+v", got.Open, got.OpenObservedAt, got.Hours)
	}
	p := got.Prices[0]
	for _, c := range []struct {
		age     time.Duration
		current bool
	}{{29 * time.Minute, true}, {30 * time.Minute, false}, {31 * time.Minute, false}} {
		if p.Current(restated.Add(c.age), 30*time.Minute) != c.current {
			t.Errorf("%v after the confirmation: current %v", c.age, !c.current)
		}
	}
	page, err := st.FuelStation(ctx, id, []string{fam}, restated.Add(29*time.Minute), 30*time.Minute)
	if err != nil || len(page.Prices) != 1 || !page.Prices[0].ObservedAt.Equal(restated) {
		t.Fatalf("by id, 29 min after the confirmation: %+v %v", page.Prices, err)
	}
	if page.Open != "closed" || page.Hours.WholeDay != nil {
		t.Errorf("by id, opening: %s, hours %+v", page.Open, page.Hours)
	}
	// Past the held window the price is not held; the station is.
	if late, err := st.FuelStation(ctx, id, []string{fam}, restated.Add(31*time.Minute), 30*time.Minute); err != nil || len(late.Prices) != 0 {
		t.Errorf("by id, 31 min after: %+v %v", late.Prices, err)
	}
}

// Every kind the platform's facility tables allow (their CHECK: rest_area,
// car_park, park_and_ride, truck, other) maps to its contract kind; a car
// park (a Parkhaus) is a garage, and only a stored other is other.
func TestParkingKindsMapEveryStoredKind(t *testing.T) {
	want := map[string]parking.Kind{"rest_area": parking.KindRestArea, "car_park": parking.KindGarage,
		"park_and_ride": parking.KindParkAndRide, "truck": parking.KindTruckParking, "other": parking.KindOther}
	st, cp := private(t, `with k(n, kind) as (values (1, 'rest_area'), (2, 'car_park'), (3, 'park_and_ride'), (4, 'truck'), (5, 'other')),
f as (select id, row_number() over (order by id) n from parking_facilities where deleted_at is null and closed_at is null and geom is not null)
update parking_facilities p set kind = k.kind from f join k using (n) where p.id = f.id`)
	type planted struct {
		id, kind string
		at       geo.Point
	}
	var rows []planted
	{
		conn, err := cp.Owner(bounded(t))
		if err != nil {
			t.Fatal(err)
		}
		r, err := conn.Query(bounded(t), `select 'p:' || id, kind, st_y(geom::geometry), st_x(geom::geometry) from parking_facilities
where deleted_at is null and closed_at is null and geom is not null order by id limit 5`)
		if err != nil {
			t.Fatal(err)
		}
		for r.Next() {
			var p planted
			if err := r.Scan(&p.id, &p.kind, &p.at.Lat, &p.at.Lon); err != nil {
				t.Fatal(err)
			}
			rows = append(rows, p)
		}
		_ = conn.Close(context.Background())
	}
	if len(rows) != len(want) {
		t.Fatalf("planted %d kinds", len(rows))
	}
	listed := families(t, st, "parking")
	for _, p := range rows {
		sites, _, _, err := st.ParkingNear(bounded(t), parking.Query{At: p.at, RadiusM: 1, Limit: 10}, listed, listed, fixtures.ReferenceInstant)
		if err != nil {
			t.Fatal(err)
		}
		i := slices.IndexFunc(sites, func(s parking.Site) bool { return s.ID == p.id })
		if i < 0 {
			t.Errorf("planted %s (%s) not read", p.id, p.kind)
		} else if sites[i].Kind != want[p.kind] {
			t.Errorf("stored %s reads as %s, want %s", p.kind, sites[i].Kind, want[p.kind])
		}
	}
}

// A warning says where its level came from, as stored: published (the
// DWD's number), derived_from_severity (mapped from its CAP severity
// word), or nothing when not stored.
func TestWarningLevelSourceIsAsStored(t *testing.T) {
	type ask struct {
		cell string
		at   time.Time
	}
	conn, err := db.Owner(bounded(t))
	if err != nil {
		t.Fatal(err)
	}
	rows, err := conn.Query(bounded(t), `select distinct warncell_id, valid_from from weather_warnings where warncell_id is not null order by 2, 1 limit 40`)
	if err != nil {
		t.Fatal(err)
	}
	var asks []ask
	for rows.Next() {
		var a ask
		if err := rows.Scan(&a.cell, &a.at); err != nil {
			t.Fatal(err)
		}
		asks = append(asks, a)
	}
	_ = conn.Close(context.Background())
	// Three warnings the cell read answers, each with the ask that reads it.
	read := func(st *store.Store, a ask) []weather.Warning {
		t.Helper()
		got, err := st.CellWarnings(bounded(t), weather.CellQuery{Cell: a.cell}, families(t, st, "weather"), a.at)
		if err != nil {
			t.Fatal(err)
		}
		return got.Warnings
	}
	seed := seeded(t)
	picked := map[string]ask{}
	for _, a := range asks {
		for _, w := range read(seed, a) {
			if _, ok := picked[w.ID]; !ok && len(picked) < 3 {
				picked[w.ID] = a
			}
		}
	}
	if len(picked) != 3 {
		t.Fatalf("the cell read answers %d warnings", len(picked))
	}
	ids := slices.Sorted(maps.Keys(picked))
	values := []string{"published", "derived_from_severity", ""}
	var plants []string
	for i, id := range ids {
		v := "null"
		if values[i] != "" {
			v = "'" + values[i] + "'"
		}
		plants = append(plants, fmt.Sprintf(`update weather_warnings set level_source = %s where 'dwd:' || id = '%s'`, v, id))
	}
	st, _ := private(t, plants...)
	for i, id := range ids {
		var got *weather.Warning
		for _, w := range read(st, picked[id]) {
			if w.ID == id {
				got = &w
			}
		}
		switch {
		case got == nil:
			t.Errorf("%s not read", id)
		case values[i] == "" && got.LevelSource != nil, values[i] != "" && (got.LevelSource == nil || *got.LevelSource != values[i]):
			t.Errorf("%s: level source %v, want %q", id, got.LevelSource, values[i])
		}
	}
}

// The register, lamp and rules fingerprints of the versions read are read
// again only when the row versions of the tables they are derived from move,
// or once db.reference_versions_ttl is over: a changed licence text moves the
// register at once; a gazetteer municipality, which the probe does not read,
// moves the lamp's reference only after the TTL (or at once with a TTL of
// zero); and a reference read that fails fails the versions read, while a
// kept one answers without it.
func TestTheReferenceFingerprintsAreReadWhenTheirRowsMove(t *testing.T) {
	st, cp := private(t)
	st.WithReferenceVersions(5 * time.Minute)
	exec := func(sql string) {
		t.Helper()
		conn, err := cp.Owner(bounded(t))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close(context.Background()) }()
		if _, err := conn.Exec(bounded(t), sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	read := func() (provenance.Versions, error) {
		t.Helper()
		return st.FreshnessVersions(bounded(t))
	}
	base, err := read()
	if err != nil || base.Register == "" || base.LampReference == "" || base.RulesReference == "" || len(base.Families) == 0 {
		t.Fatalf("versions %+v, %v", base, err)
	}
	var label, city string
	ownerOf(t, cp, `select licence_label from licence_texts order by 1 limit 1`, &label)
	ownerOf(t, cp, `select g.id::text from gazetteer_places g where g.closed_at is null and g.source_id = 'bkg_gvisys' and g.kind = 'city'
	                  and g.admin_code ~ '^[0-9]{8}$' and g.geom is not null order by g.id limit 1`, &city)
	// A probed row: the register moves at once.
	exec(`update licence_texts set url = url || '#probe' where licence_label = '` + strings.ReplaceAll(label, "'", "''") + `'`)
	moved, err := read()
	if err != nil || moved.Register == base.Register || moved.LampReference != base.LampReference {
		t.Fatalf("a licence text changed: register %q (was %q), lamp %q (was %q), %v", moved.Register, base.Register, moved.LampReference, base.LampReference, err)
	}
	// An unprobed row: the lamp's reference stays until the TTL is over.
	exec(`update gazetteer_places set geom = st_translate(geom::geometry, 0.0001, 0)::geography where id = ` + city)
	if kept, err := read(); err != nil || kept.LampReference != base.LampReference || kept.Register != moved.Register {
		t.Fatalf("within the TTL the lamp's reference is %q, want the kept %q (%v)", kept.LampReference, base.LampReference, err)
	}
	// Past the TTL (a TTL every read is older than).
	st.WithReferenceVersions(time.Nanosecond)
	lamp, err := read()
	if err != nil || lamp.LampReference == base.LampReference || lamp.Register != moved.Register {
		t.Fatalf("past the TTL the lamp's reference is %q, still %q (%v)", lamp.LampReference, base.LampReference, err)
	}
	// A TTL of zero reads the fingerprints on every versions read.
	st.WithReferenceVersions(0)
	exec(`update gazetteer_places set geom = st_translate(geom::geometry, -0.0001, 0)::geography where id = ` + city)
	if back, err := read(); err != nil || back.LampReference != base.LampReference {
		t.Fatalf("with no TTL the lamp's reference is %q, want %q back (%v)", back.LampReference, base.LampReference, err)
	}
	// A kept read answers without the reference read; a due one fails with it.
	st.WithReferenceVersions(5 * time.Minute)
	if _, err := read(); err != nil {
		t.Fatal(err)
	}
	exec(`revoke select on gazetteer_places from api_reader`)
	if kept, err := read(); err != nil || kept.LampReference != base.LampReference {
		t.Fatalf("a kept read failed or moved: %q, %v", kept.LampReference, err)
	}
	st.WithReferenceVersions(time.Nanosecond)
	if _, err := read(); err == nil {
		t.Fatal("a reference read that cannot read the gazetteer succeeded")
	}
	// The versions read itself failing fails the answer, kept or not.
	st.WithReferenceVersions(5 * time.Minute)
	exec(`revoke select on licence_texts from api_reader`)
	if _, err := read(); err == nil {
		t.Fatal("a versions read whose probe cannot be read succeeded")
	}
}

// The register is read again only when the probe of the rows it is read
// from moves, or once db.reference_versions_ttl is over: a register row
// changed is read at once; with the probe unchanged the last read answers,
// even while the register itself could not be read; past the TTL that read
// is due and fails; a probe that cannot be read fails the read.
func TestTheRegisterIsReadAgainOnlyWhenItsRowsMove(t *testing.T) {
	st, cp := private(t)
	st.WithRegisterSnapshot(0, time.Second).WithReferenceVersions(5 * time.Minute)
	exec := func(sql string) {
		t.Helper()
		conn, err := cp.Owner(bounded(t))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close(context.Background()) }()
		if _, err := conn.Exec(bounded(t), sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	version := func(fam string) (string, error) {
		t.Helper()
		terms, err := st.Register(bounded(t))
		for _, x := range terms {
			if x.Family == fam {
				return x.Version, err
			}
		}
		return "", err
	}
	var fam, reg string
	ownerOf(t, cp, `select s.source_ref, s.register_id from contract_sources s join source_register g on g.source_id = s.register_id
	                 where g.register_version is not null and s.licence_code !~* '^pending'
	                   and not coalesce((select k.excluded or k.terms_gate is not null from contract_source_rules k where s.source_ref ~ k.pattern order by k.priority limit 1), false)
	                 order by 1 limit 1`, &fam, &reg)
	base, err := version(fam)
	if err != nil || base == "" {
		t.Fatalf("%s: version %q, %v", fam, base, err)
	}
	exec(`update source_register set register_version = register_version + 1 where source_id = '` + reg + `'`)
	moved, err := version(fam)
	if err != nil || moved == base {
		t.Fatalf("a register row changed and the register read %q, still %q (%v)", moved, base, err)
	}
	exec(`revoke select on contract_sources from api_reader`)
	if kept, err := version(fam); err != nil || kept != moved {
		t.Fatalf("with the probe unchanged the register read %q, %v; want the kept %q", kept, err, moved)
	}
	st.WithReferenceVersions(time.Nanosecond)
	if _, err := st.Register(bounded(t)); err == nil {
		t.Fatal("a due register read that cannot read the register succeeded")
	}
	exec(`grant select on contract_sources to api_reader`)
	exec(`revoke select on source_register from api_reader`)
	if _, err := st.Register(bounded(t)); err == nil {
		t.Fatal("a register read whose probe cannot be read succeeded")
	}
}

// An area's punctuality is read from its Land alone, and its rows are the
// platform view's rows of that area: for a Land, a Kreis and the nation,
// with delays planted in two Länder, the bound read and the view agree row
// for row, and the answer carries the view's figures.
func TestAnAreasPunctualityIsItsLandsAlone(t *testing.T) {
	st, cp := private(t,
		`insert into public.transit_delays (observed_at, feed_ts, trip_id, stop_id, stop_sequence, delay_s, schedule_relationship, source)
		 select now() - interval '2 minutes', now() - interval '2 minutes', v.trip, v.stop, 1, v.delay, v.rel, 'delfi_rt_tu'
		 from (values ('p-1', 'de:09162:1', 900, 'SCHEDULED'), ('p-2', 'de:09162:2', 60, 'SCHEDULED'), ('p-3', 'de:09184:1', 400, 'SCHEDULED'),
		              ('p-4', 'de:05315:1', 30, 'SCHEDULED'), ('p-5', 'de:05315:2', null, 'SKIPPED'), ('p-6', 'de:05111:1', 700, 'SCHEDULED')) v(trip, stop, delay, rel)`,
		// A kreisfreie Stadt (its city row) and a Landkreis (its district row):
		// the view names a Kreis by either.
		`insert into public.gazetteer_places (kind, name, name_norm, source_id, external_id, admin_code, geom)
		 values ('city', 'Testmünchen', 'testmuenchen', 'bkg_gvisys', 'p-09162000', '09162000', vf_test.point(null)),
		        ('district', 'Testkreis', 'testkreis', 'bkg_gvisys', 'p-05315', '05315', vf_test.point(null))`)
	conn, err := cp.Owner(bounded(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	for _, area := range []string{"09", "09162", "05", "05315", "de", "11"} {
		tx, err := conn.Begin(bounded(t))
		if err != nil {
			t.Fatal(err)
		}
		q := func(sql string, args ...any) []string {
			rows, err := tx.Query(bounded(t), sql, args...)
			if err != nil {
				t.Fatalf("%s: %v", area, err)
			}
			out, err := pgx.CollectRows(rows, pgx.RowTo[string])
			if err != nil {
				t.Fatal(err)
			}
			return out
		}
		view := q(`select row(v.*)::text from transit_punctuality_area_live v where v.area = $1 order by v.level`, area)
		bound := q(`select row(v.*)::text from (`+store.PunctualityOfArea()+`) v where v.area = $1 order by v.level`, area)
		_ = tx.Rollback(context.Background())
		if strings.Join(view, "\n") != strings.Join(bound, "\n") {
			t.Errorf("%s: the bound read\n%v\nis not the view's\n%v", area, bound, view)
		}
		if area != "11" && len(view) == 0 {
			t.Errorf("%s: the view has no row; the planted delays did not reach it", area)
		}
	}
	d, err := st.Disruptions(bounded(t), transit.DisruptionQuery{Area: "09", Limit: 5, Now: time.Now()}, []string{"delfi_rt_tu"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Punctuality == nil || d.Punctuality.AreaCode != "09" || d.Punctuality.TripsObserved != 3 || d.Punctuality.TripsLate != 2 {
		t.Errorf("punctuality of Bavaria %+v, want 3 trips observed, 2 late", d.Punctuality)
	}
}

// A family with no terms link of its own links its register row's source
// page, as today's credit line does (the FlixBus and FlixTrain parts link
// the dataset's page): in its attribution and in its provenance alike.
func TestAFamilyWithoutATermsLinkLinksItsRegistersSourcePage(t *testing.T) {
	st, cp := private(t)
	var fam, page string
	ownerOf(t, cp, `select c.feed_key, g.source_url from feed_catalog c join contract_sources s on s.source_ref = c.feed_key
	                 join source_register g on g.source_id = s.register_id
	                 where g.source_url is not null and s.licence_code !~* '^pending' and s.publisher_name is not null
	                   and not coalesce((select k.excluded or k.terms_gate is not null from contract_source_rules k where c.feed_key ~ k.pattern order by k.priority limit 1), false)
	                 order by 1 limit 1`, &fam, &page)
	conn, err := cp.Owner(bounded(t))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err := conn.Exec(bounded(t), `update feed_catalog set offer_url = null where feed_key = $1`, fam); err != nil {
		t.Fatal(err)
	}
	src, err := st.Sources(bounded(t), []string{fam})
	if err != nil || len(src) != 1 || src[0].TermsURL == nil || *src[0].TermsURL != page {
		t.Fatalf("%s: sources %+v, %v; want the terms link %s", fam, src, err, page)
	}
	prov, err := st.Provenance(bounded(t), []string{fam}, "de")
	if err != nil || len(prov) != 1 || prov[0].Source.TermsURL == nil || *prov[0].Source.TermsURL != page {
		t.Fatalf("%s: provenance %+v, %v; want the terms link %s", fam, prov, err, page)
	}
}
