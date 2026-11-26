package roads

import (
	"bytes"
	"encoding/json"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/ampel"
)

// The motorway answer as the platform's motorway traffic tool gives it: the
// road operator's events at the road's newest sweep, one entry per site (a
// measure published as several sections is one entry), the counts of the
// whole road, the measured state at the counting sites, the plan's
// short-duration works in force and each jam's detail. A port of the
// platform's tool, its jam detail and its description and phase readers.

// The tool's bounds.
const (
	// MotorwayLampRows are the most entries the lamp reads.
	MotorwayLampRows = 300
	// MotorwayPlansMax is the most plan rows read.
	MotorwayPlansMax = 20
	// MotorwayMeasuredFreshMin is how old a measured state may be.
	MotorwayMeasuredFreshMin = 30
	// MotorwayDescriptionLines and MotorwayDescriptionMax are the lines of
	// a description shown and the characters of each.
	MotorwayDescriptionLines = 2
	MotorwayDescriptionMax   = 200
)

// MotorwayKinds are the kinds the tool lists.
var MotorwayKinds = []string{"warning", "roadworks", "closure"}

// MotorwayPlanFamilies are the plan sources of the short-duration works.
var MotorwayPlanFamilies = []string{"roadworks_akd"}

// The families the motorway answer reads beside its situations.
const MotorwayFamily = "autobahn"

// MeasuredFamilies and LaneFamilies are the measured state's families, at
// the sites and per lane.
var (
	MeasuredFamilies = []string{"los_nrw", "los_nrw_sites"}
	LaneFamilies     = []string{"los_nrw_lanes", "los_nrw_lane_sites"}
)

// MotorwayQuery asks a motorway's answer as the platform's motorway tool
// reads it: Limit entries of the road at its newest sweep, at Now.
type MotorwayQuery struct {
	Road  string
	Limit int
	Now   time.Time
	// Plans are the plan families the key may read; Measured and Lanes say
	// it may read the measured state at the sites and per lane.
	Plans           []string
	Measured, Lanes bool
}

// MotorwayQueryOf is the query for road with limit for a key that may read
// permitted; false when it may not read the road operator's events.
func MotorwayQueryOf(road string, limit int, now time.Time, permitted []string) (MotorwayQuery, bool) {
	all := func(fams []string) bool {
		return !slices.ContainsFunc(fams, func(f string) bool { return !slices.Contains(permitted, f) })
	}
	plans := slices.DeleteFunc(slices.Clone(MotorwayPlanFamilies), func(f string) bool { return !slices.Contains(permitted, f) })
	measured := all(MeasuredFamilies)
	return MotorwayQuery{Road: road, Limit: limit, Now: now, Plans: plans, Measured: measured, Lanes: measured && all(LaneFamilies)},
		slices.Contains(permitted, MotorwayFamily)
}

// MotorwayEntry is one entry of the answer with what the tool shows of it.
type MotorwayEntry struct {
	MotorwayEventRow
	DescriptionShown []string
	Phase            Phase
	Jam              *JamDetail
}

// MotorwayAnswer is the road's answer as the platform's motorway tool gives
// it.
type MotorwayAnswer struct {
	Tick             time.Time
	Counts           MotorwayCounts
	Measured         *Measured
	LampReasonsTotal int
	Plans            []PlanNow
	Entries          []MotorwayEntry
	// Families are the families beyond the road operator's that it states.
	Families []string
}

// MotorwayAnswerOf is the answer from read, reasons the lamp's reasons over
// every section.
func MotorwayAnswerOf(read MotorwayRead, reasons int, now time.Time) MotorwayAnswer {
	out := MotorwayAnswer{Tick: read.Tick, Counts: CountsOf(read.Counts), Measured: read.Measured, LampReasonsTotal: LampReasonsFolded(reasons, read.Lamp),
		Plans: read.Plans, Entries: make([]MotorwayEntry, len(read.Events)), Families: []string{}}
	if out.Plans == nil {
		out.Plans = []PlanNow{}
	}
	jams := Jams(read.Events)
	details := JamDetailsOf(jams, read.JamTicks, read.JamUsual, now)
	j := 0
	for i, r := range read.Events {
		out.Entries[i] = MotorwayEntry{MotorwayEventRow: r, DescriptionShown: DescriptionShown(r.Description), Phase: PhaseOf(r.Description)}
		if r.IsJam() {
			out.Entries[i].Jam = &details[j]
			j++
		}
	}
	if m := read.Measured; m != nil {
		out.Families = append(out.Families, MeasuredFamilies...)
		if m.LaneSites > 0 {
			out.Families = append(out.Families, LaneFamilies...)
		}
	}
	for _, p := range out.Plans {
		if !slices.Contains(out.Families, p.Family) {
			out.Families = append(out.Families, p.Family)
		}
	}
	return out
}

// MotorwayEventRow is one entry as the tool's events statement answers it.
type MotorwayEventRow struct {
	Road, Kind, ID       string
	FiledRoad            string
	Title, Subtitle      *string
	TrafficType          *string
	DelayS               *int
	Extent               *string
	Lat, Lon             *float64
	StartText            *string
	StartsAt             *time.Time
	Description          any
	Blocked, AverageKmh  *string
	Segments             int
	SiteIDs              []string
	SiteExtents          []*string
	RouteRecommendations []byte
}

// MotorwayCountRow is one kind's count of a road.
type MotorwayCountRow struct {
	Kind          string
	N, Jams       int
	Announced     int
	AnnouncedFrom *time.Time
}

// MotorwayCounts are the road's counts in force and announced.
type MotorwayCounts struct {
	Jams, Warnings, Closures, Roadworks int
	AnnouncedClosures                   int
	AnnouncedRoadworks                  int
	AnnouncedReports                    int
	AnnouncedFrom                       *time.Time
}

// CountsOf folds the counts statement's rows, as the tool does.
func CountsOf(rows []MotorwayCountRow) MotorwayCounts {
	var k MotorwayCounts
	for _, r := range rows {
		switch r.Kind {
		case "warning":
			k.Warnings, k.Jams, k.AnnouncedReports = r.N, r.Jams, r.Announced
		case "closure":
			k.Closures, k.AnnouncedClosures = r.N, r.Announced
		case "roadworks":
			k.Roadworks, k.AnnouncedRoadworks = r.N, r.Announced
		default:
			continue
		}
		if r.AnnouncedFrom != nil && (k.AnnouncedFrom == nil || r.AnnouncedFrom.Before(*k.AnnouncedFrom)) {
			k.AnnouncedFrom = r.AnnouncedFrom
		}
	}
	return k
}

// Measured is the measured state at the counting sites of one road.
type Measured struct {
	Sites, Standstill, Slow, Heavy, Free, LaneSites int
	MeasuredAt                                      *time.Time
	SlowNear                                        []string
}

// PlanNow is one short-duration works of the plan in force now.
type PlanNow struct {
	Family, ID, Road, Kind string
	Name, Section, Cause   *string
	From, To               *time.Time
	OnRoad                 int
}

// JamTickRow is one sweep of a jam asked for its trend.
type JamTickRow struct {
	Group   int
	At      time.Time
	Extents []*string
	DelayS  *int
}

// JamUsualRow is one jam's history at this hour.
type JamUsualRow struct {
	I, WeeksObserved, WeeksWithJam, HowCases int
	HowFrom, HowTo                           *int
}

// MotorwayRead is what the tool's statements answered for a road.
type MotorwayRead struct {
	Tick     time.Time
	Counts   []MotorwayCountRow
	Events   []MotorwayEventRow
	Lamp     []MotorwayEventRow
	Measured *Measured
	Plans    []PlanNow
	// JamTicks and JamUsual answer the jams of Events, in their order;
	// HistoryReady says the held history reaches the usual's minimum.
	JamTicks     []JamTickRow
	JamUsual     []JamUsualRow
	HistoryReady bool
}

// IsJam is a warning with a traffic type.
func (r MotorwayEventRow) IsJam() bool { return r.Kind == "warning" && r.TrafficType != nil }

// Jams are the jams among rows, in their order.
func Jams(rows []MotorwayEventRow) []MotorwayEventRow {
	out := []MotorwayEventRow{}
	for _, r := range rows {
		if r.IsJam() {
			out = append(out, r)
		}
	}
	return out
}

// FiledRoad is the road a jam is filed under, the asked road when none.
func (r MotorwayEventRow) Filed() string {
	if r.FiledRoad == "" {
		return r.Road
	}
	return r.FiledRoad
}

var stemSuffix = regexp.MustCompile(`\.de\d+$`)

// Stem is the site's id without its section suffix.
func Stem(id string) string { return stemSuffix.ReplaceAllString(id, "") }

// LampReasonsFolded is the lamp's reason count over a road once each site is
// one reason: total, the reasons of every section, less the sections beyond
// the first of each listed site whose entry gives the lamp a reason.
func LampReasonsFolded(total int, lamp []MotorwayEventRow) int {
	for _, r := range lamp {
		if r.Segments <= 1 {
			continue
		}
		if a := ampel.Of([]ampel.Signal{r.signal()}, nil); len(a.Reasons) > 0 {
			total -= r.Segments - 1
		}
	}
	return max(total, 0)
}

func (r MotorwayEventRow) signal() ampel.Signal {
	var d *float64
	if r.DelayS != nil {
		v := float64(*r.DelayS)
		d = &v
	}
	return ampel.OperatorEventSignal(ampel.OperatorEvent{Kind: r.Kind, TrafficType: r.TrafficType, DelayS: d, Title: r.Title, Subtitle: r.Subtitle,
		Description: descriptionLines(r.Description), Blocked: r.Blocked != nil && *r.Blocked == "true"})
}

func descriptionLines(d any) []*string {
	out := []*string{}
	if a, ok := d.([]any); ok {
		for _, x := range a {
			if s, ok := x.(string); ok {
				out = append(out, &s)
			}
		}
	}
	return out
}

// jsSpace is JavaScript's white space, which \s matches in the platform's
// expressions.
const jsSpace = `\t\n\v\f\r \x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}`

// js reads \s and \S in pattern as JavaScript does.
func js(pattern string) string {
	return strings.NewReplacer(`\s`, "["+jsSpace+"]", `\S`, "[^"+jsSpace+"]").Replace(pattern)
}

// Omitted marks text left out of a description.
const Omitted = "[…]"

var seitClause = regexp.MustCompile(js(`(?i),?\s*seit\s+\d{1,2}\.\d{1,2}\.\d{2,4},?\s+\d{1,2}:\d{2}(?:\s*Uhr)?`))

var beginLine = regexp.MustCompile(`(?i)^Beginn:`)

// jsTrim trims as JavaScript's trim does.
func jsTrim(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return strings.ContainsRune("\t\n\v\f\r       　\ufeff", r) || (r >= 0x2000 && r <= 0x200a)
	})
}

// CleanDescription is the description as the tool prints it: without its
// start line and its "seit" clauses (each marked omitted), a heading over
// nothing dropped.
func CleanDescription(description any) []string {
	type kept struct {
		text   string
		marked bool
	}
	var ks []kept
	pending := false
	a, _ := description.([]any)
	for _, raw := range a {
		s, _ := raw.(string)
		s = jsTrim(s)
		if s == "" {
			ks = append(ks, kept{})
			continue
		}
		if beginLine.MatchString(s) {
			pending = true
			continue
		}
		t := jsTrim(seitClause.ReplaceAllString(s, ""))
		if t == "" {
			pending = true
			continue
		}
		if t != s {
			t = jsTrim(seitClause.ReplaceAllString(s, " "+Omitted))
		}
		ks = append(ks, kept{t, pending})
		pending = false
	}
	var out []kept
	carry := false
	for i, k := range ks {
		if k.text == "" {
			continue
		}
		if strings.HasSuffix(k.text, ":") && (i+1 >= len(ks) || ks[i+1].text == "") {
			carry = true
			continue
		}
		out = append(out, kept{k.text, k.marked || carry})
		carry = false
	}
	lines := make([]string, len(out))
	for i, k := range out {
		lines[i] = k.text
		if k.marked && !strings.HasPrefix(k.text, Omitted) {
			lines[i] = Omitted + " " + k.text
		}
	}
	if last := len(lines) - 1; (pending || carry) && last >= 0 && !strings.HasSuffix(lines[last], Omitted) {
		lines[last] += " " + Omitted
	}
	return lines
}

// DescriptionShown is the description's lines the tool shows, each clipped
// at a word.
func DescriptionShown(description any) []string {
	lines := CleanDescription(description)
	lines = lines[:min(len(lines), MotorwayDescriptionLines)]
	for i, l := range lines {
		lines[i] = ClipAtWord(l, MotorwayDescriptionMax)
	}
	return lines
}

// ClipMark ends a clipped text.
const ClipMark = "…"

var clipTail = regexp.MustCompile(`[` + jsSpace + `,;:(\-–—/|·]+$`)

// ClipAtWord cuts text to max characters at the last word boundary inside
// it, marked.
func ClipAtWord(text string, maxLen int) string {
	r := []rune(text)
	if len(r) <= maxLen {
		return text
	}
	room := max(0, maxLen-len([]rune(ClipMark)))
	cut := -1
	if room < len(r) && isSpace(r[room]) {
		cut = room
	} else {
		for i := room - 1; i >= 0; i-- {
			if isSpace(r[i]) {
				cut = i
				break
			}
		}
	}
	if cut <= 0 {
		cut = room
	}
	head := clipTail.ReplaceAllString(string(r[:cut]), "")
	if head == "" {
		head = string(r[:room])
	}
	return head + ClipMark
}

func isSpace(r rune) bool {
	return strings.ContainsRune(" \t\n\v\f\r      　\ufeff", r) || (r >= 0x2000 && r <= 0x200a)
}

// Phase is a works' phase start and end and the whole measure's end, as its
// description states them.
type Phase struct {
	Start, End, MeasureEnd *time.Time
}

type stamp struct{ y, mo, d, h, mi int }

func (s stamp) key() int { return (((s.y*100+s.mo)*100+s.d)*100+s.h)*100 + s.mi }

var berlinZone, _ = time.LoadLocation("Europe/Berlin")

// berlinOffset is Berlin's offset from UTC at the instant at.
func berlinOffset(at time.Time) time.Duration {
	_, off := at.In(berlinZone).Zone()
	return time.Duration(off) * time.Second
}

// instant is the Berlin wall-clock stamp's instant, as the platform reads
// it: a doubled hour its later occurrence, a missing one shifted by the
// offset before it.
func (s stamp) instant() time.Time {
	wall := time.Date(s.y, time.Month(s.mo), s.d, s.h, s.mi, 0, 0, time.UTC)
	before, after := berlinOffset(wall.AddDate(0, 0, -1)), berlinOffset(wall.AddDate(0, 0, 1))
	var valid *time.Time
	for _, off := range []time.Duration{before, after} {
		if at := wall.Add(-off); at.Add(berlinOffset(at)).Equal(wall) && (valid == nil || at.After(*valid)) {
			valid = &at
		}
	}
	if valid != nil {
		return *valid
	}
	return wall.Add(-before)
}

var (
	stampRE    = regexp.MustCompile(js(`(\d{1,2})\.(\d{1,2})\.(\d{2,4})(?:\s+(?:um\s+)?(\d{1,2}):(\d{2}))?`))
	sameDayRE  = regexp.MustCompile(`^(\d{1,2}\.\d{1,2}\.\d{2,4}) von (\d{1,2}):(\d{2}) bis (\d{1,2}):(\d{2}) Uhr\.?$`)
	spanRE     = regexp.MustCompile(`^(\d{1,2}\.\d{1,2}\.\d{2,4} \d{1,2}:\d{2})(?: Uhr)? bis zum (\d{1,2}\.\d{1,2}\.\d{2,4} \d{1,2}:\d{2}) Uhr\.?$`)
	measureRE  = regexp.MustCompile(`^\(?Ende der Gesamtma(?:ß|ss)nahme:`)
	spacesRE   = regexp.MustCompile(js(`\s+`))
	atoiOrZero = func(s string) int { n, _ := strconv.Atoi(s); return n }
)

func stampOf(s string, hm ...string) *stamp {
	m := stampRE.FindStringSubmatch(s)
	if m == nil {
		return nil
	}
	y := atoiOrZero(m[3])
	if len(m[3]) == 2 {
		y += 2000
	}
	h, mi := m[4], m[5]
	if len(hm) == 2 {
		h, mi = hm[0], hm[1]
	}
	out := stamp{y, atoiOrZero(m[2]), atoiOrZero(m[1]), atoiOrZero(h), atoiOrZero(mi)}
	if out.mo < 1 || out.mo > 12 || out.d < 1 || out.d > 31 || out.h > 24 || out.mi > 59 {
		return nil
	}
	return &out
}

type window struct{ from, to stamp }

func windowOf(l string) *window {
	if m := sameDayRE.FindStringSubmatch(l); m != nil {
		from, to := stampOf(m[1], m[2], m[3]), stampOf(m[1], m[4], m[5])
		if from == nil || to == nil {
			return nil
		}
		return &window{*from, *to}
	}
	if m := spanRE.FindStringSubmatch(l); m != nil {
		from, to := stampOf(m[1]), stampOf(m[2])
		if from == nil || to == nil {
			return nil
		}
		return &window{*from, *to}
	}
	return nil
}

// PhaseOf reads a works' phase from its description, as the platform's
// works reader does; nil members where it states none.
func PhaseOf(description any) Phase {
	a, _ := description.([]any)
	var begin, end, measure *stamp
	var windows []window
	for _, x := range a {
		s, ok := x.(string)
		if !ok {
			continue
		}
		l := jsTrim(spacesRE.ReplaceAllString(s, " "))
		switch {
		case l == "" || strings.HasPrefix(l, "Länge:") || strings.Contains(l, "Durchfahrtsbreite:"):
		case strings.HasPrefix(l, "Beginn:"):
			begin = stampOf(l)
		case strings.HasPrefix(l, "Ende:"):
			end = stampOf(l)
		case measureRE.MatchString(l):
			measure = stampOf(l)
		default:
			if w := windowOf(l); w != nil {
				windows = append(windows, *w)
			}
		}
	}
	var earliest, latest *stamp
	for _, w := range windows {
		if earliest == nil || w.from.key() < earliest.key() {
			earliest = &w.from
		}
		if latest == nil || latest.key() < w.to.key() {
			latest = &w.to
		}
	}
	start, stop := begin, end
	if start == nil && end == nil {
		start = earliest
	}
	if stop == nil && begin == nil {
		stop = latest
	}
	at := func(s *stamp) *time.Time {
		if s == nil {
			return nil
		}
		t := s.instant()
		return &t
	}
	return Phase{Start: at(start), End: at(stop), MeasureEnd: at(measure)}
}

// The jam detail's bounds, the platform's.
const (
	JamChainTouchM      = 50
	JamEndTextMax       = 80
	JamCausesMax        = 3
	JamRouteMax         = 3
	JamRouteTextMax     = 160
	JamTrendWindowMin   = 30
	JamTrendMinTicks    = 3
	JamTrendLengthM     = 500
	JamTrendDelayS      = 120
	JamTrendShare       = 0.1
	JamUsualMinWeeks    = 4
	JamUsualWeeksBack   = 8
	JamHowLongMinCases  = 8
	earthRadiusM        = 6_371_008.8
	jamHistorySlackHour = 1
)

// LatLon is a point.
type LatLon struct{ Lat, Lon float64 }

// JamEnd is where a jam ends, as its description words it.
type JamEnd struct {
	Text  string
	Km    *float64
	Side  *string
	Place *string
}

// JamTrend is a jam's length or delay over the last half hour.
type JamTrend struct {
	Direction, Metric string
	EarlierAt         time.Time
	Earlier           float64
	LatestAt          time.Time
	Latest            float64
	Ticks             int
}

// JamUsual is whether a jam stands here at this hour in most weeks.
type JamUsual struct {
	Verdict         string
	Weeks, JamWeeks int
	Weekday, Hour   int
}

// JamDetail is one jam's detail.
type JamDetail struct {
	LengthM *float64
	Tail    *LatLon
	Head    *LatLon
	End     *JamEnd
	Causes  []string
	Detours []string
	Trend   *JamTrend
	Usual   *JamUsual
	HowLong *JamHowLong
}

// JamHowLong is how long a jam here lasts, the middle half of its runs.
type JamHowLong struct {
	Cases, FromHours, ToHours int
	DayKind                   string
	Weeks                     int
}

func metresBetween(a, b LatLon) float64 {
	rad := math.Pi / 180
	dLat, dLon := (b.Lat-a.Lat)*rad, (b.Lon-a.Lon)*rad
	h := math.Pow(math.Sin(dLat/2), 2) + math.Cos(a.Lat*rad)*math.Cos(b.Lat*rad)*math.Pow(math.Sin(dLon/2), 2)
	return 2 * earthRadiusM * math.Asin(math.Min(1, math.Sqrt(h)))
}

type segment struct{ from, to LatLon }

var numberRE = regexp.MustCompile(`^-?\d+(?:\.\d+)?$`)

func coordinate(lat, lon float64) *LatLon {
	if math.Abs(lat) > 90 || math.Abs(lon) > 180 || (lat == 0 && lon == 0) {
		return nil
	}
	return &LatLon{lat, lon}
}

func extentOf(e *string) *segment {
	if e == nil {
		return nil
	}
	parts := strings.Split(*e, ",")
	if len(parts) != 4 {
		return nil
	}
	v := make([]float64, 4)
	for i, p := range parts {
		p = jsTrim(p)
		if !numberRE.MatchString(p) {
			return nil
		}
		v[i], _ = strconv.ParseFloat(p, 64)
	}
	from, to := coordinate(v[0], v[1]), coordinate(v[2], v[3])
	if from == nil || to == nil {
		return nil
	}
	return &segment{*from, *to}
}

// indices are 0 to n-1.
func indices(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

// jamExtent is the jam's length and, when its sections chain, its tail and
// head.
func jamExtent(extents []*string) (length *float64, tail, head *LatLon) {
	if len(extents) == 0 {
		return nil, nil, nil
	}
	segs := make([]segment, len(extents))
	total := 0.0
	for i, e := range extents {
		s := extentOf(e)
		if s == nil {
			return nil, nil, nil
		}
		segs[i] = *s
		total += metresBetween(s.from, s.to)
	}
	touches := func(a, b LatLon) bool { return metresBetween(a, b) <= JamChainTouchM }
	if len(segs) == 1 {
		return &total, &segs[0].from, &segs[0].to
	}
	var starts []int
	for i, s := range segs {
		if !slices.ContainsFunc(indices(len(segs)), func(o int) bool { return o != i && touches(segs[o].to, s.from) }) {
			starts = append(starts, i)
		}
	}
	if len(starts) != 1 {
		return &total, nil, nil
	}
	chain := []segment{segs[starts[0]]}
	left := map[int]bool{}
	for i := range segs {
		if i != starts[0] {
			left[i] = true
		}
	}
	for len(left) > 0 {
		last := chain[len(chain)-1]
		next := -1
		for i := range segs {
			if left[i] && touches(last.to, segs[i].from) {
				next = i
				break
			}
		}
		if next < 0 {
			return &total, nil, nil
		}
		chain = append(chain, segs[next])
		delete(left, next)
	}
	return &total, &chain[0].from, &chain[len(chain)-1].to
}

func plainLines(d any) []string {
	out := []string{}
	for _, s := range descriptionLines(d) {
		out = append(out, jsTrim(spacesRE.ReplaceAllString(*s, " ")))
	}
	return out
}

var (
	endRE      = regexp.MustCompile(js(`(?:^|[,:]\s*)zwischen\s+(.+?)\s+und\s+\S`))
	endPlaceRE = regexp.MustCompile(js(`^(\d+(?:[.,]\d+)?)\s*km\s+(hinter|vor)\s+(\S.*)$`))
	digitRE    = regexp.MustCompile(`\d`)
	connectRE  = regexp.MustCompile(js(`(?i)(^|\s)(hinter|vor|bei|zwischen|und|Höhe)(\s|$)`))
	causesHead = regexp.MustCompile(`(?i)^Ereignismeldung:?$`)
	causeLine  = regexp.MustCompile(js(`^-\s*(.+)$`))
	causeSkip  = regexp.MustCompile(`(?i)^(Durchschnittsgeschwindigkeit|Reisezeitverlust)\b`)
)

func jamEndOf(d any) *JamEnd {
	for _, l := range plainLines(d) {
		m := endRE.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		x := jsTrim(m[1])
		if x == "" || len([]rune(x)) > JamEndTextMax {
			continue
		}
		e := &JamEnd{Text: x}
		if p := endPlaceRE.FindStringSubmatch(x); p != nil {
			km, _ := strconv.ParseFloat(strings.Replace(p[1], ",", ".", 1), 64)
			side, place := "before", jsTrim(p[3])
			if p[2] == "hinter" {
				side = "past"
			}
			e.Km, e.Side, e.Place = &km, &side, &place
		} else if !digitRE.MatchString(x) && !connectRE.MatchString(x) {
			side, place := "at", x
			e.Side, e.Place = &side, &place
		}
		return e
	}
	return nil
}

func jamCauses(d any) []string {
	out := []string{}
	inside := false
	for _, l := range plainLines(d) {
		if causesHead.MatchString(l) {
			inside = true
			continue
		}
		if !inside {
			continue
		}
		if l == "" {
			if len(out) > 0 {
				break
			}
			continue
		}
		m := causeLine.FindStringSubmatch(l)
		if m == nil {
			break
		}
		t := jsTrim(m[1])
		if causeSkip.MatchString(t) || len([]rune(t)) > JamEndTextMax {
			continue
		}
		out = append(out, t)
		if len(out) == JamCausesMax {
			break
		}
	}
	return out
}

// detours are the publisher's route recommendations: each a text, or an
// object's text members joined in their order.
func detours(raw []byte) []string {
	out := []string{}
	var items []json.RawMessage
	if json.Unmarshal(raw, &items) != nil {
		return out
	}
	for _, x := range items {
		text := ""
		if json.Unmarshal(x, &text) != nil {
			text = strings.Join(objectTexts(x), " – ")
		}
		text = jsTrim(spacesRE.ReplaceAllString(text, " "))
		if text == "" || len([]rune(text)) > JamRouteTextMax || slices.Contains(out, text) {
			continue
		}
		out = append(out, text)
		if len(out) == JamRouteMax {
			break
		}
	}
	return out
}

func jamTrend(ticks []JamTickRow, now time.Time) *JamTrend {
	from := now.Add(-JamTrendWindowMin * time.Minute)
	type pt struct {
		at time.Time
		l  *float64
		d  *int
	}
	var in []pt
	for _, t := range ticks {
		if t.At.After(from) && !t.At.After(now) {
			l, _, _ := jamExtent(t.Extents)
			in = append(in, pt{t.At, l, t.DelayS})
		}
	}
	slices.SortStableFunc(in, func(a, b pt) int { return a.at.Compare(b.at) })
	judge := func(metric string) *JamTrend {
		var ats []time.Time
		var vs []float64
		for _, p := range in {
			switch {
			case metric == "length" && p.l != nil:
				ats, vs = append(ats, p.at), append(vs, *p.l)
			case metric == "delay" && p.d != nil:
				ats, vs = append(ats, p.at), append(vs, float64(*p.d))
			}
		}
		if len(vs) < JamTrendMinTicks {
			return nil
		}
		base := float64(JamTrendDelayS)
		if metric == "length" {
			base = JamTrendLengthM
		}
		step := math.Max(base, vs[0]*JamTrendShare)
		d := vs[len(vs)-1] - vs[0]
		dir := "stable"
		if d >= step {
			dir = "growing"
		} else if d <= -step {
			dir = "shrinking"
		}
		return &JamTrend{Direction: dir, Metric: metric, EarlierAt: ats[0], Earlier: vs[0], LatestAt: ats[len(ats)-1], Latest: vs[len(vs)-1], Ticks: len(vs)}
	}
	if t := judge("length"); t != nil {
		return t
	}
	return judge("delay")
}

// objectTexts are a JSON object's (or array's) non-blank text members, in
// their order.
func objectTexts(raw json.RawMessage) []string {
	// raw is one member of a list already read whole: valid JSON, so the
	// decoder meets no error inside it.
	var parts []string
	dec := json.NewDecoder(bytes.NewReader(raw))
	open, _ := dec.Token()
	if open != json.Delim('{') && open != json.Delim('[') {
		return nil
	}
	for dec.More() {
		if open == json.Delim('{') {
			_, _ = dec.Token()
		}
		var v any
		_ = dec.Decode(&v)
		if s, ok := v.(string); ok && jsTrim(s) != "" {
			parts = append(parts, s)
		}
	}
	return parts
}

// berlinWeekdayHour is the ISO weekday (Monday 1) and hour in Berlin.
func berlinWeekdayHour(at time.Time) (int, int) {
	b := at.In(berlinZone)
	wd := int(b.Weekday())
	if wd == 0 {
		wd = 7
	}
	return wd, b.Hour()
}

// JamUsualFrom is the oldest hour the usual statement reads.
func JamUsualFrom(now time.Time) time.Time {
	return now.Add(-time.Duration(JamUsualWeeksBack*7*24+2) * time.Hour)
}

// JamHistoryReady says history from oldest reaches the usual's minimum.
func JamHistoryReady(oldest *time.Time, now time.Time) bool {
	return oldest != nil && !oldest.After(now.Add(-time.Duration(JamUsualMinWeeks*7*24+jamHistorySlackHour)*time.Hour))
}

func jamUsual(r JamUsualRow, now time.Time) (*JamUsual, *JamHowLong) {
	var u *JamUsual
	if r.WeeksObserved >= JamUsualMinWeeks {
		j := max(0, min(r.WeeksWithJam, r.WeeksObserved))
		verdict := "unusual"
		if j*2 >= r.WeeksObserved {
			verdict = "usual"
		}
		wd, h := berlinWeekdayHour(now)
		u = &JamUsual{Verdict: verdict, Weeks: r.WeeksObserved, JamWeeks: j, Weekday: wd, Hour: h}
	}
	if r.HowCases < JamHowLongMinCases || r.HowFrom == nil || r.HowTo == nil || *r.HowFrom < 1 || *r.HowTo < *r.HowFrom {
		return u, nil
	}
	kind := "weekday"
	if wd, _ := berlinWeekdayHour(now); wd >= 6 {
		kind = "weekend"
	}
	return u, &JamHowLong{Cases: r.HowCases, FromHours: *r.HowFrom, ToHours: *r.HowTo, DayKind: kind, Weeks: JamUsualWeeksBack}
}

// JamDetailsOf are the details of jams (in order) from the tick and usual
// rows asked for them.
func JamDetailsOf(jams []MotorwayEventRow, ticks []JamTickRow, usual []JamUsualRow, now time.Time) []JamDetail {
	out := make([]JamDetail, len(jams))
	for i, r := range jams {
		ext := r.SiteExtents
		if ext == nil {
			ext = []*string{r.Extent}
		}
		d := JamDetail{End: jamEndOf(r.Description), Causes: jamCauses(r.Description), Detours: detours(r.RouteRecommendations)}
		d.LengthM, d.Tail, d.Head = jamExtent(ext)
		var mine []JamTickRow
		for _, t := range ticks {
			if t.Group == i {
				mine = append(mine, t)
			}
		}
		d.Trend = jamTrend(mine, now)
		for _, u := range usual {
			if u.I == i+1 {
				d.Usual, d.HowLong = jamUsual(u, now)
				break
			}
		}
		out[i] = d
	}
	return out
}
