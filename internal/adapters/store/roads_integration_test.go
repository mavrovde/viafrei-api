//go:build integration

package store_test

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/ampel"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/fixtures"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// The instants below are read from the fixture rows of road "A101": the
// start of its situation of kind other, and the start at which most of its
// situations are active; every expectation follows the rows.
func otherSituation(t *testing.T) (id string, at geo.Point, now time.Time) {
	t.Helper()
	owner(t, `select event_id, st_y(location), st_x(location), valid_from from contract.traffic_event
where road_ref = 'A101' and event_kind = 'other' and withdrawn_at is null order by event_id limit 1`, &id, &at.Lat, &at.Lon, &now)
	return id, at, now
}

const activeOnA101 = `select count(*) from contract.traffic_event e
where e.road_ref = 'A101' and e.withdrawn_at is null and e.valid_from <= t.valid_from
  and (e.valid_to is null or e.valid_to > t.valid_from)`

func busiest(t *testing.T) (now time.Time, count int) {
	t.Helper()
	owner(t, `select t.valid_from, (`+activeOnA101+`) from contract.traffic_event t where t.road_ref = 'A101'
order by 2 desc, 1 limit 1`, &now, &count)
	return now, count
}

// countOnA101 counts the situations active on A101 at now that also match
// the given predicate over e.
func countOnA101(t *testing.T, now time.Time, predicate string) (n int) {
	t.Helper()
	owner(t, `select (`+activeOnA101+` and `+predicate+`) from (select '`+now.UTC().Format(time.RFC3339Nano)+`'::timestamptz valid_from) t`, &n)
	return n
}

// hasMessage says whether the lamp's messages carry the situation's.
func hasMessage(msgs []roads.Message, s roads.Situation) bool {
	return slices.ContainsFunc(msgs, func(m roads.Message) bool {
		return m.Kind == s.Kind && m.Headline == s.Headline &&
			(m.Description == nil) == (s.Description == nil) && (m.Description == nil || *m.Description == *s.Description) &&
			(m.DelayMin == nil) == (s.DelayMin == nil) && (m.DelayMin == nil || *m.DelayMin == *s.DelayMin)
	})
}

// noLine is the ask that blocks a situation's chainage and the order of
// travel.
const noLine = "contract.road.geometry and length_km are NULL (0312): no situation is placed on its road (atKm), and a direction cannot be ordered by travel"

// noGrade is the ask that blocks the severity floor and a direction.
const noGrade = "contract.traffic_event.severity is 'other' for every traffic message and every Autobahn row but a closure, and direction is 'unknown' (0312): min_severity above info keeps nothing, and every direction answers every situation"

// onRoads is a read of the roads families alone.
func onRoads(fams []string) roads.Families { return roads.Families{Roads: fams} }

func ids(sits []roads.Situation) []string {
	out := make([]string, len(sits))
	for i, s := range sits {
		out[i] = s.ID
	}
	return out
}

// planted finds one of the fixture's planted situations by its name.
func planted(sits []roads.Situation, name string) (roads.Situation, bool) {
	i := slices.IndexFunc(sits, func(s roads.Situation) bool { return s.ID == fixtures.PlantedEvent(name) })
	if i < 0 {
		return roads.Situation{}, false
	}
	return sits[i], true
}

func TestRoadReadsTheCanonicalReference(t *testing.T) {
	req.Covers(t, "FR-ROAD-001")
	st := seeded(t)
	r, err := st.Road(bounded(t), "A101")
	if err != nil || r.Ref != "A101" || r.Class != roads.ClassMotorway {
		t.Errorf("A101: %+v, %v", r, err)
	}
	if _, err := st.Road(bounded(t), "A999"); !errors.Is(err, roads.ErrUnknownRoad) {
		t.Errorf("an unknown road: %v", err)
	}
	if len(r.States) == 0 || r.LengthKm == nil {
		dbAsk(t, "contract.road.states and length_km are NULL (0312): a road answers no length and no states")
	}
	r, err = st.Road(bounded(t), "B304")
	if errors.Is(err, roads.ErrUnknownRoad) {
		dbAsk(t, "contract.road holds the motorways only (0312): a federal road (B304) is unknown")
	}
	if err != nil || r.Ref != "B304" || r.Class != roads.ClassFederal {
		t.Errorf("B304: %+v, %v", r, err)
	}
}

// statusTier is a kind's tier in the platform's motorway status order.
func statusTier(k roads.Kind) int {
	switch k {
	case roads.KindJam:
		return 0
	case roads.KindClosure:
		return 1
	case roads.KindRoadworks:
		return 3
	}
	return 2
}

func TestSituationsOnRoadFollowTheRoad(t *testing.T) {
	req.Covers(t, "FR-ROAD-002", "FR-ROAD-003", "FR-ROAD-004", "ENV-051", "SEC-031")
	st := seeded(t)
	now, count := busiest(t)
	fams := families(t, st, "roads")
	if count < 2 {
		t.Fatalf("the fixture holds %d situations at once on A101", count)
	}
	q := roads.CorridorQuery{Road: "A101", Direction: roads.DirectionBoth, Filter: roads.Filter{MinSeverity: roads.SeverityInfo}, Limit: 100}
	weather := families(t, st, "weather")
	p, err := st.SituationsOnRoad(bounded(t), q, roads.Families{Roads: fams, Weather: weather}, now)
	sits, msgs, more := p.Items, p.Messages, p.HasMore
	if err != nil || len(sits) == 0 || more {
		t.Fatalf("read %d situations, more %v: %v", len(sits), more, err)
	}
	if len(sits) != count || len(msgs) == 0 {
		t.Errorf("%d situations of %d, messages %v", len(sits), count, msgs)
	}
	// The freshness of every family read, from the same snapshot.
	var read []string
	for _, f := range p.Sources {
		read = append(read, f.Source.Family)
	}
	if want := append(slices.Clone(fams), weather...); len(weather) == 0 || !slices.Equal(read, slices.Sorted(slices.Values(want))) {
		t.Errorf("sources %v, want %v", read, want)
	}
	if p.Warnings == nil {
		t.Error("the warnings of a weather family were not read")
	}
	// The planted situation of an event type the service does not name is
	// active over the whole window and answers the info floor as other.
	if s, found := planted(sits, "ev-u-kind"); !found || s.Kind != roads.KindOther {
		t.Errorf("planted ev-u-kind: %v %+v", found, s)
	}
	for _, s := range sits {
		if s.Road == nil || *s.Road != "A101" || s.DistanceM != nil || !slices.Contains(fams, s.Family) || s.From.After(now) || s.Until != nil && !s.Until.After(now) {
			t.Errorf("situation %+v", s)
		}
		if !hasMessage(msgs, s) {
			t.Errorf("the message of %s missing from the lamp's %v", s.ID, msgs)
		}
	}

	one, err := st.SituationsOnRoad(bounded(t), roads.CorridorQuery{Road: "A101", Direction: roads.DirectionBoth, Filter: q.Filter, Limit: 1}, onRoads(fams), now)
	if err != nil || len(one.Items) != 1 || !one.HasMore || len(one.Messages) != len(msgs) || one.Warnings != nil {
		t.Errorf("a page of one: %d, more %v, messages %v, warnings %v, %v", len(one.Items), one.HasMore, one.Messages, one.Warnings, err)
	}

	// Filters: kind other (with the unmapped kinds).
	otherID, _, otherAt := otherSituation(t)
	op, err := st.SituationsOnRoad(bounded(t), roads.CorridorQuery{Road: "A101", Direction: roads.DirectionBoth, Filter: roads.Filter{Kinds: []roads.Kind{roads.KindOther}, MinSeverity: roads.SeverityInfo}, Limit: 100}, onRoads(fams), otherAt)
	other := op.Items
	if err != nil || !slices.ContainsFunc(other, func(s roads.Situation) bool { return s.ID == otherID }) {
		t.Errorf("kind other: %+v, %v", other, err)
	}
	for _, s := range other {
		if s.Kind != roads.KindOther {
			t.Errorf("kind other answered %s", s.Kind)
		}
	}
	if none, err := st.SituationsOnRoad(bounded(t), q, onRoads([]string{"no-such-family"}), now); err != nil || len(none.Items) != 0 || none.Messages != nil || none.HasMore || len(none.Sources) != 0 {
		t.Errorf("no family: %+v %v", none, err)
	}

	// The platform's motorway status order: jams, closures, the other
	// reports, then roadworks; every situation still has its chainage.
	for i, s := range sits {
		if s.AtKm == nil {
			dbAsk(t, noLine)
		}
		if i > 0 && statusTier(s.Kind) < statusTier(sits[i-1].Kind) {
			t.Errorf("not in the status order at %d: %s after %s", i, s.Kind, sits[i-1].Kind)
		}
	}
	// One carriageway: its own, both and unknown.
	if countOnA101(t, now, `e.direction <> 'unknown'`) == 0 {
		dbAsk(t, noGrade)
	}
	np, err := st.SituationsOnRoad(bounded(t), roads.CorridorQuery{Road: "A101", Direction: roads.DirectionNorth, Filter: q.Filter, Limit: 100}, onRoads(fams), now)
	north := np.Items
	if err != nil {
		t.Fatal(err)
	}
	if want := countOnA101(t, now, `(e.direction in ('north', 'both') or not e.direction in ('north', 'south', 'east', 'west'))`); want < 2 || len(north) != want {
		t.Fatalf("northbound answered %d of %d (at least 2 are needed to see an order)", len(north), want)
	}
	for i, s := range north {
		if !slices.Contains([]roads.Direction{roads.DirectionNorth, roads.DirectionBoth, roads.DirectionUnknown}, s.Direction) {
			t.Errorf("northbound answered %s", s.Direction)
		}
		if i > 0 && statusTier(s.Kind) < statusTier(north[i-1].Kind) {
			t.Errorf("northbound not in the status order at %d", i)
		}
	}

	// A severity floor.
	for _, floor := range []roads.Severity{roads.SeverityMinor, roads.SeverityMajor} {
		above := roads.Ordered()[floor.Rank()-1:]
		in := "e.severity in ('" + string(above[0])
		for _, v := range above[1:] {
			in += "', '" + string(v)
		}
		in += "')"
		var at time.Time
		owner(t, `select t.valid_from from contract.traffic_event t where t.road_ref = 'A101'
order by (`+activeOnA101+` and `+in+`) desc, 1 limit 1`, &at)
		want := countOnA101(t, at, in)
		gp, err := st.SituationsOnRoad(bounded(t), roads.CorridorQuery{Road: "A101", Direction: roads.DirectionBoth, Filter: roads.Filter{MinSeverity: floor}, Limit: 100}, onRoads(fams), at)
		got := gp.Items
		if err != nil || want < 2 || len(got) != want {
			t.Fatalf("min %s answered %d of %d (at least 2 are needed), %v", floor, len(got), want, err)
		}
		for _, s := range got {
			if s.Severity.Rank() < floor.Rank() {
				t.Errorf("min %s answered %s", floor, s.Severity)
			}
		}
		if _, found := planted(got, "ev-u-kind"); found {
			t.Errorf("min %s answered the situation that cannot be graded", floor)
		}
	}
}

func TestChainageRunsOverEveryPartOfTheRoad(t *testing.T) {
	req.Covers(t, "FR-ROAD-003")
	st := seeded(t)
	fams := families(t, st, "roads")
	// A990 is two straight parts from west to east with a gap; one
	// situation sits in the middle of each.
	var line bool
	owner(t, `select geometry is not null from contract.road where road_ref = '`+fixtures.TwoPartRef+`'`, &line)
	if !line {
		dbAsk(t, noLine)
	}
	var parts int
	var first, second, km float64
	var now time.Time
	owner(t, `select st_numgeometries(geometry), st_length(st_geometryn(geometry, 1)::geography), st_length(st_geometryn(geometry, 2)::geography), length_km::float8,
  (select valid_from from contract.traffic_event where event_id = '`+fixtures.PlantedEvent("ev-two-1")+`')
from contract.road where road_ref = '`+fixtures.TwoPartRef+`'`, &parts, &first, &second, &km, &now)
	if parts != 2 {
		t.Fatalf("A990 has %d parts", parts)
	}
	one, two := fixtures.PlantedEvent("ev-two-1"), fixtures.PlantedEvent("ev-two-2")
	want := map[string]float64{one: km * (first / 2) / (first + second), two: km * (first + second/2) / (first + second)}
	for dir, order := range map[roads.Direction][]string{
		roads.DirectionBoth: {one, two},
		roads.DirectionEast: {one, two},
		roads.DirectionWest: {two, one},
	} {
		p, err := st.SituationsOnRoad(bounded(t), roads.CorridorQuery{Road: "A990", Direction: dir, Filter: roads.Filter{MinSeverity: roads.SeverityInfo}, Limit: 100}, onRoads(fams), now)
		sits := p.Items
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
	np, err := st.SituationsNear(bounded(t), q, onRoads(fams), now)
	sits := np.Items
	var want int
	owner(t, `select count(*) from contract.traffic_event e
where e.withdrawn_at is null and e.source_ref in (`+quoted(fams)+`) and e.location is not null
  and e.valid_from <= '`+now.UTC().Format(time.RFC3339Nano)+`' and (e.valid_to is null or e.valid_to > '`+now.UTC().Format(time.RFC3339Nano)+`')
  and st_dwithin(e.location::geography, st_setsrid(st_makepoint(`+fmt.Sprint(at.Lon)+`, `+fmt.Sprint(at.Lat)+`), 4326)::geography, `+fmt.Sprint(wide)+`)`, &want)
	if err != nil || want < 2 || len(sits) != want || len(np.Messages) == 0 {
		t.Fatalf("read %d of %d (at least 2 are needed to see an order): %v", len(sits), want, err)
	}
	if np.Placed != nil {
		t.Errorf("an area that is no Land credits no placement: %v", np.Placed)
	}
	// A Land's answer credits the family that placed its situations there.
	var land string
	owner(t, `select state_code from contract.traffic_event e where e.withdrawn_at is null and e.state_code is not null
  and e.source_ref in (`+quoted(fams)+`) order by state_code limit 1`, &land)
	if lp, err := st.SituationsNear(bounded(t), roads.AreaQuery{State: land, Filter: q.Filter, Limit: 100}, onRoads(fams), now); err != nil ||
		!slices.Equal(lp.Placed, []string{"bkg_gvisys"}) {
		t.Errorf("Land %s placed by %v: %v", land, lp.Placed, err)
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
	unclassed := fixtures.PlantedEvent("ev-u-class")
	owner(t, `select st_y(location), st_x(location) from contract.traffic_event where event_id = '`+unclassed+`'`, &cq.At.Lat, &cq.At.Lon)
	busy, _ := busiest(t)
	ap, err := st.SituationsNear(bounded(t), cq, onRoads(fams), busy)
	around := ap.Items
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
	if len(motorway) == 0 || !slices.Equal(other, []string{unclassed}) {
		t.Fatalf("around %s: motorway %v, other %v", unclassed, motorway, other)
	}
	cq.Classes = []roads.Class{roads.ClassMotorway}
	if mw, err := st.SituationsNear(bounded(t), cq, onRoads(fams), busy); err != nil || !slices.Equal(ids(mw.Items), motorway) {
		t.Errorf("motorway class: %v, want %v, %v", ids(mw.Items), motorway, err)
	}
	cq.Classes = []roads.Class{roads.ClassFederal, roads.ClassOther}
	// The lamp reads the radius whatever classes the list keeps.
	if oth, err := st.SituationsNear(bounded(t), cq, onRoads(fams), busy); err != nil || !slices.Equal(ids(oth.Items), other) || len(oth.Messages) != len(ap.Messages) || oth.Items[0].Class != roads.ClassOther {
		t.Errorf("federal or other class: %v %v, %v", ids(oth.Items), oth.Messages, err)
	}
	q.Classes, q.Limit = nil, 1
	if page, err := st.SituationsNear(bounded(t), q, onRoads(fams), now); err != nil || len(page.Items) != 1 || page.HasMore != (len(sits) > 1) {
		t.Errorf("a page of one: %d, %v, %v", len(page.Items), page.HasMore, err)
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
// window, a hazard (a vehicle on the wrong carriageway) a traffic message
// stores on road "A 101" (with a space) and a jam on "A101": the list keeps
// what the filters keep, the lamp reads both, and the two spellings are one
// road.
func TestTheLampReadsTheScopeNotTheFilters(t *testing.T) {
	req.Covers(t, "FR-ROAD-003", "FR-ROAD-004", "FR-ROAD-006")
	st := seeded(t)
	fams := families(t, st, "roads")
	seedChanges(t, "traffic_event.event_id")
	t.Cleanup(func() {
		change(t, `delete from contract.traffic_event where event_id like $1`, fixtures.PlantedEvent("ev-scope-%"))
	})
	var n int
	owner(t, `with ins as (
  insert into contract.traffic_event (event_id, event_kind, road_ref, road_class, location, valid_from, headline, source_ref,
    origin_key, published_at, recorded_at, changed_at)
  select '`+fixtures.PlantedEvent("")+`' || v.id, v.kind, v.ref, 'motorway', e.location, '2102-01-01T00:00:00Z', v.headline, e.source_ref,
    v.id, '2102-01-01T00:00:00Z', '2102-01-01T00:00:00Z', '2102-01-01T00:00:00Z'
  from contract.traffic_event e,
    (values ('ev-scope-hazard', 'hazard', 'A 101', 'VehicleOnWrongCarriageway'), ('ev-scope-jam', 'jam', 'A101', 'AbnormalTraffic')) v(id, kind, ref, headline)
  where e.event_id = '`+fixtures.PlantedEvent("ev-u-kind")+`'
  returning 1)
select count(*) from ins`, &n)
	if n != 2 {
		t.Fatalf("planted %d of 2", n)
	}
	hazard, jam := fixtures.PlantedEvent("ev-scope-hazard"), fixtures.PlantedEvent("ev-scope-jam")
	at := time.Date(2102, 1, 1, 1, 0, 0, 0, time.UTC)
	// The lamp reads the hazard whatever the list keeps.
	reads := func(msgs []roads.Message) bool {
		return slices.ContainsFunc(msgs, func(m roads.Message) bool {
			return m.Kind == roads.KindHazard && m.Headline == "VehicleOnWrongCarriageway"
		})
	}
	both := roads.CorridorQuery{Road: "A101", Direction: roads.DirectionBoth, Filter: roads.Filter{MinSeverity: roads.SeverityInfo}, Limit: 100}
	all, err := st.SituationsOnRoad(bounded(t), both, onRoads(fams), at)
	if err != nil || !slices.Contains(ids(all.Items), hazard) || !slices.Contains(ids(all.Items), jam) {
		t.Fatalf("A101 and A 101 as one road: %v, %v", ids(all.Items), err)
	}
	both.Kinds = []roads.Kind{roads.KindJam}
	jams, err := st.SituationsOnRoad(bounded(t), both, onRoads(fams), at)
	if err != nil || !slices.Contains(ids(jams.Items), jam) || slices.ContainsFunc(jams.Items, func(s roads.Situation) bool { return s.Kind != roads.KindJam }) || !reads(jams.Messages) {
		t.Errorf("kinds=jam: %v, messages %v, %v", ids(jams.Items), jams.Messages, err)
	}
	both.Filter = roads.Filter{MinSeverity: roads.SeverityMajor}
	major, err := st.SituationsOnRoad(bounded(t), both, onRoads(fams), at)
	if err != nil || slices.Contains(ids(major.Items), hazard) || !reads(major.Messages) {
		t.Errorf("min_severity=major: %v, messages %v, %v", ids(major.Items), major.Messages, err)
	}
	var here geo.Point
	owner(t, `select st_y(location), st_x(location) from contract.traffic_event where event_id = '`+hazard+`'`, &here.Lat, &here.Lon)
	near, err := st.SituationsNear(bounded(t), roads.AreaQuery{At: here, RadiusM: 1_000, Filter: roads.Filter{Kinds: []roads.Kind{roads.KindJam}, MinSeverity: roads.SeverityMajor}, Limit: 100}, onRoads(fams), at)
	if err != nil || slices.Contains(ids(near.Items), hazard) || !reads(near.Messages) {
		t.Errorf("near, jams of major: %v, messages %v, %v", ids(near.Items), near.Messages, err)
	}
	// The lamp's safety words are German message text; a traffic message's
	// headline is its event type.
	if l, r := roads.LampOf(all.Messages, nil, nil); l != roads.LampRed || r == nil || *r != roads.ReasonSafety {
		dbAsk(t, "contract.traffic_event.headline of a traffic message is its event type and description is NULL (0312): the lamp's safety words (Falschfahrer, Person, Tier) never match a Land or city traffic message")
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
	// A withdrawn situation is not answered.
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
	if _, err := st.SituationsOnRoad(ctx, roads.CorridorQuery{Road: "A101", Limit: 1}, onRoads([]string{"x"}), time.Now()); err == nil {
		t.Error("SituationsOnRoad on an ended context succeeded")
	}
	if _, err := st.SituationsNear(ctx, roads.AreaQuery{Limit: 1}, onRoads([]string{"x"}), time.Now()); err == nil {
		t.Error("SituationsNear on an ended context succeeded")
	}
	if _, err := st.Situation(ctx, "x", []string{"x"}, time.Now()); err == nil || errors.Is(err, roads.ErrUnknownSituation) {
		t.Errorf("Situation on an ended context: %v", err)
	}
	// A page the database refuses (a negative limit) inside the snapshot.
	if _, err := st.SituationsNear(bounded(t), roads.AreaQuery{Limit: -2}, onRoads([]string{"x"}), time.Now()); err == nil {
		t.Error("SituationsNear with a limit the database refuses succeeded")
	}
}

// TestTheLampReadsTheWeatherWarningsOfTheScope plants, far after the fixture's
// window, weather warnings in a district that road "A101" crosses and in the
// district furthest from it, and one each that must not be read (cancelled,
// withdrawn, ended, of a family not asked for): the corridor reads the
// warnings of an area the road's line crosses, the radius those of an area
// within it, both in the snapshot of the page.
func TestTheLampReadsTheWeatherWarningsOfTheScope(t *testing.T) {
	req.Covers(t, "FR-ROAD-004", "FR-ROAD-006")
	dbAsk(t, noBoundary+"; "+noLine)
}

// TestAReadIsOneSnapshot proves the page and the reads of the lamp's scope
// see one snapshot: a row committed by another session between two
// statements of one read is not seen by the second (repeatable read).
func TestAReadIsOneSnapshot(t *testing.T) {
	req.Covers(t, "FR-ROAD-004")
	st := seeded(t)
	ctx := bounded(t)
	var iso string
	var before, after int
	err := st.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, "show transaction_isolation").Scan(&iso); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, "select count(*) from contract.rule_topic").Scan(&before); err != nil {
			return err
		}
		// Another session commits a row between the two statements.
		conn, err := db.Owner(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
		if _, err := conn.Exec(ctx, `insert into contract.rule_topic (topic_code, title, title_en, reviewed_on) values ('snapshot_probe', 'Probe', 'Probe', '2026-01-01')`); err != nil {
			return err
		}
		return tx.QueryRow(ctx, "select count(*) from contract.rule_topic").Scan(&after)
	})
	t.Cleanup(func() { change(t, `delete from contract.rule_topic where topic_code = 'snapshot_probe'`) })
	if err != nil || iso != "repeatable read" || before == 0 || after != before {
		t.Errorf("isolation %q, %d then %d rows, %v", iso, before, after, err)
	}
}

// TestTheLampReadsItsRecordsAndItsSight plants, far after the fixture's
// window, an operator event (an Autobahn row) and a traffic message on road
// "A101" with the fields the lamp reads, state codes on every
// junction (each junction's state is its nearest municipality's), coverage
// rows and a confirmation: the page carries each record as written, the
// states of the road's junctions (or of the junctions within the radius,
// else of the nearest), the coverage of the roads families read only, and
// the newest confirmation of every family read that has a freshness row.
func TestTheLampReadsItsRecordsAndItsSight(t *testing.T) {
	req.Covers(t, "FR-ROAD-004", "FR-ROAD-006")
	st := seeded(t)
	fams := []string{"roads_alpha", "roads_alpha_stale", "roads_epsilon"}
	weather := []string{"dwd_alpha", "dwd_alpha_stale", "dwd_eta"}
	seedChanges(t, "junction.state_code", "message_coverage.current_only", "source_freshness.confirmed_at", "source_freshness.confirm_interval_s", "source_freshness.updates_paused", "traffic_event.event_id")
	var saved string
	owner(t, `select coalesce(string_agg(junction_id || '=' || coalesce(state_code, ''), ',' order by junction_id), '') from contract.junction`, &saved)
	t.Cleanup(func() {
		for _, kv := range strings.Split(saved, ",") {
			id, code, _ := strings.Cut(kv, "=")
			change(t, `update contract.junction set state_code = nullif($2, '') where junction_id = $1`, id, code)
		}
	})
	change(t, `update contract.junction set state_code = case when road_ref <> 'A101' then '97'
  when right(junction_id, 1) in ('0', '2', '4', '6', '8') then '98' else '99' end`)
	var n int
	owner(t, `with ins as (insert into contract.message_coverage (state_code, source_ref, coverage_role, current_only)
values ('98', '`+fams[0]+`', 'own', true), ('98', '`+fams[1]+`', 'carried', false), ('98', '`+weather[0]+`', 'carried', false)
returning 1) select count(*) from ins`, &n)
	t.Cleanup(func() { change(t, `delete from contract.message_coverage where state_code = '98'`) })
	confirmed := time.Date(2102, 1, 1, 0, 30, 0, 0, time.UTC)
	var held struct {
		at       *time.Time
		interval *int64
		paused   bool
	}
	owner(t, `select confirmed_at, confirm_interval_s::bigint, updates_paused from contract.source_freshness where source_ref = '`+fams[0]+`'`, &held.at, &held.interval, &held.paused)
	t.Cleanup(func() {
		change(t, `update contract.source_freshness set confirmed_at = $2, confirm_interval_s = $3, updates_paused = $4 where source_ref = $1`, fams[0], held.at, held.interval, held.paused)
	})
	if change(t, `update contract.source_freshness set confirmed_at = '2102-01-01T00:30:00Z', confirm_interval_s = 300, updates_paused = true where source_ref = $1`, fams[0]) != 1 {
		t.Fatalf("%s has no freshness row", fams[0])
	}
	message := fixtures.PlantedEvent("ev-lamp-message")
	t.Cleanup(func() {
		change(t, `delete from contract.traffic_event where event_id in ('autobahn:ev-lamp-operator', $1)`, message)
	})
	owner(t, `with ins as (
  insert into contract.traffic_event (event_id, event_kind, road_ref, road_class, location, valid_from,
    headline, description, source_ref, origin_key, published_at, recorded_at, changed_at, subtitle, is_blocked, traffic_type, delay_s)
  select v.id, v.kind, 'A101', 'motorway', e.location, '2102-01-01T00:00:00Z',
    v.headline, v.description, coalesce(v.family, e.source_ref), v.origin,
    case when v.family is null then '2102-01-01T00:00:00Z'::timestamptz end, '2102-01-01T00:00:00Z', '2102-01-01T00:00:00Z',
    v.subtitle, v.blocked, v.traffic, v.delay_s
  from contract.traffic_event e,
    (values ('autobahn:ev-lamp-operator', 'jam', 'Stau', E'Zeile 1\nZeile 2', 'autobahn', 'ev-lamp-operator', 'AS Nord', true, 'stationary', 1800),
            ('`+message+`', 'roadworks', 'MaintenanceWorks', null, null, 'ev-lamp-message', null, null, null, null))
    v(id, kind, headline, description, family, origin, subtitle, blocked, traffic, delay_s)
  where e.event_id = '`+fixtures.PlantedEvent("ev-u-kind")+`'
  returning 1)
select count(*) from ins`, &n)
	if n != 2 {
		t.Fatalf("planted %d of 2", n)
	}
	// The operator event is the Autobahn family's.
	asked := roads.Families{Roads: slices.Clone(fams), Weather: weather}
	if !slices.Contains(asked.Roads, "autobahn") {
		asked.Roads = append(asked.Roads, "autobahn")
	}
	var confirmations []string
	owner(t, `select coalesce(array_agg(source_ref order by source_ref), '{}') from contract.source_freshness
where source_ref = any('{`+strings.Join(append(slices.Clone(asked.Roads), weather...), ",")+`}')`, &confirmations)
	at := time.Date(2102, 1, 1, 1, 0, 0, 0, time.UTC)
	p, err := st.SituationsOnRoad(bounded(t), roads.CorridorQuery{Road: "A101", Direction: roads.DirectionBoth, Filter: roads.Filter{MinSeverity: roads.SeverityInfo}, Limit: 100}, asked, at)
	if err != nil {
		t.Fatal(err)
	}
	lines, delayS, sub, traffic := "Zeile 1\nZeile 2", 1800, "AS Nord", "stationary"
	mins := 30
	for _, want := range []roads.Message{
		{Record: roads.RecordOperatorEvent, Kind: roads.KindJam, Headline: "Stau", Subtitle: &sub, Description: &lines, Blocked: true, TrafficType: &traffic, DelayS: &delayS, DelayMin: &mins},
		{Record: roads.RecordTrafficMessage, Kind: roads.KindRoadworks, Headline: "MaintenanceWorks"},
	} {
		if !slices.ContainsFunc(p.Messages, func(m roads.Message) bool { return reflect.DeepEqual(m, want) }) {
			t.Errorf("the record %s is not read as written: %+v", want.Headline, p.Messages)
		}
	}
	if !slices.Equal(p.States, []string{"98", "99"}) {
		t.Errorf("the corridor's states %v", p.States)
	}
	if want := (map[string]ampel.Coverage{"98": {Own: []string{fams[0]}, Carried: []string{fams[1]}, CurrentOnly: []string{fams[0]}}}); !reflect.DeepEqual(p.Coverage, want) {
		t.Errorf("coverage %+v, want %+v", p.Coverage, want)
	}
	if got := slices.Sorted(maps.Keys(p.Confirmations)); !slices.Equal(got, confirmations) {
		t.Errorf("confirmations of %v, want those of the families with a freshness row %v", got, confirmations)
	}
	if c := p.Confirmations[fams[0]]; c.At == nil || !c.At.Equal(confirmed) || c.IntervalS == nil || *c.IntervalS != 300 || !c.Paused {
		t.Errorf("the confirmation of %s: %+v", fams[0], c)
	}
	var other struct {
		at       *time.Time
		interval *int64
	}
	owner(t, `select confirmed_at, confirm_interval_s::bigint from contract.source_freshness where source_ref = '`+fams[1]+`'`, &other.at, &other.interval)
	if c := p.Confirmations[fams[1]]; (c.At == nil) != (other.at == nil) || c.At != nil && !c.At.Equal(*other.at) || fmt.Sprint(deref(c.IntervalS)) != fmt.Sprint(deref(other.interval)) || c.Paused {
		t.Errorf("the confirmation of %s: %+v, want %v %v", fams[1], c, other.at, other.interval)
	}
	// The radius: the junctions within it; with none, the nearest junction.
	var jn geo.Point
	var jstate string
	owner(t, `select st_y(location), st_x(location), state_code from contract.junction where road_ref = 'A101' order by junction_id limit 1`, &jn.Lat, &jn.Lon, &jstate)
	area := func(here geo.Point, radius int) []string {
		t.Helper()
		ap, err := st.SituationsNear(bounded(t), roads.AreaQuery{At: here, RadiusM: radius, Filter: roads.Filter{MinSeverity: roads.SeverityInfo}, Limit: 1}, onRoads(fams), at)
		if err != nil {
			t.Fatal(err)
		}
		return ap.States
	}
	if got := area(jn, 1); !slices.Equal(got, []string{jstate}) {
		t.Errorf("a junction within the radius: %v, want %s", got, jstate)
	}
	if got := area(geo.Point{Lat: jn.Lat + 0.0002, Lon: jn.Lon}, 1); !slices.Equal(got, []string{jstate}) {
		t.Errorf("no junction within the radius, the nearest: %v, want %s", got, jstate)
	}
	if got := area(jn, 2_000_000); !slices.Equal(got, []string{"97", "98", "99"}) {
		t.Errorf("every junction within the radius: %v", got)
	}
	dbAsk(t, "contract.traffic_event never holds a bridge_state record and a traffic message's place_text is NULL (0312): the lamp reads no bridge state")
}

// TestTheLampReadsWhenItsScopeNextChanges plants, far after the fixture's
// window, situations on road "A101" and warnings in and out of its scope:
// the page's next change is the earliest start or end after the read among
// the scope's situations that are not withdrawn and, when weather families
// are read, its warnings; one that starts later is not yet a message.
func TestTheLampReadsWhenItsScopeNextChanges(t *testing.T) {
	req.Covers(t, "FR-ROAD-004", "NFR-PERF-005")
	st := seeded(t)
	fams := families(t, st, "roads")
	seedChanges(t, "traffic_event.event_id")
	var n int
	t.Cleanup(func() {
		change(t, `delete from contract.traffic_event where event_id like $1`, fixtures.PlantedEvent("ev-next-%"))
	})
	owner(t, `with ins as (
  insert into contract.traffic_event (event_id, event_kind, road_ref, road_class, location, valid_from, valid_to,
    headline, source_ref, origin_key, published_at, recorded_at, changed_at, withdrawn_at)
  select '`+fixtures.PlantedEvent("")+`' || v.id, 'jam', 'A101', 'motorway', e.location, v.since::timestamptz, v.until::timestamptz,
    v.headline, e.source_ref, v.id, '2102-01-01T00:00:00Z', '2102-01-01T00:00:00Z', '2102-01-01T00:00:00Z', v.withdrawn::timestamptz
  from contract.traffic_event e,
    (values ('ev-next-now', 'AbnormalTrafficNow', '2102-01-01T00:00:00Z', '2102-01-01T02:00:00Z', null),
            ('ev-next-later', 'AbnormalTrafficLater', '2102-01-01T01:30:00Z', null, null),
            ('ev-next-withdrawn', 'AbnormalTrafficWithdrawn', '2102-01-01T00:00:00Z', '2102-01-01T01:05:00Z', '2102-01-01T00:30:00Z'))
    v(id, headline, since, until, withdrawn)
  where e.event_id = '`+fixtures.PlantedEvent("ev-u-kind")+`'
  returning 1)
select count(*) from ins`, &n)
	if n != 3 {
		t.Fatalf("planted %d of 3", n)
	}
	at := time.Date(2102, 1, 1, 1, 0, 0, 0, time.UTC)
	q := roads.CorridorQuery{Road: "A101", Direction: roads.DirectionBoth, Filter: roads.Filter{MinSeverity: roads.SeverityInfo}, Limit: 100}
	for _, c := range []struct {
		name string
		at   time.Time
		want time.Time
	}{
		{"a situation starts", at, at.Add(30 * time.Minute)},
		{"a situation ends", at.Add(45 * time.Minute), at.Add(time.Hour)},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, err := st.SituationsOnRoad(bounded(t), q, onRoads(fams), c.at)
			if err != nil || p.NextChange == nil || !p.NextChange.Equal(c.want) {
				t.Fatalf("next change %v, want %s, %v", p.NextChange, c.want, err)
			}
			later := slices.ContainsFunc(p.Messages, func(m roads.Message) bool { return m.Headline == "AbnormalTrafficLater" })
			if later != c.at.After(at) {
				t.Errorf("the later situation is a message: %v", later)
			}
		})
	}
	t.Run("nothing starts or ends after the last end", func(t *testing.T) {
		if p, err := st.SituationsOnRoad(bounded(t), q, onRoads(fams), at.Add(2*time.Hour)); err != nil || p.NextChange != nil {
			t.Errorf("next change %v, %v", p.NextChange, err)
		}
	})
	t.Run("a warning in scope starts", func(t *testing.T) { dbAsk(t, noBoundary) })
}

func TestSituationsOnRoadOverAWindowAndAPage(t *testing.T) {
	req.Covers(t, "FR-ROAD-001", "FR-ROAD-002")
	st := seeded(t)
	now, count := busiest(t)
	fams := roads.Families{Roads: families(t, st, "roads")}
	q := roads.CorridorQuery{Road: "A101", Direction: roads.DirectionBoth, Filter: roads.Filter{MinSeverity: roads.SeverityInfo}, Limit: 100}
	wide := q
	wide.From, wide.Until = now.AddDate(-1, 0, 0), now.AddDate(1, 0, 0)
	all, err := st.SituationsOnRoad(bounded(t), wide, fams, now)
	if err != nil || len(all.Items) <= count {
		t.Fatalf("a two-year window read %d situations, %d in force: %v", len(all.Items), count, err)
	}
	paged := wide
	paged.Limit, paged.Offset = 1, 1
	p, err := st.SituationsOnRoad(bounded(t), paged, fams, now)
	if err != nil || len(p.Items) != 1 || p.Items[0].ID != all.Items[1].ID || !p.HasMore {
		t.Errorf("the second page of one: %+v, %v", p.Items, err)
	}
	var near roads.AreaQuery
	for _, v := range all.Items {
		if v.Location != nil {
			near.At = *v.Location
		}
	}
	near.RadiusM, near.Limit, near.MinSeverity = 2_000_000, 100, roads.SeverityInfo
	near.From, near.Until = wide.From, wide.Until
	if a, err := st.SituationsNear(bounded(t), near, fams, now); err != nil || len(a.Items) == 0 {
		t.Errorf("near over the window: %d, %v", len(a.Items), err)
	}
}

// TestAnUnregisteredRoadIsReadByItsReference (G6): a road the register does
// not hold is read from the situations whose reference names it, none
// placed along a line, with the families read that hold a situation on a
// road of its class; a class no family holds names none.
func TestAnUnregisteredRoadIsReadByItsReference(t *testing.T) {
	req.Covers(t, "FR-ROAD-001", "FR-ROAD-003")
	st := seeded(t)
	// The seed is insert-only, so the planted situation stays: it has no
	// position (no read near a position or in a Land sees it) and lies on a
	// road no other test asks for, in 2102; a second run plants it once.
	var family string
	owner(t, `select source_ref from contract.traffic_event where event_id = '`+fixtures.PlantedEvent("ev-u-kind")+`'`, &family)
	change(t, `insert into contract.traffic_event (event_id, event_kind, road_ref, road_class, valid_from, headline, source_ref,
    origin_key, published_at, recorded_at, changed_at)
  select $1, 'other', 'L 1025', 'state', '2102-01-01T00:00:00Z', 'Baustelle', $2,
    'ev-land-l', '2102-01-01T00:00:00Z', '2102-01-01T00:00:00Z', '2102-01-01T00:00:00Z'
  where not exists (select 1 from contract.traffic_event where event_id = $1)`, fixtures.PlantedEvent("ev-land-l"), family)
	now := time.Date(2102, 1, 1, 1, 0, 0, 0, time.UTC)
	id := fixtures.PlantedEvent("ev-land-l")
	fams := roads.Families{Roads: families(t, st, "roads")}
	q := roads.CorridorQuery{Road: "L1025", Unregistered: true, Class: roads.ClassState, Direction: roads.DirectionBoth,
		Filter: roads.Filter{MinSeverity: roads.SeverityInfo}, Limit: 10}
	p, err := st.SituationsOnRoad(bounded(t), q, fams, now)
	if err != nil || !slices.Equal(ids(p.Items), []string{id}) || p.Items[0].AtKm != nil {
		t.Fatalf("L1025: %+v, %v", p.Items, err)
	}
	if !slices.Equal(p.Covering, []string{family}) {
		t.Errorf("covering %v, want [%s]", p.Covering, family)
	}
	q.Road, q.Class = "B6", roads.ClassFederal
	p, err = st.SituationsOnRoad(bounded(t), q, fams, now)
	if err != nil || len(p.Items) != 0 || p.Covering == nil || len(p.Covering) != 0 {
		t.Errorf("B6: %+v, covering %#v, %v", p.Items, p.Covering, err)
	}
	// A registered road names no coverage.
	q.Road, q.Unregistered, q.Class = "A101", false, ""
	if p, err = st.SituationsOnRoad(bounded(t), q, fams, now); err != nil || p.Covering != nil {
		t.Errorf("A101: covering %v, %v", p.Covering, err)
	}
}
