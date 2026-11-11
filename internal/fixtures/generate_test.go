package fixtures_test

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/fixtures"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func generate(t *testing.T, profile string, seed uint64) ([]byte, *fixtures.Manifest) {
	t.Helper()
	var buf bytes.Buffer
	m, err := fixtures.Generate(&buf, profile, seed, "0.1")
	if err != nil {
		t.Fatalf("profile %s seed %d: %v", profile, seed, err)
	}
	return buf.Bytes(), m
}

func TestSameSeedGivesTheSameBytes(t *testing.T) {
	req.Covers(t, "TST-014")
	a, ma := generate(t, "tiny", 42)
	b, mb := generate(t, "tiny", 42)
	if !bytes.Equal(a, b) || ma.Checksum != mb.Checksum {
		t.Fatalf("two runs of seed 42 differ: %s vs %s", ma.Checksum, mb.Checksum)
	}
	if got := fmt.Sprintf("sha256:%x", sha256.Sum256(a)); got != ma.Checksum {
		t.Fatalf("manifest checksum %s is not the checksum of the output %s", ma.Checksum, got)
	}
}

func TestAnotherSeedGivesOtherBytes(t *testing.T) {
	req.Covers(t, "TST-014")
	_, a := generate(t, "tiny", 1)
	_, b := generate(t, "tiny", 2)
	if a.Checksum == b.Checksum {
		t.Fatal("seeds 1 and 2 produced the same set")
	}
}

func TestUnknownProfileFails(t *testing.T) {
	req.Covers(t, "TST-011", "TST-014")
	var buf bytes.Buffer
	m, err := fixtures.Generate(&buf, "huge", 1, "0.1")
	if err == nil || m != nil || buf.Len() != 0 {
		t.Fatalf("unknown profile: manifest %v, error %v, %d bytes written", m, err, buf.Len())
	}
	if !strings.Contains(err.Error(), "tiny, ci, benchmark") {
		t.Fatalf("the error does not list the known profiles: %v", err)
	}
}

// TestEveryProfileCoversEverything runs the coverage check (TST-014) on the
// profiles cheap enough for a unit test; benchmark has the same code paths
// at a larger scale.
func TestEveryProfileCoversEverything(t *testing.T) {
	req.Covers(t, "TST-014")
	for _, p := range []string{"tiny", "ci"} {
		_, m := generate(t, p, 7)
		if gaps := m.Check(); len(gaps) > 0 {
			t.Errorf("%s: %v", p, gaps)
		}
		if len(m.RowsPerLand) != 16 || len(m.RowsPerFamily) != len(fixtures.Families) || len(m.Rows) != len(fixtures.Tables) {
			t.Errorf("%s: %d Länder, %d families, %d tables", p, len(m.RowsPerLand), len(m.RowsPerFamily), len(m.Rows))
		}
	}
}

func TestCoverageCheckGoesRedOnEachGap(t *testing.T) {
	req.Covers(t, "TST-011", "TST-014")
	_, m := generate(t, "tiny", 3)
	cases := map[string]func(m *fixtures.Manifest){
		"Land 07 has no rows":                                 func(m *fixtures.Manifest) { delete(m.RowsPerLand, "07") },
		"family delta has no rows":                            func(m *fixtures.Manifest) { m.RowsPerFamily["delta"] = 0 },
		"licence class share_alike is not represented":        func(m *fixtures.Manifest) { m.LicenceClasses["beta"] = "zero" },
		"edge case dst_autumn is missing":                     func(m *fixtures.Manifest) { delete(m.EdgeCases, "dst_autumn") },
		"table occupancy_observation is empty":                func(m *fixtures.Manifest) { m.Rows["occupancy_observation"] = 0 },
		"edge case road_without_events is missing":            func(m *fixtures.Manifest) { m.EdgeCases["road_without_events"] = 0 },
		"licence class not_permitted is not represented":      func(m *fixtures.Manifest) { delete(m.LicenceClasses, "epsilon") },
		"family epsilon has no rows":                          func(m *fixtures.Manifest) { delete(m.RowsPerFamily, "epsilon") },
		"edge case unknown_code is missing":                   func(m *fixtures.Manifest) { delete(m.EdgeCases, "unknown_code") },
		"table place_alias is empty":                          func(m *fixtures.Manifest) { delete(m.Rows, "place_alias") },
		"edge case max_decimal_price is missing":              func(m *fixtures.Manifest) { delete(m.EdgeCases, "max_decimal_price") },
		"edge case foreign_unknown_report is missing":         func(m *fixtures.Manifest) { delete(m.EdgeCases, "foreign_unknown_report") },
		"Land 16 has no rows":                                 func(m *fixtures.Manifest) { m.RowsPerLand["16"] = 0 },
		"licence class restricted_purpose is not represented": func(m *fixtures.Manifest) { m.LicenceClasses["gamma"] = "" },
	}
	for want, plant := range cases {
		c := clone(m)
		plant(c)
		if gaps := c.Check(); !slices.Contains(gaps, want) {
			t.Errorf("planted %q, the check said %v", want, gaps)
		}
	}
}

func clone(m *fixtures.Manifest) *fixtures.Manifest {
	c := *m
	cp := func(in map[string]int) map[string]int {
		out := make(map[string]int, len(in))
		for k, v := range in {
			out[k] = v
		}
		return out
	}
	c.Rows, c.RowsPerLand, c.RowsPerFamily, c.EdgeCases = cp(m.Rows), cp(m.RowsPerLand), cp(m.RowsPerFamily), cp(m.EdgeCases)
	c.LicenceClasses = make(map[string]string, len(m.LicenceClasses))
	for k, v := range m.LicenceClasses {
		c.LicenceClasses[k] = v
	}
	return &c
}

var pointRE = regexp.MustCompile(`POINT\(([0-9.]+) ([0-9.]+)\)`)

// TestPositionsLieInGermanyAndOutsideTheEmptyArea: every point is inside the
// bounding box, and none is inside the area planted to stay empty.
func TestPositionsLieInGermanyAndOutsideTheEmptyArea(t *testing.T) {
	req.Covers(t, "TST-014")
	out, m := generate(t, "ci", 11)
	ea, de := m.EmptyArea, fixtures.GermanyBox()
	points := pointRE.FindAllSubmatch(out, -1)
	if len(points) < 1000 {
		t.Fatalf("only %d points read; the check would prove little", len(points))
	}
	for _, p := range points {
		lon, _ := strconv.ParseFloat(string(p[1]), 64)
		lat, _ := strconv.ParseFloat(string(p[2]), 64)
		if lon < de[0] || lon > de[2] || lat < de[1] || lat > de[3] {
			t.Fatalf("point %s outside the bounding box", p[0])
		}
		if lon >= ea[0] && lon <= ea[2] && lat >= ea[1] && lat <= ea[3] {
			t.Fatalf("point %s inside the empty area", p[0])
		}
	}
}

// TestPlantedInstantsArePresent checks the instants of the time edge cases
// by value, not only by the manifest's counters.
func TestPlantedInstantsArePresent(t *testing.T) {
	req.Covers(t, "TST-014")
	out, _ := generate(t, "tiny", 5)
	for _, want := range []string{
		"2026-03-29T01:00:00Z", "2026-03-29T00:59:59Z", // spring change
		"2026-10-25T01:00:00Z",                         // autumn change
		"2026-03-29T00:00:00Z",                         // UTC midnight
		"2026-03-28T23:00:00Z", "2026-03-29T22:00:00Z", // Berlin midnight, winter and summer time
		"2026-03-31T22:00:00Z", "2025-12-31T23:00:00Z", // month and year end
		"999.999", "x_unmapped",
		"Straßenbrück-Ölau an der Au", "Oberunterhinterkieselbrückenwaldsiedlungsgemeinschaftsgemarkung",
	} {
		if !bytes.Contains(out, []byte(want)) {
			t.Errorf("planted value %q is missing", want)
		}
	}
}

// TestNoPersonalDataOrSecretShape (TST-009): no e-mail address, no key
// shaped like a credential, and every web address on the reserved
// .example top-level domain.
func TestNoPersonalDataOrSecretShape(t *testing.T) {
	req.Covers(t, "TST-009")
	out, _ := generate(t, "tiny", 9)
	if i := bytes.IndexByte(out, '@'); i >= 0 {
		t.Errorf("an @ at byte %d: %q", i, out[max(0, i-20):min(len(out), i+20)])
	}
	if m := regexp.MustCompile(`(?i)(api_?key|secret|password|token)\s*[:=]`).Find(out); m != nil {
		t.Errorf("a credential-shaped assignment: %q", m)
	}
	urls := regexp.MustCompile(`https?://([^'/]+)`).FindAllSubmatch(out, -1)
	if len(urls) == 0 {
		t.Fatal("no web address read; the check would prove nothing")
	}
	for _, u := range urls {
		if !bytes.HasSuffix(u[1], []byte(".example")) {
			t.Errorf("web address %q is not on the reserved .example domain", u[0])
		}
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, errors.New("disk full") }

// TestAWriteFailureIsAnError: output that cannot be written fails the run; a
// manifest is never returned for a set that did not reach its destination.
func TestAWriteFailureIsAnError(t *testing.T) {
	req.Covers(t, "TST-014")
	m, err := fixtures.Generate(brokenWriter{}, "tiny", 1, "0.1")
	if err == nil || m != nil || !strings.Contains(err.Error(), "disk full") {
		t.Fatalf("manifest %v, error %v, want the write error and no manifest", m, err)
	}
}

// TestAGapInTheGeneratedSetIsAnError: a family the generator produces no rows
// for fails Generate itself, with the manifest returned for diagnosis.
func TestAGapInTheGeneratedSetIsAnError(t *testing.T) {
	req.Covers(t, "TST-014")
	saved := fixtures.Families
	t.Cleanup(func() { fixtures.Families = saved })
	fixtures.Families = append(slices.Clone(saved), struct{ Name, Category string }{"omega", "attribution"})
	var buf bytes.Buffer
	m, err := fixtures.Generate(&buf, "tiny", 1, "0.1")
	if err == nil || m == nil || !strings.Contains(err.Error(), "family omega has no rows") {
		t.Fatalf("manifest %v, error %v, want a coverage-gap error naming omega", m != nil, err)
	}
}

// TestPlantedRowsAreReproducibleAndApart: the planted rows are the same for
// the same seed and profile, carry every planted situation, and are written
// on their own, so the set's seed.sql is the same with or without them.
func TestPlantedRowsAreReproducibleAndApart(t *testing.T) {
	req.Covers(t, "TST-014")
	var a, b, set bytes.Buffer
	if err := fixtures.Planted(&a, "tiny", 1, "0.1"); err != nil {
		t.Fatal(err)
	}
	if err := fixtures.Planted(&b, "tiny", 1, "0.1"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Error("two runs planted different rows")
	}
	for _, id := range []string{"'" + fixtures.TwoPartRef + "'", "'ev-two-1'", "'ev-two-2'", "'ev-u-kind'", "'ev-u-severity'", "'ev-u-direction'", "'ev-u-class'"} {
		if !strings.Contains(a.String(), id) {
			t.Errorf("the planted rows lack %s", id)
		}
	}
	if _, err := fixtures.Generate(&set, "tiny", 1, "0.1"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(set.String(), "'ev-two-1'") || strings.Contains(set.String(), "'"+fixtures.TwoPartRef+"'") {
		t.Error("a planted row is in the generated set")
	}
}

// TestPlantedRefusesWhatGenerateRefuses: an unknown profile and output that
// cannot be written are errors, as for the set itself.
func TestPlantedRefusesWhatGenerateRefuses(t *testing.T) {
	req.Covers(t, "TST-014")
	if err := fixtures.Planted(io.Discard, "huge", 1, "0.1"); err == nil || !strings.Contains(err.Error(), "unknown profile") {
		t.Errorf("an unknown profile: %v", err)
	}
	if err := fixtures.Planted(brokenWriter{}, "tiny", 1, "0.1"); err == nil || !strings.Contains(err.Error(), "disk full") {
		t.Errorf("a broken writer: %v", err)
	}
}
