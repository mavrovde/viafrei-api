package places

import (
	"context"
	"testing"
)

func addr(id, street, number string, postcode, place *string, lat, lon float64, literal int) AddressHit {
	return AddressHit{ID: id, Street: street, HouseNumber: number, Postcode: postcode, Place: place, Lat: lat, Lon: lon,
		ExtractDate: "2026-10-27", LiteralScore: literal, HouseNumberNorm: number}
}

func TestResolveAnAddressInATown(t *testing.T) {
	f := &fakeReads{addresses: map[string][]AddressHit{AddrByPlace: {
		addr("1", "Bahnhofstraße", "12", sp("36037"), sp("Fulda"), 50.55296, 9.68109, 2),
		addr("2", "Bahnhofstraße", "12", nil, sp("Fulda"), 50.55309, 9.6812, 2),
		addr("3", "Bahnhofstraße", "12", sp("36037"), sp("Fulda"), 50.553, 9.681, 1),
	}}}
	r, err := resolveWith(f, "Bahnhofstraße 12, Fulda", "de")
	if err != nil || r.Kind != ResolvedPoint || r.Label != "Bahnhofstraße 12, 36037 Fulda" || r.Via != "address" || r.SourceIDs[0] != "osm" {
		t.Fatalf("got %+v %v", r, err)
	}
}

func TestResolveAnAddressInTwoTownsIsAList(t *testing.T) {
	f := &fakeReads{addresses: map[string][]AddressHit{AddrByPostcode: {
		addr("1", "Hauptstraße", "1", sp("10001"), sp("Altdorf"), 50, 9, 1),
		addr("2", "Hauptstraße", "1", sp("10001"), sp("Neudorf"), 50.2, 9.2, 1),
	}}}
	r, err := resolveWith(f, "Hauptstr. 1, 10001", "en")
	if err != nil || r.Kind != ResolvedCandidates || len(r.Options) != 2 || r.Options[1].Label != "Hauptstraße 1, 10001 Neudorf" {
		t.Fatalf("got %+v %v", r, err)
	}
}

func TestResolveAnAddressByItsStreetAndTheNearestNumber(t *testing.T) {
	f := &fakeReads{addresses: map[string][]AddressHit{
		AddrStreetByPostcode: {addr("1", "Weg", "4-8", sp("10001"), nil, 50, 9, 1), addr("2", "Weg", "6a", sp("10001"), nil, 50, 9.001, 1)},
	}}
	r, err := resolveWith(f, "Weg 6, 10001 Altdorf", "de")
	if err != nil || r.Kind != ResolvedPoint || r.Label != "Weg 4-8, 10001" {
		t.Fatalf("got %+v %v", r, err)
	}
	f = &fakeReads{addresses: map[string][]AddressHit{
		AddrByPlacePrefix: {addr("1", "Weg", "6", nil, sp("Altdorf-Süd"), 50, 9, 1)},
	}}
	if r, err = resolveWith(f, "Weg 6 Altdorf", "de"); err != nil || r.Kind != ResolvedPoint || r.Label != "Weg 6, Altdorf-Süd" {
		t.Fatalf("prefix: got %+v %v", r, err)
	}
	f = &fakeReads{addresses: map[string][]AddressHit{
		AddrStreetByPlace: {addr("1", "Weg", "6", sp("10002"), sp("Altdorf"), 50, 9, 1), addr("2", "Weg", "6", sp("10001"), sp("Altdorf"), 50.1, 9, 1)},
	}}
	if r, err = resolveWith(f, "Weg 6b, 10001 Altdorf", "de"); err != nil || r.Kind != ResolvedPoint || r.Label != "Weg 6, 10001 Altdorf" {
		t.Fatalf("street in town: got %+v %v", r, err)
	}
}

func TestResolveAnAddressNotFound(t *testing.T) {
	r, err := resolveWith(&fakeReads{loaded: map[string]bool{LoadedAddresses: true}}, "Weg 6, Altdorf", "de")
	if err != nil || r.Kind != ResolvedNone || r.Reason != "address_not_found" {
		t.Fatalf("got %+v %v", r, err)
	}
	if r, err = resolveWith(&fakeReads{}, "Weg 6, Altdorf", "de"); err != nil || r.Reason != "house_number" {
		t.Fatalf("not loaded: got %+v %v", r, err)
	}
	if r, err = resolveWith(&fakeReads{}, "Weg 6", "de"); err != nil || r.Reason != "house_number" {
		t.Fatalf("no town: got %+v %v", r, err)
	}
}

func TestResolveAnAddressFailedReads(t *testing.T) {
	for _, read := range []string{"address:" + AddrByPostcode, "address:" + AddrStreetByPostcode, "loaded:" + LoadedAddresses} {
		f := &fakeReads{fail: map[string]bool{read: true}}
		if _, err := resolveWith(f, "Weg 6, 10001", "de"); err == nil {
			t.Errorf("%s: no error", read)
		}
	}
	for _, read := range []string{"address:" + AddrByPlace, "address:" + AddrByPlacePrefix, "address:" + AddrStreetByPlace} {
		f := &fakeReads{fail: map[string]bool{read: true}}
		if _, err := resolveWith(f, "Weg 6, Altdorf", "de"); err == nil {
			t.Errorf("%s: no error", read)
		}
	}
}

func TestHouseNumberMatch(t *testing.T) {
	for _, c := range [][3]string{
		{"6", "4,6", "contained"}, {"6b", "6a-6c", "contained"}, {"6", "6a-c", ""}, {"6", "2-10", "contained"}, {"5", "2-10", ""},
		{"6b", "6", "base"}, {"6b", "2-10", "base"}, {"6", "6a", "suffixed"}, {"6", "7", ""},
		{"6-8", "6", ""}, {"x", "6", ""}, {"6", "8-6", ""}, {"6", "5-8", "contained"},
	} {
		if got := houseNumberMatch(c[0], c[1]); got != c[2] {
			t.Errorf("%s in %s: %q", c[0], c[1], got)
		}
	}
}

func TestParseAddressQuery(t *testing.T) {
	if parseAddressQuery("  ") != nil || parseAddressQuery("Weg") != nil || parseAddressQuery("Weg 6") != nil {
		t.Fatal("read a non-address")
	}
	a := parseAddressQuery("Altdorf, Am Ring 3a")
	if a == nil || a.streetNorm != "amring" || a.houseNumberNorm != "3a" || *a.placeNorm != "altdorf" || a.postcode != nil {
		t.Fatalf("got %+v", a)
	}
	if normalizeStreet("Haupt Str") != "hauptstrasse" {
		t.Fatal(normalizeStreet("Haupt Str"))
	}
}

func TestResolveAStreetInATown(t *testing.T) {
	one := StreetHit{Street: "Unter den Linden", Place: sp("Berlin"), Addresses: 40, Lat: 52.517, Lon: 13.388}
	f := &fakeReads{streets: map[string][]StreetHit{StreetByPlace: {one}}}
	r, err := (&resolver{ctx: context.Background(), reads: f, lang: "de", memo: map[string][]GazetteerRow{}}).resolveStreet("Unter den Linden, Berlin")
	if err != nil || r == nil || r.Kind != ResolvedPoint || r.Label != "Unter den Linden, Berlin" {
		t.Fatalf("got %+v %v", r, err)
	}
	two := one
	two.Addresses, two.Lat = 3, 52.6
	f = &fakeReads{streets: map[string][]StreetHit{StreetByPostcode: {one, two, {Street: "Unter den Linden", Postcode: sp("10117"), Lat: 52.5}}}}
	rv := &resolver{ctx: context.Background(), reads: f, lang: "en", memo: map[string][]GazetteerRow{}}
	r, err = rv.resolveStreet("10117 Berlin, Unter den Linden")
	if err != nil || r == nil || r.Kind != ResolvedCandidates || r.Options[0].Label != "Unter den Linden, Berlin (40 addresses)" || r.Options[2].Label != "Unter den Linden, 10117" {
		t.Fatalf("list: got %+v %v", r, err)
	}
	for _, q := range []string{"Unter den Linden", "a, b, c", ", Berlin", "Unter den Linden, Berlin"} {
		if r, err := (&resolver{ctx: context.Background(), reads: &fakeReads{}, lang: "de"}).resolveStreet(q); err != nil || r != nil {
			t.Errorf("%q: got %+v %v", q, r, err)
		}
	}
	f = &fakeReads{fail: map[string]bool{"street:" + StreetByPlace: true}}
	if _, err := (&resolver{ctx: context.Background(), reads: f, lang: "de"}).resolveStreet("Weg, Altdorf"); err == nil {
		t.Fatal("no error")
	}
	if hits, err := (&resolver{ctx: context.Background(), reads: f}).findStreet("", nil, nil); err != nil || hits != nil {
		t.Fatal("empty street read")
	}
}

func TestNameMissReason(t *testing.T) {
	rv := &resolver{ctx: context.Background(), reads: &fakeReads{loaded: map[string]bool{LoadedGazetteer: true}}}
	if r, _ := rv.nameMissReason("x"); r.Reason != "not_found" {
		t.Fatal(r.Reason)
	}
	rv.reads = &fakeReads{}
	if r, _ := rv.nameMissReason("x"); r.Reason != "gazetteer_empty" {
		t.Fatal(r.Reason)
	}
	rv.reads = &fakeReads{fail: map[string]bool{"loaded:" + LoadedGazetteer: true}}
	if _, err := rv.nameMissReason("x"); err == nil {
		t.Fatal("no error")
	}
}

func FuzzParseAddressQuery(f *testing.F) {
	for _, s := range []string{"Am Ring 3a, 12345 Altdorf", "Altdorf, Am Ring 3a", "Hauptstraße 12 Köln", "Weg 6", ",,", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if a := parseAddressQuery(s); a != nil && (a.houseNumberNorm == "" || (a.postcode == nil && a.placeNorm == nil)) {
			t.Fatalf("parseAddressQuery(%q) = %+v: an address without its number or its town", s, a)
		}
	})
}
