package places

import (
	"testing"
)

func cityRows(name string, lat, lon float64, pop int, ags string) []GazetteerRow {
	return []GazetteerRow{
		withAdmin(row("b-"+name, "city", name, lat, lon, pop, "bkg_gvisys", 1), ags),
		row("g-"+name, "city", name, lat+0.004, lon+0.004, pop, "geonames", 1),
	}
}

func poi(id, name, category, value string, lat, lon, score float64, tier int) PoiHit {
	return PoiHit{ID: id, Name: name, Category: category, CategoryValue: value, Lat: lat, Lon: lon, Score: score, Tier: tier, ExtractDate: "2026-10-27"}
}

func TestResolveAMainStation(t *testing.T) {
	f := &fakeReads{cands: map[string][]GazetteerRow{
		"leipzig hauptbahnhof": {withAdmin(row("z1", "stop", "Leipzig, Hauptbahnhof/Ostseite", 51.344, 12.382, 0, "delfi_zhv", 0.7), "14713000")},
		"leipzig hbf": {
			row("s1", "station", "Leipzig Hbf", 51.3455, 12.3821, 0, "db_stada", 1),
			withAdmin(row("z2", "stop", "Leipzig Hbf", 51.3454, 12.382, 0, "delfi_zhv", 1), "14713000"),
		},
	}}
	r, err := resolveWith(f, "Leipzig Hauptbahnhof", "de")
	if err != nil || r.Kind != ResolvedPoint || r.Label != "Leipzig Hbf" || r.PlaceKind != "station" {
		t.Fatalf("got %+v %v", r, err)
	}
	if r, err = resolveWith(f, "Leipzig main station", "en"); err != nil || r.Label != "Leipzig Hbf" {
		t.Fatalf("english: got %+v %v", r, err)
	}
}

func TestResolveAStationByTheTownAlone(t *testing.T) {
	f := &fakeReads{cands: map[string][]GazetteerRow{"fulda": {row("s1", "station", "Fulda", 50.5546, 9.6838, 0, "db_stada", 1)}}}
	var r Resolution
	var err error
	returnsWithin(t, "resolveWith of a station word", func() { r, err = resolveWith(f, "Fulda Bahnhof", "de") })
	if err != nil || r.Kind != ResolvedPoint || r.Label != "Fulda" || r.PlaceKind != "station" {
		t.Fatalf("got %+v %v", r, err)
	}
	f = &fakeReads{cands: map[string][]GazetteerRow{"frankfurt hbf": {row("s1", "station", "Frankfurt (Main) Hbf", 50.107, 8.663, 0, "db_stada", 0.6)}}}
	returnsWithin(t, "resolveWith of a main station", func() { r, err = resolveWith(f, "Frankfurt Hbf", "de") })
	if err != nil || r.Kind != ResolvedPoint || r.Label != "Frankfurt (Main) Hbf" {
		t.Fatalf("qualified: got %+v %v", r, err)
	}
}

func TestQualifiedMainStation(t *testing.T) {
	for _, c := range []struct {
		name, town string
		want       bool
	}{
		{"Frankfurt (Main) Hbf", "Frankfurt", true},
		{"Frankfurt (Main) Hbf", "Frankfurt am Main", true},
		{"Frankfurt (Main) Hbf", "Frankfurt Oder", false},
		{"Frankfurt (Oder) Hbf", "Frankfurt Main", false},
		{"Halle (Saale) Hbf", "Halle Saale extra", false},
		{"Frankfurt Hbf", "Frankfurt", false},
		{"München (Ost) Hbf", "Muenchen", true},
		{"Weil (Rhein) Hbf", "Weil", true},
		{"Weil (Rhein) Hbf", "", false},
	} {
		if got := isQualifiedMainStationOf(c.name, c.town); got != c.want {
			t.Errorf("%s / %s: %v", c.name, c.town, got)
		}
	}
	if withoutStationWord("Bahnhof") != nil || withoutStationWord("Altdorf") != nil {
		t.Error("station word alone read")
	}
	if p := withoutStationWord("Altdorf Bf."); p == nil || *p != "Altdorf" {
		t.Errorf("got %v", p)
	}
	if _, _, ok := townStationSpellings("Hbf"); ok {
		t.Error("spelled no town")
	}
}

func TestResolveATownWithAnEpithet(t *testing.T) {
	f := &fakeReads{cands: map[string][]GazetteerRow{
		"hansestadt rostock": {withAdmin(row("z1", "stop", "Rostock, Gartenstadt", 54.086, 12.088, 0, "delfi_zhv", 0.444), "13003000")},
		"rostock":            cityRows("Rostock", 54.0885, 12.136, 205307, "13003000"),
	}}
	r, err := resolveWith(f, "Hansestadt Rostock", "de")
	if err != nil || r.Kind != ResolvedPoint || r.Label != "Rostock" || r.AGS != "13003000" {
		t.Fatalf("got %+v %v", r, err)
	}
}

func TestResolveADistrictOfATown(t *testing.T) {
	f := &fakeReads{cands: map[string][]GazetteerRow{
		"berlin": cityRows("Berlin", 52.5177, 13.4055, 3685265, "11000000"),
		"mitte":  {withAdmin(row("b-mitte", "city", "Mitte", 52.5177, 13.4055, 0, "bkg_gvisys", 1), "11000000")},
	}}
	r, err := resolveWith(f, "Berlin Mitte", "de")
	if err != nil || r.Kind != ResolvedPoint || r.Label != "Mitte, Berlin" {
		t.Fatalf("got %+v %v", r, err)
	}
	if r, err = resolveWith(f, "Mitte, Berlin", "de"); err != nil || r.Kind != ResolvedPoint || r.Label != "Mitte, Berlin" {
		t.Fatalf("comma: got %+v %v", r, err)
	}
}

func TestResolveALandmarkOfATownAdjective(t *testing.T) {
	f := &fakeReads{
		cands: map[string][]GazetteerRow{"koln": cityRows("Köln", 50.9381, 6.9571, 1024621, "05315000")},
		pois: map[string][]PoiHit{
			PoiByPlace + ":dom": {poi("1", "Dom", "amenity", "place_of_worship", 50.9413, 6.958, 1, 6), poi("2", "Dom - Hotel", "tourism", "hotel", 50.9411, 6.9561, 0.5, 2)},
		},
	}
	r, err := resolveWith(f, "Kölner Dom", "de")
	if err != nil || r.Kind != ResolvedPoint || r.Label != "Dom, Köln" || r.Via != "poi" {
		t.Fatalf("got %+v %v", r, err)
	}
}

func TestResolveAThingTheTownDoesNotHoldIsItsCentre(t *testing.T) {
	f := &fakeReads{cands: map[string][]GazetteerRow{
		"regensburg":     cityRows("Regensburg", 49.0178, 12.0974, 153094, "09362000"),
		"regensburg dom": {withAdmin(row("b-r", "city", "Regensburg", 49.0178, 12.0974, 153094, "bkg_gvisys", 0.733), "09362000")},
	}}
	r, err := resolveWith(f, "Regensburg Dom", "en")
	if err != nil || r.Kind != ResolvedPoint || r.CentreOf != "Dom" || r.Label != `Regensburg (measured from the town centre — I found no "Dom" there)` {
		t.Fatalf("got %+v %v", r, err)
	}
}

func TestResolveTheCentreWordOfATown(t *testing.T) {
	f := &fakeReads{cands: map[string][]GazetteerRow{"stuttgart": cityRows("Stuttgart", 48.7784, 9.18, 632865, "08111000")}}
	r, err := resolveWith(f, "Stuttgart Innenstadt", "de")
	if err != nil || r.Kind != ResolvedPoint || r.Label != "Stuttgart, Innenstadt" {
		t.Fatalf("got %+v %v", r, err)
	}
	if base, hint, ok := splitCentreHint("Altdorf,  city   centre"); !ok || base != "Altdorf" || hint != "city centre" {
		t.Fatalf("%q %q %v", base, hint, ok)
	}
}

func TestResolveAPlaceOfInterestAnywhere(t *testing.T) {
	f := &fakeReads{
		pois:   map[string][]PoiHit{PoiAnywhere + ":brandenburger tor": {poi("1", "Brandenburger Tor", "tourism", "attraction", 52.5163, 13.3777, 1, 6)}},
		loaded: map[string]bool{LoadedGazetteer: true},
	}
	f.pois[PoiAnywhere+":brandenburger tor"][0].Place = sp("Berlin")
	r, err := resolveWith(f, "Brandenburger Tor", "de")
	if err != nil || r.Kind != ResolvedPoint || r.Label != "Brandenburger Tor, Berlin" || r.Via != "poi" {
		t.Fatalf("got %+v %v", r, err)
	}
}

func TestResolveNothingNamed(t *testing.T) {
	f := &fakeReads{loaded: map[string]bool{LoadedGazetteer: true}}
	r, err := resolveWith(f, "xyzzyplugh", "de")
	if err != nil || r.Kind != ResolvedNone || r.Reason != "not_found" || r.Query != "xyzzyplugh" {
		t.Fatalf("got %+v %v", r, err)
	}
}

func TestResolveFailedReadsOnTheWay(t *testing.T) {
	for _, c := range []struct{ q, read string }{
		{"Leipzig Hauptbahnhof", "candidates:leipzig hbf"},
		{"Berlin Mitte", "candidates:mitte"},
		{"Kölner Dom", "poi:" + PoiByPlace + ":dom"},
		{"Brandenburger Tor", "poi:" + PoiAnywhere + ":brandenburger tor"},
		{"xyzzyplugh", "loaded:" + LoadedGazetteer},
		{"Hansestadt Rostock", "candidates:rostock"},
		{"Stuttgart Innenstadt", "candidates:stuttgart"},
	} {
		f := &fakeReads{fail: map[string]bool{c.read: true}, cands: map[string][]GazetteerRow{
			"berlin": cityRows("Berlin", 52.5177, 13.4055, 3685265, "11000000"), "koln": cityRows("Köln", 50.9381, 6.9571, 1024621, "05315000"),
			"stuttgart": cityRows("Stuttgart", 48.7784, 9.18, 632865, "08111000"),
		}}
		if _, err := resolveWith(f, c.q, "de"); err == nil {
			t.Errorf("%s / %s: no error (asked %v)", c.q, c.read, f.asked)
		}
	}
}
