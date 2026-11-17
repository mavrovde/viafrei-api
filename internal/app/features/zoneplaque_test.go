package features_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/rules"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// plaqueFake is the Rules port with the zone list and the Gemeinde lookup.
type plaqueFake struct {
	*fake
	zones                 []rules.Zone
	entries               []rules.ListEntry
	gemeinde              *places.Place
	zoneErr, entryErr     error
	gemErr                error
	gotCodes, gotGemFams  []string
	gemCalls, entryCalls  int
	regErrOnCall, regRead int
}

func (f *plaqueFake) Register(ctx context.Context) ([]access.Terms, error) {
	f.regRead++
	if f.regErrOnCall != 0 && f.regRead == f.regErrOnCall {
		return nil, errStore
	}
	return f.fake.Register(ctx)
}

func (f *plaqueFake) Topic(context.Context, string) (rules.Topic, error) { return rules.Topic{}, nil }

func (f *plaqueFake) Zones(context.Context, rules.ZoneQuery, []string, time.Time) ([]rules.Zone, error) {
	return slices.Clone(f.zones), f.zoneErr
}

func (f *plaqueFake) ZoneEntries(_ context.Context, codes []string) ([]rules.ListEntry, error) {
	f.entryCalls++
	f.gotCodes = codes
	return f.entries, f.entryErr
}

func (f *plaqueFake) GemeindeAt(_ context.Context, _ geo.Point, reach int, fams []string) (*places.Place, error) {
	f.gemCalls++
	f.gotGemFams = fams
	if reach != rules.GemeindeReachM {
		return nil, errors.New("reach")
	}
	return f.gemeinde, f.gemErr
}

func plaqueService(f *plaqueFake, domains ...string) *features.Service {
	if f.fake == nil {
		f.fake = &fake{}
	}
	f.reg = []access.Terms{{Family: "z1", Domain: "roads", UseClasses: all, Version: "v1"}}
	for _, d := range domains {
		f.reg = append(f.reg, access.Terms{Family: "p1", Domain: d, UseClasses: all, Version: "v1"})
	}
	return features.New(features.Repositories{Register: f, Rules: f}, features.Settings{Language: "en"}, func() time.Time { return now })
}

func TestLowEmissionZonesStatesThePlaqueAsThePlatform(t *testing.T) {
	req.Covers(t, "SEC-030")
	k := key("commercial", []access.Scope{"roads", "places"}, "z1", "p1")
	at := geo.Point{Lat: 52.37, Lon: 9.73}
	lifted := time.Date(2024, 2, 22, 0, 0, 0, 0, time.UTC)
	national, city := "national", "city"
	hannover := rules.Zone{Key: "uba/DE_NI_1", Role: "zone", Name: "DE_NI_1", Inside: true, Family: "z1", Scope: &national}
	gem := "03241001"
	entries := []rules.ListEntry{{Key: "hannover", Name: "Hannover", PlaceCodes: []string{gem}, Covered: true, LiftedOn: &lifted}}

	// The register's zone: the point's Gemeinde finds the entry, and its
	// family is credited beside the zone's.
	f := &plaqueFake{zones: []rules.Zone{hannover}, entries: entries, gemeinde: &places.Place{OfficialKey: &gem, Family: "p1"}}
	a, err := plaqueService(f, "places").LowEmissionZones(context.Background(), k, rules.ZoneQuery{At: &at})
	if err != nil || a.Answer == nil || a.Answer.Entry == nil || !a.Answer.FoundByPoint || !a.Answer.LiftedOn.Equal(lifted) ||
		!slices.Equal(names(a.Meta.Families), []string{"z1", "p1"}) || !slices.Equal(f.gotCodes, []string{gem}) || !slices.Equal(f.gotGemFams, []string{"p1"}) {
		t.Errorf("by the point: %+v %v", a, err)
	}

	// A city's own document: its Gemeinde finds the entry; the point's
	// Gemeinde is asked too but not credited.
	own := "09162000"
	m := rules.Zone{Key: "m/1", Role: "zone", Name: "Umweltzone", Inside: true, Family: "z1", Scope: &city, PlaceCode: &own}
	f = &plaqueFake{zones: []rules.Zone{m}, entries: []rules.ListEntry{{Key: "muenchen", PlaceCodes: []string{own}}}, gemeinde: &places.Place{OfficialKey: &gem, Family: "p1"}}
	a, err = plaqueService(f, "places").LowEmissionZones(context.Background(), k, rules.ZoneQuery{At: &at})
	if err != nil || a.Answer.FoundByPoint || !slices.Equal(names(a.Meta.Families), []string{"z1"}) || !slices.Equal(f.gotCodes, []string{own, gem}) {
		t.Errorf("by the source: %+v %v", a, err)
	}

	// No places family: no Gemeinde is asked; a Kreis key finds no entry;
	// none within reach asks for nothing at all.
	f = &plaqueFake{zones: []rules.Zone{hannover}}
	if a, err := plaqueService(f).LowEmissionZones(context.Background(), key("commercial", []access.Scope{"roads"}, "z1"), rules.ZoneQuery{At: &at}); err != nil || f.gemCalls != 0 || f.entryCalls != 0 || !a.Answer.Plaque || a.Answer.Entry != nil {
		t.Errorf("no places: %+v %v", a, err)
	}
	kreis := "03241"
	f = &plaqueFake{zones: []rules.Zone{hannover}, gemeinde: &places.Place{OfficialKey: &kreis}}
	if _, err := plaqueService(f, "places").LowEmissionZones(context.Background(), k, rules.ZoneQuery{At: &at}); err != nil || f.entryCalls != 0 {
		t.Errorf("a Kreis key: %d %v", f.entryCalls, err)
	}
	f = &plaqueFake{}
	if a, err := plaqueService(f, "places").LowEmissionZones(context.Background(), k, rules.ZoneQuery{At: &at}); err != nil || a.Verdict.Kind != "unknown" || f.gemCalls != 0 || a.Answer.Name != nil {
		t.Errorf("unknown: %+v %v", a, err)
	}

	// A city asked by name gets its rows and no verdict.
	f = &plaqueFake{zones: []rules.Zone{m}}
	if a, err := plaqueService(f, "places").LowEmissionZones(context.Background(), k, rules.ZoneQuery{City: "München"}); err != nil || a.Verdict != nil || a.Answer != nil || len(a.Zones) != 1 {
		t.Errorf("city: %+v %v", a, err)
	}

	for name, f := range map[string]*plaqueFake{
		"zones":      {zoneErr: errStore},
		"gate":       {zones: []rules.Zone{hannover}, regErrOnCall: 2},
		"gemeinde":   {zones: []rules.Zone{hannover}, gemErr: errStore},
		"entries":    {zones: []rules.Zone{hannover}, gemeinde: &places.Place{OfficialKey: &gem}, entryErr: errStore},
		"provenance": {fake: &fake{provErr: errStore, provErrOnCall: 2}, zones: []rules.Zone{hannover}, entries: entries, gemeinde: &places.Place{OfficialKey: &gem, Family: "p1"}},
	} {
		if _, err := plaqueService(f, "places").LowEmissionZones(context.Background(), k, rules.ZoneQuery{At: &at}); !errors.Is(err, errStore) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestLocationZoneStatesThePlaqueByItsMunicipality(t *testing.T) {
	req.Covers(t, "SEC-030")
	full := key("commercial", []access.Scope{"places"}, "f1", "z1")
	at := places.LocationQuery{At: geo.Point{Lat: 52.37, Lon: 9.73}}
	gem := "03241001"
	national := "national"
	zones := []rules.Zone{{Key: "uba/DE_NI_1", Role: "zone", Name: "DE_NI_1", Inside: true, Family: "z1", Scope: &national}}
	entries := []rules.ListEntry{{Key: "hannover", Name: "Hannover", PlaceCodes: []string{gem}}}
	f := &locationFake{roads: true, zones: zones, entries: entries, v: places.Location{Municipality: &places.Place{OfficialKey: &gem, Family: "f1"}}}
	a, err := locationService(f).Location(context.Background(), full, at)
	if err != nil || a.ZoneAnswer == nil || a.ZoneAnswer.Entry == nil || !a.ZoneAnswer.FoundByPoint || !slices.Equal(f.gotCodes, []string{gem}) ||
		*a.ZoneAnswer.Name != "Umweltzone ()" || !slices.Equal(names(a.Meta.Families), []string{"f1", "z1"}) {
		t.Errorf("by the municipality: %+v %v", a, err)
	}
	f = &locationFake{roads: true, zones: zones, entryErr: errStore, v: places.Location{Municipality: &places.Place{OfficialKey: &gem, Family: "f1"}}}
	if _, err := locationService(f).Location(context.Background(), full, at); !errors.Is(err, errStore) {
		t.Errorf("entries: %v", err)
	}
}
