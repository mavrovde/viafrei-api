package places_test

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// A place's Kreis is said as the platform's place search says it.
func TestKreisOfSaysTheKreisAsThePlatform(t *testing.T) {
	names := map[string]string{
		"06534": "Marburg-Biedenkopf", "06633": "Landkreis Kassel", "05334": "Städteregion Aachen",
		"03241": "Region Hannover", "05315": "Köln", "09162": "München",
	}
	k := func(s string) *string { return &s }
	for _, c := range []struct {
		kind     places.Kind
		key      *string
		kreisKey string
		want     string
	}{
		{"municipality", k("06534014"), "06534", "Kreis Marburg-Biedenkopf"},
		{"municipality", k("06633001"), "06633", "Landkreis Kassel"},
		{"municipality", k("05334002"), "05334", "Städteregion Aachen"},
		{"municipality", k("03241001"), "03241", "Region Hannover"},
		{"municipality", k("05315000"), "", "kreisfreie Stadt"},
		{"locality", k("05315000"), "05315", "kreisfreie Stadt Köln"},
		{"station_area", k("09162000"), "09162", "kreisfreie Stadt München"},
		{places.KindJunction, k("06534014"), "06534", "Kreis Marburg-Biedenkopf"},
		{"municipality", k("07111001"), "07111", ""},
		{"municipality", k("11000000"), "", ""},
		{"municipality", k("0653401"), "", ""},
		{"municipality", nil, "", ""},
		{"district", k("06534014"), "", ""},
		{"state", k("06000000"), "", ""},
	} {
		got, key := places.KreisOf(c.kind, c.key, names), places.KreisKey(c.kind, c.key)
		if (got == nil) != (c.want == "") || got != nil && *got != c.want || (key == nil) != (c.kreisKey == "") || key != nil && *key != c.kreisKey {
			t.Errorf("%s %v: %v (key %v), want %q (key %q)", c.kind, c.key, got, key, c.want, c.kreisKey)
		}
	}
}
