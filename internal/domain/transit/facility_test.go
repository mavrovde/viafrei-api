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
