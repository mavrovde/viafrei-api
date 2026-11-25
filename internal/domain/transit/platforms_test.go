package transit_test

import (
	"slices"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

func strp(s string) *string { return &s }
func intp(n int) *int       { return &n }

func TestPlatformsOfAsToday(t *testing.T) {
	rows := []transit.PlatformRow{
		{ElementID: "q2", StopPlaceID: "s1", QuayName: strp("Bahnsteig Gleis 10/11"), HasAssessment: true, StepFree: strp("true"), HeightCm: intp(76)},
		{ElementID: "q2a", StopPlaceID: "s1", ParentQuayID: strp("q2"), PlateCode: strp("11")},
		{ElementID: "q2b", StopPlaceID: "s1", ParentQuayID: strp("q2"), PlateCode: strp("10"), HeightCm: intp(55)},
		{ElementID: "q2b1", StopPlaceID: "s1", ParentQuayID: strp("q2b"), HeightCm: intp(96)},
		{ElementID: "q1", StopPlaceID: "s1", PlateCode: strp("1"), HasAssessment: true, StepFree: strp("FALSE"), Wheelchair: strp("partial")},
		{ElementID: "q3", StopPlaceID: "s1", ParentQuayID: strp("gone"), PlateCode: strp("3")},
		{ElementID: "s1", StopPlace: true, StopPlaceID: "s1", HasAssessment: true, StepFree: strp("x")},
		{ElementID: "s1b", StopPlace: true, StopPlaceID: "s1"},
		{ElementID: "q9", StopPlaceID: "s2", StationName: strp("Nord"), QuayName: strp("B")},
		{ElementID: "q8", StopPlaceID: "s2", QuayName: strp("A")},
		{ElementID: "s2", StopPlace: true, StopPlaceID: "s2", StationName: strp("Nord Bf")},
	}
	got := transit.PlatformsOf(rows)
	if len(got) != 2 || got[0].StopPlaceID != "s1" || got[0].Station == nil || got[0].Station.StepFree != "unknown" {
		t.Fatalf("%+v", got)
	}
	ps := got[0].Platforms
	if len(ps) != 3 || ps[0].ID != "q1" || ps[1].ID != "q3" || ps[2].ID != "q2" || !slices.Equal(ps[2].Tracks, []string{"10", "11"}) ||
		*ps[2].Height != (transit.HeightRange{Min: 55, Max: 96}) || ps[0].Facts.StepFree != "no" || ps[0].Facts.Wheelchair != "partial" ||
		ps[1].Facts != nil || ps[0].Height != nil {
		t.Errorf("%+v", ps)
	}
	if c := got[0].StepFreeCounts; c["yes"] != 1 || c["no"] != 1 || c["unknown"] != 1 {
		t.Errorf("counts %v", c)
	}
	if s := got[1]; *s.StationName != "Nord Bf" || s.Station != nil || *s.Platforms[0].Name != "A" || len(s.Platforms[0].Tracks) != 0 {
		t.Errorf("%+v", s)
	}
	ok, blocked := transit.StepFreeNow(got, []transit.Facility{{Kind: "elevator", Position: strp("zu Gleis 10/11"), State: "inactive"}})
	if len(ok) != 0 || len(blocked) != 1 || blocked[0].Lift != "out" {
		t.Errorf("%v %v", ok, blocked)
	}
	ok, _ = transit.StepFreeNow(got, nil)
	if len(ok) != 1 || ok[0].Lift != "none" {
		t.Errorf("%v", ok)
	}
}

func TestUnitTracksAndPlatformLift(t *testing.T) {
	for in, want := range map[string][]string{
		"zu Gleis 4/5":            {"4", "5"},
		"Aufzug Gleise 1-3":       {"1", "2", "3"},
		"Gleis 7a und 8, Gleis 9": {"7a", "8"},
		"Gleis 1-20":              nil,
		"Ausgang":                 nil,
	} {
		if got := transit.UnitTracks(&in); !slices.Equal(got, want) {
			t.Errorf("%q: %v, want %v", in, got, want)
		}
	}
	if transit.UnitTracks(nil) != nil {
		t.Error("nil")
	}
	lift := func(s string) transit.Facility {
		return transit.Facility{Kind: "elevator", Position: strp("Gleis 4"), State: s}
	}
	for want, us := range map[string][]transit.Facility{
		"ok":      {lift("active")},
		"unknown": {lift("active"), lift("unknown")},
		"out":     {lift("unknown"), lift("inactive")},
		"none":    {{Kind: "escalator", Position: strp("Gleis 4"), State: "active"}},
	} {
		if got := transit.PlatformLift(us, []string{"4"}); got != want {
			t.Errorf("%v: %s, want %s", us, got, want)
		}
	}
}
