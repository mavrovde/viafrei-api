package places

import (
	"testing"
)

func neustadtReads() *fakeReads { return neustadtOf("07316000") }

func neustadtOf(first string) *fakeReads {
	return &fakeReads{
		cands: map[string][]GazetteerRow{"neustadt": {
			withAdmin(row("n1", "city", "Neustadt", 49.35, 8.14, 53984, "bkg_gvisys", 1), first),
			withAdmin(row("n2", "city", "Neustadt", 52.5, 9.46, 53000, "bkg_gvisys", 1), "03241013"),
			withAdmin(row("n3", "city", "Neustadt", 49.73, 12.17, 52000, "bkg_gvisys", 1), "09374140"),
			withAdmin(row("n4", "city", "Neustadt", 49.8, 12.3, 52000, "bkg_gvisys", 1), "09374141"),
			row("n5", "city", "Neustadt", 50.1, 10.1, 0, "geonames", 1),
		}},
		kreise: map[string]string{"03241": "Region Hannover", "09374": "Neustadt a.d.Waldnaab"},
		lands:  map[int]string{1: "Bayern"},
	}
}

func TestResolveTheKreisfreeTownOfNamesakes(t *testing.T) {
	r, err := resolveWith(neustadtReads(), "Neustadt", "de")
	if err != nil || r.Kind != ResolvedPoint || r.AGS != "07316000" || len(r.Alternatives) != 2 ||
		r.Label != "Neustadt, auch: Neustadt (Niedersachsen, Region Hannover), Neustadt (Bayern, Kreis Neustadt a.d.Waldnaab)" {
		t.Fatalf("got %+v %v", r, err)
	}
	// The Bavarian one names two towns of its Kreis: only the first
	// alternative's label names one place.
	for _, a := range r.Alternatives[:1] {
		if p, err := resolveWith(neustadtReads(), a.Label, "de"); err != nil || p.Kind != ResolvedPoint || p.Lat != a.Lat {
			t.Errorf("%q: got %+v %v", a.Label, p, err)
		}
	}
}

func TestResolveNamesakesAreAListWhoseLabelsResolve(t *testing.T) {
	neustadtReads := func() *fakeReads { return neustadtOf("07337050") }
	for _, lang := range []string{"de", "en"} {
		r, err := resolveWith(neustadtReads(), "Neustadt", lang)
		if err != nil || r.Kind != ResolvedCandidates || len(r.Options) < 4 {
			t.Fatalf("%s: got %+v %v", lang, r, err)
		}
		seen := map[string]bool{}
		for _, o := range r.Options {
			if seen[o.Label] {
				t.Fatalf("%s: label twice: %q", lang, o.Label)
			}
			seen[o.Label] = true
			p, err := resolveWith(neustadtReads(), o.Label, lang)
			if err != nil || p.Kind != ResolvedPoint || p.Lat != o.Lat || p.Lon != o.Lon {
				t.Errorf("%s: %q: got %+v %v", lang, o.Label, p, err)
			}
		}
	}
}
