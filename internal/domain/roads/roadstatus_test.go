package roads_test

import (
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func sp(s string) *string { return &s }
func bp(b bool) *bool     { return &b }
func ip(n int) *int       { return &n }

var rsNow = time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)

func TestRoadStatusQuery(t *testing.T) {
	req.Covers(t, "FR-ROAD-003")
	q, ok := roads.RoadStatusQueryOf("A9", roads.ClassMotorway, 50, rsNow, time.Time{}, []string{"routing_bab", "roadworks_bast", "autobahn"})
	if !ok || !slices.Equal(q.Open, []string{"autobahn", "roadworks_bast"}) || !slices.Equal(q.Strategies, []string{"routing_bab"}) ||
		q.Limit != roads.RoadStatusMaxLimit || !q.WindowEnd.Equal(rsNow.Add(72*time.Hour)) {
		t.Errorf("%+v %v", q, ok)
	}
	until := rsNow.Add(time.Hour)
	q, ok = roads.RoadStatusQueryOf("B27", roads.ClassFederal, 0, rsNow, until, []string{"routing_bab"})
	if ok || q.Strategies != nil || q.Limit != 1 || !q.WindowEnd.Equal(until) {
		t.Errorf("%+v %v", q, ok)
	}
}

func TestCurrentStrategyFamilies(t *testing.T) {
	req.Covers(t, "FR-ROAD-003")
	at := func(d time.Duration) *time.Time { v := rsNow.Add(-d); return &v }
	checks := []roads.SourceCheck{{Family: "a", CheckedAt: at(20 * time.Minute)}, {Family: "b", CheckedAt: at(time.Hour), CadenceS: ip(60)},
		{Family: "c", CheckedAt: at(48 * time.Hour), Paused: bp(true)}, {Family: "d"}, {Family: "e", CheckedAt: at(time.Minute), CadenceS: ip(60), Paused: bp(false)}}
	cur, stale := roads.CurrentStrategyFamilies([]string{"a", "b", "c", "d", "e", "f"}, checks, rsNow)
	if !slices.Equal(cur, []string{"a", "c", "e"}) || len(stale) != 1 || stale[0].Family != "b" {
		t.Errorf("%v %+v", cur, stale)
	}
}

func TestRoadEventTerm(t *testing.T) {
	req.Covers(t, "FR-ROAD-003")
	for _, c := range []struct {
		ty   *string
		fam  string
		want string
	}{{sp("roadClosed"), "x", "closure"}, {nil, "roadworks_ald_ni", "roadworks"}, {sp("MaintenanceWorks"), "x", "roadworks"}, {sp("abnormalTraffic"), "x", "restriction"}, {nil, "x", "restriction"}} {
		if got := roads.RoadEventTerm(c.ty, c.fam); got != c.want {
			t.Errorf("%v %s: %s", c.ty, c.fam, got)
		}
	}
	if roads.OwnRoad(nil, "A1") != "A1" || roads.OwnRoad(sp(" A 45 | Olpe"), "A4") != "A45" || roads.OwnRoad(sp("A1234"), "A4") != "A4" || roads.OwnRoad(sp("B1"), "A4") != "A4" {
		t.Error("own road")
	}
}

func TestPickPage(t *testing.T) {
	req.Covers(t, "FR-ROAD-003")
	mk := func(s ...string) []roads.RoadStatusItem {
		out := make([]roads.RoadStatusItem, len(s))
		for i, v := range s {
			out[i] = roads.RoadStatusItem{Status: v, ID: string(rune('a' + i))}
		}
		return out
	}
	ids := func(items []roads.RoadStatusItem) string {
		out := ""
		for _, i := range items {
			out += i.ID
		}
		return out
	}
	if len(roads.PickPage(mk("closed"), 0)) != 0 || ids(roads.PickPage(mk("closed", "planned"), 3)) != "ab" {
		t.Error("small")
	}
	if got := ids(roads.PickPage(mk("closed", "closed", "closed", "closed", "planned", "planned"), 4)); got != "abcd" {
		t.Errorf("mixed %s", got)
	}
	if got := ids(roads.PickPage(mk("closed", "closed", "closed"), 2)); got != "ab" {
		t.Errorf("closed %s", got)
	}
	d := 5
	from := rsNow
	items := []roads.RoadStatusItem{{Status: "planned", ID: "p"}, {Status: "closed", ID: "z", DistanceM: &d}, {Status: "closed", ID: "y"},
		{Status: "closed", ID: "x", From: &from}, {Status: "closed", ID: "w", Road: "A1"}}
	roads.SortRoadStatus(items)
	if got := ids(items); got != "zywxp" {
		t.Errorf("sort %s", got)
	}
}

func TestRoadStatusAnswer(t *testing.T) {
	req.Covers(t, "FR-ROAD-003")
	q, _ := roads.RoadStatusQueryOf("A9", roads.ClassMotorway, 2, rsNow, time.Time{}, []string{"autobahn", "roadworks_bast", "nms_events", "roadworks_akd", "roadworks_ald_ni", "traffic_bw"})
	seen := rsNow.Add(-time.Hour)
	checked := rsNow.Add(-time.Minute)
	desc := []any{"Beginn: 01.11.26 um 08:00 Uhr", "Ende: 30.12.26 um 18:00 Uhr", "(Ende der Gesamtmaßnahme: 31.12.27)", "Baustelle"}
	future := rsNow.Add(time.Hour)
	r := roads.RoadStatusRead{Tick: rsNow,
		Live: []roads.RoadStatusLiveRow{{Family: "autobahn", Road: "A9", Kind: "closure", ID: "c1", Title: sp("A9 | Sperrung"), Total: 2, Closed: 1, Families: []string{"autobahn"}},
			{Family: "autobahn", Road: "A9", Kind: "roadworks", ID: "r1", Blocked: sp("false"), Start: sp("2026-11-01T07:00:00.000Z"), Description: desc, Segments: 2}},
		Planned: []roads.RoadStatusPlannedRow{
			{Family: "roadworks_bast", Road: sp("A9"), From: sp("k1"), EventType: sp("MaintenanceWorks"), LastSeenAt: seen, Total: 6, Planned: 2, Current: 1, Closed: 1, Programme: 1,
				Families: []string{"roadworks_bast", "nms_events", "roadworks_ald_ni", "roadworks_akd"}, CurrentFamilies: []string{"roadworks_bast"}, AlsoFamilies: []string{"traffic_bw", "traffic_sh"}},
			{Family: "nms_events", Road: sp("A9"), EventType: sp("reroutingManagement"), ClosesText: bp(true), LastSeenAt: seen, DetourVehicles: bp(true)},
			{Family: "nms_events", Road: sp("A9"), EventType: sp("roadClosed"), TypeCloses: bp(false), LastSeenAt: rsNow, WindowCount: ip(2)},
			{Family: "roadworks_akd", LastSeenAt: seen},
			{Family: "roadworks_ald_ni", Road: sp("A9"), LastSeenAt: seen},
			{Family: "roadworks_bast", Road: sp("A9"), Start: &future, LastSeenAt: rsNow.Add(-2 * time.Hour)},
			{Family: "roadworks_bast", Road: sp("A9"), Windowed: bp(true), WindowNow: bp(false), LastSeenAt: seen},
		},
		Checked: map[string]time.Time{"roadworks_bast": checked, "nms_events": seen.Add(-time.Hour)},
	}
	a := roads.RoadStatusAnswerOf(q, r)
	k := a.Counts
	if k.Total != 8 || k.Closed != 2 || k.Restricted != 2 || k.Planned != 2 || k.Programme != 1 || k.Current != 1 || !slices.Equal(k.CurrentFamilies, []string{"roadworks_bast"}) {
		t.Errorf("counts %+v", k)
	}
	if len(a.Items) != 2 || a.Hidden != 6 || a.HiddenClosed != 0 || !a.CanRaise || a.Items[0].ID != "c1" || a.Items[1].Status != "closed" {
		t.Errorf("page %+v %d %d %v", a.Items, a.Hidden, a.HiddenClosed, a.CanRaise)
	}
	fams := []string{}
	for _, s := range a.Answered {
		fams = append(fams, s.Family)
	}
	if !slices.Equal(fams, []string{"autobahn", "roadworks_bast", "nms_events", "roadworks_ald_ni", "traffic_bw", "roadworks_akd"}) ||
		!a.Answered[0].AsOf.Equal(rsNow) || !a.Answered[1].AsOf.Equal(checked) || !a.Answered[2].AsOf.Equal(rsNow) || a.Answered[4].AsOf != nil {
		t.Errorf("answered %+v", a.Answered)
	}
	q.Limit = 20
	all := roads.RoadStatusAnswerOf(q, r).Items
	by := map[string]roads.RoadStatusItem{}
	for _, i := range all {
		by[i.ID+i.Status] = i
	}
	works := by["r1restricted"]
	if works.Road != "A9" || works.From == nil || works.To == nil || works.MeasureTo == nil || !works.To.Equal(*works.MeasureTo) || len(works.Description) != 3 || *works.Kind != "roadworks" {
		t.Errorf("works %+v %v %v %q", works, works.To, works.MeasureTo, works.Description)
	}
	if i := by["roadworks_bast:k1current"]; i.Term != "roadworks" || i.WindowStarts == nil {
		t.Errorf("current %+v", i)
	}
	if i := by["nms_events:?closed"]; i.Term != "closure" || (!i.Detour && !i.DetourVehicles) {
		t.Errorf("message %+v", by)
	}
	if _, ok := by["roadworks_akd:?planned"]; !ok || by["roadworks_ald_ni:?programme"].Term != "roadworks" || len(all) != 9 {
		t.Errorf("rest %+v", by)
	}
	// Nothing listed and no sweep: the sources that may answer, the road
	// operator only with a sweep and the plans-only source never.
	q.Open = []string{"autobahn", "roadworks_akd", "roadworks_bast"}
	if a = roads.RoadStatusAnswerOf(q, roads.RoadStatusRead{}); len(a.Answered) != 1 || a.Answered[0].Family != "roadworks_bast" || a.Answered[0].AsOf != nil ||
		a.Items == nil || a.CanRaise || a.Counts.CurrentFamilies == nil || a.Strategies == nil || a.StrategiesStale == nil {
		t.Errorf("empty %+v", a)
	}
	q.Open = []string{"autobahn", "roadworks_akd"}
	if a = roads.RoadStatusAnswerOf(q, roads.RoadStatusRead{}); len(a.Answered) != 1 || a.Answered[0].Family != "autobahn" {
		t.Errorf("only live %+v", a.Answered)
	}
}

func TestStrategies(t *testing.T) {
	req.Covers(t, "FR-ROAD-003")
	from := rsNow.Add(-time.Hour)
	causes := []any{map[string]any{"#text": " Unfall ", "@lang": "DE"}, map[string]any{"#text": "accident", "@lang": "en"}, map[string]any{"#text": " "}, "x"}
	comments := []any{map[string]any{"#text": "via A 92", "@lang": "en"}}
	rows := []roads.StrategyRow{
		{Family: "routing_bab", SourceID: "s", PublicationID: "1", Road: "A9", EventType: sp("generalNetworkManagement"), NowInForce: true, Via: sp(" A92 "), Words: sp("Empfehlung"),
			CauseValues: causes, CommentValues: comments, WindowFrom: &from, WindowTo: &rsNow},
		{Family: "routing_bayern", SourceID: "s", PublicationID: "2", Road: "A9", EventType: sp("reroutingManagement"), NowInForce: true, Via: sp(" "), WindowTo: &rsNow,
			CauseValues: []any{map[string]any{"#text": "Stau"}}},
		{Family: "routing_bab", SourceID: "s", PublicationID: "3", EventType: sp("generalNetworkManagement"), NowInForce: true, Words: sp("Umleitung U1")},
		{Family: "routing_bab", EventType: sp("generalNetworkManagement")},
		{Family: "routing_bab", NowInForce: true},
		{Family: "routing_bab", NowInForce: true, EventType: sp("roadClosed")},
		{Family: "roadworks_akd", NowInForce: true, EventType: sp("generalNetworkManagement")},
	}
	s := roads.StrategiesOf(rows)
	if len(s) != 3 {
		t.Fatalf("%+v", s)
	}
	if a := s[0]; a.Kind != "guidance" || *a.Via != "A92" || *a.Title != "Unfall" || *a.TitleEn != "accident" || *a.WordsEn != "via A 92" || a.ID != "s#1" || !a.Until.Equal(rsNow) {
		t.Errorf("%+v", a)
	}
	if b := s[1]; b.Kind != "detour" || b.Via != nil || *b.Title != "Stau" || b.Until != nil || b.TitleEn != nil {
		t.Errorf("%+v", b)
	}
	if c := s[2]; c.Kind != "detour" || c.Title != nil {
		t.Errorf("%+v", c)
	}
}

func TestRoadStatusLiveDates(t *testing.T) {
	req.Covers(t, "FR-ROAD-003")
	q, _ := roads.RoadStatusQueryOf("A9", roads.ClassMotorway, 5, rsNow, time.Time{}, []string{"autobahn"})
	desc := []any{"Ende: 30.12.26 um 18:00 Uhr", "(Ende der Gesamtmaßnahme: 01.12.26)"}
	r := roads.RoadStatusRead{Tick: rsNow, Live: []roads.RoadStatusLiveRow{{Family: "autobahn", Road: "A9", Kind: "roadworks", ID: "a", Start: sp("bad"), Description: desc, Total: 2},
		{Family: "autobahn", Road: "A9", Kind: "roadworks", ID: "b", Description: []any{"Ende: 30.12.26 um 18:00 Uhr"}}}}
	for _, i := range roads.RoadStatusAnswerOf(q, r).Items {
		if i.From != nil || i.To == nil || i.To.Month() != time.December || i.To.Day() != 30 {
			t.Errorf("%s %v %v", i.ID, i.From, i.To)
		}
	}
}
