package transit

import (
	"slices"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestFacilitiesListBrokenFirst(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-003")
	s := func(v string) *string { return &v }
	all := []Facility{
		{ID: "a", Kind: "elevator", Position: s("zu Gleis 2"), State: "active"},
		{ID: "b", Kind: "escalator", Position: s("zu Gleis 1"), State: "inactive"},
		{ID: "c", Kind: "elevator", Position: s("zu Gleis 1"), State: "active"},
		{ID: "d", Kind: "elevator", State: "no_data"},
		{ID: "e", Kind: "elevator", Position: s("Ausgang"), State: "inactive"},
	}
	ids := func(f Facilities) []string {
		out := []string{}
		for _, r := range f.Rows {
			out = append(out, r.ID)
		}
		return out
	}
	got := ListFacilities(all, FacilityQuery{Limit: 10})
	if !slices.Equal(ids(got), []string{"e", "b", "d", "c", "a"}) || got.Active != 2 || got.Inactive != 2 || got.Unknown != 1 || got.Rows[2].State != "unknown" {
		t.Errorf("every kind %v %+v", ids(got), got)
	}
	got = ListFacilities(all, FacilityQuery{Kind: "elevator", Limit: 2})
	if !slices.Equal(ids(got), []string{"e", "d"}) || got.Active != 2 || got.Inactive != 1 || got.Unknown != 1 {
		t.Errorf("lifts %v %+v", ids(got), got)
	}
}

// One row per equipment number: the first, unless it is unknown and a later
// copy has a state; a row without a number is its own unit. A unit sharing
// its kind and position with another is marked, even beyond the limit.
func TestOneRowPerUnitAndTwinsAreMarked(t *testing.T) {
	no := func(s string) *string { return &s }
	fs := OnePerUnit([]Facility{
		{ID: "a", EquipmentNo: no("1"), State: "unknown"}, {ID: "b", EquipmentNo: no("1"), State: "active"},
		{ID: "c", EquipmentNo: no("1"), State: "inactive"}, {ID: "d", EquipmentNo: no("2"), State: "active"},
		{ID: "e", EquipmentNo: no("2"), State: "unknown"}, {ID: "f"}, {ID: "g"},
	})
	var ids []string
	for _, f := range fs {
		ids = append(ids, f.ID)
	}
	if !slices.Equal(ids, []string{"b", "d", "f", "g"}) {
		t.Errorf("one per unit: %v", ids)
	}
	if got := OnePerUnit(nil); got == nil || len(got) != 0 {
		t.Errorf("none: %v", got)
	}
	gleis := no("zu Gleis 8/9")
	all := []Facility{
		{ID: "x", Kind: "elevator", Position: gleis, State: "active"}, {ID: "y", Kind: "elevator", Position: gleis, State: "unknown"},
		{ID: "z", Kind: "escalator", Position: gleis, State: "active"},
	}
	if l := ListFacilities(all, FacilityQuery{Limit: 1}); len(l.Rows) != 1 || !l.Rows[0].NameShared {
		t.Errorf("a twin beyond the limit: %+v", l.Rows)
	}
	if l := ListFacilities(all, FacilityQuery{Kind: "escalator", Limit: 5}); len(l.Rows) != 1 || l.Rows[0].NameShared {
		t.Errorf("no twin: %+v", l.Rows)
	}
}
