package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// TestParkingAroundIsServed: listParkingOptions carries meta.around and each
// site's nearby amenities, getNearby the OpenStreetMap car parks, each
// answer matching the contract.
func TestParkingAroundIsServed(t *testing.T) {
	req.Covers(t, "FR-PARK-002", "FR-BRIEF-005")
	d := "2026-10-27"
	name := "Oper"
	a := &parking.Around{
		Nearby:      [][]parking.Nearby{{{Amenity: "cafe", Name: "Eck", Metres: 9, At: geo.Point{Lat: 1, Lon: 2}, ExtractDate: &d}}, {}},
		OSMAsked:    true,
		OSMCarParks: []parking.OSMCarPark{{ID: "way/1", Name: &name, At: geo.Point{Lat: 3, Lon: 4}, DistanceM: 12.5, ExtractDate: &d}},
		ContextRead: true,
		Context: []parking.ContextRow{{Kind: "coach_parking", Family: "src-alpha-parking", ID: "x", Facts: map[string]any{"coaches": 3.0},
			LastSeenAt: time.Date(2026, 11, 25, 8, 0, 0, 0, time.UTC), Total: 1, PartsIn: 1}},
	}
	// Both operations carry it, and each answer matches the contract.
	f := everyAnswer()
	f.parking.Around = a
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/parking-options?at=52.52,13.405", bearer)
	for _, want := range []string{`"around":{"osmCarParks":[{"id":"way/1"`, `"osmDataAsOf":"2026-10-27"`, `"roadContext":[{"kind":"coach_parking"`, `"nearbyAmenities":[{"kind":"cafe"`} {
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), want) {
			t.Errorf("listParkingOptions lacks %s: %d %s", want, w.Code, w.Body)
		}
	}
	pk := f.parking
	f.nearby = features.NearbyAnswer{Parking: &pk, Meta: features.Answer[struct{}]{At: fixedNow}, Parts: []features.PartResult{{Part: "parking", Status: features.StatusOK}}}
	w = do(t, h, http.MethodGet, "/v1/nearby?at=50.94,6.96&categories=parking", bearer)
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"osmParking":[{"id":"way/1"`) {
		t.Errorf("getNearby: %d %s", w.Code, w.Body)
	}
	pk.Around = nil
	w = do(t, h, http.MethodGet, "/v1/nearby?at=50.94,6.96&categories=parking", bearer)
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), `"osmParking"`) {
		t.Errorf("getNearby without: %d %s", w.Code, w.Body)
	}
}
