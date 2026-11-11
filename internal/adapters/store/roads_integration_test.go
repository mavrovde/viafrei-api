//go:build integration

package store_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// The instants below are read from the fixture rows of road "A 101": the
// start of its situation of kind other, and the start at which most of its
// situations are active; every expectation follows the rows.
func otherSituation(t *testing.T) (id string, at geo.Point, now time.Time) {
	t.Helper()
	owner(t, `select event_id, st_y(location), st_x(location), valid_from from contract.traffic_event
where road_ref = 'A 101' and event_kind = 'other' and withdrawn_at is null order by event_id limit 1`, &id, &at.Lat, &at.Lon, &now)
	return id, at, now
}

const activeOnA101 = `select count(*) from contract.traffic_event e
where e.road_ref = 'A 101' and e.withdrawn_at is null and e.valid_from <= t.valid_from
  and (e.valid_to is null or e.valid_to > t.valid_from)`

func busiest(t *testing.T) (now time.Time, count int) {
	t.Helper()
	owner(t, `select t.valid_from, (`+activeOnA101+`) from contract.traffic_event t where t.road_ref = 'A 101'
order by 2 desc, 1 limit 1`, &now, &count)
	return now, count
}

// countOnA101 counts the situations active on A 101 at now that also match
// the given predicate over e.
func countOnA101(t *testing.T, now time.Time, predicate string) (n int) {
	t.Helper()
	owner(t, `select (`+activeOnA101+` and `+predicate+`) from (select '`+now.UTC().Format(time.RFC3339Nano)+`'::timestamptz valid_from) t`, &n)
	return n
}

// hasSignal says whether the lamp's signals carry the situation's.
func hasSignal(sigs []roads.Signal, s roads.Situation) bool {
	return slices.ContainsFunc(sigs, func(g roads.Signal) bool {
		return g.Kind == s.Kind && g.Severity == s.Severity && (g.DelayMin == nil) == (s.DelayMin == nil) && (g.DelayMin == nil || *g.DelayMin == *s.DelayMin)
	})
}

func ids(sits []roads.Situation) []string {
	out := make([]string, len(sits))
	for i, s := range sits {
		out[i] = s.ID
	}
	return out
}

// planted finds one of the fixture's planted situations.
func planted(sits []roads.Situation, id string) (roads.Situation, bool) {
	i := slices.IndexFunc(sits, func(s roads.Situation) bool { return s.ID == id })
	if i < 0 {
		return roads.Situation{}, false
	}
	return sits[i], true
}

func TestRoadReadsTheCanonicalReference(t *testing.T) {
	req.Covers(t, "FR-ROAD-001")
	st := seeded(t)
	r, err := st.Road(bounded(t), "A101")
	if err != nil || r.Ref != "A101" || r.Class != roads.ClassMotorway || len(r.States) == 0 || r.LengthKm == nil {
		t.Errorf("A101: %+v, %v", r, err)
	}
	r, err = st.Road(bounded(t), "B304")
	if err != nil || r.Ref != "B304" || r.Class != roads.ClassFederal {
		t.Errorf("B304: %+v, %v", r, err)
	}
	if _, err := st.Road(bounded(t), "A999"); !errors.Is(err, roads.ErrUnknownRoad) {
		t.Errorf("an unknown road: %v", err)
	}
}

func TestSituationsOnRoadFollowTheRoad(t *testing.T) {
	req.Covers(t, "FR-ROAD-002", "FR-ROAD-003", "FR-ROAD-004", "ENV-051", "SEC-031")
	st := seeded(t)
	now, count := busiest(t)
	fams := families(t, st, "roads")
	if count < 2 {
		t.Fatalf("the fixture holds %d situations at once on A 101", count)
	}
	q := roads.CorridorQuery{Road: "A101", Direction: roads.DirectionBoth, Filter: roads.Filter{MinSeverity: roads.SeverityInfo}, Limit: 100}
	sits, sigs, more, err := st.SituationsOnRoad(bounded(t), q, fams, now)
	if err != nil || len(sits) == 0 || more {
		t.Fatalf("read %d situations, more %v: %v", len(sits), more, err)
	}
	if len(sits) != count || len(sigs) == 0 {
		t.Errorf("%d situations of %d, signals %v", len(sits), count, sigs)
	}
	// The planted situations, one unmapped code in each coded column, are
	// active over the whole window and answer the info floor, mapped.
	for id, ok := range map[string]func(roads.Situation) bool{
		"ev-u-kind":      func(s roads.Situation) bool { return s.Kind == roads.KindOther },
		"ev-u-severity":  func(s roads.Situation) bool { return s.Severity == roads.SeverityOther },
		"ev-u-direction": func(s roads.Situation) bool { return s.Direction == roads.DirectionUnknown },
		"ev-u-class":     func(s roads.Situation) bool { return s.Class == roads.ClassOther },
	} {
		if s, found := planted(sits, id); !found || !ok(s) {
			t.Errorf("planted %s: %v %+v", id, found, s)
		}
	}
	for i, s := range sits {
		if s.Road == nil || *s.Road != "A101" || s.AtKm == nil || s.DistanceM != nil || !slices.Contains(fams, s.Family) || s.From.After(now) || s.Until != nil && !s.Until.After(now) {
			t.Errorf("situation %+v", s)
		}
		if i > 0 && *s.AtKm < *sits[i-1].AtKm {
			t.Errorf("both directions not in ascending chainage at %d: %v after %v", i, *s.AtKm, *sits[i-1].AtKm)
		}
		if !hasSignal(sigs, s) {
			t.Errorf("the signal of %s missing from the lamp's %v", s.ID, sigs)
		}
	}

	page, all, more, err := st.SituationsOnRoad(bounded(t), roads.CorridorQuery{Road: "A101", Direction: roads.DirectionBoth, Filter: q.Filter, Limit: 1}, fams, now)
	if err != nil || len(page) != 1 || !more || len(all) != len(sigs) {
		t.Errorf("a page of one: %d, more %v, signals %v, %v", len(page), more, all, err)
	}

	// One carriageway: its own, both and unknown (with the unmapped
	// direction), in the order of travel, decided by the line's own ends.
	var risesNorth bool
	owner(t, `select st_y(st_endpoint(st_geometryn(geometry, st_numgeometries(geometry)))) >= st_y(st_startpoint(st_geometryn(geometry, 1))) from contract.road where road_ref = 'A 101'`, &risesNorth)
	north, _, _, err := st.SituationsOnRoad(bounded(t), roads.CorridorQuery{Road: "A101", Direction: roads.DirectionNorth, Filter: q.Filter, Limit: 100}, fams, now)
	if err != nil {
		t.Fatal(err)
	}
	if want := countOnA101(t, now, `(e.direction in ('north', 'both') or not e.direction in ('north', 'south', 'east', 'west'))`); want < 2 || len(north) != want {
		t.Fatalf("northbound answered %d of %d (at least 2 are needed to see an order)", len(north), want)
	}
	if _, found := planted(north, "ev-u-direction"); !found {
		t.Error("northbound left out the unmapped direction")
	}
	for i, s := range north {
		if !slices.Contains([]roads.Direction{roads.DirectionNorth, roads.DirectionBoth, roads.DirectionUnknown}, s.Direction) {
			t.Errorf("northbound answered %s", s.Direction)
		}
		if i > 0 && (risesNorth && *s.AtKm < *north[i-1].AtKm || !risesNorth && *s.AtKm > *north[i-1].AtKm) {
			t.Errorf("northbound not in the order of travel at %d (rises north %v)", i, risesNorth)
		}
	}

	// Filters: kind other (with the unmapped kinds); a severity floor.
	otherID, _, otherAt := otherSituation(t)
	other, _, _, err := st.SituationsOnRoad(bounded(t), roads.CorridorQuery{Road: "A101", Direction: roads.DirectionBoth, Filter: roads.Filter{Kinds: []roads.Kind{roads.KindOther}, MinSeverity: roads.SeverityInfo}, Limit: 100}, fams, otherAt)
	if err != nil || !slices.ContainsFunc(other, func(s roads.Situation) bool { return s.ID == otherID }) {
		t.Errorf("kind other: %+v, %v", other, err)
	}
	for _, s := range other {
		if s.Kind != roads.KindOther {
			t.Errorf("kind other answered %s", s.Kind)
		}
	}
	for _, floor := range []roads.Severity{roads.SeverityMinor, roads.SeverityMajor} {
		above := roads.Ordered()[floor.Rank()-1:]
		in := "e.severity in ('" + string(above[0])
		for _, v := range above[1:] {
			in += "', '" + string(v)
		}
		in += "')"
		var at time.Time
		owner(t, `select t.valid_from from contract.traffic_event t where t.road_ref = 'A 101'
order by (`+activeOnA101+` and `+in+`) desc, 1 limit 1`, &at)
		want := countOnA101(t, at, in)
		got, _, _, err := st.SituationsOnRoad(bounded(t), roads.CorridorQuery{Road: "A101", Direction: roads.DirectionBoth, Filter: roads.Filter{MinSeverity: floor}, Limit: 100}, fams, at)
		if err != nil || want < 2 || len(got) != want {
			t.Fatalf("min %s answered %d of %d (at least 2 are needed), %v", floor, len(got), want, err)
		}
		for _, s := range got {
			if s.Severity.Rank() < floor.Rank() {
				t.Errorf("min %s answered %s", floor, s.Severity)
			}
		}
		if _, found := planted(got, "ev-u-severity"); found {
			t.Errorf("min %s answered the situation that cannot be graded", floor)
		}
	}
	if none, sigs, more, err := st.SituationsOnRoad(bounded(t), q, []string{"no-such-family"}, now); err != nil || len(none) != 0 || sigs != nil || more {
		t.Errorf("no family: %d %v %v %v", len(none), sigs, more, err)
	}
}

func TestChainageRunsOverEveryPartOfTheRoad(t *testing.T) {
	req.Covers(t, "FR-ROAD-003")
	st := seeded(t)
	fams := families(t, st, "roads")
	// A 990 is two straight parts from west to east with a gap; one
	// situation sits in the middle of each.
	var parts int
	var first, second, km float64
	var now time.Time
	owner(t, `select st_numgeometries(geometry), st_length(st_geometryn(geometry, 1)::geography), st_length(st_geometryn(geometry, 2)::geography), length_km::float8,
  (select valid_from from contract.traffic_event where event_id = 'ev-two-1')
from contract.road where road_ref = 'A 990'`, &parts, &first, &second, &km, &now)
	if parts != 2 {
		t.Fatalf("A 990 has %d parts", parts)
	}
	want := map[string]float64{"ev-two-1": km * (first / 2) / (first + second), "ev-two-2": km * (first + second/2) / (first + second)}
	for dir, order := range map[roads.Direction][]string{
		roads.DirectionBoth: {"ev-two-1", "ev-two-2"},
		roads.DirectionEast: {"ev-two-1", "ev-two-2"},
		roads.DirectionWest: {"ev-two-2", "ev-two-1"},
	} {
		sits, _, _, err := st.SituationsOnRoad(bounded(t), roads.CorridorQuery{Road: "A990", Direction: dir, Filter: roads.Filter{MinSeverity: roads.SeverityInfo}, Limit: 100}, fams, now)
		if err != nil || !slices.Equal(ids(sits), order) {
			t.Fatalf("%s: %v, %v", dir, ids(sits), err)
		}
		for _, s := range sits {
			if s.AtKm == nil || math.Abs(*s.AtKm-want[s.ID]) > 0.06 {
				t.Errorf("%s at km %v, want %.2f", s.ID, s.AtKm, want[s.ID])
			}
		}
	}
}

func TestSituationsNearAreBySeverityThenDistance(t *testing.T) {
	req.Covers(t, "FR-ROAD-006", "SEC-031")
	st := seeded(t)
	id, at, now := otherSituation(t)
	fams := families(t, st, "roads")
	// Wide enough that the radius holds several situations at that instant.
	const wide = 1_000_000
	q := roads.AreaQuery{At: at, RadiusM: wide, Filter: roads.Filter{MinSeverity: roads.SeverityInfo}, Limit: 100}
	sits, sigs, _, err := st.SituationsNear(bounded(t), q, fams, now)
	var want int
	owner(t, `select count(*) from contract.traffic_event e
where e.withdrawn_at is null and e.source_ref in (`+quoted(fams)+`) and e.location is not null
  and e.valid_from <= '`+now.UTC().Format(time.RFC3339Nano)+`' and (e.valid_to is null or e.valid_to > '`+now.UTC().Format(time.RFC3339Nano)+`')
  and st_dwithin(e.location::geography, st_setsrid(st_makepoint(`+fmt.Sprint(at.Lon)+`, `+fmt.Sprint(at.Lat)+`), 4326)::geography, `+fmt.Sprint(wide)+`)`, &want)
	if err != nil || want < 2 || len(sits) != want || len(sigs) == 0 {
		t.Fatalf("read %d of %d (at least 2 are needed to see an order): %v", len(sits), want, err)
	}
	found := false
	for i, s := range sits {
		if s.DistanceM == nil || *s.DistanceM > wide || s.AtKm != nil || s.Location == nil {
			t.Errorf("situation %+v", s)
		}
		if s.ID == id {
			found = *s.DistanceM == 0 && s.Kind == roads.KindOther
		}
		if i > 0 {
			p := sits[i-1]
			if s.Severity.Rank() > p.Severity.Rank() || s.Severity == p.Severity && *s.DistanceM < *p.DistanceM {
				t.Errorf("order at %d: %s %d after %s %d", i, s.Severity, *s.DistanceM, p.Severity, *p.DistanceM)
			}
		}
	}
	if !found {
		t.Errorf("the situation at the position is not answered at distance 0")
	}
	// Classes, around the planted situation of an unmapped road class, wide
	// enough to reach situations on motorways.
	cq := roads.AreaQuery{RadiusM: 1_000_000, Filter: q.Filter, Limit: 100}
	owner(t, `select st_y(location), st_x(location) from contract.traffic_event where event_id = 'ev-u-class'`, &cq.At.Lat, &cq.At.Lon)
	busy, _ := busiest(t)
	around, aroundSigs, _, err := st.SituationsNear(bounded(t), cq, fams, busy)
	if err != nil {
		t.Fatal(err)
	}
	var motorway, other []string
	for _, s := range around {
		if s.Class == roads.ClassMotorway {
			motorway = append(motorway, s.ID)
		} else {
			other = append(other, s.ID)
		}
	}
	if len(motorway) == 0 || !slices.Equal(other, []string{"ev-u-class"}) {
		t.Fatalf("around ev-u-class: motorway %v, other %v", motorway, other)
	}
	cq.Classes = []roads.Class{roads.ClassMotorway}
	if mw, _, _, err := st.SituationsNear(bounded(t), cq, fams, busy); err != nil || !slices.Equal(ids(mw), motorway) {
		t.Errorf("motorway class: %v, want %v, %v", ids(mw), motorway, err)
	}
	cq.Classes = []roads.Class{roads.ClassFederal, roads.ClassOther}
	// The lamp reads the radius whatever classes the list keeps.
	if oth, sigs, _, err := st.SituationsNear(bounded(t), cq, fams, busy); err != nil || !slices.Equal(ids(oth), other) || len(sigs) != len(aroundSigs) || oth[0].Class != roads.ClassOther {
		t.Errorf("federal or other class: %v %v, %v", ids(oth), sigs, err)
	}
	q.Classes, q.Limit = nil, 1
	if page, _, more, err := st.SituationsNear(bounded(t), q, fams, now); err != nil || len(page) != 1 || more != (len(sits) > 1) {
		t.Errorf("a page of one: %d, %v, %v", len(page), more, err)
	}
}

// quoted writes a list of families as SQL string literals.
func quoted(fams []string) string {
	out := ""
	for i, f := range fams {
		if i > 0 {
			out += ", "
		}
		out += "'" + f + "'"
	}
	return out
}

// TestTheLampReadsTheScopeNotTheFilters plants, far after the fixture's
// window, a second spelling of road "A 101" ("A101", without the space), a
// minor hazard (a wrong-way driver) stored on that spelling, and a short jam
// on "A 101": the list keeps what the filters keep, the lamp reads both, and
// the two spellings are one road.
func TestTheLampReadsTheScopeNotTheFilters(t *testing.T) {
	req.Covers(t, "FR-ROAD-003", "FR-ROAD-004", "FR-ROAD-006")
	st := seeded(t)
	fams := families(t, st, "roads")
	var spelling string
	owner(t, `insert into contract.road (road_ref, road_class, name, length_km, geometry, states)
select 'A101', road_class, name, length_km, geometry, states from contract.road where road_ref = 'A 101'
returning road_ref`, &spelling)
	t.Cleanup(func() {
		var gone string
		owner(t, `delete from contract.road where road_ref = 'A101' returning road_ref`, &gone)
	})
	var n int
	owner(t, `with ins as (
  insert into contract.traffic_event (event_id, event_kind, severity, road_ref, road_class, direction, direction_label,
    location, valid_from, valid_to, lanes_closed, lanes_total, delay_min, affected_length_m,
    headline, description, text_lang, source_ref, origin_key, published_at, recorded_at, changed_at, withdrawn_at)
  select v.id, v.kind, 'minor', v.ref, 'motorway', 'both', null,
    e.location, '2102-01-01T00:00:00Z', null, null, null, v.delay, null,
    v.headline, null, 'de', e.source_ref, 'o-' || v.id, '2102-01-01T00:00:00Z', '2102-01-01T00:00:00Z', '2102-01-01T00:00:00Z', null
  from contract.traffic_event e,
    (values ('ev-scope-hazard', 'hazard', 'A101', null::int, 'Falschfahrer'), ('ev-scope-jam', 'jam', 'A 101', 5, 'Stau')) v(id, kind, ref, delay, headline)
  where e.event_id = 'ev-u-kind'
  returning 1)
select count(*) from ins`, &n)
	t.Cleanup(func() {
		var gone int
		owner(t, `with del as (delete from contract.traffic_event where event_id like 'ev-scope-%' returning 1) select count(*) from del`, &gone)
	})
	if n != 2 {
		t.Fatalf("planted %d of 2", n)
	}
	at := time.Date(2102, 1, 1, 1, 0, 0, 0, time.UTC)
	// The lamp reads the hazard (red on its own) whatever the list keeps.
	reads := func(sigs []roads.Signal) bool {
		l, _ := roads.LampOf(sigs, false, false)
		return l == roads.LampRed && slices.ContainsFunc(sigs, func(g roads.Signal) bool {
			return g.Kind == roads.KindHazard && g.Severity == roads.SeverityMinor
		})
	}
	both := roads.CorridorQuery{Road: "A101", Direction: roads.DirectionBoth, Filter: roads.Filter{MinSeverity: roads.SeverityInfo}, Limit: 100}
	all, _, _, err := st.SituationsOnRoad(bounded(t), both, fams, at)
	if err != nil || !slices.Contains(ids(all), "ev-scope-hazard") || !slices.Contains(ids(all), "ev-scope-jam") {
		t.Fatalf("A101 and A 101 as one road: %v, %v", ids(all), err)
	}
	both.Kinds = []roads.Kind{roads.KindJam}
	jams, sigs, _, err := st.SituationsOnRoad(bounded(t), both, fams, at)
	if err != nil || !slices.Contains(ids(jams), "ev-scope-jam") || slices.ContainsFunc(jams, func(s roads.Situation) bool { return s.Kind != roads.KindJam }) || !reads(sigs) {
		t.Errorf("kinds=jam: %v, signals %v, %v", ids(jams), sigs, err)
	}
	both.Filter = roads.Filter{MinSeverity: roads.SeverityMajor}
	major, sigs, _, err := st.SituationsOnRoad(bounded(t), both, fams, at)
	if err != nil || slices.Contains(ids(major), "ev-scope-hazard") || !reads(sigs) {
		t.Errorf("min_severity=major: %v, signals %v, %v", ids(major), sigs, err)
	}
	var here geo.Point
	owner(t, `select st_y(location), st_x(location) from contract.traffic_event where event_id = 'ev-scope-hazard'`, &here.Lat, &here.Lon)
	near, sigs, _, err := st.SituationsNear(bounded(t), roads.AreaQuery{At: here, RadiusM: 1_000, Filter: roads.Filter{Kinds: []roads.Kind{roads.KindJam}, MinSeverity: roads.SeverityMajor}, Limit: 100}, fams, at)
	if err != nil || slices.Contains(ids(near), "ev-scope-hazard") || !reads(sigs) {
		t.Errorf("near, jams of major: %v, signals %v, %v", ids(near), sigs, err)
	}
}

func TestSituationReadsOneOfTheGivenFamilies(t *testing.T) {
	req.Covers(t, "FR-ROAD-002", "SEC-031")
	st := seeded(t)
	id, _, _ := otherSituation(t)
	fams := families(t, st, "roads")
	s, err := st.Situation(bounded(t), id, fams, time.Time{})
	if err != nil || s.ID != id || s.Kind != roads.KindOther || s.Road == nil || *s.Road != "A101" || s.AtKm != nil || s.DistanceM != nil {
		t.Errorf("situation %+v, %v", s, err)
	}
	if _, err := st.Situation(bounded(t), id, []string{"no-such-family"}, time.Time{}); !errors.Is(err, roads.ErrUnknownSituation) {
		t.Errorf("another family: %v", err)
	}
	// The situation of an unmapped kind is withdrawn in the fixture.
	var withdrawn string
	owner(t, `select event_id from contract.traffic_event where withdrawn_at is not null order by event_id limit 1`, &withdrawn)
	if _, err := st.Situation(bounded(t), withdrawn, fams, time.Time{}); !errors.Is(err, roads.ErrUnknownSituation) {
		t.Errorf("a withdrawn situation: %v", err)
	}
	// An ended situation answers while it ended after endedSince
	// (roads.ended_retention_h), and not once it ended before.
	var ended string
	var until time.Time
	owner(t, `select event_id, valid_to from contract.traffic_event where valid_to is not null and withdrawn_at is null order by event_id limit 1`, &ended, &until)
	if s, err := st.Situation(bounded(t), ended, fams, until.Add(-time.Second)); err != nil || s.ID != ended || s.Until == nil || !s.Until.Equal(until) {
		t.Errorf("ended within the retention: %+v, %v", s, err)
	}
	if _, err := st.Situation(bounded(t), ended, fams, until); !errors.Is(err, roads.ErrUnknownSituation) {
		t.Errorf("ended before the retention: %v", err)
	}
}

func TestRoadReadsReportStorageFaults(t *testing.T) {
	st := seeded(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := st.Road(ctx, "A101"); err == nil || errors.Is(err, roads.ErrUnknownRoad) {
		t.Errorf("Road on an ended context: %v", err)
	}
	if _, _, _, err := st.SituationsOnRoad(ctx, roads.CorridorQuery{Road: "A101", Limit: 1}, []string{"x"}, time.Now()); err == nil {
		t.Error("SituationsOnRoad on an ended context succeeded")
	}
	if _, _, _, err := st.SituationsNear(ctx, roads.AreaQuery{Limit: 1}, []string{"x"}, time.Now()); err == nil {
		t.Error("SituationsNear on an ended context succeeded")
	}
	if _, err := st.Situation(ctx, "x", []string{"x"}, time.Now()); err == nil || errors.Is(err, roads.ErrUnknownSituation) {
		t.Errorf("Situation on an ended context: %v", err)
	}
	// A page the database refuses (a negative limit) inside the snapshot.
	if _, _, _, err := st.SituationsNear(bounded(t), roads.AreaQuery{Limit: -2}, []string{"x"}, time.Now()); err == nil {
		t.Error("SituationsNear with a limit the database refuses succeeded")
	}
}
