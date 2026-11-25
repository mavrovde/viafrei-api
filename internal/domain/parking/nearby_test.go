package parking_test

import (
	"reflect"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
)

func sp(s string) *string { return &s }

func TestNearbyRadius(t *testing.T) {
	for _, c := range []struct {
		kind  parking.Kind
		along bool
		want  int
	}{
		{parking.KindGarage, false, 500}, {parking.KindParkAndRide, false, 500}, {parking.KindOther, false, 500},
		{parking.KindTruckParking, false, 300}, {parking.KindRestArea, false, 300}, {parking.KindGarage, true, 300},
	} {
		if got := parking.NearbyRadiusM(c.kind, c.along); got != c.want {
			t.Errorf("%s along %v: %d", c.kind, c.along, got)
		}
	}
}

func TestNearbyOfFollowsThePlatformRule(t *testing.T) {
	d := sp("2026-10-27")
	rows := []parking.NearbyRow{
		{Site: 1, Amenity: "toilet", Name: sp("WC"), Metres: 40},
		{Site: 1, Amenity: "cafe", Name: sp(" Café Eck "), Metres: 20, ExtractDate: d},
		{Site: 1, Amenity: "restaurant", Name: sp("Zur Post"), Metres: 20},
		{Site: 1, Amenity: "cafe", Name: sp("Zweites"), Metres: 30},
		{Site: 1, Amenity: "shop", Name: sp("  "), Metres: 10},
		{Site: 1, Amenity: "snack", Name: nil, Metres: 11},
		{Site: 1, Amenity: "fuel", Name: sp("Tanke"), Metres: 5},
		{Site: 2, Amenity: "shower", Name: sp("Dusche"), Metres: 1},
	}
	got := parking.NearbyOf(rows, 1, parking.Stated{Present: []string{"toilet"}})
	want := []parking.Nearby{{Amenity: "cafe", Name: "Café Eck", Metres: 20, ExtractDate: d}, {Amenity: "restaurant", Name: "Zur Post", Metres: 20}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v", got)
	}
	if got := parking.NearbyOf(rows, 3, parking.Stated{}); got == nil || len(got) != 0 {
		t.Fatalf("a site with none: %+v", got)
	}
	var many []parking.NearbyRow
	for i, a := range parking.NearbyAmenities {
		many = append(many, parking.NearbyRow{Site: 1, Amenity: a, Name: sp(a), Metres: i})
	}
	if got := parking.NearbyOf(many, 1, parking.Stated{}); len(got) != parking.NearbyPerSiteMax || got[4].Amenity != "cafe" {
		t.Fatalf("at most five: %+v", got)
	}
}

func TestOSMQuery(t *testing.T) {
	for _, c := range []struct {
		q       parking.Query
		ok      bool
		pattern bool
	}{
		{parking.Query{}, true, false}, {parking.Query{Kind: "car_park"}, true, false}, {parking.Query{Kind: "park_and_ride"}, true, true},
		{parking.Query{Kind: "truck"}, false, false}, {parking.Query{Kind: "rest_area"}, false, false}, {parking.Query{Road: "A7"}, false, false},
	} {
		p, ok := parking.OSMQuery(c.q)
		if ok != c.ok || (p != nil) != c.pattern || (p != nil && *p != parking.OSMParkAndRidePattern) {
			t.Errorf("%+v: %v %v", c.q, p, ok)
		}
	}
}

func TestContextAsked(t *testing.T) {
	if !parking.ContextAsked(parking.Query{}) {
		t.Fatal("a plain question reads it")
	}
	for _, q := range []parking.Query{{Road: "A1"}, {Kind: "car_park"}, {FreeMin: 1}, {Amenities: []string{"toilet"}}} {
		if parking.ContextAsked(q) {
			t.Errorf("%+v reads it", q)
		}
	}
}

// The platform's sameParkingName on the same pairs (osm-twins, 2026-11-25).
func TestSameParkingNameAsThePlatform(t *testing.T) {
	for _, c := range []struct {
		a, b *string
		want bool
	}{
		{sp("Parkhaus Am Markt"), sp("Am Markt"), true}, {sp("Tiefgarage Rathaus"), sp("Parkhaus Rathaus"), true},
		{sp("P+R Nord 2"), sp("P+R Nord 02"), false}, {sp("P+R Nord 2"), sp("P+R Nord 3"), false}, {sp("Parkplatz"), sp("Parkhaus"), false},
		{sp("Kaufhof Parkhaus"), sp("Galeria Kaufhof"), true}, {sp("Domgarage"), sp("Dom"), false}, {sp("Hauptbahnhof Süd"), sp("Hauptbahnhof Sued"), true},
		{nil, sp("x"), false}, {sp("Stadthalle"), sp("Stadthallengarage"), false}, {sp("Europaplatz"), sp("Europa"), true},
		{sp("Parkhaus II"), sp("Parkhaus III"), false}, {sp("Rathaus"), nil, false}, {sp("ÖPNV-Platz Ost"), sp("Oepnv Platz"), false},
		{sp("Rathausstraße"), sp("Rathaus"), true}, {sp("Messe West"), sp("Messe West"), true}, {sp("P & R Bahnhof"), sp("Park and Ride"), false},
		{sp("Rathaus 1"), sp("Rathaus Nord 1"), false}, {sp("Kino 01"), sp("Kino 1"), true},
	} {
		if got := parking.SameParkingName(c.a, c.b); got != c.want {
			t.Errorf("%v %v: %v", deref(c.a), deref(c.b), got)
		}
	}
}

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func TestFoldOSM(t *testing.T) {
	at := func(lat float64) geo.Point { return geo.Point{Lat: lat, Lon: 10} }
	// 0.0009° of latitude is about 100 m.
	rows := []parking.OSMCarPark{
		{ID: "way/1", Name: sp("Parkhaus Am Markt"), At: at(50)},
		{ID: "way/2", Name: sp("parkhaus am markt"), At: at(50.0009)},   // the same name within 150 m: folded
		{ID: "way/3", Name: nil, At: at(50.003)},                        // no name: kept by the name fold
		{ID: "way/4", Name: sp("Rathaus"), At: at(50.01)},               // a listed site's name within 200 m: a twin
		{ID: "way/5", Name: sp("Tiefgarage Am Markt"), At: at(50.0011)}, // the same parking name within 150 m of way/1: folded
		{ID: "way/6", Name: sp("Bahnhof Ost"), At: at(50.05)},           // within 75 m of a listed site: a twin
		{ID: "way/7", Name: sp("Stadthalle"), At: at(50.1)},
		{ID: "way/8", Name: sp("Oper"), At: at(50.2)},
	}
	held := []parking.Site{
		{Name: sp("Parkhaus Rathaus"), Location: at(50.0115)},
		{Name: nil, Location: at(50.0503)},
		{Name: sp("Anderes"), Location: at(50.0032)},
	}
	// way/3 is within 75 m of the third listed site.
	got := parking.FoldOSM(rows, held, 2)
	if len(got) != 2 || got[0].ID != "way/1" || got[1].ID != "way/7" {
		t.Fatalf("got %+v", got)
	}
	if got := parking.FoldOSM(nil, nil, 3); got == nil || len(got) != 0 {
		t.Fatalf("none: %+v", got)
	}
}

func TestOSMDataAsOf(t *testing.T) {
	if parking.OSMDataAsOf(nil) != nil || parking.OSMDataAsOf([]parking.OSMCarPark{{}}) != nil {
		t.Fatal("none states one")
	}
	got := parking.OSMDataAsOf([]parking.OSMCarPark{{ExtractDate: sp("2026-10-01")}, {}, {ExtractDate: sp("2026-10-27")}, {ExtractDate: sp("2026-10-02")}})
	if got == nil || *got != "2026-10-27" {
		t.Fatalf("got %v", got)
	}
}
