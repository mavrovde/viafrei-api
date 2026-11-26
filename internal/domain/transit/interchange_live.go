package transit

import (
	"math"
	"slices"
	"strings"
	"time"
	"unicode"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// The interchange's departures as today's readInterchange composes them:
// the DELFI timetable's U-Bahn and tram calls (URBAN_RAIL_AT_STOPS_SQL),
// the DELFI Trip Updates over them where that source is live
// (composeLiveDepartures, its Trip Updates path: tripUpdateFigure, the
// implausible bound, oneTrainPerGroup, dropDeparted), then the window, the
// planned order and the first InterchangeMax.

// The bounds of the read and of the overlay (live-departures.ts).
const (
	// InterchangePlanRead is the calls read before and after now.
	InterchangePlanRead = 30
	// TripUpdatesRead caps the Trip Updates rows read.
	TripUpdatesRead   = 5000
	tuRunWindowH      = 12
	stalePredictionH  = 2
	liveDelayMaxH     = 6
	railTwinWindowMin = 2
	liveFreshCadences = 3
	defaultCadenceS   = 600
)

var (
	tuRunWindow     = tuRunWindowH * time.Hour
	stalePrediction = stalePredictionH * time.Hour
	liveDelayMax    = liveDelayMaxH * time.Hour
	railTwinWindow  = railTwinWindowMin * time.Minute
)

// PlannedCall is one row of the interchange's plan read: a timetable call.
type PlannedCall struct {
	TripID        string
	StopID        string
	StopSequence  int
	At            time.Time
	ServiceDate   string
	RouteType     int
	Line          *string
	TripShortName *string
	Destination   *string
	Platform      *string
	RouteColor    *string
}

// TripUpdateRow is the current state of one stop of a trip, or (no stop
// sequence) of the trip itself, in the DELFI Trip Updates.
type TripUpdateRow struct {
	TripID       string
	StopID       *string
	StopSequence *int
	DelayS       *int
	Relationship string
	EventAt      *time.Time
	StartDate    *string
	ObservedAt   time.Time
}

// FeedCheck is a source's last check: when, its cadence, and whether its
// refresh is paused.
type FeedCheck struct {
	CheckedAt *time.Time
	CadenceS  *int
	Paused    bool
}

// FeedLive is today's liveFeedAt: the check is no older than three
// cadences (600 s when none is stated), or the source is paused.
func FeedLive(c *FeedCheck, now time.Time) bool {
	if c == nil || c.CheckedAt == nil {
		return false
	}
	if c.Paused {
		return true
	}
	cadence := defaultCadenceS
	if c.CadenceS != nil && *c.CadenceS != 0 {
		cadence = *c.CadenceS
	}
	return now.Sub(*c.CheckedAt) <= time.Duration(liveFreshCadences*cadence)*time.Second
}

// tuFigure is a Trip Updates figure of a call (tripUpdateFigure).
type tuFigure struct {
	delayS    *int
	cancelled bool
}

var notRunning = map[string]bool{"CANCELED": true, "CANCELLED": true, "DELETED": true}

// tripUpdateFigure is the call's figure from the rows of its trip: the
// trip cancelled, its own stop skipped, the newest delay at or before its
// stop, or its own stop's time; nil without one.
func tripUpdateFigure(p PlannedCall, rows []TripUpdateRow) *tuFigure {
	ymd := strings.ReplaceAll(p.ServiceDate, "-", "")
	var stopRows []TripUpdateRow
	var tripRow *TripUpdateRow
	newestStop := time.Time{}
	found := false
	for i := range rows {
		r := rows[i]
		if r.TripID != p.TripID || r.ObservedAt.Sub(p.At).Abs() > tuRunWindow ||
			(r.StopSequence == nil && r.StartDate != nil && *r.StartDate != ymd) {
			continue
		}
		found = true
		if r.StopSequence != nil {
			stopRows = append(stopRows, r)
			if r.ObservedAt.After(newestStop) {
				newestStop = r.ObservedAt
			}
		} else if tripRow == nil || r.ObservedAt.After(tripRow.ObservedAt) {
			tripRow = &rows[i]
		}
	}
	if !found {
		return nil
	}
	if tripRow != nil && notRunning[strings.ToUpper(tripRow.Relationship)] && (len(stopRows) == 0 || !tripRow.ObservedAt.Before(newestStop)) {
		return &tuFigure{cancelled: true}
	}
	upTo := slices.DeleteFunc(stopRows, func(r TripUpdateRow) bool { return *r.StopSequence > p.StopSequence })
	slices.SortStableFunc(upTo, func(a, b TripUpdateRow) int { return *b.StopSequence - *a.StopSequence })
	for _, r := range upTo {
		rel := strings.ToUpper(r.Relationship)
		own := *r.StopSequence == p.StopSequence
		switch {
		case rel == "NO_DATA":
			return nil
		case rel == "SKIPPED":
			if own {
				return &tuFigure{cancelled: true}
			}
			continue
		case r.DelayS != nil:
			d := *r.DelayS
			return &tuFigure{delayS: &d}
		case own && r.EventAt != nil:
			off := r.EventAt.Sub(p.At)
			if off.Abs() > tuRunWindow {
				return nil
			}
			d := int(jsRound(off.Seconds()))
			return &tuFigure{delayS: &d}
		}
		return nil
	}
	return nil
}

// jsRound rounds half up, as JavaScript's Math.round.
func jsRound(x float64) float64 { return math.Floor(x + 0.5) }

// composed is one composed call before the window.
type composed struct {
	Departure
	delayS *int
	weight int
}

// ComposeInterchange is today's readInterchange over its reads: each
// planned call with its Trip Updates figure (none when the source is not
// live, an implausible one refused), one train of twins, the departed
// dropped, then the calls planned or leaving at from or later, in planned
// order, the first InterchangeMax, those of the U-Bahn and the tram.
func ComposeInterchange(plan []PlannedCall, tu []TripUpdateRow, tuLive bool, from, now time.Time) []Departure {
	byTrip := map[string][]TripUpdateRow{}
	if tuLive {
		for _, r := range tu {
			byTrip[r.TripID] = append(byTrip[r.TripID], r)
		}
	}
	rows := make([]composed, 0, len(plan))
	for _, p := range plan {
		c := composed{Departure: Departure{JourneyID: p.TripID, StopPointID: p.StopID, CallOrder: p.StopSequence, Line: p.Line,
			RouteType: p.RouteType, TrainNumber: p.TripShortName, Destination: p.Destination, PlannedAt: p.At,
			Platform: p.Platform, PlannedPlatform: p.Platform, Family: InterchangeFamily, Families: []string{InterchangeFamily}}, weight: 1}
		rt := p.RouteType
		c.Mode = BoardModeOf(&rt)
		if f := tripUpdateFigure(p, byTrip[p.TripID]); f != nil {
			c.Families = []string{InterchangeFamily, TripUpdatesFamily}
			c.Live, c.Family, c.weight = true, TripUpdatesFamily, 5
			if f.delayS != nil && time.Duration(math.Abs(float64(*f.delayS)))*time.Second > liveDelayMax {
				c.NoForecast = true
			} else {
				c.delayS, c.Cancelled = f.delayS, f.cancelled
			}
		}
		if c.Platform != nil {
			c.weight += 2
		}
		rows = append(rows, c)
	}
	rows = oneTrain(rows)
	staleBefore := now.Add(-stalePrediction)
	kept := rows[:0]
	for _, c := range rows {
		leaves := c.PlannedAt
		if c.delayS != nil {
			leaves = leaves.Add(time.Duration(*c.delayS) * time.Second)
		}
		if c.PlannedAt.Before(staleBefore) || (c.Cancelled && c.PlannedAt.Before(now)) || (!c.Cancelled && leaves.Before(now)) {
			continue
		}
		if c.PlannedAt.Before(from) && leaves.Before(from) {
			continue
		}
		kept = append(kept, c)
	}
	col := collate.New(language.German)
	slices.SortStableFunc(kept, func(a, b composed) int {
		if n := a.PlannedAt.Compare(b.PlannedAt); n != 0 {
			return n
		}
		return col.CompareString(deref(a.Line), deref(b.Line))
	})
	out := []Departure{}
	for _, c := range kept[:min(InterchangeMax, len(kept))] {
		if c.Mode == "rail" {
			continue
		}
		d := c.Departure
		if c.delayS != nil {
			m := int(jsRound(float64(*c.delayS) / 60))
			e := c.PlannedAt.Add(time.Duration(*c.delayS) * time.Second)
			d.DelayMin, d.ExpectedAt = &m, &e
		}
		out = append(out, d)
	}
	return out
}

// oneTrain is oneTrainPerGroup over one group: in planned order, a call
// that is a twin of one kept is folded into it, the heavier kept, their
// families merged.
func oneTrain(rows []composed) []composed {
	slices.SortStableFunc(rows, func(a, b composed) int { return a.PlannedAt.Compare(b.PlannedAt) })
	var kept []composed
	for _, r := range rows {
		i := slices.IndexFunc(kept, func(k composed) bool { return areTwins(k, r) })
		if i < 0 {
			kept = append(kept, r)
			continue
		}
		fams := mergedFamilies(kept[i].Families, r.Families)
		if r.weight > kept[i].weight {
			kept[i] = r
		}
		kept[i].Families = fams
	}
	return kept
}

func mergedFamilies(a, b []string) []string {
	out := slices.Clone(a)
	for _, f := range b {
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	slices.Sort(out)
	return out
}

func isRailRouteType(t int) bool { return t == 2 || (t >= 100 && t <= 199) }

// areTwins is today's areTwins of two calls of the timetable.
func areTwins(a, b composed) bool {
	if a.Cancelled != b.Cancelled {
		return false
	}
	ra, rb := isRailRouteType(a.RouteType), isRailRouteType(b.RouteType)
	if !ra || !rb {
		if ra || rb {
			return false
		}
		return sameLine(a.Line, b.Line) && sameVehicle(a, b)
	}
	if !sameLine(a.Line, b.Line) {
		return false
	}
	da := destinationKey(a.Destination)
	if da == nil || *da != deref(destinationKey(b.Destination)) {
		return false
	}
	return samePlatform(a.Platform, b.Platform) && a.PlannedAt.Sub(b.PlannedAt).Abs() <= railTwinWindow
}

// lineKey is a line name without white space, in upper case; nil when
// empty.
func lineKey(line *string) *string {
	if line == nil {
		return nil
	}
	k := strings.ToUpper(strings.Join(strings.Fields(*line), ""))
	if k == "" {
		return nil
	}
	return &k
}

func sameKey(a, b *string) bool { return a != nil && b != nil && *a == *b }

func serviceKey(line *string) *string {
	if line == nil {
		return nil
	}
	f := strings.Fields(*line)
	last := ""
	if len(f) > 0 {
		last = f[len(f)-1]
	}
	return lineKey(&last)
}

func lineFormKey(line *string) *string {
	if line == nil {
		return nil
	}
	s := stripBrackets(*line, parens, brackets)
	return lineKey(&s)
}

// stripBrackets replaces, left to right, each run of an open bracket of
// pairs up to its first close (none between) with a space, as the pattern
// \([^)]*\)|\[[^\]]*\] does.
func stripBrackets(s string, pairs ...[2]rune) string {
	var b strings.Builder
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		if k := slices.IndexFunc(pairs, func(p [2]rune) bool { return p[0] == rs[i] }); k >= 0 {
			if j := slices.Index(rs[i+1:], pairs[k][1]); j >= 0 {
				b.WriteRune(' ')
				i += j + 1
				continue
			}
		}
		b.WriteRune(rs[i])
	}
	return b.String()
}

var (
	parens   = [2]rune{'(', ')'}
	brackets = [2]rune{'[', ']'}
)

func sameLine(a, b *string) bool {
	return sameKey(serviceKey(a), serviceKey(b)) || sameKey(lineFormKey(a), lineFormKey(b))
}

// destinationKey folds a destination: lower case, the words Hauptbahnhof
// and Bahnhof as hbf and bf, only its letters and digits; nil when empty.
func destinationKey(d *string) *string {
	if d == nil {
		return nil
	}
	var b, word strings.Builder
	flush := func() {
		switch w := word.String(); w {
		case "hauptbahnhof":
			b.WriteString("hbf")
		case "bahnhof":
			b.WriteString("bf")
		default:
			b.WriteString(w)
		}
		word.Reset()
	}
	for _, r := range strings.ToLower(*d) {
		if unicode.IsLetter(r) {
			word.WriteRune(r)
			continue
		}
		flush()
		if unicode.IsNumber(r) {
			b.WriteRune(r)
		}
	}
	flush()
	k := b.String()
	if k == "" {
		return nil
	}
	return &k
}

func samePlatform(a, b *string) bool {
	pa, pb := strings.TrimSpace(deref(a)), strings.TrimSpace(deref(b))
	return pa == "" || pb == "" || strings.EqualFold(pa, pb)
}

func townWord(d *string) *string {
	if d == nil {
		return nil
	}
	w, _, _ := strings.Cut(NameFold(*d), " ")
	if len([]rune(w)) < 3 {
		return nil
	}
	return &w
}

func sameVehicle(a, b composed) bool {
	if !a.PlannedAt.Equal(b.PlannedAt) || !samePlatform(a.Platform, b.Platform) {
		return false
	}
	if sameKey(destinationKey(a.Destination), destinationKey(b.Destination)) {
		return true
	}
	return sameKey(townWord(a.Destination), townWord(b.Destination)) && !differentDestinations(*a.Destination, *b.Destination)
}

var (
	genericDestinationWords = setOf("bahnhof", "hauptbahnhof", "hbf", "bf", "platz", "strasse", "str", "west", "ost", "nord", "sued",
		"mitte", "zentrum", "gvh", "vrs", "vrr", "via", "ueber", "richtung", "und", "der", "die", "das", "am", "an", "im", "in")
	destinationAbbreviations = map[string]string{"btf": "betriebshof", "bhf": "bahnhof", "bf": "bahnhof", "hbf": "hauptbahnhof",
		"str": "strasse", "pl": "platz", "ri": "richtung"}
	stationKindWords = setOf("bus", "busbf", "busbahnhof", "zob", "omnibusbahnhof", "bushaltestelle")
	stationWords     = setOf("hauptbahnhof", "bahnhof", "bus", "busbf", "busbahnhof", "zob", "omnibusbahnhof", "bushaltestelle")
	umlautFold       = strings.NewReplacer("ä", "ae", "ö", "oe", "ü", "ue", "ß", "ss")
)

func setOf(ws ...string) map[string]bool {
	m := map[string]bool{}
	for _, w := range ws {
		m[w] = true
	}
	return m
}

// destinationTokens are a destination's words: lower case, umlauts and ß
// spelt out, split at any other than a to z and 0 to 9, abbreviations
// written out.
func destinationTokens(d string) []string {
	f := strings.FieldsFunc(umlautFold.Replace(strings.ToLower(d)), func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < '0' || r > '9')
	})
	for i, w := range f {
		if full, ok := destinationAbbreviations[w]; ok {
			f[i] = full
		}
	}
	return f
}

// placeWordsOf are the distinct telling words of a destination without its
// bracketed qualifier, in order, the bus-station words left out.
func placeWordsOf(d string) []string {
	var out []string
	for _, w := range destinationTokens(stripBrackets(d, parens)) {
		if len(w) >= 3 && !genericDestinationWords[w] && !stationKindWords[w] && !slices.Contains(out, w) {
			out = append(out, w)
		}
	}
	return out
}

func sameWord(a, b string) bool {
	return a == b || (min(len(a), len(b)) >= 4 && (strings.HasPrefix(a, b) || strings.HasPrefix(b, a)))
}

func holds(ws []string, w string) bool {
	return slices.ContainsFunc(ws, func(x string) bool { return sameWord(x, w) })
}

func differentPlacesInOneTown(a, b string) bool {
	x, y := placeWordsOf(a), placeWordsOf(b)
	return slices.ContainsFunc(x, func(w string) bool { return !holds(y, w) }) &&
		slices.ContainsFunc(y, func(w string) bool { return !holds(x, w) })
}

func namesOnlyTheStationAgainst(a, b string) bool {
	x, y := placeWordsOf(a), placeWordsOf(b)
	if len(x) != 1 || len(y) < 2 || !sameWord(y[0], x[0]) {
		return false
	}
	return slices.ContainsFunc(destinationTokens(stripBrackets(a, parens)), func(w string) bool { return stationWords[w] })
}

func differentDestinations(a, b string) bool {
	return differentPlacesInOneTown(a, b) || namesOnlyTheStationAgainst(a, b) || namesOnlyTheStationAgainst(b, a)
}
