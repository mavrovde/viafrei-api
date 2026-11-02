package fixtures

import (
	"math/rand/v2"
	"strings"
	"testing"
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
