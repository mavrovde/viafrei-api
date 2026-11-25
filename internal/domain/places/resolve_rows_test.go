package places

import (
	"testing"
)

// loc is a located row of a test.
func loc(r GazetteerRow) located { return located{GazetteerRow: r, lat: *r.Lat, lon: *r.Lon} }

func locs(rows ...GazetteerRow) []located { return locate(rows) }

func ids(rows []located) string {
	s := ""
	for _, r := range rows {
		s += r.ID + " "
	}
	return s
}

func TestIslandsPlaced(t *testing.T) {
	if placeIslands(nil) != nil {
		t.Fatal("empty")
	}
	// An island whose only town of the name lies beyond the village reach
	// but within the island's: the island itself leads.
	island := row("i", "island", "Pellwarder", 54.5, 8.6, 0, "geonames", 1)
	far := row("t", "city", "Pellwarder", 54.62, 8.6, 900, "bkg_gvisys", 1)
	if got := ids(placeIslands(locs(island, far))); got != "i t " {
		t.Fatalf("got %s", got)
	}
	// An island no town of its name belongs to, beside a town of the name.
	away := row("c", "city", "Pellwarder Ost", 50, 10, 900, "bkg_gvisys", 1)
	if got := ids(placeIslands(locs(island, away))); got != "c " {
		t.Fatalf("dropped: got %s", got)
	}
}

func TestTranslationsAndViews(t *testing.T) {
	munich := loc(withEn(row("m", "city", "München", 48.1, 11.5, 1_500_000, "geonames", 1), "Munich"))
	en := loc(row("e", "city", "Munich", 48.1, 11.5, 0, "geonames", 1))
	if !isTranslationOf(munich, en, englishFolds{}) || !isTranslationOf(en, munich, englishFolds{}) {
		t.Fatal("translation either way")
	}
	if g := englishNamesOf(munich, englishFolds{}); !g["munich"] {
		t.Fatalf("own english name: %v", g)
	}
	a := loc(row("a1", "admin", "Saarland", 49.4, 6.9, 990_000, "bkg_gvisys", 1))
	b := loc(row("a2", "admin", "Saarland", 49.45, 6.95, 990_000, "geonames", 1))
	c := loc(row("a3", "admin", "Saarland", 49.45, 6.95, 0, "geonames", 1))
	if !isSameArea(a, b) || isSameArea(a, c) || isSameArea(a, loc(row("x", "district", "Saarland", 49.4, 6.9, 990_000, "x", 1))) {
		t.Fatal("same area")
	}
	town := loc(row("t", "city", "Lindau", 47.5, 9.7, 25_000, "bkg_gvisys", 1))
	stop := loc(row("s", "stop", "Lindau", 47.52, 9.7, 0, "delfi_zhv", 1))
	if !isSameNamedTown(stop, town) || isSameNamedTown(stop, stop) {
		t.Fatal("same named town either way")
	}
	if landKeyOf("admin", sp("123"), "x", nil) != "" || landKeyOf("admin", nil, "nirgendwo", nil) != "" || landKeyOf("admin", nil, "x", sp("Bavaria")) != "09" {
		t.Fatal("land key")
	}
	if wordsContain("", "a") || !wordsContain("a b", "b") {
		t.Fatal("words contain")
	}
	d := loc(withAdmin(row("d1", "district", "Saarland", 49.4, 6.9, 0, "bkg_gvisys", 1), "10041"))
	farD := loc(withAdmin(row("d2", "district", "Saarland", 52, 10, 0, "bkg_gvisys", 1), "10042"))
	if k := containerKeysOf(d, []located{d, farD}); len(k) != 1 || !k["10041"] {
		t.Fatalf("container keys %v", k)
	}
	if areaOf(loc(row("l", "admin", "Hinterland", 1, 1, 0, "x", 1))) != "" || areaOf(loc(row("l", "admin", "Bayern", 1, 1, 0, "x", 1))) != "09" {
		t.Fatal("area of a Land by name")
	}
	if radiusOf(loc(row("i", "island", "Sylt", 1, 1, 0, "x", 1))) != samePlaceKm {
		t.Fatal("island radius")
	}
}

func TestJudgeHelpers(t *testing.T) {
	rows := []GazetteerRow{
		withAdmin(row("c", "city", "Rostock", 54, 12, 205_000, "bkg_gvisys", 1), "13003001"),
		row("g", "city", "Rostock", 54, 12, 205_000, "geonames", 1),
	}
	k := withAdmin(row("k", "district", "Rostock", 54, 12, 205_000, "bkg_gvisys", 1), "13003")
	if isCityKreis(rows[0], rows) || !isCityKreis(k, rows) || isCityKreis(k, nil) {
		t.Fatal("city kreis by population")
	}
	if typoDistance("", "ab") != 2 || typoDistance("ab", "") != 2 {
		t.Fatal("typo distance of nothing")
	}
	a, b, c := loc(rows[0]), loc(rows[1]), loc(k)
	if got := ids(moveFirst([]located{a, b, c}, c)); got != "k c g " {
		t.Fatalf("move first %s", got)
	}
	if isKreisfrei(c, nil, englishFolds{}) {
		t.Fatal("a Kreis is no kreisfree town")
	}
	j := loc(row("j", "junction", "AS Lindau", 47.5, 9.7, 0, "autobahn", 0.8))
	st := loc(row("s", "station", "Lindau", 47.5, 9.7, 0, "db_stada", 1))
	far := loc(row("f", "stop", "Lindau Nord", 48, 9.7, 0, "delfi_zhv", 0.9))
	if !forecourtAnswers(st, []located{j}) || forecourtAnswers(st, []located{far}) || isJunctionNamedAfter(st, st) {
		t.Fatal("forecourt")
	}
	big := loc(withAdmin(row("b", "city", "Neustadt", 49, 8, 50_000, "bkg_gvisys", 1), "07316000"))
	other := loc(withAdmin(row("o", "city", "Neustadt", 50, 11, 40_000, "bkg_gvisys", 1), "09999000"))
	if kreisfreiAnswers(big, []located{other}, []located{big, other}) {
		t.Fatal("two kreisfree towns: no single answer")
	}
	area := loc(withAdmin(row("l", "district", "Neustadt", 49, 8, 0, "bkg_gvisys", 1), "07316"))
	if !liesInside(big, area, []located{big, area}, englishFolds{}) || liesInside(other, area, []located{other, area}, englishFolds{}) {
		t.Fatal("lies inside by keys")
	}
}

func TestFoldViewsOrders(t *testing.T) {
	// The exonym row gives way to its endonym of equal score and rank.
	en := withEn(row("e", "city", "Munich", 48.1, 11.5, 0, "geonames", 1), "Munich")
	de := withEn(row("d", "city", "München", 48.1, 11.5, 1_500_000, "bkg_gvisys", 1), "Munich")
	if got := foldViews(locs(en, de)); got[0].ID != "d" {
		t.Fatalf("endonym first: %s", ids(got))
	}
	// The Land's official row leads its Land.
	land := withAdmin(row("o", "admin", "Saarland", 49.4, 6.9, 0, "bkg_gvisys", 1), "10")
	name := row("n", "admin", "Saarland", 49.3, 7, 0, "geonames", 1)
	if got := foldViews(locs(name, land)); got[0].ID != "o" {
		t.Fatalf("land first: %s", ids(got))
	}
	// A big town inside a district of its name leads the district.
	dist := withAdmin(row("k", "district", "Rostock", 54, 12, 205_000, "bkg_gvisys", 1), "13003")
	town := withAdmin(row("t", "city", "Rostock", 54.01, 12.01, 205_000, "bkg_gvisys", 1), "13003000")
	if got := foldViews(locs(dist, town)); got[0].ID != "t" {
		t.Fatalf("town first: %s", ids(got))
	}
	// A station's town moves before it, once.
	st := row("s", "station", "Lindau", 47.5, 9.7, 0, "db_stada", 1)
	tw := row("c", "city", "Lindau", 47.501, 9.701, 25_000, "geonames", 1)
	if got := townsBeforeTheirStations(locs(st, tw, st)); ids(got) != "c s " {
		t.Fatalf("stations: %s", ids(got))
	}
}

func TestDecideBranches(t *testing.T) {
	// A populated town wins over a smaller namesake; the namesake is named.
	a := loc(withAdmin(row("a", "city", "Hofstetten", 47.9, 10.3, 600_000, "bkg_gvisys", 1), "09763000"))
	b := loc(withAdmin(row("b", "city", "Hofstetten", 50, 8.4, 25_000, "bkg_gvisys", 1), "06433011"))
	r := decide([]located{a, b}, "de", []located{a, b}, "hofstetten", map[string]string{}, "hofstetten", map[string]string{})
	if r.Kind != ResolvedPoint || len(r.Alternatives) != 1 {
		t.Fatalf("namesake: %+v", r)
	}
	// A stop field of one name answers as its first stop.
	s1 := loc(row("s1", "stop", "Lindau Bahnhof Nord", 47.5, 9.7, 0, "delfi_zhv", 0.8))
	s2 := loc(row("s2", "stop", "Lindau Bahnhof Nord", 47.501, 9.701, 0, "delfi_zhv", 0.8))
	if r := decide([]located{s1, s2}, "de", nil, "", nil, "lindau bahnhof nord", nil); r.Kind != ResolvedPoint || r.Label != "Lindau Bahnhof Nord" {
		t.Fatalf("field: %+v", r)
	}
	// A kreisfree town over its smaller same-named neighbour, named beside it.
	k := loc(withAdmin(row("k", "city", "Neustadt", 49.35, 8.14, 53_000, "bkg_gvisys", 1), "07316000"))
	n := loc(row("n", "city", "Neustadt an der Aisch", 49.6, 10.6, 12_000, "geonames", 1))
	r = decide([]located{k, n}, "de", []located{k, n}, "neustadt", nil, "neustadt", nil)
	if r.Kind != ResolvedPoint || len(r.Alternatives) != 1 {
		t.Fatalf("kreisfree: %+v", r)
	}
	// An island leads with its biggest town as companion.
	is := loc(row("i", "island", "Sandoog", 54.5, 8.6, 0, "geonames", 1))
	tw := loc(row("t", "city", "Sandoog", 54.51, 8.61, 800, "bkg_gvisys", 1))
	if c := islandCompanions(is, []located{is, tw}, "de"); len(c) != 1 || c[0].Kind != "city" {
		t.Fatalf("companions %+v", c)
	}
	if oneTownOf(a, nil) != nil {
		t.Fatal("one town of none")
	}
	empty := loc(row("e", "city", "Einödhof", 50.6, 9.6, 0, "geonames", 1))
	stop := loc(row("p", "stop", "Einödhof", 50.601, 9.6, 0, "delfi_zhv", 0.8))
	if got := oneTownOf(empty, []located{stop}); got == nil || got.ID != "e" {
		t.Fatalf("empty town of its stop: %+v", got)
	}
}

func TestJudgeWithoutAPoint(t *testing.T) {
	r := row("x", "city", "Nirgendwo", 0, 0, 0, "geonames", 1)
	r.Lat, r.Lon = nil, nil
	j := judgeCandidates([]GazetteerRow{r}, "Nirgendwo", "de", nil, nil)
	if j == nil || j.res.Reason != "no_coordinate" || !j.exact {
		t.Fatalf("got %+v", j)
	}
	d := withAdmin(row("d", "district", "Rostock", 54, 12, 205_000, "bkg_gvisys", 1), "13003")
	c := withAdmin(row("c", "city", "Rostock", 54, 12, 205_000, "bkg_gvisys", 1), "13003000")
	if j := judgeCandidates([]GazetteerRow{d, c}, "Landkreis Rostock", "de", nil, nil); j == nil || j.res.PlaceKind != "district" {
		t.Fatalf("a city kreis named a Landkreis stays: %+v", j)
	}
}
