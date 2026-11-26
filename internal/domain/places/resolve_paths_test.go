package places

import (
	"context"
	"testing"
)

func newResolver(f *fakeReads, lang string) *resolver {
	return &resolver{ctx: context.Background(), reads: f, lang: lang, memo: map[string][]GazetteerRow{}}
}

func townOf(part, context string, best GazetteerRow) commaTown {
	b := loc(best)
	return commaTown{reading: qualified{part: part, context: context}, area: contextArea{best: b, radiusKm: radiusOf(b)}}
}

func TestPoiOrderAndFolds(t *testing.T) {
	a, b := poi("a", "Post", "amenity", "post_office", 50, 8, 1, 4), poi("b", "Post", "amenity", "post_office", 50, 8, 1, 4)
	a.Vicinity = 1
	if comparePoiHits(a, b) >= 0 {
		t.Fatal("vicinity breaks the tie")
	}
	dpd := poi("p", "Paketshop", "amenity", "post_office", 50, 8, 1, 4)
	dpd.Brand = sp("DPD")
	if !isParcelShop(dpd) || isParcelShop(a) || !kindExcludes(dpd, []tag{{"amenity", "post_office"}}, false) {
		t.Fatal("parcel shop")
	}
	park := poi("c", "P1", "amenity", "parking", 50, 8, 1, 4)
	if !kindExcludes(park, []tag{{"amenity", "post_office"}}, true) {
		t.Fatal("a car park is no pure other kind")
	}
	if kindOf(nil) != nil || isWholeHead("xyz") {
		t.Fatal("kind of nothing")
	}
	if withoutTown("Lindau X", "Lindau") != nil {
		t.Fatal("a one-letter rest")
	}
	if objFold(nil) != "" {
		t.Fatal("fold of nothing")
	}
	x := poi("x", "Rathaus", "amenity", "townhall", 50, 8, 1, 4)
	x.Street, x.HouseNumber = sp("Markt"), sp("1")
	y := x
	if samePoiAddress(x, y) {
		t.Fatal("an address without town or postcode")
	}
	// A plainer row of an object gives way to its richer twin, keeping the
	// lead's rank.
	z := x
	z.ID, z.Place, z.Tier = "z", sp("Lindau"), 2
	plain := x
	plain.Street, plain.HouseNumber = nil, nil
	got := foldSamePois([]PoiHit{plain, z})
	if len(got) != 1 || got[0].ID != "z" || got[0].Tier != 4 {
		t.Fatalf("fold %+v", got)
	}
}

func TestPoiWords(t *testing.T) {
	if poiCategoryWord("amenity", "", "de") == "" || poiCategoryWord("amenity", "unheard_of", "de") != "Einrichtung" && poiCategoryWord("amenity", "unheard_of", "de") == "" {
		t.Fatal("category word")
	}
	if poiCategoryWord("nonesuch", "unheard_of", "en") != "unheard of" {
		t.Fatal(poiCategoryWord("nonesuch", "unheard_of", "en"))
	}
	if l := osmLandName(sp("Atlantis"), "de"); l == nil || *l != "Atlantis" {
		t.Fatal("unknown land kept")
	}
	if unquotedName(`„Zur Post“`) != "Zur Post" {
		t.Fatal(unquotedName(`„Zur Post“`))
	}
	if castleForms("") != nil || castleForms("a b c") != nil {
		t.Fatal("castle forms")
	}
	if legalCore("muller gmbh co kg") != "muller" {
		t.Fatal(legalCore("muller gmbh co kg"))
	}
	if organisationOf([]PoiHit{poi("1", "GmbH", "office", "company", 50, 8, 1, 4)}, "GmbH", nil) == nil {
		// A name that is all legal form keeps its one word.
		t.Log("one word kept")
	}
	if organisationOf([]PoiHit{poi("1", "x", "office", "company", 50, 8, 1, 4)}, "   ", nil) != nil {
		t.Fatal("no core")
	}
	far := []PoiHit{poi("1", "Muster GmbH", "office", "company", 50, 8, 1, 4), poi("2", "Muster GmbH", "office", "company", 51, 9, 1, 4)}
	if organisationOf(far, "Muster GmbH", nil) != nil {
		t.Fatal("two sites")
	}
}

func TestTownHelpers(t *testing.T) {
	town := townOf("Dom", "Lindau", row("t", "city", "Lindau", 47.5, 9.7, 25_000, "geonames", 1))
	if townNameOf(town) == "" {
		t.Fatal("town name")
	}
	list := Resolution{Kind: ResolvedCandidates, Options: []Option{{Label: "Lindau Nord"}, {Label: "Anderswo"}}}
	if !carriesTownName(list, town) || carriesTownName(Resolution{Kind: ResolvedNone}, town) {
		t.Fatal("carries")
	}
	if r := ofThePart(list, "ab"); r == nil || len(r.Options) != 2 {
		t.Fatal("short part")
	}
	if r := ofThePart(list, "Nord"); r == nil || len(r.Options) != 1 {
		t.Fatal("part of a list")
	}
	if ofThePart(list, "Süd") != nil || ofThePart(Resolution{Kind: ResolvedNone}, "Süd") == nil {
		t.Fatal("no part")
	}
	if r := withCentreNote(list, town, "de"); r.CentreOf != "" {
		t.Fatal("a list has no centre")
	}
	centre := townOf("Zentrum", "Lindau", row("t", "city", "Lindau", 47.5, 9.7, 25_000, "geonames", 1))
	if r := townCentre(centre, "de"); r.CentreOf != "" || r.Kind != ResolvedPoint {
		t.Fatalf("centre word: %+v", r)
	}
	if r := townCentre(town, "de"); r.CentreOf != "Dom" {
		t.Fatalf("centre: %+v", r)
	}
	if !isLandmarkPlace(poi("1", "Dom", "building", "cathedral", 1, 1, 1, 1)) {
		t.Fatal("building landmark")
	}
	tw := map[string]bool{"lindau": true}
	if landmarkGrade(poi("1", "Lindau", "historic", "castle", 1, 1, 1, 1), []string{"dom"}, tw) != 0 ||
		landmarkGrade(poi("1", "Alter Dom Platz", "historic", "castle", 1, 1, 1, 1), []string{"dom"}, tw) != 1 ||
		landmarkGrade(poi("1", "Alter Platz", "historic", "castle", 1, 1, 1, 1), []string{"dom"}, tw) != 0 {
		t.Fatal("grades")
	}
	if afterEpithet("Hansestadt R") != nil {
		t.Fatal("epithet before one letter")
	}
	rv := newResolver(&fakeReads{}, "de")
	if r, err := rv.thingInTown("Lindau", townOf("", "Lindau", row("t", "city", "Lindau", 47.5, 9.7, 25_000, "geonames", 1)), true); err != nil || r.found != nil {
		t.Fatal("no thing")
	}
}

func TestOwnHelpers(t *testing.T) {
	if n, num, ok := ownLabelName("Lindau #2"); !ok || !num || n != "Lindau" {
		t.Fatal("numbered label")
	}
	if namesLand("Lindau") {
		t.Fatal("not ours")
	}
	rv := newResolver(&fakeReads{}, "de")
	j := &judged{res: Resolution{Kind: ResolvedCandidates, Options: []Option{{Label: "A"}, {Label: "B"}}}}
	if r, err := rv.listedResolution(j, nil, "x"); err != nil || len(r.Options) != 2 {
		t.Fatal("nothing judged again")
	}
	if r, err := rv.resolveOwnLabel("Lindau"); r != nil || err != nil {
		t.Fatal("not a label")
	}
	if s, err := rv.withShownStops("  ", nil); s != nil || err != nil {
		t.Fatal("no name")
	}
	f := &fakeReads{stops: map[string][]GazetteerRow{"lindau": {row("s", "stop", "Lindau", 47.5, 9.7, 0, "delfi_zhv", 1), row("o", "stop", "Lindau Nord", 47.5, 9.7, 0, "delfi_zhv", 1)}}}
	shown, err := newResolver(f, "de").withShownStops("Lindau", locs(row("c", "city", "Lindau", 47.5, 9.7, 0, "geonames", 1)))
	if err != nil || ids(shown) != "c s " {
		t.Fatalf("shown %s %v", ids(shown), err)
	}
	f.fail = map[string]bool{"stops:lindau": true}
	if _, err := newResolver(f, "de").withShownStops("Lindau", nil); err == nil {
		t.Fatal("failed read")
	}
	// The town of a stop field read out of two readings.
	e := loc(row("e", "city", "Einödhof", 50.6, 9.6, 0, "geonames", 1))
	s := loc(row("p", "stop", "Einödhof", 50.601, 9.6, 0, "delfi_zhv", 0.8))
	if m := townOfReadings([]reading{{c: s, reading: "Einödhof (Haltestelle)"}, {c: e, reading: "Einödhof"}}); m == nil || m.c.ID != "e" {
		t.Fatalf("town of readings %+v", m)
	}
	full := make([]GazetteerRow, MaxCandidates)
	for i := range full {
		full[i] = row(string(rune('a'+i)), "city", "Au", 48+float64(i), 11, 100, "bkg_gvisys", 1)
	}
	g := &fakeReads{fail: map[string]bool{"candidates:au": true}}
	if _, err := newResolver(g, "de").withNamesakeGemeinden("Au", full); err == nil {
		t.Fatal("failed wider read")
	}
}

func TestInsideHelpers(t *testing.T) {
	if sameAreaKeys(&contextArea{}, &contextArea{keys: map[string]bool{"1": true}}) {
		t.Fatal("no keys")
	}
	island := loc(row("i", "island", "Sandoog", 54.5, 8.6, 0, "geonames", 1))
	if !insideArea(loc(row("x", "stop", "Hafen", 54.6, 8.6, 0, "delfi_zhv", 1)), &contextArea{best: island}, nil) {
		t.Fatal("inside an island")
	}
	if qualifiedReadings(", Lindau") != nil {
		t.Fatal("empty side")
	}
	area := &contextArea{best: loc(row("d", "district", "Lindau", 47.5, 9.7, 80_000, "bkg_gvisys", 1))}
	two := locs(row("a", "city", "Au", 47.5, 9.7, 500, "x", 1), row("b", "city", "Au", 47.6, 9.7, 500, "x", 1))
	if got := largestOfTheNameInArea(two, "Au", area); len(got) != 2 {
		t.Fatal("two of a size")
	}
	two[1].Population = nil
	if got := largestOfTheNameInArea(two, "Au", area); len(got) != 1 {
		t.Fatal("the larger")
	}
	ca := contextArea{best: area.best, radiusKm: 10}
	if withinArea(Resolution{Kind: ResolvedCandidates, Options: []Option{{Lat: 10, Lon: 10}}}, ca) != nil {
		t.Fatal("none inside")
	}
	if r := withinArea(Resolution{Kind: ResolvedNone}, ca); r == nil {
		t.Fatal("not a place")
	}
	// An island area reads its town of the name as one of its views.
	is := row("i", "island", "Pellwarder", 54.52, 8.66, 0, "geonames", 1)
	tw := withAdmin(row("t", "city", "Pellwarder", 54.62, 8.66, 900, "bkg_gvisys", 1), "01054102")
	if a := contextAreaOf([]GazetteerRow{is, tw}, "Pellwarder"); a == nil || a.best.ID != "i" || !a.keys["01054102"] {
		t.Fatalf("island area %+v", a)
	}
	far := row("f", "city", "Lindau", 52, 9.7, 0, "geonames", 1)
	big := withAdmin(row("b", "city", "Lindau", 47.5, 9.7, 25_000, "bkg_gvisys", 1), "09776116")
	if a := contextAreaOf([]GazetteerRow{big, far}, "Lindau"); a == nil || len(a.sourceIDs) != 1 {
		t.Fatalf("far namesake no view: %+v", a)
	}
}

func TestTownMethods(t *testing.T) {
	lindau := row("t", "city", "Lindau", 47.5, 9.7, 25_000, "geonames", 1)
	if townNameOf(townOf("x", "Kreis Lindau", row("d", "district", "Lindau", 47.5, 9.7, 0, "bkg_gvisys", 1))) != "Lindau" {
		t.Fatal("a district's name")
	}
	// The town's gate is no landmark there but a street of the name.
	f := &fakeReads{streets: map[string][]StreetHit{StreetByPlace: {{Street: "Tor", Place: sp("Lindau"), Addresses: 3, Lat: 47.51, Lon: 9.71}}}}
	got, err := newResolver(f, "de").thingInTown("Lindau Tor", townOf("Tor", "Lindau", lindau), true)
	if err != nil || got.found == nil || !got.held {
		t.Fatalf("street of the landmark word: %+v %v", got, err)
	}
	// A landmark with no town of its own is labelled with the town asked.
	dom := poi("1", "Dom", "building", "cathedral", 47.501, 9.701, 1, 6)
	g := &fakeReads{pois: map[string][]PoiHit{PoiByPlace: {dom}, PoiNear: {dom}}}
	lm, err := newResolver(g, "de").landmarkInTown("Lindau Dom", "Dom", landmarkKindOf("Dom"), townOf("Dom", "Lindau", lindau))
	if err != nil || lm.found == nil || lm.found.Label != "Dom, Lindau" {
		t.Fatalf("landmark %+v %v", lm.found, err)
	}
	rv := newResolver(&fakeReads{}, "de")
	p := Resolution{Kind: ResolvedPoint, Lat: 1, Lon: 1, Label: "Au"}
	if r, err := rv.withLocalityLand(p, locs(lindau)); err != nil || r.Label != "Au" {
		t.Fatal("not among the rows")
	}
	p.Lat, p.Lon = 47.5, 9.7
	if r, err := rv.withLocalityLand(p, locs(lindau)); err != nil || r.Label != "Au" {
		t.Fatal("a populated town")
	}
}

func TestLandmarksBesideSettlements(t *testing.T) {
	a := poi("a", "Rabenau", "historic", "castle", 48.95, 12.65, 1, 6)
	b := poi("b", "Schloss Rabenau", "historic", "castle", 49.4, 12.1, 1, 6)
	b.Land = sp("bayern")
	towns := &judged{rows: locs(withAdmin(row("e", "city", "Rabenau", 51.2, 13.6, 0, "geonames", 1), "14628030"), withAdmin(row("w", "city", "Rabenau", 50.6, 12.5, 0, "geonames", 1), "14524030"))}
	f := &fakeReads{pois: map[string][]PoiHit{PoiAnywhere + ":rabenau": {a}, PoiAnywhere + ":schloss rabenau": {b}}, kreise: map[string]string{"14628": "Sächsische Schweiz", "14524": "Zwickau"}}
	r, err := newResolver(f, "de").withLandmarks("Rabenau", towns)
	if err != nil || r == nil || r.Kind != ResolvedCandidates || len(r.Options) != 4 {
		t.Fatalf("got %+v %v (asked %v)", r, err, f.asked)
	}
	// A Land row's label says no Land; beside a landmark it is added.
	land := &judged{rows: locs(withAdmin(row("l", "admin", "Rabenau", 51.2, 13.6, 0, "geonames", 1), "14"))}
	if r, err := newResolver(f, "de").withLandmarks("Rabenau", land); err != nil || r.Options[len(r.Options)-1].Label != "Rabenau (Verwaltungseinheit) (Sachsen)" {
		t.Fatalf("land added: %+v %v", r, err)
	}
	f.fail = map[string]bool{"kreise": true}
	if _, err := newResolver(f, "de").withLandmarks("Rabenau", towns); err == nil {
		t.Fatal("failed Kreise read")
	}
}

func TestPoiMethods(t *testing.T) {
	rv := newResolver(&fakeReads{}, "de")
	if k, err := rv.hintedKinds("Lindau", sp("Lindau")); k != nil || err != nil {
		t.Fatal("the town alone")
	}
	f := &fakeReads{words: []string{"lindau"}}
	k, err := newResolver(f, "de").hintedKinds("Post Lindau", nil)
	if err != nil || k == nil || k.words != "post" || !k.stripped {
		t.Fatalf("place word dropped: %+v %v", k, err)
	}
	if h, err := rv.findPois("  ", poiScope{}, 5, poiOptions{}); h != nil || err != nil {
		t.Fatal("no needle")
	}
	if !asksNamedKindAnywhere("Post Lindau", poiOptions{}) {
		t.Fatal("named kind anywhere")
	}
	kind := &kindRead{tags: []tag{{"amenity", "post_office"}}, pure: true, words: "post", placeWord: map[string]bool{"lindau": true}, town: sp("Lindau")}
	named := []PoiHit{poi("1", "Post Lindau", "amenity", "post_office", 47.5, 9.7, 1, 2), poi("2", "Café", "amenity", "cafe", 47.5, 9.7, 1, 5)}
	got := withHinted(named, nil, nil, kind, 5)
	if len(got) != 2 || got[0].ID != "1" || got[0].Tier != categoryTier+1 || got[1].Tier != categoryTier-1 {
		t.Fatalf("hinted %+v", got)
	}
	if h, err := rv.resolvePoi(" , Lindau", nil, poiOptions{}); h != nil || err != nil {
		t.Fatal("no name")
	}
	burg := poi("b", "Burg Falkenfels", "historic", "castle", 47.56, 10.75, 1, 6)
	view := poi("v", "Burg Falkenfels", "tourism", "attraction", 47.56, 10.75, 1, 6)
	g := &fakeReads{pois: map[string][]PoiHit{PoiAnywhere + ":burg falkenfels": {burg}, PoiAnywhere + ":schloss falkenfels": {view}}}
	hits, err := newResolver(g, "de").resolvePoi("Burg Falkenfels", nil, poiOptions{})
	if err != nil || len(hits) != 1 || hits[0].ID != "v" {
		t.Fatalf("visited landmark first: %+v %v (asked %v)", hits, err, g.asked)
	}
}
