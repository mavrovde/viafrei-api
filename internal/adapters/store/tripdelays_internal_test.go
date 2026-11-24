package store

import (
	"strings"
	"testing"
)

// TestTheTripDelaysLandBoundRefusesARelationWithoutItsPlace: the bound goes
// where the relation filters the Trip Updates' source, exactly once; a
// relation without that place, or with it twice, panics instead of serving
// every Land's delays unbound.
func TestTheTripDelaysLandBoundRefusesARelationWithoutItsPlace(t *testing.T) {
	if got := tripDelaysOfLand(relTransitTripDelay); !strings.Contains(got, landOfDelay+" = left($1::text, 2) AND d.source = 'delfi_rt_tu'::text AND") {
		t.Fatalf("the bound is not in its place: %s", got)
	}
	const at = "WHERE d.source = 'delfi_rt_tu'::text AND"
	for name, rel := range map[string]string{
		"no place":   "select 1 from r d WHERE true",
		"two places": "select 1 " + at + " x " + at + " y",
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil || !strings.Contains(r.(string), "the Land bound has no place") {
					t.Fatalf("want the panic, got %v", r)
				}
			}()
			tripDelaysOfLand(rel)
		})
	}
}
