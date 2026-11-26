package places

import (
	"context"
	"testing"
)

func TestAddressHelpers(t *testing.T) {
	if buildAddress("Weg", "6", "  ") != nil {
		t.Fatal("no town, no postcode")
	}
	for _, c := range [][3]string{{"1", "2-10", ""}, {"12", "2-10", ""}} {
		if got := houseNumberMatch(c[0], c[1]); got != c[2] {
			t.Errorf("%s in %s: %q", c[0], c[1], got)
		}
	}
	a := addr("1", "Weg", "6", nil, nil, 50, 9, 1)
	b := addr("2", "Weg", "6", sp("10001"), sp("Altdorf"), 50, 9, 1)
	if got := foldSameAddresses([]AddressHit{a, b}); len(got) != 1 || got[0].ID != "2" {
		t.Fatalf("fold: %+v", got)
	}
	c := addr("3", "Weg", "6", sp("10001"), sp("Neudorf"), 50.1, 9, 1)
	if g := addressTowns([]AddressHit{b, c, b}); len(g) != 2 || len(g[0]) != 2 {
		t.Fatalf("towns: %+v", g)
	}
	rv := &resolver{ctx: context.Background(), reads: &fakeReads{}}
	if hits, err := rv.findStreet("weg", nil, sp("10001")); err != nil || hits != nil {
		t.Fatalf("postcode only: %v %v", hits, err)
	}
}

func TestCandidateHelpers(t *testing.T) {
	for in, want := range map[string]string{"Harz, Landkreis": "harz", "Landkreis Harz": "harz", "Landkreis": "landkreis", ",": ""} {
		if got := stripAdminWords(in); got != want {
			t.Errorf("%q: %q", in, got)
		}
	}
	rv := &resolver{ctx: context.Background(), reads: &fakeReads{}, memo: map[string][]GazetteerRow{}}
	if rows, err := rv.findCandidates("!!", 5, CandidateOptions{}); err != nil || rows != nil {
		t.Fatal("empty fold read")
	}
	f := &fakeReads{roads: map[string][]GazetteerRow{"A7": {row("r1", "road", "A7", 51, 9.5, 0, "autobahn", 1)}}}
	rv = &resolver{ctx: context.Background(), reads: f, memo: map[string][]GazetteerRow{}}
	if rows, err := rv.findCandidates("A 7", 5, CandidateOptions{}); err != nil || len(rows) != 1 {
		t.Fatalf("road: %v %v", rows, err)
	}
	en := "Rhein Main"
	rows := withWordOrder([]GazetteerRow{
		row("1", "city", "Rhein Main", 50, 8, 0, "geonames", 1),
		withEn(row("2", "city", "Zzz", 50, 8, 0, "geonames", 1), en),
		row("3", "city", "Main Rhein", 50, 8, 0, "geonames", 1),
		row("4", "city", "Mainz", 50, 8, 0, "geonames", 0.5),
	}, []string{"main rhein"})
	if rows[0].ID != "3" || rows[1].Score != 1-WordOrderPenalty || rows[2].Score != 1-WordOrderPenalty || rows[3].ID != "4" {
		t.Fatalf("order: %+v", rows)
	}
}
