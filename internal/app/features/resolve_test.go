package features_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// resolveFake answers every candidate read with its rows and nothing else.
type resolveFake struct {
	*fake
	rows    []places.GazetteerRow
	readErr error
	gotFams []string
}

func (f *resolveFake) ResolveReads(families []string) places.ResolveReads {
	f.gotFams = slices.Clone(families)
	return f
}

func (f *resolveFake) CandidateRows(context.Context, places.CandidateRead) ([]places.GazetteerRow, error) {
	return f.rows, f.readErr
}

func (f *resolveFake) RoadRows(context.Context, string, int) ([]places.GazetteerRow, error) {
	return nil, nil
}

func (f *resolveFake) LandsNear(context.Context, []float64, []float64, float64, []string) (map[int]string, error) {
	return nil, nil
}

func (f *resolveFake) KreisNames(context.Context, []string) (map[string]string, error) {
	return nil, nil
}

func (f *resolveFake) ShownStops(context.Context, string, int) ([]places.GazetteerRow, error) {
	return nil, nil
}

func (f *resolveFake) JunctionsNamed(context.Context, string, float64, int) ([]places.NamedJunction, error) {
	return nil, nil
}

func (f *resolveFake) LCLNear(_ context.Context, lons, _ []float64) ([][]places.LCLPoint, error) {
	return make([][]places.LCLPoint, len(lons)), nil
}

func (f *resolveFake) PoiRows(context.Context, places.PoiRead) ([]places.PoiHit, error) {
	return nil, nil
}

func (f *resolveFake) PlaceWords(context.Context, []string) ([]string, bool, error) {
	return nil, false, nil
}

func (f *resolveFake) AddressRows(context.Context, string, []any) ([]places.AddressHit, error) {
	return nil, nil
}

func (f *resolveFake) StreetRows(context.Context, string, []any) ([]places.StreetHit, error) {
	return nil, nil
}

func (f *resolveFake) IsLoaded(context.Context, string) (bool, error) { return true, nil }

func resolveService(f *resolveFake) *features.Service {
	if f.fake == nil {
		f.fake = &fake{}
	}
	if f.reg == nil {
		f.reg = append(registerOf("places"), access.Terms{Family: "t1", Domain: "transit", UseClasses: all, Version: "v1"})
	}
	return features.New(features.Repositories{Register: f, Resolver: f}, features.Settings{Language: "en"}, func() time.Time { return now })
}

func town(source string) []places.GazetteerRow {
	lat, lon, pop := 47.55, 9.69, 25_000
	return []places.GazetteerRow{{ID: "1", Kind: "city", Name: "Lindau", NameNorm: "lindau", SourceID: source, ExternalID: "1", Lat: &lat, Lon: &lon, Population: &pop, Score: 1}}
}

func TestResolvePlace(t *testing.T) {
	k := key("commercial", []access.Scope{"places"}, "f1", "t1")
	f := &resolveFake{rows: town("f1")}
	a, err := resolveService(f).ResolvePlace(context.Background(), k, "Lindau")
	if err != nil || a.Resolution.Kind != places.ResolvedPoint || a.Lang != "en" || !slices.Equal(names(a.Meta.Families), []string{"f1"}) || !a.Meta.At.Equal(now) {
		t.Fatalf("answer %+v %v", a, err)
	}
	// The stop families of transit ride along; a key reading no roads
	// family resolves without them.
	if !slices.Equal(f.gotFams, []string{"f1", "t1"}) {
		t.Errorf("families %v", f.gotFams)
	}
	// No one place: nothing credited.
	none := &resolveFake{}
	if a, err := resolveService(none).ResolvePlace(context.Background(), k, "Lindau"); err != nil || a.Resolution.Kind == places.ResolvedPoint || a.Meta.Families != nil {
		t.Fatalf("none %+v %v", a, err)
	}
}

func TestResolvePlaceRefusals(t *testing.T) {
	k := key("commercial", []access.Scope{"places"}, "f1", "t1")
	for name, f := range map[string]*resolveFake{
		"register":   {fake: &fake{regErr: errStore}},
		"read":       {rows: town("f1"), readErr: errStore},
		"provenance": {rows: town("f1"), fake: &fake{provErr: errStore}},
	} {
		if _, err := resolveService(f).ResolvePlace(context.Background(), k, "Lindau"); !errors.Is(err, errStore) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := resolveService(&resolveFake{}).ResolvePlace(context.Background(), key("commercial", []access.Scope{"energy"}), "Lindau"); err == nil {
		t.Error("no places scope: no refusal")
	}
	if _, err := resolveService(&resolveFake{rows: town("zz")}).ResolvePlace(context.Background(), k, "Lindau"); !errors.Is(err, features.ErrGateViolation) {
		t.Errorf("a family the key may not read: %v", err)
	}
}
