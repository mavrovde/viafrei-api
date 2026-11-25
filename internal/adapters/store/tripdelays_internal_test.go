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

// TestThePunctualityAreaBoundRefusesADefinitionWithoutItsPlace: the Länder of
// the punctuality definition are bound to the area's, and the area read reads
// that bound definition in place of the view, each exactly once; a definition
// or relation without that place, or with it twice, panics instead of
// counting every Land unbound.
func TestThePunctualityAreaBoundRefusesADefinitionWithoutItsPlace(t *testing.T) {
	if strings.Contains(punctualityOfArea, "VALUES ('01'::text)") || !strings.Contains(punctualityOfArea, `ARRAY["left"($1::text, 2)]`) {
		t.Fatalf("the Länder are not bound to the area: %s", punctualityOfArea)
	}
	if got := areaBound(relTransitPunctuality); strings.Contains(got, "FROM transit_punctuality_area_live p") || !strings.Contains(got, punctualityOfArea) {
		t.Fatalf("the area read still reads the view: %s", got)
	}
	const lands = "( VALUES ('01'::text), ('02'::text), ('03'::text), ('04'::text), ('05'::text), ('06'::text), ('07'::text), ('08'::text), ('09'::text), ('10'::text), ('11'::text), ('12'::text), ('13'::text), ('14'::text), ('15'::text), ('16'::text)) l(land)"
	const view = "FROM transit_punctuality_area_live p"
	for name, c := range map[string]struct {
		bind func(string) string
		def  string
		want string
	}{
		"no Länder":      {landsOfArea, "select 1", "the area bound has no place"},
		"Länder twice":   {landsOfArea, lands + lands, "the area bound has no place"},
		"no view":        {areaBound, "select 1", "the area bound has no place"},
		"the view twice": {areaBound, view + " " + view, "the area bound has no place"},
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if r := recover(); r == nil || !strings.Contains(r.(string), c.want) {
					t.Fatalf("want the panic, got %v", r)
				}
			}()
			c.bind(c.def)
		})
	}
}

// TestTheKeptOfficialCellsNeverGrowWithoutEnd: 64 families sets are kept;
// the 65th drops them and is kept alone.
func TestTheKeptOfficialCellsNeverGrowWithoutEnd(t *testing.T) {
	var o officialCells
	for i := range 64 {
		o.keep(strings.Repeat("k", i+1), []string{"x"})
	}
	if len(o.kept) != 64 {
		t.Fatalf("kept %d sets, want 64", len(o.kept))
	}
	o.keep("last", []string{"y"})
	if len(o.kept) != 1 || o.kept["last"][0] != "y" {
		t.Fatalf("after the 65th: %v", o.kept)
	}
}
