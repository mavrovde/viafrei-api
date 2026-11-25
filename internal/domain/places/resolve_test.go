package places

import (
	"testing"
)

func hamburgReads() *fakeReads {
	hh := withEn(row("g1", "city", "Hamburg", 53.55073, 9.99302, 1973896, "geonames", 1), "Hamburg")
	return &fakeReads{cands: map[string][]GazetteerRow{"hamburg": {
		hh,
		withAdmin(row("b1", "city", "Hamburg", 53.5511, 9.9969, 1862565, "bkg_gvisys", 1), "02000000"),
		withAdmin(row("b2", "district", "Hamburg", 53.5511, 9.9969, 1862565, "bkg_gvisys", 1), "02000"),
		row("g2", "district", "Hamburg, Freie und Hansestadt", 53.55, 10, 1862565, "geonames", 1),
		withAdmin(row("b3", "admin", "Hamburg", 53.567, 10.028, 1862565, "bkg_gvisys", 1), "02"),
		row("s1", "station", "Hamburg-Harburg", 53.456, 9.992, 0, "db_stada", 0.727),
	}}}
}

func TestResolveATownByItsName(t *testing.T) {
	r, err := resolveWith(hamburgReads(), "Hamburg", "de")
	if err != nil || r.Kind != ResolvedPoint || r.Label != "Hamburg" || r.PlaceKind != "city" || r.AGS != "02000000" || r.Lat != 53.55073 {
		t.Fatalf("got %+v %v", r, err)
	}
	if len(r.SourceIDs) != 1 || r.SourceIDs[0] != "geonames" {
		t.Fatalf("credits %v", r.SourceIDs)
	}
}

func syltReads() *fakeReads {
	return &fakeReads{
		cands: map[string][]GazetteerRow{"sylt": {
			withAdmin(row("b1", "city", "Sylt", 54.90786, 8.30875, 14167, "bkg_gvisys", 1), "01054168"),
			withEn(row("g1", "city", "Sylt", 54.883, 8.35, 14167, "geonames", 1), "Sylt"),
			withEn(row("g2", "island", "Sylt", 54.90439, 8.33819, 0, "geonames", 1), "Sylt"),
			row("s1", "station", "Morsum (Sylt)", 54.872, 8.432, 0, "db_stada", 0.417),
			withAdmin(row("z1", "stop", "Hörnum(Sylt)", 54.758, 8.294, 0, "delfi_zhv", 0.417), "01054046"),
		}},
		kreise: map[string]string{"01054": "Nordfriesland"},
	}
}

func TestResolveAnIslandTownNamesTheIsland(t *testing.T) {
	for lang, want := range map[string]string{"de": "Sylt (Stadt/Gemeinde), auch: Sylt (Insel)", "en": "Sylt (town), also: Sylt (island)"} {
		r, err := resolveWith(syltReads(), "Sylt", lang)
		if err != nil || r.Kind != ResolvedPoint || r.Label != want || len(r.Alternatives) != 1 || r.Alternatives[0].Kind != "island" {
			t.Fatalf("%s: got %+v %v", lang, r, err)
		}
		if ChosenName(r, lang) != r.Alternatives[0].Label[:4]+r.Label[4:len(r.Label)-len(", "+alsoWord(lang)+": "+r.Alternatives[0].Label)] {
			t.Fatalf("%s: name %q", lang, ChosenName(r, lang))
		}
	}
}

func TestResolveAForeignTownIsOutsideGermany(t *testing.T) {
	f := &fakeReads{cands: map[string][]GazetteerRow{"paris": {row("g1", "city", "Parin", 53.913, 11.189, 0, "geonames", 0.5)}}}
	r, err := resolveWith(f, "Paris", "en")
	if err != nil || r.Kind != ResolvedNone || r.Reason != "outside_germany" || r.Query != "Paris" {
		t.Fatalf("got %+v %v", r, err)
	}
}

func TestResolveNothingAsked(t *testing.T) {
	r, err := resolveWith(&fakeReads{}, "  ", "de")
	if err != nil || r.Kind != ResolvedNone || r.Reason != "not_found" {
		t.Fatalf("got %+v %v", r, err)
	}
}

func TestResolveAnEnglishNameReadsTheGermanOne(t *testing.T) {
	f := &fakeReads{cands: map[string][]GazetteerRow{"munchen": {withAdmin(row("b1", "city", "München", 48.137, 11.575, 1512491, "bkg_gvisys", 1), "09162000")}}}
	r, err := resolveWith(f, "Munich", "en")
	if err != nil || r.Kind != ResolvedPoint || r.Label != "München" {
		t.Fatalf("got %+v %v", r, err)
	}
	if name, q, ok := germanNameOf("Munich, Bavaria"); !ok || name != "München" || q != "München, Bavaria" {
		t.Fatalf("comma: %q %q %v", name, q, ok)
	}
}

func TestResolveAFailedReadIsTheError(t *testing.T) {
	f := hamburgReads()
	f.fail = map[string]bool{"candidates:hamburg": true}
	if _, err := resolveWith(f, "Hamburg", "de"); err == nil {
		t.Fatal("no error")
	}
}

func TestResolvedKinds(t *testing.T) {
	for _, c := range [][3]string{
		{"city", "", "municipality"}, {"admin", "", "region"}, {"station", "", "station_area"}, {"road", "", "motorway"},
		{"district", "", "district"}, {"island", "", "island"}, {"stop", "", "stop"}, {"junction", "", "junction"}, {"motorway", "", "motorway"},
		{"", "poi", "poi"}, {"", "address", "address"}, {"", "gazetteer", "other"},
	} {
		if got := ResolvedKind(c[0], c[1], ""); got != c[2] {
			t.Errorf("%v: %s", c, got)
		}
	}
	if got := ResolvedKind("admin", "", "01"); got != "state" {
		t.Errorf("a Land: %s", got)
	}
	if ChosenName(Resolution{Label: "Hamburg"}, "de") != "Hamburg" {
		t.Error("no alternatives")
	}
}
