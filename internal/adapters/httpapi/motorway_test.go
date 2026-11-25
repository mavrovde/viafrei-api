package httpapi

import (
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestMotorwayBlock(t *testing.T) {
	req.Covers(t, "FR-ROAD-003")
	if motorwayBlock(nil) != nil {
		t.Fatal("none")
	}
	s := func(v string) *string { return &v }
	now := time.Date(2026, 11, 25, 8, 0, 0, 0, time.UTC)
	km, l, d := 2.5, 2234.4, 89
	jam := roads.MotorwayEntry{MotorwayEventRow: roads.MotorwayEventRow{ID: "j", Kind: "warning", DelayS: &d, AverageKmh: s(" 12.5"), Blocked: s("true"), StartText: s("2026-11-25T07:00:00.000Z")},
		Jam: &roads.JamDetail{LengthM: &l, End: &roads.JamEnd{Text: "x", Km: &km, Side: s("past"), Place: s("y")},
			Trend: &roads.JamTrend{Direction: "growing"}, Usual: &roads.JamUsual{Verdict: "usual"}, HowLong: &roads.JamHowLong{Cases: 9}}}
	other := roads.MotorwayEntry{MotorwayEventRow: roads.MotorwayEventRow{ID: "o", AverageKmh: s(""), StartText: s("gestern")}, Jam: &roads.JamDetail{}}
	works := roads.MotorwayEntry{MotorwayEventRow: roads.MotorwayEventRow{ID: "w", AverageKmh: s("schnell"), StartsAt: &now}}
	b := motorwayBlock(&roads.MotorwayAnswer{Tick: now, Counts: roads.MotorwayCounts{Jams: 1, AnnouncedReports: 2}, Measured: &roads.Measured{Sites: 3},
		Plans: []roads.PlanNow{{Family: "f", Name: s("n")}}, Entries: []roads.MotorwayEntry{jam, other, works, {}}})
	if b.RoadWide.Jams != 1 || b.RoadWide.Announced.Reports != 2 || b.Measured.Sites != 3 || b.Measured.SlowNear == nil || *b.PlannedNow[0].Name != "n" || len(b.Events) != 4 {
		t.Fatalf("%+v", b)
	}
	j := b.Events[0]
	if *j.DelayMinutes != 1 || *j.AverageKmh != 12.5 || !j.Blocked || !j.Since.Equal(now.Add(-time.Hour)) || *j.Jam.LengthM != 2234 || *j.Jam.EndKm != 2.5 ||
		j.Jam.Trend.Direction != "growing" || j.Jam.Usual.Verdict != "usual" || j.Jam.HowLong.Cases != 9 || j.SiteIDs == nil || j.DescriptionShown == nil {
		t.Errorf("jam %+v %+v", j, j.Jam)
	}
	if o := b.Events[1]; o.AverageKmh != nil || o.Since != nil || o.Jam == nil || o.Jam.EndText != nil || o.Jam.Causes == nil || o.Jam.Trend != nil {
		t.Errorf("other %+v", o)
	}
	if w := b.Events[2]; w.AverageKmh != nil || !w.Since.Equal(now) || w.Jam != nil || w.DelayMinutes != nil {
		t.Errorf("works %+v", w)
	}
	if e := b.Events[3]; e.AverageKmh != nil || e.Since != nil || e.Blocked {
		t.Errorf("empty %+v", e)
	}
	if b = motorwayBlock(&roads.MotorwayAnswer{}); b.Measured != nil || b.PlannedNow == nil || b.Events == nil {
		t.Errorf("bare %+v", b)
	}
}
