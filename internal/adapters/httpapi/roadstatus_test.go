package httpapi

import (
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// TestRoadStatusBlock: the road status is carried member for member, every
// list a list, and no status is no block.
func TestRoadStatusBlock(t *testing.T) {
	req.Covers(t, "FR-ROAD-003")
	if roadStatusBlock(nil) != nil {
		t.Fatal("nil")
	}
	at := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	via := "A92"
	a := &roads.RoadStatusAnswer{WindowEnd: at, Counts: roads.RoadStatusCounts{Closed: 1, Total: 2, CurrentFamilies: []string{"roadworks_bast"}},
		Items:  []roads.RoadStatusItem{{Status: "closed", Term: "closure", Family: "autobahn", Road: "A9", ID: "x", Description: []string{"d"}, From: &at, DetourVia: &via}},
		Hidden: 1, HiddenClosed: 0, CanRaise: true,
		Answered: []roads.SourceAnswered{{Family: "autobahn", AsOf: &at}}, Strategies: []roads.Strategy{{Road: "A9", Family: "routing_bab", ID: "s#1", Kind: "detour", Via: &via, Since: &at}},
		StrategiesStale: []roads.SourceCheck{{Family: "routing_dvl_ni", CheckedAt: &at}}}
	b := roadStatusBlock(a)
	if !b.WindowEnd.Equal(at) || b.Counts.Closed != 1 || b.Counts.Total != 2 || b.Counts.CurrentFamilies[0] != "roadworks_bast" || b.Hidden != 1 || !b.CanRaise || !b.PlannedNoCoords {
		t.Errorf("%+v", b)
	}
	if i := b.Items[0]; i.ID != "x" || i.Status != "closed" || *i.DetourVia != via || !i.From.Equal(at) || len(i.Description) != 1 || i.WindowStarts == nil || i.WindowEnds == nil {
		t.Errorf("%+v", i)
	}
	if b.SourcesAnswered[0].Family != "autobahn" || b.Guidance[0].Kind != "detour" || *b.Guidance[0].Via != via || b.GuidanceNotRead[0].Family != "routing_dvl_ni" {
		t.Errorf("%+v", b)
	}
	if e := roadStatusBlock(&roads.RoadStatusAnswer{}); e.Items == nil || e.SourcesAnswered == nil || e.Guidance == nil || e.GuidanceNotRead == nil || e.Counts.CurrentFamilies == nil {
		t.Errorf("empty %+v", e)
	}
}
