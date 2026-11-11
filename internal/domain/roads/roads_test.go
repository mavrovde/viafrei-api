package roads_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestStoredCodesMapToTheDomain(t *testing.T) {
	req.Covers(t, "FR-ROAD-002", "ENV-051")
	for raw, want := range map[string]roads.Kind{
		"closure": roads.KindClosure, "roadworks": roads.KindRoadworks, "jam": roads.KindJam, "accident": roads.KindAccident,
		"hazard": roads.KindHazard, "restriction": roads.KindRestriction, "weather": roads.KindWeather,
		"other": roads.KindOther, "x_unmapped": roads.KindOther, "": roads.KindOther,
	} {
		if got := roads.MapKind(raw); got != want {
			t.Errorf("kind %q -> %q, want %q", raw, got, want)
		}
	}
	for raw, want := range map[string]roads.Severity{
		"info": roads.SeverityInfo, "minor": roads.SeverityMinor, "lane_closed": roads.SeverityLaneClosed,
		"major": roads.SeverityMajor, "road_closed": roads.SeverityRoadClosed, "x_unmapped": roads.SeverityOther,
	} {
		if got := roads.MapSeverity(raw); got != want {
			t.Errorf("severity %q -> %q, want %q", raw, got, want)
		}
	}
	for raw, want := range map[string]roads.Direction{
		"north": roads.DirectionNorth, "east": roads.DirectionEast, "south": roads.DirectionSouth, "west": roads.DirectionWest,
		"both": roads.DirectionBoth, "unknown": roads.DirectionUnknown, "x_unmapped": roads.DirectionUnknown,
	} {
		if got := roads.MapDirection(raw); got != want {
			t.Errorf("direction %q -> %q, want %q", raw, got, want)
		}
	}
	for raw, want := range map[string]roads.Class{
		"motorway": roads.ClassMotorway, "federal": roads.ClassFederal, "state": roads.ClassState,
		"urban": roads.ClassUrban, "x_unmapped": roads.ClassOther,
	} {
		if got := roads.MapClass(raw); got != want {
			t.Errorf("class %q -> %q, want %q", raw, got, want)
		}
	}
	if len(roads.Kinds) != 8 || len(roads.Severities) != 6 || len(roads.Directions) != 6 || len(roads.Classes) != 5 || len(roads.Lamps) != 4 {
		t.Error("an enumeration lost or gained a value")
	}
}

func TestSeveritiesAreOrdered(t *testing.T) {
	req.Covers(t, "FR-ROAD-002")
	want := []roads.Severity{roads.SeverityInfo, roads.SeverityMinor, roads.SeverityLaneClosed, roads.SeverityMajor, roads.SeverityRoadClosed}
	got := roads.Ordered()
	if !slices.Equal(got, want) {
		t.Fatalf("ordered %v", got)
	}
	got[0] = "changed"
	if roads.Ordered()[0] != roads.SeverityInfo {
		t.Error("Ordered hands out the package's own list")
	}
	for i, s := range want {
		if s.Rank() != i+1 {
			t.Errorf("%s rank %d, want %d", s, s.Rank(), i+1)
		}
	}
	if roads.SeverityOther.Rank() != 0 || roads.SeverityInfo.Floor() != 0 || roads.SeverityMinor.Floor() != 2 || roads.SeverityRoadClosed.Floor() != 5 {
		t.Error("the rank of other or a floor is wrong")
	}
	if c := roads.Compass(); !slices.Equal(c, []roads.Direction{roads.DirectionNorth, roads.DirectionEast, roads.DirectionSouth, roads.DirectionWest}) {
		t.Errorf("compass %v", c)
	} else if c[0] = "changed"; roads.Compass()[0] != roads.DirectionNorth {
		t.Error("Compass hands out the package's own list")
	}
}

func TestRoadReferencesAreCanonical(t *testing.T) {
	req.Covers(t, "FR-ROAD-001")
	for in, want := range map[string]string{"A8": "A8", "a8": "A8", "a 8": "A8", "A 100": "A100", "b27": "B27", "B 2700": "B2700"} {
		got, err := roads.ParseRoad(in)
		if err != nil || got != want {
			t.Errorf("%q -> %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "A", "8", "C8", "A  8", "A12345", " A8", "A8 ", "A-8", "A8a"} {
		if _, err := roads.ParseRoad(in); !errors.Is(err, roads.ErrRoadReference) {
			t.Errorf("%q read without error", in)
		}
	}
	if roads.Canonical("a 101") != "A101" {
		t.Error("a stored reference is not shown canonically")
	}
}

func sev(s roads.Severity) *roads.Severity { return &s }

func TestTheLampRule(t *testing.T) {
	req.Covers(t, "FR-ROAD-004")
	for name, c := range map[string]struct {
		in     []roads.Severity
		lamp   roads.Lamp
		reason *roads.Severity
	}{
		"nothing":                     {nil, roads.LampGreen, nil},
		"info":                        {[]roads.Severity{roads.SeverityInfo}, roads.LampGreen, sev(roads.SeverityInfo)},
		"minor beats info":            {[]roads.Severity{roads.SeverityInfo, roads.SeverityMinor, roads.SeverityInfo}, roads.LampGreen, sev(roads.SeverityMinor)},
		"lane closed":                 {[]roads.Severity{roads.SeverityMinor, roads.SeverityLaneClosed}, roads.LampAmber, sev(roads.SeverityLaneClosed)},
		"major":                       {[]roads.Severity{roads.SeverityMajor, roads.SeverityLaneClosed}, roads.LampAmber, sev(roads.SeverityMajor)},
		"road closed":                 {[]roads.Severity{roads.SeverityMajor, roads.SeverityRoadClosed, roads.SeverityInfo}, roads.LampRed, sev(roads.SeverityRoadClosed)},
		"ungraded alone":              {[]roads.Severity{roads.SeverityOther}, roads.LampAmber, sev(roads.SeverityOther)},
		"ungraded beats minor":        {[]roads.Severity{roads.SeverityMinor, roads.SeverityOther}, roads.LampAmber, sev(roads.SeverityOther)},
		"major beats ungraded":        {[]roads.Severity{roads.SeverityOther, roads.SeverityMajor}, roads.LampAmber, sev(roads.SeverityMajor)},
		"closed beats ungraded first": {[]roads.Severity{roads.SeverityOther, roads.SeverityRoadClosed}, roads.LampRed, sev(roads.SeverityRoadClosed)},
	} {
		lamp, reason := roads.LampOf(c.in)
		if lamp != c.lamp || (reason == nil) != (c.reason == nil) || reason != nil && *reason != *c.reason {
			t.Errorf("%s: %s %v, want %s %v", name, lamp, reason, c.lamp, c.reason)
		}
	}
}

func TestFiltersSplitTheUnmappedValue(t *testing.T) {
	req.Covers(t, "FR-ROAD-006", "ENV-051")
	known, other := roads.Filter{}.KnownKinds()
	if !slices.Equal(known, roads.Kinds[:7]) || !other {
		t.Errorf("no kinds keeps every kind: %v %v", known, other)
	}
	known[0] = "changed"
	if roads.Kinds[0] != roads.KindClosure {
		t.Error("an empty filter hands out the package's own list")
	}
	known, other = roads.Filter{Kinds: []roads.Kind{roads.KindJam, roads.KindOther, roads.KindClosure}}.KnownKinds()
	if !slices.Equal(known, []roads.Kind{roads.KindJam, roads.KindClosure}) || !other {
		t.Errorf("with other: %v %v", known, other)
	}
	known, other = roads.Filter{Kinds: []roads.Kind{roads.KindJam}}.KnownKinds()
	if !slices.Equal(known, []roads.Kind{roads.KindJam}) || other {
		t.Errorf("without other: %v %v", known, other)
	}
	classes, otherClass := roads.AreaQuery{Classes: []roads.Class{roads.ClassOther}}.KnownClasses()
	if classes == nil || len(classes) != 0 || !otherClass {
		t.Errorf("other alone keeps no mapped class: %v %v", classes, otherClass)
	}
	classes, otherClass = roads.AreaQuery{}.KnownClasses()
	if !slices.Equal(classes, roads.Classes[:4]) || !otherClass {
		t.Errorf("no classes: %v %v", classes, otherClass)
	}
}
