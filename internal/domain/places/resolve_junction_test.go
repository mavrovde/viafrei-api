package places

import (
	"context"
	"testing"
)

func junctionReads() *fakeReads {
	road := "A7"
	gaz := row("a1", "junction", "Kassel-Mitte", 51.3, 9.5, 0, "autobahn", 0.8)
	gaz.Road = &road
	return &fakeReads{
		cands: map[string][]GazetteerRow{"kassel mitte": {gaz}},
		junctions: map[string][]NamedJunction{
			"fulda nord": {{ID: "n1", Name: "Fulda-Nord", Lat: 50.6, Lon: 9.7, Score: 0.95}, {ID: "n2", Name: "Fulda Nord", Lat: 50.6001, Lon: 9.7001, Score: 0.9}},
			"kassel":     {{ID: "n3", Name: "Kassel", Lat: 51.35, Lon: 9.5, Score: 0.7}, {ID: "n4", Name: "Kreuz Kassel", Lat: 51.25, Lon: 9.4, Score: 0.7}, {ID: "n5", Name: "Rastplatz Kassel", Lat: 51.2, Lon: 9.4, Score: 0.9}},
			"lohfelden":  {{ID: "n6", Name: "Lohfelden", Lat: 51.27, Lon: 9.55, Score: 1}},
		},
		lcl: []LCLPoint{{Road: "A7", Name: sp("Kassel"), DistanceM: 100}},
	}
}

func TestResolveAJunctionByItsWord(t *testing.T) {
	r, err := resolveWith(junctionReads(), "AS Fulda-Nord", "de")
	if err != nil || r.Kind != ResolvedPoint || r.Label != "Fulda-Nord (Anschlussstelle)" || r.Via != "poi" || r.PlaceKind != "junction" {
		t.Fatalf("got %+v %v", r, err)
	}
	if r, err = resolveWith(junctionReads(), "Fulda-Nord (junction)", "en"); err != nil || r.Kind != ResolvedPoint || r.Label != "Fulda-Nord (junction)" {
		t.Fatalf("label: got %+v %v", r, err)
	}
}

func TestResolveJunctionsOfOneNameAreAList(t *testing.T) {
	r, err := resolveWith(junctionReads(), "junction Kassel", "en")
	if err != nil || r.Kind != ResolvedCandidates || len(r.Options) != 2 {
		t.Fatalf("got %+v %v", r, err)
	}
	pick := r.Options[1].Label
	p, err := resolveWith(junctionReads(), pick, "en")
	if err != nil || p.Kind != ResolvedPoint || p.Lat != r.Options[1].Lat || p.Label != pick[:len(pick)-len(", 51.250, 9.400")] {
		t.Fatalf("option %q: got %+v %v", pick, p, err)
	}
	if p, err = resolveWith(junctionReads(), "junction Kassel, 10.000, 10.000", "en"); err != nil || p.Kind == ResolvedPoint {
		t.Fatalf("no option there: got %+v %v", p, err)
	}
}

func TestResolveAJunctionOnTheRoadAsked(t *testing.T) {
	f := junctionReads()
	f.lcl = nil
	f.junctions["kassel"] = f.junctions["kassel"][:2]
	r, err := resolveWith(f, "junction Kassel (A7)", "de")
	if err != nil || r.Kind != ResolvedCandidates {
		t.Fatalf("no road known: got %+v %v", r, err)
	}
	f.lclAt = map[float64][]LCLPoint{9.5: {{Road: "A7", Name: sp("Kassel"), DistanceM: 100}}, 9.4: {{Road: "A49", Name: sp("Kassel"), DistanceM: 100}}}
	if r, err = resolveWith(f, "junction Kassel (A7)", "de"); err != nil || r.Kind != ResolvedPoint || r.Lon != 9.5 {
		t.Fatalf("on the A7: got %+v %v", r, err)
	}
	f.fail = map[string]bool{"lcl": true}
	if r, err = resolveWith(f, "junction Kassel (A7)", "de"); err != nil || r.Kind != ResolvedCandidates {
		t.Fatalf("lcl failed: got %+v %v", r, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Resolve(ctx, f, "junction Kassel (A7)", "de"); err == nil {
		t.Fatal("cancelled: no error")
	}
}

func TestResolveAnAutobahnJunction(t *testing.T) {
	r, err := resolveWith(junctionReads(), "Anschlussstelle Kassel-Mitte", "de")
	if err != nil || r.Kind != ResolvedPoint || r.Via != "gazetteer" || r.SourceIDs[0] != "autobahn" {
		t.Fatalf("got %+v %v", r, err)
	}
}

func TestResolveAJunctionWithAHouseNumberLikeName(t *testing.T) {
	f := junctionReads()
	f.junctions["lohfelden 12"] = []NamedJunction{{ID: "n7", Name: "Lohfelden 12", Lat: 51.27, Lon: 9.55, Score: 1}}
	r, err := resolveWith(f, "AS Lohfelden 12", "de")
	if err != nil || r.Kind != ResolvedPoint || r.PlaceKind != "junction" {
		t.Fatalf("got %+v %v", r, err)
	}
}

func TestResolveJunctionFailedReads(t *testing.T) {
	for _, read := range []string{"junctions:fulda nord", "candidates:fulda nord"} {
		f := junctionReads()
		f.fail = map[string]bool{read: true}
		if _, err := resolveWith(f, "AS Fulda-Nord", "de"); err == nil {
			t.Errorf("%s: no error", read)
		}
		if _, err := resolveWith(f, "AS Fulda-Nord, 50.600, 9.700", "de"); err == nil {
			t.Errorf("%s: option: no error", read)
		}
	}
	rv := &resolver{ctx: context.Background(), reads: &fakeReads{}}
	if r, err := rv.resolveJunction("ab", "AS ab"); r != nil || err != nil {
		t.Fatal("short core read")
	}
	if r, err := rv.junctionsNamed("AS ab", 5); r != nil || err != nil {
		t.Fatal("short core read")
	}
}

func TestJunctionLabels(t *testing.T) {
	for _, c := range [][3]string{
		{"Kreuz Kassel", "de", "Kreuz Kassel"},
		{"Kassel-Nord", "en", "Kassel-Nord (junction)"},
		{"AK Kassel", "en", "AK Kassel"},
	} {
		if got := junctionLabel(c[0], c[1]); got != c[2] {
			t.Errorf("%v: %s", c, got)
		}
	}
	if askedJunctionRoad("Kassel") != "" || askedJunctionRoad("Kassel A 007") != "A7" {
		t.Error("road asked")
	}
}

func TestResolveAJunctionOptionOfTwoOfOneName(t *testing.T) {
	road := "A7"
	gaz := row("a2", "junction", "Kassel", 52.1, 10.1, 0, "autobahn", 1)
	gaz.Road = &road
	f := &fakeReads{
		cands:     map[string][]GazetteerRow{"kassel": {gaz}},
		junctions: map[string][]NamedJunction{"kassel": {{ID: "1", Name: "Kassel", Lat: 51.35, Lon: 9.5, Score: 1}, {ID: "2", Name: "Kassel", Lat: 52, Lon: 9.9, Score: 1}}},
	}
	for _, c := range []struct {
		q   string
		via string
	}{{"Kassel (junction), 52.000, 9.900", "poi"}, {"Kassel (junction), 52.100, 10.100", "gazetteer"}} {
		r, err := resolveWith(f, c.q, "en")
		if err != nil || r.Kind != ResolvedPoint || r.Label != "Kassel (junction)" || r.Via != c.via {
			t.Fatalf("%s: got %+v %v", c.q, r, err)
		}
	}
	if r, err := resolveWith(f, "junction Kassel (A7)", "en"); err != nil || r.Kind != ResolvedPoint || r.Lat != 52.1 {
		t.Fatalf("road: got %+v %v", r, err)
	}
	one := &fakeReads{junctions: map[string][]NamedJunction{"kassel": {{ID: "1", Name: "Kassel", Lat: 51.35, Lon: 9.5, Score: 1}}}}
	for _, q := range []string{"Kassel (junction), 52.000, 9.900", "Altdorf, 52.000, 9.900", "Kassel (junction), 50.000, 9.000"} {
		r := &resolver{ctx: context.Background(), reads: one, memo: map[string][]GazetteerRow{}}
		if p, err := r.resolveJunctionOption(q); err != nil || p != nil {
			t.Errorf("%s: got %+v %v", q, p, err)
		}
	}
}

func TestJunctionTwinsByTheirWords(t *testing.T) {
	kept := junctionField([]junctionRow{{name: "Fulda Nord West", lat: 50.6, lon: 9.7}, {name: "Fulda Nord", lat: 50.6, lon: 9.7}}, "Fulda Nord")
	if len(kept) != 1 {
		t.Fatalf("got %+v", kept)
	}
	if junctionLabel("Dreieck", "en") != "Dreieck (Interchange)" {
		t.Fatal(junctionLabel("Dreieck", "en"))
	}
}
