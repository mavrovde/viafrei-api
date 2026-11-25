package places

import "testing"

// A town is folded as the gazetteer's names, its administrative words
// stripped as the platform strips them.
func TestAGemeindeIsFoldedWithoutItsAdministrativeWords(t *testing.T) {
	s := func(v string) *string { return &v }
	for in, want := range map[string]string{
		"Sankt Peter-Ording, Gemeinde": "sankt peter ording",
		"Kreis Fulda":                  "fulda",
		"Markt Schwaben":               "markt schwaben",
		"Landkreis":                    "landkreis",
		"Köln, Innenstadt":             "koln innenstadt",
		"München":                      "munchen",
		" ,Stadt":                      "stadt",
	} {
		if got := GemeindeFold(s(in)); got == nil || *got != want {
			t.Errorf("%q: %v, want %q", in, got, want)
		}
	}
	if GemeindeFold(nil) != nil || GemeindeFold(s(" , ")) != nil {
		t.Error("no town folds to nil")
	}
}
