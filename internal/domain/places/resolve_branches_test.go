package places

import "testing"

func TestRemainingBranches(t *testing.T) {
	is := loc(row("i", "island", "Sandoog", 54.5, 8.6, 0, "geonames", 1))
	t1 := loc(row("a", "city", "Sandoog", 54.51, 8.61, 300, "bkg_gvisys", 1))
	t2 := loc(row("b", "city", "Sandoog", 54.52, 8.62, 800, "bkg_gvisys", 1))
	if c := islandCompanions(is, []located{is, t1, t2}, "de"); len(c) != 1 || c[0].Lat != 54.52 {
		t.Fatalf("the biggest town: %+v", c)
	}
	far := loc(row("j", "island", "Sandoog", 54.55, 8.65, 0, "osm", 1))
	if c := islandCompanions(t1, []located{t1, far, is}, "de"); len(c) != 1 || c[0].Kind != "island" || c[0].SourceID != "geonames" {
		t.Fatalf("the town's island: %+v", c)
	}
	if labelsOr(nil, 0, t1) != "Sandoog" {
		t.Fatal("label fallback")
	}
	if sameAreaKeys(&contextArea{keys: map[string]bool{"1": true}}, &contextArea{keys: map[string]bool{"2": true}}) {
		t.Fatal("other keys")
	}
	city := loc(row("c", "city", "Au", 48, 11, 0, "geonames", 1))
	area := loc(row("d", "district", "Au", 48.01, 11, 0, "geonames", 1))
	if !liesInside(city, area, nil, englishFolds{}) {
		t.Fatal("inside by distance")
	}
	s1 := loc(row("s1", "stop", "Au", 48, 11, 0, "delfi_zhv", 1))
	s2 := loc(row("s2", "stop", "Au", 48, 11, 0, "delfi_zhv", 1))
	s2.Rank = 1
	if isAnotherViewOf(s1, s2, englishFolds{}, nil, nil) {
		t.Fatal("a higher-ranked row")
	}
	d1 := loc(row("d1", "district", "Au", 48, 11, 90_000, "bkg_gvisys", 1))
	d2 := loc(row("d2", "district", "Au", 48.05, 11.05, 90_000, "geonames", 1))
	if !isAnotherViewOf(d1, d2, englishFolds{}, nil, nil) {
		t.Fatal("the same area")
	}
	f := &fakeReads{cands: map[string][]GazetteerRow{"lindau": {row("l", "stop", "Lindau (Bayern)", 47.5, 9.7, 0, "delfi_zhv", 0.9)}}}
	if r, err := newResolver(f, "de").resolveOwnLabel("Lindau (Bayern)"); r != nil || err != nil {
		t.Fatal("a row of the label's own name")
	}
	g := &fakeReads{words: []string{"stadtmuseum"}}
	if k, err := newResolver(g, "de").hintedKinds("Stadtmuseum", nil); k != nil || err != nil {
		t.Fatalf("a place word only: %+v", k)
	}
}

func TestResolveAsWrittenPaths(t *testing.T) {
	// A text near the town's name, far off: the town's centre.
	lindau := cityRows("Lindau", 47.55, 9.69, 25_000, "09776116")
	g := &fakeReads{cands: map[string][]GazetteerRow{"lindau": lindau, "lindau dom": {row("x", "stop", "Lindow Dom", 52.95, 12.97, 0, "delfi_zhv", 0.8)}}}
	r, err := resolveWith(g, "Lindau Dom", "de")
	if err != nil || r.Kind != ResolvedPoint || r.CentreOf != "Dom" {
		t.Fatalf("centre: %+v %v (asked %v)", r, err, g.asked)
	}
	// A comma text read as written inside the town it names.
	h := &fakeReads{cands: map[string][]GazetteerRow{"lindau": lindau, "hafen lindau": {row("h", "stop", "Lindau Hafen", 47.545, 9.685, 0, "delfi_zhv", 0.9)}}}
	if r, err := resolveWith(h, "Hafen, Lindau", "de"); err != nil || r.Kind != ResolvedPoint || r.Label != "Lindau Hafen" {
		t.Fatalf("bounded: %+v %v (asked %v)", r, err, h.asked)
	}
	two := []GazetteerRow{row("h1", "stop", "Lindau Hafen", 47.545, 9.685, 0, "delfi_zhv", 0.9), row("h2", "stop", "Lindau Hafen Ost", 47.57, 9.71, 0, "delfi_zhv", 0.9)}
	h = &fakeReads{cands: map[string][]GazetteerRow{"lindau": lindau, "hafen lindau": two}, fail: map[string]bool{"lands": true}}
	if _, err := resolveWith(h, "Hafen, Lindau", "de"); err == nil {
		t.Fatalf("bounded list, Lands failing: no error (asked %v)", h.asked)
	}
}
