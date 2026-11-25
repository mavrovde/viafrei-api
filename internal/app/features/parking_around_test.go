package features_test

import (
	"context"
	"errors"
	"reflect"
	"slices"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// aroundRegister adds OpenStreetMap and a publisher that warns about its
// figures.
var aroundRegister = append(slices.Clone(register),
	access.Terms{Family: parking.OSMFamily, Domain: "places", UseClasses: all, Version: "v1"},
	access.Terms{Family: "hh_parkhaeuser", Domain: "parking", UseClasses: all, Version: "v1"},
)

func str(s string) *string { return &s }

func aroundAsk(t *testing.T, f *fake, q parking.Query, sources ...string) (features.Answer[parking.Site], error) {
	t.Helper()
	f.reg = aroundRegister
	return service(f, nil).Parking(context.Background(), key("commercial", allScopes, sources...), q)
}

func TestParkingAroundIsReadOnlyWhenAsked(t *testing.T) {
	req.Covers(t, "FR-PARK-002")
	f := &fake{parks: []parking.Site{{ID: "a", Family: "p1"}}}
	a, err := aroundAsk(t, f, parking.Query{Kind: "truck", CarParks: true}, "p1", parking.OSMFamily)
	if err != nil || a.Around != nil || f.gotAround != nil {
		t.Fatalf("a lorry question read %+v (%v)", f.gotAround, err)
	}
}

func TestParkingAroundComposesTheThreeBlocks(t *testing.T) {
	req.Covers(t, "FR-PARK-002", "SEC-032", "ENV-003")
	at := geo.Point{Lat: 53.55, Lon: 10}
	f := &fake{
		parks: []parking.Site{
			{ID: "a", Family: "p1", Kind: parking.KindGarage, Location: at, Name: str("Am Markt"), Amenities: parking.Stated{Present: []string{"toilet"}}},
			{ID: "b", Family: "p1", Kind: parking.KindTruckParking, Location: geo.Point{Lat: 53.6, Lon: 10}},
		},
		around: parking.AroundRead{
			Nearby: []parking.NearbyRow{
				{Site: 1, Amenity: "toilet", Name: str("WC"), Metres: 5},
				{Site: 1, Amenity: "cafe", Name: str("Eck"), Metres: 9},
			},
			CarParks: []parking.OSMCarPark{{ID: "way/1", Name: str("Parkhaus Am Markt"), At: at}, {ID: "way/2", Name: str("Oper"), At: geo.Point{Lat: 53.5, Lon: 10}}},
			Context:  []parking.ContextRow{{Kind: "street_parking", Family: "p1", ID: "x"}},
		},
	}
	q := parking.Query{At: at, RadiusM: 1000, Limit: 3, CarParks: true, Around: true}
	a, err := aroundAsk(t, f, q, "p1", parking.OSMFamily)
	if err != nil {
		t.Fatal(err)
	}
	want := parking.AroundQuery{At: at, Now: now, CarParks: true, CarParkRadiusM: 1000, CarParkFetch: 12, Context: true,
		ContextFamilies: []string{"p1"}, OSMFamilies: []string{parking.OSMFamily},
		Sites: []geo.Point{at, {Lat: 53.6, Lon: 10}}, Radii: []float64{500, 300}}
	if !reflect.DeepEqual(*f.gotAround, want) {
		t.Errorf("asked %+v", *f.gotAround)
	}
	r := a.Around
	if len(r.Nearby) != 2 || len(r.Nearby[0]) != 1 || r.Nearby[0][0].Amenity != "cafe" || len(r.Nearby[1]) != 0 {
		t.Errorf("nearby %+v", r.Nearby)
	}
	if !r.OSMAsked || len(r.OSMCarParks) != 1 || r.OSMCarParks[0].ID != "way/2" || !r.ContextRead || len(r.Context) != 1 {
		t.Errorf("around %+v", r)
	}
	if !slices.Equal(names(a.Families), []string{parking.OSMFamily, "p1"}) {
		t.Errorf("credited %v", names(a.Families))
	}
}

func TestParkingAroundOfAKeyWithoutOpenStreetMap(t *testing.T) {
	req.Covers(t, "SEC-030")
	f := &fake{parks: []parking.Site{{ID: "a", Family: "p1"}}}
	a, err := aroundAsk(t, f, parking.Query{Kind: "park_and_ride", CarParks: true, Around: true, Limit: 2}, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if f.gotAround.OSMFamilies != nil || f.gotAround.Pattern == nil || *f.gotAround.Pattern != parking.OSMParkAndRidePattern || f.gotAround.Context {
		t.Errorf("asked %+v", *f.gotAround)
	}
	if a.Around.OSMAsked || a.Around.ContextRead || len(a.Around.Nearby[0]) != 0 || !slices.Equal(names(a.Families), []string{"p1"}) {
		t.Errorf("around %+v, credited %v", a.Around, names(a.Families))
	}
}

func TestParkingAroundFailures(t *testing.T) {
	req.Covers(t, "SEC-032")
	f := &fake{parks: []parking.Site{{ID: "a", Family: "p1"}}, aroundErr: errStore}
	if _, err := aroundAsk(t, f, parking.Query{CarParks: true}, "p1"); !errors.Is(err, errStore) {
		t.Errorf("a failed read: %v", err)
	}
	// A row of a family the key may not read is refused.
	f = &fake{around: parking.AroundRead{Context: []parking.ContextRow{{Family: "p2"}}}}
	if _, err := aroundAsk(t, f, parking.Query{Around: true}, "p1"); !errors.Is(err, features.ErrGateViolation) {
		t.Errorf("a row outside the key: %v", err)
	}
}

func TestAPublishersWarningGoesWithItsFigures(t *testing.T) {
	req.Covers(t, "FR-PARK-003", "ENV-003")
	warn := provenance.Note{Kind: provenance.Condition, Text: "ungeprüft"}
	notes := map[string][]provenance.Note{"hh_parkhaeuser": {warn}}
	for _, c := range []struct {
		name string
		site parking.Site
		want []string
	}{
		{"with a figure", parking.Site{ID: "a", Family: "hh_parkhaeuser", Occupancy: free(3, "hh_parkhaeuser")}, []string{"ungeprüft"}},
		{"without one", parking.Site{ID: "a", Family: "hh_parkhaeuser"}, nil},
	} {
		a, err := aroundAsk(t, &fake{parks: []parking.Site{c.site}, notes: notes}, parking.Query{}, "hh_parkhaeuser")
		if err != nil {
			t.Fatal(err)
		}
		if got := provenance.Notes(a.Families, provenance.Condition); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v", c.name, got)
		}
	}
}
