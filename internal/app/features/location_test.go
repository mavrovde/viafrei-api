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

type locationFake struct {
	*fake
	v       places.Location
	err     error
	zones   []rules.Zone
	zoneErr error
	gotZone []rules.ZoneQuery
	roads   bool
	// entries the zone list holds, the codes asked and its error.
	entries  []rules.ListEntry
	gotCodes []string
	entryErr error
}

func (f *locationFake) Zones(_ context.Context, q rules.ZoneQuery, _ []string, _ time.Time) ([]rules.Zone, error) {
	f.gotZone = append(f.gotZone, q)
	return f.zones, f.zoneErr
}

func (f *locationFake) Topic(context.Context, string, string) (rules.Topic, error) {
	return rules.Topic{}, nil
}

func (f *locationFake) ZoneEntries(_ context.Context, codes []string) ([]rules.ListEntry, error) {
	f.gotCodes = codes
	return f.entries, f.entryErr
}

func (f *locationFake) GemeindeAt(context.Context, geo.Point, int, []string) (*places.Place, error) {
	return nil, nil
}

func (f *locationFake) Location(context.Context, places.LocationQuery, []string) (places.Location, error) {
	return f.v, f.err
}

func locationService(f *locationFake) *features.Service {
	if f.fake == nil {
		f.fake = &fake{}
	}
	f.reg = registerOf("places")
	if f.roads {
		f.reg = append(f.reg, access.Terms{Family: "z1", Domain: "roads", UseClasses: all, Version: "v1"},
			access.Terms{Family: "z2", Domain: "roads", UseClasses: []access.UseClass{"consumer"}, Version: "v1"})
	}
	return features.New(features.Repositories{Register: f, Locations: f, Rules: f}, features.Settings{Language: "en"}, func() time.Time { return now })
}

func TestLocation(t *testing.T) {
	req.Covers(t, "SEC-030")
	full := key("commercial", []access.Scope{"places"}, "f1")
	v := places.Location{Address: &places.Address{Family: "f1"}, District: &places.Place{Family: "f1"}, State: &places.Place{Family: "f1"}}
	a, err := locationService(&locationFake{v: v}).Location(context.Background(), full, places.LocationQuery{})
	if err != nil || a.Address == nil || a.Municipality != nil || !a.Meta.At.Equal(now) || a.Zone != nil || !slices.Equal(names(a.Meta.Families), []string{"f1"}) {
		t.Errorf("answer %+v %v", a, err)
	}
	// nothing found: the places families consulted are named
	if a, err := locationService(&locationFake{}).Location(context.Background(), full, places.LocationQuery{}); err != nil || !slices.Equal(names(a.Meta.Families), []string{"f1"}) {
		t.Errorf("empty answer %+v %v", a, err)
	}
	// A position that may lie abroad credits the addresses it was judged by.
	abroad := places.Location{State: &places.Place{Family: "f1"}, MayLieAbroad: true, AbroadFamily: "f2"}
	a, err = locationService(&locationFake{v: abroad}).Location(context.Background(), key("consumer", []access.Scope{"places"}, "f1", "f2"), places.LocationQuery{})
	if err != nil || len(a.Meta.Families) != 2 {
		t.Errorf("abroad %+v %v", a.Meta.Families, err)
	}
	for name, c := range map[string]struct {
		f    *locationFake
		want error
	}{
		"register":   {&locationFake{fake: &fake{regErr: errStore}}, errStore},
		"no places":  {&locationFake{fake: &fake{}}, access.ErrNonePermitted},
		"read":       {&locationFake{err: errStore}, errStore},
		"post-check": {&locationFake{v: places.Location{Municipality: &places.Place{Family: "f2"}}}, features.ErrGateViolation},
		"zones":      {&locationFake{roads: true, zoneErr: errStore}, errStore},
		"zone check": {&locationFake{roads: true, zones: []rules.Zone{{Role: "zone", Inside: true, Family: "z2"}}}, features.ErrGateViolation},
		"provenance": {&locationFake{fake: &fake{provErr: errStore}}, errStore},
	} {
		k := full
		if name == "no places" {
			k = key("commercial", []access.Scope{"places"}, "f2")
		}
		if c.f.roads {
			k = key("commercial", []access.Scope{"places"}, "f1", "z1")
		}
		if _, err := locationService(c.f).Location(context.Background(), k, places.LocationQuery{}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}

func TestLocationZoneAsThePlatformStatesIt(t *testing.T) {
	req.Covers(t, "SEC-030")
	full := key("commercial", []access.Scope{"places"}, "f1", "z1")
	at := places.LocationQuery{At: geo.Point{Lat: 52.5, Lon: 13.4}}
	inside := []rules.Zone{{Key: "k1", Role: "zone", Name: "Berlin", Inside: true, Family: "z1"}, {Key: "k2", Role: "exempt_road", Name: "A100", Family: "z1"}}
	for name, c := range map[string]struct {
		zones []rules.Zone
		kind  string
		fams  []string
	}{
		"inside, with its exempt road":      {inside, "inside", []string{"f1", "z1"}},
		"none within reach: no attribution": {nil, "unknown", []string{"f1"}},
	} {
		f := &locationFake{roads: true, zones: c.zones, v: places.Location{State: &places.Place{Family: "f1"}}}
		a, err := locationService(f).Location(context.Background(), full, at)
		if err != nil || a.Zone == nil || a.Zone.Kind != c.kind || !slices.Equal(names(a.Meta.Families), c.fams) {
			t.Errorf("%s: %+v %v", name, a, err)
			continue
		}
		if len(f.gotZone) != 1 || f.gotZone[0].At == nil || *f.gotZone[0].At != at.At {
			t.Errorf("%s: asked %+v", name, f.gotZone)
		}
	}
	// a key that may read no zone source gets no verdict, and no error
	f := &locationFake{roads: true, zones: inside}
	if a, err := locationService(f).Location(context.Background(), key("commercial", []access.Scope{"places"}, "f1"), at); err != nil || a.Zone != nil || len(f.gotZone) != 0 {
		t.Errorf("no zone source: %+v %v", a, err)
	}
}
