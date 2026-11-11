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
	for in, want := range map[string]string{"A8": "A8", "a8": "A8", "a 8": "A8", "A 100": "A100", "b27": "B27", "B 2700": "B2700", "A08": "A8", "a 008": "A8", "A0": "A0", "A000": "A0", "B 0270": "B270"} {
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
	if roads.Canonical("a 101") != "A101" || roads.Canonical("") != "" || roads.Canonical("A") != "A" {
		t.Error("a stored reference is not shown canonically")
	}
}

func why(r roads.Reason) *roads.Reason { return &r }

func delay(m int) *int { return &m }

func TestTheLampRule(t *testing.T) {
	req.Covers(t, "FR-ROAD-004", "ENV-051")
	sig := func(k roads.Kind, s roads.Severity) roads.Signal { return roads.Signal{Kind: k, Severity: s} }
	jam := func(d *int) roads.Signal {
		return roads.Signal{Kind: roads.KindJam, Severity: roads.SeverityMinor, DelayMin: d}
	}
	for name, c := range map[string]struct {
		in             []roads.Signal
		partial, stale bool
		lamp           roads.Lamp
		reason         *roads.Reason
	}{
		"nothing":                 {nil, false, false, roads.LampGreen, nil},
		"light roadworks":         {[]roads.Signal{sig(roads.KindRoadworks, roads.SeverityMinor), sig(roads.KindRoadworks, roads.SeverityInfo)}, false, false, roads.LampAmber, why(roads.ReasonNarrowing)},
		"narrowing":               {[]roads.Signal{sig(roads.KindRoadworks, roads.SeverityLaneClosed)}, false, false, roads.LampAmber, why(roads.ReasonNarrowing)},
		"major roadworks":         {[]roads.Signal{sig(roads.KindRoadworks, roads.SeverityMajor)}, false, false, roads.LampAmber, why(roads.ReasonNarrowing)},
		"closed roadworks":        {[]roads.Signal{sig(roads.KindRoadworks, roads.SeverityRoadClosed)}, false, false, roads.LampRed, why(roads.ReasonRoadClosed)},
		"closure of the road":     {[]roads.Signal{sig(roads.KindClosure, roads.SeverityRoadClosed)}, false, false, roads.LampRed, why(roads.ReasonRoadClosed)},
		"closure of a lane":       {[]roads.Signal{sig(roads.KindClosure, roads.SeverityLaneClosed)}, false, false, roads.LampAmber, why(roads.ReasonNarrowing)},
		"closure, minor":          {[]roads.Signal{sig(roads.KindClosure, roads.SeverityMinor)}, false, false, roads.LampRed, why(roads.ReasonRoadClosed)},
		"hazard":                  {[]roads.Signal{sig(roads.KindHazard, roads.SeverityInfo)}, false, false, roads.LampRed, why(roads.ReasonSafety)},
		"major accident":          {[]roads.Signal{sig(roads.KindAccident, roads.SeverityMajor)}, false, false, roads.LampAmber, why(roads.ReasonWarning)},
		"accident with a closure": {[]roads.Signal{sig(roads.KindAccident, roads.SeverityRoadClosed)}, false, false, roads.LampRed, why(roads.ReasonRoadClosed)},
		"minor accident":          {[]roads.Signal{sig(roads.KindAccident, roads.SeverityLaneClosed)}, false, false, roads.LampAmber, why(roads.ReasonWarning)},
		"jam at the threshold":    {[]roads.Signal{jam(delay(roads.JamRedMin))}, false, false, roads.LampRed, why(roads.ReasonLongJam)},
		"jam under the threshold": {[]roads.Signal{jam(delay(roads.JamRedMin - 1))}, false, false, roads.LampAmber, why(roads.ReasonJam)},
		"jam without a delay":     {[]roads.Signal{jam(nil)}, false, false, roads.LampAmber, why(roads.ReasonJam)},
		"severe weather":          {[]roads.Signal{sig(roads.KindWeather, roads.SeverityMajor)}, false, false, roads.LampRed, why(roads.ReasonSevereWeather)},
		"weather":                 {[]roads.Signal{sig(roads.KindWeather, roads.SeverityLaneClosed)}, false, false, roads.LampAmber, why(roads.ReasonWarning)},
		"restriction":             {[]roads.Signal{sig(roads.KindRestriction, roads.SeverityInfo)}, false, false, roads.LampAmber, why(roads.ReasonNarrowing)},
		"unmapped kind":           {[]roads.Signal{sig(roads.KindOther, roads.SeverityInfo)}, false, false, roads.LampAmber, why(roads.ReasonWarning)},
		"ungraded roadworks":      {[]roads.Signal{sig(roads.KindRoadworks, roads.SeverityOther)}, false, false, roads.LampAmber, why(roads.ReasonNarrowing)},
		"ungraded closure":        {[]roads.Signal{sig(roads.KindClosure, roads.SeverityOther)}, false, false, roads.LampRed, why(roads.ReasonRoadClosed)},
		"first reason wins":       {[]roads.Signal{sig(roads.KindOther, roads.SeverityInfo), jam(nil), sig(roads.KindWeather, roads.SeverityMajor), sig(roads.KindHazard, roads.SeverityInfo)}, false, false, roads.LampRed, why(roads.ReasonSafety)},
		"light, then a jam":       {[]roads.Signal{sig(roads.KindRoadworks, roads.SeverityMinor), jam(nil)}, false, false, roads.LampAmber, why(roads.ReasonJam)},
		"warning, then works":     {[]roads.Signal{sig(roads.KindAccident, roads.SeverityMinor), sig(roads.KindRoadworks, roads.SeverityInfo)}, false, false, roads.LampAmber, why(roads.ReasonNarrowing)},
		"closure, then a hazard":  {[]roads.Signal{sig(roads.KindClosure, roads.SeverityRoadClosed), sig(roads.KindHazard, roads.SeverityInfo)}, false, false, roads.LampRed, why(roads.ReasonRoadClosed)},
		"amber order":             {[]roads.Signal{sig(roads.KindOther, roads.SeverityInfo), sig(roads.KindRoadworks, roads.SeverityMajor)}, false, false, roads.LampAmber, why(roads.ReasonNarrowing)},
		"stale alone":             {nil, false, true, roads.LampAmber, why(roads.ReasonStaleSource)},
		"amber beats stale":       {[]roads.Signal{jam(nil)}, false, true, roads.LampAmber, why(roads.ReasonJam)},
		"partial alone":           {nil, true, true, roads.LampUnknown, why(roads.ReasonPartial)},
		"partial beats amber":     {[]roads.Signal{jam(nil)}, true, false, roads.LampUnknown, why(roads.ReasonPartial)},
		"red beats partial":       {[]roads.Signal{jam(nil), sig(roads.KindClosure, roads.SeverityRoadClosed)}, true, true, roads.LampRed, why(roads.ReasonRoadClosed)},
	} {
		lamp, reason := roads.LampOf(c.in, c.partial, c.stale)
		if lamp != c.lamp || (reason == nil) != (c.reason == nil) || reason != nil && *reason != *c.reason {
			t.Errorf("%s: %s %v, want %s %v", name, lamp, reason, c.lamp, c.reason)
		}
	}
	d := 40
	s := roads.Situation{Kind: roads.KindJam, Severity: roads.SeverityMajor, DelayMin: &d}
	if got := roads.SignalOf(s); got.Kind != roads.KindJam || got.Severity != roads.SeverityMajor || got.DelayMin != &d {
		t.Errorf("signal %+v", got)
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
