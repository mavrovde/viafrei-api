package transit

import (
	"testing"
	"time"
)

var icNow = time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)

func sp(s string) *string { return &s }
func ip(i int) *int       { return &i }

func TestFeedLiveAsLiveFeedAt(t *testing.T) {
	old, fresh := icNow.Add(-31*time.Minute), icNow.Add(-29*time.Minute)
	for _, c := range []struct {
		c    *FeedCheck
		want bool
	}{
		{nil, false},
		{&FeedCheck{}, false},
		{&FeedCheck{CheckedAt: &old, Paused: true}, true},
		{&FeedCheck{CheckedAt: &fresh}, true},
		{&FeedCheck{CheckedAt: &old}, false},
		{&FeedCheck{CheckedAt: &fresh, CadenceS: ip(0)}, true},
		{&FeedCheck{CheckedAt: &old, CadenceS: ip(60)}, false},
		{&FeedCheck{CheckedAt: &old, CadenceS: ip(1200)}, true},
	} {
		if got := FeedLive(c.c, icNow); got != c.want {
			t.Errorf("%+v: %v", c.c, got)
		}
	}
}

func TestTripUpdateFigureAsThePlatform(t *testing.T) {
	p := PlannedCall{TripID: "t", StopSequence: 5, At: icNow, ServiceDate: "2026-11-25"}
	row := func(seq *int, rel string, delay *int) TripUpdateRow {
		return TripUpdateRow{TripID: "t", StopSequence: seq, Relationship: rel, DelayS: delay, ObservedAt: icNow}
	}
	at := icNow.Add(90 * time.Second)
	far := icNow.Add(13 * time.Hour)
	trip := row(nil, "CANCELED", nil)
	for name, c := range map[string]struct {
		rows   []TripUpdateRow
		delay  *int
		cancel bool
		none   bool
	}{
		"none":            {rows: nil, none: true},
		"other trip":      {rows: []TripUpdateRow{{TripID: "x", StopSequence: ip(5), DelayS: ip(1), ObservedAt: icNow}}, none: true},
		"old run":         {rows: []TripUpdateRow{{TripID: "t", StopSequence: ip(5), DelayS: ip(1), ObservedAt: far}}, none: true},
		"other day":       {rows: []TripUpdateRow{{TripID: "t", StartDate: sp("20261124"), Relationship: "CANCELED", ObservedAt: icNow}}, none: true},
		"cancelled trip":  {rows: []TripUpdateRow{{TripID: "t", StartDate: sp("20261125"), Relationship: "canceled", ObservedAt: icNow}}, cancel: true},
		"trip then stops": {rows: []TripUpdateRow{trip, {TripID: "t", StopSequence: ip(3), DelayS: ip(60), Relationship: "SCHEDULED", ObservedAt: icNow.Add(time.Minute)}}, delay: ip(60)},
		"older trip row":  {rows: []TripUpdateRow{{TripID: "t", Relationship: "SCHEDULED", ObservedAt: icNow.Add(-time.Minute)}, trip}, cancel: true},
		"running trip":    {rows: []TripUpdateRow{{TripID: "t", Relationship: "SCHEDULED", ObservedAt: icNow}}, none: true},
		"no data":         {rows: []TripUpdateRow{row(ip(4), "NO_DATA", nil)}, none: true},
		"own skipped":     {rows: []TripUpdateRow{row(ip(5), "skipped", nil)}, cancel: true},
		"earlier skipped": {rows: []TripUpdateRow{row(ip(4), "SKIPPED", nil), row(ip(2), "SCHEDULED", ip(120))}, delay: ip(120)},
		"newest earlier":  {rows: []TripUpdateRow{row(ip(2), "SCHEDULED", ip(30)), row(ip(4), "SCHEDULED", ip(90)), row(ip(6), "SCHEDULED", ip(600))}, delay: ip(90)},
		"own time":        {rows: []TripUpdateRow{{TripID: "t", StopSequence: ip(5), Relationship: "SCHEDULED", EventAt: &at, ObservedAt: icNow}}, delay: ip(90)},
		"own time far":    {rows: []TripUpdateRow{{TripID: "t", StopSequence: ip(5), Relationship: "SCHEDULED", EventAt: &far, ObservedAt: icNow}}, none: true},
		"earlier no time": {rows: []TripUpdateRow{{TripID: "t", StopSequence: ip(4), Relationship: "SCHEDULED", EventAt: &at, ObservedAt: icNow}}, none: true},
		"later only":      {rows: []TripUpdateRow{row(ip(6), "SCHEDULED", ip(60))}, none: true},
	} {
		f := tripUpdateFigure(p, c.rows)
		switch {
		case c.none:
			if f != nil {
				t.Errorf("%s: %+v", name, f)
			}
		case f == nil || f.cancelled != c.cancel || (c.delay == nil) != (f.delayS == nil) || (c.delay != nil && *c.delay != *f.delayS):
			t.Errorf("%s: %+v", name, f)
		}
	}
}

func TestComposeInterchangeAsReadInterchange(t *testing.T) {
	at := func(m int) time.Time { return icNow.Add(time.Duration(m) * time.Minute) }
	plan := []PlannedCall{
		{TripID: "gone", StopSequence: 1, At: at(-5), RouteType: 400, Line: sp("U1"), Destination: sp("A")},
		{TripID: "late", StopSequence: 1, At: at(-5), RouteType: 400, Line: sp("U2"), Destination: sp("B"), Platform: sp("1")},
		{TripID: "stale", StopSequence: 1, At: at(-150), RouteType: 400, Line: sp("U3"), Destination: sp("C")},
		{TripID: "cxpast", StopSequence: 1, At: at(-1), RouteType: 0, Line: sp("4"), Destination: sp("D")},
		{TripID: "cx", StopSequence: 1, At: at(3), RouteType: 0, Line: sp("5"), Destination: sp("E")},
		{TripID: "wild", StopSequence: 1, At: at(4), RouteType: 900, Line: sp("6"), Destination: sp("F")},
		{TripID: "b", StopSequence: 1, At: at(6), RouteType: 900, Line: sp("Ä7"), Destination: sp("G")},
		{TripID: "a", StopSequence: 1, At: at(6), RouteType: 900, Line: sp("A7"), Destination: sp("G")},
		{TripID: "twin1", StopSequence: 1, At: at(8), RouteType: 900, Line: sp("8"), Destination: sp("Messe")},
		{TripID: "twin2", StopSequence: 1, At: at(8), RouteType: 900, Line: sp("8"), Destination: sp("Messe"), Platform: sp("2")},
		{TripID: "early", StopSequence: 1, At: at(9), RouteType: 1, Line: sp("U9"), Destination: sp("H")},
	}
	tu := []TripUpdateRow{
		{TripID: "late", StopSequence: ip(1), Relationship: "SCHEDULED", DelayS: ip(600), ObservedAt: icNow},
		{TripID: "cxpast", Relationship: "CANCELED", ObservedAt: icNow},
		{TripID: "cx", Relationship: "CANCELED", ObservedAt: icNow},
		{TripID: "wild", StopSequence: ip(1), Relationship: "SCHEDULED", DelayS: ip(7 * 3600), ObservedAt: icNow},
		{TripID: "early", StopSequence: ip(1), Relationship: "SCHEDULED", DelayS: ip(-90), ObservedAt: icNow},
	}
	got := ComposeInterchange(plan, tu, true, icNow, icNow)
	want := []string{"late", "cx", "wild", "a", "b", "twin2", "early"}
	if len(got) != len(want) {
		t.Fatalf("%d rows: %+v", len(got), got)
	}
	for i, w := range want {
		if got[i].JourneyID != w {
			t.Errorf("%d: %s, want %s", i, got[i].JourneyID, w)
		}
	}
	if d := got[0]; !d.Live || *d.DelayMin != 10 || !d.ExpectedAt.Equal(at(5)) || d.Family != TripUpdatesFamily {
		t.Errorf("late %+v", d)
	}
	if d := got[1]; !d.Cancelled || d.DelayMin != nil {
		t.Errorf("cancelled %+v", d)
	}
	if d := got[2]; !d.Live || !d.NoForecast || d.DelayMin != nil || d.Cancelled {
		t.Errorf("implausible %+v", d)
	}
	if d := got[5]; *d.Platform != "2" || len(d.Families) != 1 {
		t.Errorf("twin %+v", d)
	}
	if d := got[6]; *d.DelayMin != -1 || d.Mode != "subway" {
		t.Errorf("early %+v", d)
	}
	// Not live: the timetable alone, the late U2 gone.
	if got := ComposeInterchange(plan, tu, false, icNow, icNow); len(got) != 6 || got[0].JourneyID != "cx" || got[0].Live {
		t.Errorf("timetable %+v", got)
	}
	// From later than now: a call planned before from that leaves after it stays.
	if got := ComposeInterchange(plan, tu, true, at(4), icNow); len(got) != 6 || got[0].JourneyID != "late" {
		t.Errorf("from %+v", got)
	}
}

func TestTwinsAsAreTwins(t *testing.T) {
	c := func(line, dest, plat string, rt, m int, cancelled bool) composed {
		d := composed{Departure: Departure{RouteType: rt, PlannedAt: icNow.Add(time.Duration(m) * time.Minute), Cancelled: cancelled}}
		if line != "" {
			d.Line = &line
		}
		if dest != "" {
			d.Destination = &dest
		}
		if plat != "" {
			d.Platform = &plat
		}
		return d
	}
	for name, x := range map[string]struct {
		a, b composed
		want bool
	}{
		"cancelled differs":    {c("U1", "A", "", 1, 0, false), c("U1", "A", "", 1, 0, true), false},
		"rail and urban":       {c("U1", "A", "", 2, 0, false), c("U1", "A", "", 1, 0, false), false},
		"urban same":           {c("U 1", "Hauptbahnhof", "", 1, 0, false), c("U1", "Hbf", "", 1, 0, false), true},
		"urban other minute":   {c("U1", "A", "", 1, 0, false), c("U1", "A", "", 1, 1, false), false},
		"urban other line":     {c("U1", "A", "", 1, 0, false), c("U2", "A", "", 1, 0, false), false},
		"urban platforms":      {c("U1", "A", "1", 1, 0, false), c("U1", "A", "2", 1, 0, false), false},
		"urban same town":      {c("12", "Köln Messe", "", 0, 0, false), c("12", "Köln Messe/Deutz", "", 0, 0, false), true},
		"urban other place":    {c("12", "Köln Ost", "", 0, 0, false), c("12", "Köln Messe", "", 0, 0, false), true},
		"urban two places":     {c("12", "Köln Sülz", "", 0, 0, false), c("12", "Köln Messe", "", 0, 0, false), false},
		"urban station only":   {c("12", "Köln Hbf", "", 0, 0, false), c("12", "Köln Messe", "", 0, 0, false), false},
		"urban no town":        {c("12", "Ab", "", 0, 0, false), c("12", "Ab X", "", 0, 0, false), false},
		"urban no destination": {c("12", "", "", 0, 0, false), c("12", "", "", 0, 0, false), false},
		"rail same":            {c("RE 1", "Köln Hbf", "", 2, 0, false), c("RE1", "Köln Hauptbahnhof", "", 106, 2, false), true},
		"rail far":             {c("RE 1", "Köln Hbf", "", 2, 0, false), c("RE1", "Köln Hbf", "", 2, 3, false), false},
		"rail other line":      {c("RE 1", "Köln Hbf", "", 2, 0, false), c("RE 2", "Köln Hbf", "", 2, 0, false), false},
		"rail no destination":  {c("RE 1", "", "", 2, 0, false), c("RE 1", "", "", 2, 0, false), false},
		"rail other dest":      {c("RE 1", "Köln", "", 2, 0, false), c("RE 1", "Bonn", "", 2, 0, false), false},
		"line forms":           {c("S1 (Ersatz)", "A", "", 0, 0, false), c("S1", "A", "", 0, 0, false), true},
	} {
		if got := areTwins(x.a, x.b); got != x.want {
			t.Errorf("%s: %v", name, got)
		}
	}
}

func TestDestinationWordsAsThePlatform(t *testing.T) {
	if lineKey(nil) != nil || lineKey(sp(" ")) != nil || *lineKey(sp("u 1")) != "U1" {
		t.Error("lineKey")
	}
	if serviceKey(nil) != nil || serviceKey(sp("")) != nil || *serviceKey(sp("Bus 12")) != "12" {
		t.Error("serviceKey")
	}
	if lineFormKey(nil) != nil || *lineFormKey(sp("[a(b]c)")) != "C)" || *lineFormKey(sp("x(y")) != "X(Y" {
		t.Error("lineFormKey")
	}
	if destinationKey(nil) != nil || destinationKey(sp("--")) != nil || *destinationKey(sp("Bonn Hauptbahnhof 2")) != "bonnhbf2" ||
		*destinationKey(sp("Bahnhof-Süd")) != "bfsüd" {
		t.Error("destinationKey")
	}
	if townWord(nil) != nil || townWord(sp("Ab")) != nil || *townWord(sp("Köln Ost")) != "koln" {
		t.Error("townWord")
	}
	if got := placeWordsOf("Btf Köln (Rhein) Str Köln ZOB"); len(got) != 2 || got[0] != "betriebshof" || got[1] != "koeln" {
		t.Errorf("placeWordsOf %v", got)
	}
	if !sameWord("koel", "koeln") || sameWord("koe", "koeln") || !sameWord("ab", "ab") {
		t.Error("sameWord")
	}
	if differentDestinations("Köln Messe", "Köln Messe/Deutz") || !differentDestinations("Köln Sülz", "Köln Messe") ||
		!differentDestinations("Köln Messe", "Köln Bahnhof") || differentDestinations("Köln Ost", "Köln Ostheim") {
		t.Error("differentDestinations")
	}
	if namesOnlyTheStationAgainst("Köln", "Köln Messe") || namesOnlyTheStationAgainst("Köln Hbf", "Bonn Messe") {
		t.Error("namesOnlyTheStationAgainst")
	}
	if got := mergedFamilies([]string{"tripupdates"}, []string{"delfi_static", "tripupdates"}); len(got) != 2 || got[0] != "delfi_static" {
		t.Errorf("mergedFamilies %v", got)
	}
	if !samePlatform(nil, sp("1")) || !samePlatform(sp(" a "), sp("A")) || samePlatform(sp("1"), sp("2")) {
		t.Error("samePlatform")
	}
}
