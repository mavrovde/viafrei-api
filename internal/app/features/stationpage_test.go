package features_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

func pageFake() *trFake {
	sp, ags, line, gleis := "1:de:1:1", "01001000", "RE1", "Gleis 1"
	until := now.Add(time.Hour)
	return &trFake{
		record:  transit.StationRecord{StationNo: "1", Name: "Nord", StopPlaceID: &sp, Municipality: &ags, SeenAt: now.Add(-time.Hour), Family: "t1", Flags: []string{"wifi"}},
		station: transit.Station{ID: sp, Family: "t1", StopIDs: []string{sp, "1:de:1:1:a"}, EVANumbers: []string{"8000001"}},
		calls: []transit.Departure{
			{JourneyID: "a", PlannedAt: now.Add(time.Minute), RouteType: 106, Line: &line, Family: "t1", Families: []string{"t1"}},
			{JourneyID: "b", PlannedAt: now.Add(2 * time.Minute), RouteType: 106, Line: &line, Family: "t1", Families: []string{"t1"}},
		},
		facilities: []transit.Facility{{ID: "e1", Kind: "elevator", Position: &gleis, State: "active", Family: "a1", Listing: "a1"}},
		platforms:  []transit.PlatformRow{{ElementID: "q1", StopPlaceID: "x", PlateCode: ptr("1"), HasAssessment: true, StepFree: ptr("true"), Family: "a2"}},
		alertRows: []transit.StationAlertRow{{ID: "s:1", MessageKey: "s:1", Header: "Bauarbeiten", Until: &until, Lines: []string{"RE1"},
			StopIDs: []string{"de:1:1"}, CarriedBy: []string{"t1"}, Family: "t1"}},
	}
}

var pageKey = key("commercial", []access.Scope{"transit"}, "t1", "a1", "a2")

func TestStationPageBuildsEverySectionAsToday(t *testing.T) {
	f := pageFake()
	p, err := trService(f).StationPage(context.Background(), pageKey, features.StationPageQuery{StationID: "8000001", Departures: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range features.PageSections {
		if _, ok := p.Sections[id]; !ok {
			t.Errorf("section %s missing", id)
		}
	}
	if len(p.Departures) != 1 || p.Ampel.Lamp != "yellow" || len(p.Alerts) != 1 || !p.Alerts[0].AtStation || len(p.Lifts) != 1 ||
		len(p.Platforms) != 1 || len(p.StepFree) != 1 || p.StepFree[0].Lift != "ok" || len(p.Blocked) != 0 {
		t.Fatalf("%+v", p)
	}
	if !slices.Equal(f.gotAlerts.IDs, []string{"de:1:1", "de:1:1:a"}) || !slices.Equal(f.gotAlerts.Lines, []string{"RE1"}) || !slices.Equal(f.gotAlerts.Kreis, []string{"01001"}) {
		t.Errorf("asked %+v", f.gotAlerts)
	}
	src := func(id string) []string { return p.Sections[id].Sources }
	if !slices.Equal(src("lifts"), []string{"a1"}) || !slices.Equal(src("platforms"), []string{"a2"}) || !slices.Equal(src("step_free_now"), []string{"a2", "a1"}) ||
		!slices.Equal(src("parking_charging"), []string{"t1"}) || !slices.Equal(src("service_alerts"), []string{"t1"}) || p.Sections["departures"].At == nil {
		t.Errorf("sources %+v", p.Sections)
	}
	if !slices.Equal(names(p.Meta.Families), []string{"a1", "a2", "t1"}) {
		t.Errorf("credits %v", names(p.Meta.Families))
	}
}

// A section whose read has nothing is present and empty: a station with no
// stop place, a key that may not read accessibility, no flags.
func TestStationPageKeepsEmptySections(t *testing.T) {
	f := pageFake()
	f.record.StopPlaceID, f.record.Flags, f.record.SeenAt = nil, nil, now.Add(time.Hour)
	p, err := trService(f).StationPage(context.Background(), pageKey, features.StationPageQuery{StationID: "1", Departures: 10})
	if err != nil || p.Sections["departures"].At != nil || p.Sections["rail_ampel"].At != nil || len(p.Departures) != 0 || p.Ampel.Lamp != "yellow" ||
		len(p.Sections["parking_charging"].Sources) != 0 || p.Sections["lifts"].At != nil || len(f.gotAlerts.IDs) != 0 || !p.Sections["header"].At.Equal(now) {
		t.Errorf("no stop place %+v: %v", p, err)
	}
	f = pageFake()
	f.platforms = nil
	f.facilities = nil
	p, err = trService(f).StationPage(context.Background(), key("commercial", []access.Scope{"transit"}, "t1"), features.StationPageQuery{StationID: "1", Departures: 10})
	if err != nil || len(p.Lifts) != 0 || p.Sections["step_free_now"].At != nil || len(p.Departures) != 2 {
		t.Errorf("no accessibility %+v: %v", p, err)
	}
	p, err = trService(f).StationPage(context.Background(), pageKey, features.StationPageQuery{StationID: "1", Departures: 10})
	if err != nil || p.Sections["lifts"].At != nil || p.Sections["platforms"].At != nil || !slices.Equal(p.Sections["step_free_now"].Sources, []string{"t1"}) {
		t.Errorf("no units %+v: %v", p.Sections, err)
	}
	// No stop and no line to ask about: no alert read.
	f = pageFake()
	f.station.StopIDs, f.calls = nil, nil
	f.alertErr = errStore
	if _, err := trService(f).StationPage(context.Background(), pageKey, features.StationPageQuery{StationID: "1", Departures: 10}); err != nil {
		t.Errorf("nothing to ask: %v", err)
	}
}

func TestStationPageFailsAsItsReads(t *testing.T) {
	errFake := func(set func(*trFake)) *trFake { f := pageFake(); set(f); return f }
	for name, c := range map[string]struct {
		f    *trFake
		k    access.Record
		want error
	}{
		"register":   {errFake(func(f *trFake) { f.fake = &fake{regErr: errStore} }), pageKey, errStore},
		"no transit": {pageFake(), key("commercial", []access.Scope{"transit"}, "a1"), access.ErrNonePermitted},
		"unknown":    {errFake(func(f *trFake) { f.recErr = transit.ErrNotFound }), pageKey, transit.ErrNotFound},
		"board":      {errFake(func(f *trFake) { f.depErr = errStore }), pageKey, errStore},
		"facilities": {errFake(func(f *trFake) { f.facErr = errStore }), pageKey, errStore},
		"alerts":     {errFake(func(f *trFake) { f.alertErr = errStore }), pageKey, errStore},
		"post-check": {errFake(func(f *trFake) { f.alertRows[0].CarriedBy = []string{"t2"} }), pageKey, features.ErrGateViolation},
		"credits":    {errFake(func(f *trFake) { f.fake, f.record.StopPlaceID = &fake{provErr: errStore}, nil }), pageKey, errStore},
	} {
		if _, err := trService(c.f).StationPage(context.Background(), c.k, features.StationPageQuery{StationID: "1", Departures: 10}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
	// A station the board does not know is a page without a board.
	f := pageFake()
	f.stErr = transit.ErrNotFound
	if p, err := trService(f).StationPage(context.Background(), pageKey, features.StationPageQuery{StationID: "1", Departures: 10}); err != nil || p.Sections["departures"].At != nil {
		t.Errorf("no board %+v: %v", p, err)
	}
}
