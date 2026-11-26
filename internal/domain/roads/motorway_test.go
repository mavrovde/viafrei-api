package roads_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func lines(s ...any) any { return s }

// The description lines as the platform's motorway tool prints them: the
// expected values are the platform's own output for the same lines.
func TestMotorwayDescription(t *testing.T) {
	req.Covers(t, "FR-ROAD-003")
	for _, c := range []struct {
		in   any
		want []string
	}{
		{nil, []string{}},
		{lines("Stau", 7, "", "  Länge 2 km  "), []string{"Stau", "Länge 2 km"}},
		{lines("Beginn: 06.10.26 um 21:03 Uhr", "Baustelle"), []string{"[…] Baustelle"}},
		{lines("Zusammengesetzte Verkehrsinformation, seit 06.10.2026, 19:03", "x"), []string{"Zusammengesetzte Verkehrsinformation […]", "x"}},
		{lines("seit 06.10.2026, 19:03"), []string{}},
		{lines("a", "seit 06.10.2026, 19:03"), []string{"a […]"}},
		{lines("Zeitraum dieser Bauphase:", "", "b"), []string{"[…] b"}},
		{lines("Zeitraum:"), []string{}},
		{lines("x", "Zeitraum:"), []string{"x […]"}},
		{lines("Beginn: x", "[…] y"), []string{"[…] y"}},
		{lines("a […]", "Beginn: x"), []string{"a […]"}},
	} {
		got := roads.CleanDescription(c.in)
		if !slices.Equal(got, c.want) && (len(got) != 0 || len(c.want) != 0) {
			t.Errorf("%v: %q, want %q", c.in, got, c.want)
		}
	}
	long := strings.Repeat("wort ", 60)
	if got := roads.DescriptionShown(lines(long, "zwei", "drei")); len(got) != 2 || !strings.HasSuffix(got[0], "wort…") || len([]rune(got[0])) > 200 || got[1] != "zwei" {
		t.Errorf("shown %q", got)
	}
	for in, want := range map[string]string{
		"kurz":                                  "kurz",
		"Ende der Gesamtmaßnahme: 13.12.2026":   "Ende der…",
		"Ende der Gesamtmaßnahme:  13.12.2026":  "Ende der…",
		"abcdefghijklmnopqrstuvwxyz":            "abcdefghijklmnopqrs…",
		"abcdefghijklmnopqrs uvwxyz":            "abcdefghijklmnopqrs…",
		"(—  abcdefghijklmnopqrstuvwxyz":        "(—  abcdefghijklmno…",
		"Sperrung: Haar, Ottobrunn – Unterhach": "Sperrung: Haar…",
	} {
		if got := roads.ClipAtWord(in, 20); got != want {
			t.Errorf("clip %q: %q, want %q", in, got, want)
		}
	}
	if got := roads.ClipAtWord("abc", 0); got != "…" {
		t.Errorf("no room: %q", got)
	}
}

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}

func TestMotorwayPhase(t *testing.T) {
	req.Covers(t, "FR-ROAD-003")
	iso := func(p *time.Time) string {
		if p == nil {
			return "-"
		}
		return p.UTC().Format(time.RFC3339)
	}
	for _, c := range []struct {
		in               any
		start, end, meas string
	}{
		{"no list", "-", "-", "-"},
		{lines("Beginn: 01.11.26 um 08:00 Uhr", "Ende: 30.12.2026 um 18:00 Uhr", "(Ende der Gesamtmassnahme: 31.12.27)", 4), "2026-11-01T07:00:00Z", "2026-12-30T17:00:00Z", "2027-12-30T23:00:00Z"},
		{lines("Die Baustelle ist zu folgenden Zeiträumen gültig:", "25.11.26 von 08:00 bis 15:00 Uhr", "24.11.26 von 22:00 bis 24:00 Uhr.", "Länge: 2 km | Max. 80 km/h", "Maximale Durchfahrtsbreite: 4 m"),
			"2026-11-24T21:00:00Z", "2026-11-25T14:00:00Z", "-"},
		{lines("01.03.26 12:00 Uhr bis zum 05.03.26 06:00 Uhr", "02.03.26 12:00 bis zum 31.13.26 06:00 Uhr", "32.03.26 von 08:00 bis 15:00 Uhr", "x 1.1.26 von 1:00 bis 2:00 Uhr"),
			"2026-03-01T11:00:00Z", "2026-03-05T05:00:00Z", "-"},
		// The doubled hour is its later occurrence, the missing one shifted.
		{lines("Beginn: 25.10.26 um 02:30 Uhr", "Ende: 29.03.26 um 02:30 Uhr"), "2026-10-25T01:30:00Z", "2026-03-29T01:30:00Z", "-"},
		{lines("Beginn: 13.13.26", "Ende: 01.01.2026 um 25:00"), "-", "-", "-"},
		{lines("Ende: 01.01.26"), "-", "2025-12-31T23:00:00Z", "-"},
		{lines("Beginn: kein Datum"), "-", "-", "-"},
	} {
		p := roads.PhaseOf(c.in)
		if got := []string{iso(p.Start), iso(p.End), iso(p.MeasureEnd)}; !slices.Equal(got, []string{c.start, c.end, c.meas}) {
			t.Errorf("%v: %v, want %v %v %v", c.in, got, c.start, c.end, c.meas)
		}
	}
}

func ptr[T any](v T) *T { return &v }

func TestMotorwayCountsAndLamp(t *testing.T) {
	req.Covers(t, "FR-ROAD-003")
	from, earlier := at("2026-11-26T08:00:00Z"), at("2026-11-25T20:00:00Z")
	k := roads.CountsOf([]roads.MotorwayCountRow{{Kind: "warning", N: 4, Jams: 2, Announced: 1, AnnouncedFrom: &from}, {Kind: "closure", N: 1, Announced: 2, AnnouncedFrom: &earlier},
		{Kind: "roadworks", N: 3, Announced: 1, AnnouncedFrom: &from}, {Kind: "other", N: 9}})
	if k != (roads.MotorwayCounts{Jams: 2, Warnings: 4, Closures: 1, Roadworks: 3, AnnouncedClosures: 2, AnnouncedRoadworks: 1, AnnouncedReports: 1, AnnouncedFrom: &earlier}) {
		t.Errorf("counts %+v", k)
	}
	lamp := []roads.MotorwayEventRow{
		{Kind: "roadworks", Segments: 3, Title: ptr("A1 | Baustelle"), Description: lines("Fahrbahnverengung")},
		{Kind: "warning", Segments: 2, TrafficType: ptr("STATIONARY_TRAFFIC"), DelayS: ptr(2400), Blocked: ptr("false")},
		{Kind: "closure", Segments: 1, Blocked: ptr("true")},
	}
	if got := roads.LampReasonsFolded(6, lamp); got != 3 {
		t.Errorf("folded %d", got)
	}
	if got := roads.LampReasonsFolded(0, lamp); got != 0 {
		t.Errorf("floor %d", got)
	}
}

func TestMotorwayQuery(t *testing.T) {
	req.Covers(t, "FR-ROAD-003")
	now := at("2026-11-25T08:00:00Z")
	q, ok := roads.MotorwayQueryOf("A1", 5, now, []string{"autobahn", "roadworks_akd", "los_nrw", "los_nrw_sites", "los_nrw_lanes"})
	if !ok || !q.Measured || q.Lanes || !slices.Equal(q.Plans, []string{"roadworks_akd"}) || q.Road != "A1" || q.Limit != 5 || !q.Now.Equal(now) {
		t.Errorf("%+v %v", q, ok)
	}
	if q, ok = roads.MotorwayQueryOf("A1", 5, now, []string{"los_nrw"}); ok || q.Measured || len(q.Plans) != 0 {
		t.Errorf("none: %+v %v", q, ok)
	}
	if !roads.JamUsualFrom(now).Equal(now.Add(-(8*7*24 + 2) * time.Hour)) {
		t.Error("usual from")
	}
	old := now.Add(-(4*7*24 + 1) * time.Hour)
	if !roads.JamHistoryReady(&old, now) || roads.JamHistoryReady(ptr(old.Add(time.Second)), now) || roads.JamHistoryReady(nil, now) {
		t.Error("history ready")
	}
	if roads.Stem("x.de12") != "x" || roads.Stem("x.de") != "x.de" {
		t.Error("stem")
	}
	r := roads.MotorwayEventRow{Road: "A1"}
	if r.Filed() != "A1" {
		t.Error("filed")
	}
	if r.FiledRoad = "A3"; r.Filed() != "A3" {
		t.Error("filed road")
	}
}

func extent(s string) *string { return &s }

func TestMotorwayAnswer(t *testing.T) {
	req.Covers(t, "FR-ROAD-003", "FR-ROAD-004")
	now := at("2026-11-28T10:00:00Z") // a Saturday, 11:00 in Berlin
	jam := roads.MotorwayEventRow{Kind: "warning", ID: "j.de1", TrafficType: ptr("QUEUING_TRAFFIC"), DelayS: ptr(600), Segments: 1,
		SiteExtents:          []*string{extent("50,8,50.01,8"), extent("50.01,8,50.02,8")},
		Description:          lines("Stau", "A1: zwischen 2,5 km hinter AS Nord und AS Süd", "", "Ereignismeldung", "", "- Unfall", "- Reisezeitverlust 10 min", "- "+strings.Repeat("x", 81), "- Gaffer", "- Regen", "- Nebel"),
		RouteRecommendations: json.RawMessage(`["U 17", 4, "U 17", "  ", "` + strings.Repeat("y", 161) + `", {"a": "U 19", "b": "ab AS Nord"}, ["U 21", 7, " "], "U 23"]`)}
	other := roads.MotorwayEventRow{Kind: "warning", ID: "k", TrafficType: ptr("SLOW_TRAFFIC"), Extent: extent("50,8,50.01,8"),
		Description: lines("zwischen Köln und Bonn", "Ereignismeldung:", "- Glätte", "", "- Nebel"), RouteRecommendations: json.RawMessage(`{"x": 1}`)}
	works := roads.MotorwayEventRow{Kind: "roadworks", ID: "r", Segments: 2, Description: lines("Beginn: 01.11.26 um 08:00 Uhr")}
	ticks := []roads.JamTickRow{
		{Group: 0, At: now.Add(-25 * time.Minute), Extents: []*string{extent("50,8,50.005,8")}, DelayS: ptr(300)},
		{Group: 0, At: now.Add(-15 * time.Minute), Extents: []*string{extent("50,8,50.01,8")}, DelayS: ptr(400)},
		{Group: 0, At: now.Add(-40 * time.Minute), Extents: []*string{extent("50,8,50.01,8")}},
		{Group: 0, At: now.Add(-5 * time.Minute), Extents: []*string{extent("50,8,50.02,8")}, DelayS: ptr(600)},
		{Group: 1, At: now.Add(-20 * time.Minute), DelayS: ptr(600)},
		{Group: 1, At: now.Add(-10 * time.Minute), DelayS: ptr(500)},
		{Group: 1, At: now.Add(-1 * time.Minute), DelayS: ptr(300)},
	}
	usual := []roads.JamUsualRow{{I: 1, WeeksObserved: 8, WeeksWithJam: 3, HowCases: 9, HowFrom: ptr(1), HowTo: ptr(3)}, {I: 1}, {I: 2, WeeksObserved: 3, HowCases: 9, HowFrom: ptr(0), HowTo: ptr(2)}}
	read := roads.MotorwayRead{Tick: now, Events: []roads.MotorwayEventRow{jam, other, works}, Lamp: []roads.MotorwayEventRow{jam, other, works},
		Measured: &roads.Measured{Sites: 2, LaneSites: 1}, JamTicks: ticks, JamUsual: usual}
	a := roads.MotorwayAnswerOf(read, 4, now)
	if len(a.Entries) != 3 || a.Entries[2].Jam != nil || a.Entries[2].Phase.Start == nil || len(a.Plans) != 0 ||
		!slices.Equal(a.Families, []string{"los_nrw", "los_nrw_sites", "los_nrw_lanes", "los_nrw_lane_sites"}) || a.LampReasonsTotal != 3 {
		t.Fatalf("%+v", a)
	}
	j := a.Entries[0].Jam
	if j.LengthM == nil || *j.LengthM < 2200 || *j.LengthM > 2240 || j.Tail == nil || j.Head.Lat != 50.02 {
		t.Errorf("extent %+v", j)
	}
	if e := j.End; e == nil || e.Text != "2,5 km hinter AS Nord" || *e.Km != 2.5 || *e.Side != "past" || *e.Place != "AS Nord" {
		t.Errorf("end %+v", j.End)
	}
	if !slices.Equal(j.Causes, []string{"Unfall", "Gaffer", "Regen"}) || !slices.Equal(j.Detours, []string{"U 17", "U 19 – ab AS Nord", "U 21"}) {
		t.Errorf("causes %q detours %q", j.Causes, j.Detours)
	}
	if tr := j.Trend; tr == nil || tr.Metric != "length" || tr.Direction != "growing" || tr.Ticks != 3 {
		t.Errorf("trend %+v", j.Trend)
	}
	if u := j.Usual; u == nil || u.Verdict != "unusual" || u.Weekday != 6 || u.Hour != 11 || j.HowLong == nil || j.HowLong.DayKind != "weekend" {
		t.Errorf("usual %+v %+v", j.Usual, j.HowLong)
	}
	k := a.Entries[1].Jam
	if e := k.End; e == nil || *e.Side != "at" || e.Km != nil || !slices.Equal(k.Causes, []string{"Glätte"}) || len(k.Detours) != 0 {
		t.Errorf("other end %+v", k)
	}
	if tr := k.Trend; tr == nil || tr.Metric != "delay" || tr.Direction != "shrinking" || k.Usual != nil || k.HowLong != nil {
		t.Errorf("other trend %+v %+v", k.Trend, k.Usual)
	}
	// Fewer sweeps, a measured state without lanes, a plan, and the usual verdict.
	read.JamTicks, read.Measured = ticks[:2], &roads.Measured{Sites: 1}
	read.Plans = []roads.PlanNow{{Family: "roadworks_akd"}, {Family: "roadworks_akd"}}
	read.JamUsual = []roads.JamUsualRow{{I: 1, WeeksObserved: 4, WeeksWithJam: 2, HowCases: 8, HowFrom: ptr(2), HowTo: ptr(1)}}
	now = at("2026-11-25T10:00:00Z")
	a = roads.MotorwayAnswerOf(read, 0, now)
	j = a.Entries[0].Jam
	if j.Trend != nil || j.Usual.Verdict != "usual" || j.HowLong != nil || !slices.Equal(a.Families, []string{"los_nrw", "los_nrw_sites", "roadworks_akd"}) {
		t.Errorf("second %+v %+v", j, a.Families)
	}
	read.JamUsual = []roads.JamUsualRow{{I: 1, WeeksObserved: 4, HowCases: 8, HowFrom: ptr(1), HowTo: ptr(1)}}
	if h := roads.MotorwayAnswerOf(read, 0, now).Entries[0].Jam.HowLong; h == nil || h.DayKind != "weekday" || h.Weeks != 8 {
		t.Errorf("weekday %+v", h)
	}
	sunday := at("2026-11-29T22:30:00Z") // 23:30 on Sunday in Berlin
	if j := roads.MotorwayAnswerOf(read, 0, sunday).Entries[0].Jam; j.Usual.Weekday != 7 || j.Usual.Hour != 23 || j.HowLong.DayKind != "weekend" {
		t.Errorf("sunday %+v %+v", j.Usual, j.HowLong)
	}
}

// The extents of a jam: one chain gives its tail and head; a gap, a fork or
// a bad extent none.
func TestMotorwayJamExtent(t *testing.T) {
	req.Covers(t, "FR-ROAD-003")
	now := at("2026-11-25T08:00:00Z")
	for name, c := range map[string]struct {
		ext           []*string
		length, chain bool
	}{
		"single":     {[]*string{extent("50,8,50.01,8")}, true, true},
		"reversed":   {[]*string{extent("50.01,8,50.02,8"), extent("50,8,50.01,8")}, true, true},
		"gap":        {[]*string{extent("50,8,50.01,8"), extent("50.03,8,50.04,8")}, true, false},
		"loop":       {[]*string{extent("50,8,50.01,8"), extent("50.01,8,50,8")}, true, false},
		"fork":       {[]*string{extent("50,8,50.01,8"), extent("50.01,8,50.02,8"), extent("50.01,8,50.02,8.1")}, true, false},
		"null":       {[]*string{nil}, false, false},
		"three":      {[]*string{extent("50,8,50.01")}, false, false},
		"text":       {[]*string{extent("50,8,50.01,x")}, false, false},
		"range":      {[]*string{extent("95,8,50.01,8")}, false, false},
		"zero":       {[]*string{extent("50,8,0,0")}, false, false},
		"empty list": {[]*string{}, false, false},
	} {
		r := roads.MotorwayEventRow{Kind: "warning", TrafficType: ptr("x"), SiteExtents: c.ext}
		j := roads.MotorwayAnswerOf(roads.MotorwayRead{Events: []roads.MotorwayEventRow{r}}, 0, now).Entries[0].Jam
		if (j.LengthM != nil) != c.length || (j.Tail != nil) != c.chain || (j.Head != nil) != c.chain || j.End != nil {
			t.Errorf("%s: %+v", name, j)
		}
	}
	// No site extents: the entry's own.
	r := roads.MotorwayEventRow{Kind: "warning", TrafficType: ptr("x"), Extent: extent("50,8,50.01,8"),
		Description: lines("A1, zwischen "+strings.Repeat("z", 81)+" und B", "zwischen 3 Brücken und B", "kein Ende", "Ereignismeldung", "- A", "Hinweis", "- B"), RouteRecommendations: json.RawMessage(`[{"a": 1}, {]`)}
	j := roads.MotorwayAnswerOf(roads.MotorwayRead{Events: []roads.MotorwayEventRow{r}}, 0, now).Entries[0].Jam
	if j.LengthM == nil || j.End == nil || j.End.Text != "3 Brücken" || j.End.Side != nil || len(j.Detours) != 0 {
		t.Errorf("own extent %+v %+v", j, j.End)
	}
	if got := roads.Jams([]roads.MotorwayEventRow{r, {Kind: "warning"}, {Kind: "closure", TrafficType: ptr("x")}}); len(got) != 1 {
		t.Errorf("jams %d", len(got))
	}
}
