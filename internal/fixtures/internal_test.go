package fixtures

import (
	"math/rand/v2"
	"regexp"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// TestSourcePickerRefusesAMissingFamily: asking for a family the domain has no
// live source of is a generator bug, and it stops the run by name rather than
// writing a row with an empty source.
func TestSourcePickerRefusesAMissingFamily(t *testing.T) {
	g := &gen{r: rand.New(rand.NewPCG(2, 2)), sources: map[string][]source{}} // #nosec G404 -- test data
	defer func() {
		msg, _ := recover().(string)
		if !strings.Contains(msg, "no source alpha for roads") {
			t.Fatalf("recover() = %q, want a panic naming the family and domain", msg)
		}
	}()
	// Seed 2 skips the stale branch on its first draw; the panic is the
	// only way out for an empty candidate list either way.
	g.src("roads", "alpha")
	t.Fatal("src returned without a source")
}

// TestOtherFamilyRefusesADomainOfOneFamily: the foreign report needs a second
// live family; a domain without one is a generator bug, named.
func TestOtherFamilyRefusesADomainOfOneFamily(t *testing.T) {
	g := &gen{sources: map[string][]source{"energy": {{ref: "a", family: "alpha", domain: "energy"}, {ref: "a-stale", family: "beta", domain: "energy", stale: true}}}, family: map[string]string{"a": "alpha"}}
	defer func() {
		msg, _ := recover().(string)
		if !strings.Contains(msg, "no second family for energy") {
			t.Fatalf("recover() = %q, want a panic naming the domain", msg)
		}
	}()
	g.otherFamily("energy", "a")
	t.Fatal("otherFamily returned without a family")
}

// TestAlongEndsOnTheLastPoint: a fraction past the end of the road clamps to
// its last point instead of running off the polyline.
func TestAlongEndsOnTheLastPoint(t *testing.T) {
	rd := road{points: []lonLat{{10, 50}, {10, 50.1}, {10, 50.2}}}
	rd.length = haversineKm(rd.points[0], rd.points[1]) + haversineKm(rd.points[1], rd.points[2])
	for f, want := range map[float64]lonLat{0: {10, 50}, 0.5: {10, 50.1}, 1: {10, 50.2}, 3: {10, 50.2}} {
		got := rd.along(f)
		if d := haversineKm(got, want); d > 1e-6 {
			t.Errorf("along(%v) = %v, want %v", f, got, want)
		}
	}
}

// TestNamesAreBuiltFromOurSyllablesOnly holds the "fictional in content" rule
// of TST-014: every generated name parses as syllables of our own lists plus
// one of our endings, so none can come from a real-world list of names.
func TestNamesAreBuiltFromOurSyllablesOnly(t *testing.T) {
	req.Covers(t, "TST-014")
	alt := func(list []string) string {
		quoted := make([]string, len(list))
		for i, s := range list {
			quoted[i] = regexp.QuoteMeta(s)
		}
		return "(?:" + strings.Join(quoted, "|") + ")"
	}
	syl := alt(onsets) + alt(nuclei) + alt(codas)
	grammar := map[string]*regexp.Regexp{
		"settlement": regexp.MustCompile("^" + syl + syl + alt(endings) + "$"),
		"region":     regexp.MustCompile("^" + syl + syl + "land$"),
		"person":     regexp.MustCompile("^" + syl + syl + "$"),
	}
	n := &namer{r: rand.New(rand.NewPCG(3, 4))}
	for i := 0; i < 5000; i++ {
		for kind, name := range map[string]string{"settlement": n.settlement(), "region": n.region(), "person": n.person()} {
			lower := strings.ToLower(name[:1]) + name[1:]
			if r, _ := utf8.DecodeRuneInString(name); !unicode.IsUpper(r) || !grammar[kind].MatchString(lower) {
				t.Fatalf("%s %q is not built from the syllable lists", kind, name)
			}
		}
	}
	// The check can say no: a real-sounding name that is not in the grammar.
	if grammar["settlement"].MatchString("xylophonstadt") {
		t.Fatal("the grammar accepts a name outside the lists")
	}
}
