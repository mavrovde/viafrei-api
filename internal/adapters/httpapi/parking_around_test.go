package httpapi

import (
	"reflect"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// TestParkingAround: what is beside a parking list is carried member for
// member, and a block not read is null.
func TestParkingAround(t *testing.T) {
	req.Covers(t, "FR-PARK-002")
	if parkingAround(nil) != nil || siteNearby(nil, 0) != nil || siteNearby(&parking.Around{}, 0) != nil {
		t.Fatal("nothing asked is null")
	}
	d := "2026-10-27"
	seen := time.Date(2026, 11, 25, 8, 0, 0, 0, time.UTC)
	n, name := "P1", "Oper"
	a := &parking.Around{
		Nearby:      [][]parking.Nearby{{{Amenity: "cafe", Name: "Eck", Metres: 9, At: geo.Point{Lat: 1, Lon: 2}, ExtractDate: &d}}, {}},
		OSMAsked:    true,
		OSMCarParks: []parking.OSMCarPark{{ID: "way/1", Name: &name, At: geo.Point{Lat: 3, Lon: 4}, DistanceM: 12.5, ExtractDate: &d}},
		ContextRead: true,
		Context: []parking.ContextRow{{Kind: "coach_parking", Family: "hh", ID: "x", Name: &n, Facts: map[string]any{"coaches": 3.0}, At: geo.Point{Lat: 5, Lon: 6},
			DistanceM: 99.4, LastSeenAt: seen, Total: 4, Variants: map[string]int{"parking": 4}, PartsIn: 1}},
	}
	got := parkingAround(a)
	want := &ParkingAround{
		OSMCarParks: []OSMCarPark{{ID: "way/1", Name: &name, Location: Position{Lat: 3, Lon: 4}, DistanceM: 13}},
		OSMDataAsOf: &d,
		RoadContext: []ParkingContextItem{{Kind: "coach_parking", SourceFamily: "hh", ID: "x", Name: &n, Facts: map[string]any{"coaches": 3.0},
			Location: Position{Lat: 5, Lon: 6}, DistanceM: 99, SeenAt: seen, KindTotal: 4, KindVariants: map[string]int{"parking": 4}, PartsInside: 1}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
	if got := siteNearby(a, 0); !reflect.DeepEqual(got, []NearbyAmenity{{Kind: "cafe", Name: "Eck", DistanceM: 9, Location: Position{Lat: 1, Lon: 2}, OSMDataAsOf: &d}}) {
		t.Errorf("nearby %+v", got)
	}
	if got := siteNearby(a, 1); got == nil || len(got) != 0 {
		t.Errorf("a site with none: %+v", got)
	}
	if got := parkingAround(&parking.Around{}); got.OSMCarParks != nil || got.RoadContext != nil || got.OSMDataAsOf != nil {
		t.Errorf("not read: %+v", got)
	}
}
