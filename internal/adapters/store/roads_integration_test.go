//go:build integration

package store_test

import (
	"context"
	"errors"
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
	sits, sevs, more, err := st.SituationsOnRoad(bounded(t), q, fams, now)
	if err != nil || len(sits) == 0 || more {
		t.Fatalf("read %d situations, more %v: %v", len(sits), more, err)
	}
	if len(sits) != count || len(sevs) == 0 {
		t.Errorf("%d situations of %d, severities %v", len(sits), count, sevs)
	}
	for i, s := range sits {
		if s.Road == nil || *s.Road != "A101" || s.AtKm == nil || s.DistanceM != nil || !slices.Contains(fams, s.Family) || s.From.After(now) || s.Until != nil && !s.Until.After(now) {
			t.Errorf("situation %+v", s)
		}
		if i > 0 && *s.AtKm < *sits[i-1].AtKm {
			t.Errorf("both directions not in ascending chainage at %d: %v after %v", i, *s.AtKm, *sits[i-1].AtKm)
		}
		if !slices.Contains(sevs, s.Severity) {
			t.Errorf("severity %s missing from the lamp's severities %v", s.Severity, sevs)
		}
	}

	page, all, more, err := st.SituationsOnRoad(bounded(t), roads.CorridorQuery{Road: "A101", Direction: roads.DirectionBoth, Filter: q.Filter, Limit: 1}, fams, now)
	if err != nil || len(page) != 1 || !more || len(all) != len(sevs) {
		t.Errorf("a page of one: %d, more %v, severities %v, %v", len(page), more, all, err)
	}

	// One carriageway: its own, both and unknown, in the order of travel.
	var risesNorth bool
	owner(t, `select st_y(st_endpoint(st_geometryn(geometry, 1))) >= st_y(st_startpoint(st_geometryn(geometry, 1))) from contract.road where road_ref = 'A 101'`, &risesNorth)
	north, _, _, err := st.SituationsOnRoad(bounded(t), roads.CorridorQuery{Road: "A101", Direction: roads.DirectionNorth, Filter: q.Filter, Limit: 100}, fams, now)
	if err != nil {
		t.Fatal(err)
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
	major, _, _, err := st.SituationsOnRoad(bounded(t), roads.CorridorQuery{Road: "A101", Direction: roads.DirectionBoth, Filter: roads.Filter{MinSeverity: roads.SeverityMajor}, Limit: 100}, fams, now)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range major {
		if s.Severity.Rank() < roads.SeverityMajor.Rank() {
			t.Errorf("min major answered %s", s.Severity)
		}
	}
	if none, sevs, more, err := st.SituationsOnRoad(bounded(t), q, []string{"no-such-family"}, now); err != nil || len(none) != 0 || sevs != nil || more {
		t.Errorf("no family: %d %v %v %v", len(none), sevs, more, err)
	}
}

func TestSituationsNearAreBySeverityThenDistance(t *testing.T) {
	req.Covers(t, "FR-ROAD-006", "SEC-031")
	st := seeded(t)
	id, at, now := otherSituation(t)
	fams := families(t, st, "roads")
	q := roads.AreaQuery{At: at, RadiusM: everywhere, Filter: roads.Filter{MinSeverity: roads.SeverityInfo}, Limit: 100}
	sits, sevs, _, err := st.SituationsNear(bounded(t), q, fams, now)
	if err != nil || len(sits) == 0 || len(sevs) == 0 {
		t.Fatalf("read %d: %v", len(sits), err)
	}
	found := false
	for i, s := range sits {
		if s.DistanceM == nil || *s.DistanceM > everywhere || s.AtKm != nil || s.Location == nil {
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
	q.Classes = []roads.Class{roads.ClassMotorway}
	if mw, _, _, err := st.SituationsNear(bounded(t), q, fams, now); err != nil || len(mw) != len(sits) {
		t.Errorf("motorway class: %d of %d, %v", len(mw), len(sits), err)
	}
	q.Classes = []roads.Class{roads.ClassFederal, roads.ClassOther}
	if none, sevs, more, err := st.SituationsNear(bounded(t), q, fams, now); err != nil || len(none) != 0 || sevs != nil || more {
		t.Errorf("no situation is held on a federal or unmapped class: %d %v %v %v", len(none), sevs, more, err)
	}
	q.Classes, q.Limit = nil, 1
	if page, _, more, err := st.SituationsNear(bounded(t), q, fams, now); err != nil || len(page) != 1 || more != (len(sits) > 1) {
		t.Errorf("a page of one: %d, %v, %v", len(page), more, err)
	}
}

func TestSituationReadsOneOfTheGivenFamilies(t *testing.T) {
	req.Covers(t, "FR-ROAD-002", "SEC-031")
	st := seeded(t)
	id, _, _ := otherSituation(t)
	fams := families(t, st, "roads")
	s, err := st.Situation(bounded(t), id, fams)
	if err != nil || s.ID != id || s.Kind != roads.KindOther || s.Road == nil || *s.Road != "A101" || s.AtKm != nil || s.DistanceM != nil {
		t.Errorf("situation %+v, %v", s, err)
	}
	if _, err := st.Situation(bounded(t), id, []string{"no-such-family"}); !errors.Is(err, roads.ErrUnknownSituation) {
		t.Errorf("another family: %v", err)
	}
	// The situation of an unmapped kind is withdrawn in the fixture.
	var withdrawn string
	owner(t, `select event_id from contract.traffic_event where withdrawn_at is not null order by event_id limit 1`, &withdrawn)
	if _, err := st.Situation(bounded(t), withdrawn, fams); !errors.Is(err, roads.ErrUnknownSituation) {
		t.Errorf("a withdrawn situation: %v", err)
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
	if _, err := st.Situation(ctx, "x", []string{"x"}); err == nil || errors.Is(err, roads.ErrUnknownSituation) {
		t.Errorf("Situation on an ended context: %v", err)
	}
}
