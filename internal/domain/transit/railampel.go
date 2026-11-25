package transit

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
	"time"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// The rail lamp of a station page, as today's rule (RAIL_AMPEL_RULE): over
// the board's trains of the next RailWindowMin minutes, red when a fifth is cancelled,
// two fifths are late or the median delay is a quarter hour, yellow on a
// cancellation, a tenth late, three platform changes, an alert or a blind
// source, green with RailMinTrips trains and none of that, else unknown.
const (
	RailWindowMin    = 60
	RailMinTrips     = 3
	railLateOverMin  = 5
	railRedCancelPct = 20
	railRedLatePct   = 40
	railRedMedianMin = 15
	railYellowLate   = 10
	railYellowMoved  = 3
)

// Travel modes a traveller recognises, as today's travelModeOf.
const (
	TravelLong     = "long"
	TravelRegional = "regional"
	TravelSBahn    = "sbahn"
	TravelSubway   = "subway"
	TravelTram     = "tram"
	TravelBus      = "bus"
	TravelSEV      = "sev"
)

var (
	longName        = regexp.MustCompile(`^(?:ICE|ICD|IC|EC|ECE|EN|NJ|RJ|RJX|FLX|TGV)(?:\s?\d|$)`)
	sbahnName       = regexp.MustCompile(`^S\s?\d`)
	railLineName    = regexp.MustCompile(`^(?:S|RE|RB|IRE|ICE|IC|EC|ECE|RJ|RJX|NJ|EN|FLX|MEX|TGV)\s?\d`)
	replacementWord = regexp.MustCompile(`(?i)(?:^|[^A-Za-z])(?:SEV(?:[^A-Za-z]|$)|Ersatz|Schienenersatz)`)
	evLine          = regexp.MustCompile(`^EV\s?\d`)
)

func isBusRouteType(t int) bool {
	return t == 3 || t == 11 || (t >= 200 && t <= 209) || (t >= 700 && t <= 716) || t == 800
}

func isRailType(t int) bool { return t == 2 || (t >= 100 && t <= 117) }

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// isReplacementBus is today's isReplacementBus of a departure with a route
// type.
func isReplacementBus(line, category string, routeType int) bool {
	if routeType == 714 || category == "SEV" || category == "EV" {
		return true
	}
	name := strings.TrimPrefix(strings.TrimSpace(line), "Bus ")
	bus := category == "BUS" || isBusRouteType(routeType)
	otherwise := (category != "" && category != "BUS") || !isBusRouteType(routeType)
	if !otherwise && (replacementWord.MatchString(name) || evLine.MatchString(name)) {
		return true
	}
	return bus && railLineName.MatchString(name)
}

// TravelOf is a departure's travel mode, as today's travelModeOf of its
// route type, line and category.
func TravelOf(d Departure) string {
	t, line, cat := d.RouteType, strings.TrimSpace(deref(d.Line)), strings.ToUpper(strings.TrimSpace(deref(d.Category)))
	switch {
	case isReplacementBus(deref(d.Line), cat, t):
		return TravelSEV
	case t == 1 || (t >= 400 && t <= 405):
		return TravelSubway
	case t == 0 || (t >= 900 && t <= 906):
		return TravelTram
	case isBusRouteType(t):
		return TravelBus
	case t == 101 || t == 102 || t == 105:
		return TravelLong
	case t == 109:
		return TravelSBahn
	case isRailType(t) && (t == 2 || t == 100 || t == 103) && longName.MatchString(line):
		return TravelLong
	case isRailType(t) && sbahnName.MatchString(line):
		return TravelSBahn
	}
	return TravelRegional
}

// RailReason is one reason of a lamp: Kind cancelled, late, median,
// platform, alert, sight or line, the lamp it raises, and its figures.
type RailReason struct {
	Kind    string
	Lamp    string
	N, Of   int
	Side    string
	Min     int
	Sources []string
	Line    string
	Long    bool
}

// RailLine is the lamp of one line (all long-distance trains are one).
type RailLine struct {
	Line    string
	Long    bool
	Lamp    string
	Trips   int
	Reasons []RailReason
}

// RailAmpel is a station's rail lamp, its reasons and its lines' lamps.
type RailAmpel struct {
	Lamp    string
	Trips   int
	Reasons []RailReason
	Lines   []RailLine
}

var lampOrder = map[string]int{"red": 3, "yellow": 2, "unknown": 1, "green": 0}

type railTrip struct {
	line             string
	long             bool
	delay            *int
	cancelled, moved bool
}

func judgeRail(ts []railTrip, alerts int, sight []string) (string, []RailReason) {
	of := len(ts)
	enough := of >= RailMinTrips
	var cancelled, late, moved int
	var delays []int
	for _, t := range ts {
		if t.cancelled {
			cancelled++
			continue
		}
		if t.delay != nil {
			delays = append(delays, *t.delay)
			if *t.delay > railLateOverMin {
				late++
			}
		}
	}
	for _, t := range ts {
		if t.moved {
			moved++
		}
	}
	atLeast := func(n, pct int) bool { return n*100 >= pct*of }
	var red, yellow []RailReason
	if cancelled > 0 {
		if enough && atLeast(cancelled, railRedCancelPct) {
			red = append(red, RailReason{Kind: "cancelled", Lamp: "red", N: cancelled, Of: of, Side: "dep"})
		} else {
			yellow = append(yellow, RailReason{Kind: "cancelled", Lamp: "yellow", N: cancelled, Of: of, Side: "dep"})
		}
	}
	if enough && late > 0 && atLeast(late, railRedLatePct) {
		red = append(red, RailReason{Kind: "late", Lamp: "red", N: late, Of: of, Side: "dep"})
	} else if enough && late > 0 && atLeast(late, railYellowLate) {
		yellow = append(yellow, RailReason{Kind: "late", Lamp: "yellow", N: late, Of: of, Side: "dep"})
	}
	if enough && len(delays) >= RailMinTrips {
		slices.Sort(delays)
		m := len(delays) / 2
		median := float64(delays[m])
		if len(delays)%2 == 0 {
			median = float64(delays[m-1]+delays[m]) / 2
		}
		if median >= railRedMedianMin {
			red = append(red, RailReason{Kind: "median", Lamp: "red", Min: int(median + 0.5)})
		}
	}
	if moved >= railYellowMoved {
		yellow = append(yellow, RailReason{Kind: "platform", Lamp: "yellow", N: moved})
	}
	if alerts > 0 {
		yellow = append(yellow, RailReason{Kind: "alert", Lamp: "yellow", N: alerts})
	}
	if len(sight) > 0 {
		yellow = append(yellow, RailReason{Kind: "sight", Lamp: "yellow", Sources: sight})
	}
	lamp := "unknown"
	switch {
	case len(red) > 0:
		lamp = "red"
	case len(yellow) > 0:
		lamp = "yellow"
	case enough:
		lamp = "green"
	}
	return lamp, append(red, yellow...)
}

func foldRailLine(s string) string { return strings.ToLower(strings.Join(strings.Fields(s), "")) }

// RailAmpelOf is today's railAmpelOf over the departures planned within
// RailWindowMin minutes of at, no bus among them, and the lines of the alerts in force
// (nil when they could not be read: a blind source); board false when the
// board could not be read.
func RailAmpelOf(at time.Time, deps []Departure, board bool, alerts [][]string) RailAmpel {
	until := at.Add(time.Duration(RailWindowMin) * time.Minute)
	var trips []railTrip
	for _, d := range deps {
		if TravelOf(d) == TravelBus || d.PlannedAt.After(until) {
			continue
		}
		trips = append(trips, railTrip{line: strings.TrimSpace(deref(d.Line)), long: TravelOf(d) == TravelLong, delay: d.DelayMin, cancelled: d.Cancelled, moved: d.PlatformChanged})
	}
	var sight []string
	if alerts == nil {
		sight = []string{"alerts"}
	}
	stationSight := sight
	if !board {
		stationSight = append(slices.Clone(sight), "board")
	}
	lamp, reasons := judgeRail(trips, len(alerts), stationSight)
	var keys []string
	groups := map[string][]railTrip{}
	for _, t := range trips {
		if t.line == "" {
			continue
		}
		k := foldRailLine(t.line)
		if t.long {
			k = "\x00long"
		}
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		groups[k] = append(groups[k], t)
	}
	lines := make([]RailLine, 0, len(keys))
	for _, k := range keys {
		ts := groups[k]
		named := 0
		if !strings.HasPrefix(k, "\x00") {
			for _, a := range alerts {
				if slices.ContainsFunc(a, func(l string) bool { return foldRailLine(l) == k }) {
					named++
				}
			}
		}
		l, r := judgeRail(ts, named, sight)
		lines = append(lines, RailLine{Line: ts[0].line, Long: ts[0].long, Lamp: l, Trips: len(ts), Reasons: r})
	}
	german := collate.New(language.German, collate.Numeric)
	slices.SortStableFunc(lines, func(a, b RailLine) int {
		return cmp.Or(cmp.Compare(boolInt(b.Long), boolInt(a.Long)), german.CompareString(a.Line, b.Line))
	})
	for _, l := range lines {
		if l.Trips < RailMinTrips || (l.Lamp != "red" && l.Lamp != "yellow") {
			continue
		}
		if lampOrder[l.Lamp] > lampOrder[lamp] {
			lamp = l.Lamp
			reasons = append(reasons, RailReason{Kind: "line", Lamp: l.Lamp, Line: l.Line, Long: l.Long})
		}
	}
	slices.SortStableFunc(reasons, func(a, b RailReason) int { return lampOrder[b.Lamp] - lampOrder[a.Lamp] })
	return RailAmpel{Lamp: lamp, Trips: len(trips), Reasons: reasons, Lines: lines}
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
