package httpapi

import (
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// MotorwayBlock is data.motorway: the platform's motorway traffic answer.
type MotorwayBlock struct {
	AsOf             time.Time           `json:"asOf" doc:"The road operator's newest sweep of the road, which every entry and count is of."`
	RoadWide         MotorwayRoadWide    `json:"roadWide" doc:"The counts of the whole road at the sweep, whatever direction, kinds, limit or cursor ask."`
	Measured         *MotorwayMeasured   `json:"measured" doc:"The measured state at the road's counting sites in the last 30 minutes; null when the key may not read it or no site measured."`
	LampReasonsTotal int                 `json:"lampReasonsTotal" doc:"The lamp's reasons when a measure published as several sections is one reason, as the platform counts them."`
	PlannedNow       []MotorwayPlan      `json:"plannedNow" doc:"The plan's short-duration works in force on the road now (at most 20); empty when none or the key may not read the plan."`
	Events           []MotorwayEntryView `json:"events" doc:"The road operator's warnings, roadworks and closures at the sweep, one entry per site, in the platform's order (jams first), up to limit."`
}

// MotorwayRoadWide are the road's counts.
type MotorwayRoadWide struct {
	Jams      int               `json:"jams"`
	Warnings  int               `json:"warnings"`
	Closures  int               `json:"closures"`
	Roadworks int               `json:"roadworks"`
	Announced MotorwayAnnounced `json:"announced" doc:"Entries not yet in force: their counts and the earliest start."`
}

// MotorwayAnnounced are the counts of entries not yet in force.
type MotorwayAnnounced struct {
	Closures  int        `json:"closures"`
	Roadworks int        `json:"roadworks"`
	Reports   int        `json:"reports"`
	From      *time.Time `json:"from" nullable:"true" doc:"The earliest start of an announced entry; null when none."`
}

// MotorwayMeasured is the measured state at the counting sites.
type MotorwayMeasured struct {
	Sites      int        `json:"sites"`
	Standstill int        `json:"standstill"`
	Slow       int        `json:"slow"`
	Heavy      int        `json:"heavy"`
	Free       int        `json:"free"`
	LaneSites  int        `json:"laneSites" doc:"The sites whose state is measured per lane; 0 when the key may not read it."`
	MeasuredAt *time.Time `json:"measuredAt" nullable:"true"`
	SlowNear   []string   `json:"slowNear" doc:"The places near the slow or standing sites, as the location register names them."`
}

// MotorwayPlan is one short-duration works of the plan.
type MotorwayPlan struct {
	Family  string     `json:"family"`
	ID      string     `json:"id"`
	Kind    string     `json:"kind"`
	Name    *string    `json:"name" nullable:"true"`
	Section *string    `json:"section" nullable:"true"`
	Cause   *string    `json:"cause" nullable:"true"`
	From    *time.Time `json:"from" nullable:"true"`
	To      *time.Time `json:"to" nullable:"true"`
}

// MotorwayEntryView is one entry.
type MotorwayEntryView struct {
	ID               string        `json:"id"`
	Kind             string        `json:"kind" enum:"warning,roadworks,closure"`
	Title            *string       `json:"title" nullable:"true"`
	Subtitle         *string       `json:"subtitle" nullable:"true"`
	TrafficType      *string       `json:"congestion" nullable:"true" doc:"The publisher's state of the traffic (queuing, stationary, heavy, slow); set marks a jam."`
	DelayMinutes     *int          `json:"delayMinutes" nullable:"true"`
	AverageKmh       *float64      `json:"averageSpeedKmh" nullable:"true" doc:"The publisher's average speed; null when it states none."`
	Blocked          bool          `json:"blocked"`
	Lat              *float64      `json:"lat" nullable:"true"`
	Lon              *float64      `json:"lon" nullable:"true"`
	Extent           *string       `json:"extent" nullable:"true" doc:"The publisher's extent of the site: lat,lon of its start then of its end."`
	Since            *time.Time    `json:"since" nullable:"true" doc:"The start the in-force rule used: the publisher's start, or the first phase its description states."`
	Segments         int           `json:"segments" doc:"The sections the publisher gives the site as."`
	SiteIDs          []string      `json:"siteIds"`
	DescriptionShown []string      `json:"description" doc:"The description's lines the platform shows: without its start line and its since clauses (marked with an ellipsis in brackets), at most two, each clipped at a word to 200 characters."`
	Phase            MotorwayPhase `json:"phase"`
	Jam              *MotorwayJam  `json:"jam" doc:"A jam's detail; null for any other entry."`
}

// MotorwayPhase are a works' phase dates as its description states them.
type MotorwayPhase struct {
	Start      *time.Time `json:"start" nullable:"true"`
	End        *time.Time `json:"end" nullable:"true"`
	MeasureEnd *time.Time `json:"measureEnd" nullable:"true" doc:"The end of the whole measure."`
}

// MotorwayJam is a jam's detail.
type MotorwayJam struct {
	LengthM  *int             `json:"lengthM" nullable:"true"`
	EndText  *string          `json:"endText" nullable:"true" doc:"Where the jam ends, as its description words it."`
	EndKm    *float64         `json:"endKm" nullable:"true"`
	EndSide  *string          `json:"endSide" nullable:"true" enum:"past,before,at"`
	EndPlace *string          `json:"endPlace" nullable:"true"`
	Causes   []string         `json:"causes"`
	Detours  []string         `json:"detours"`
	Trend    *MotorwayTrend   `json:"trend" doc:"Its length, else its delay, over the last 30 minutes; null under three sweeps."`
	Usual    *MotorwayUsual   `json:"usual" doc:"Whether it stands here at this Berlin weekday and hour in most of the last eight weeks; null under four weeks observed."`
	HowLong  *MotorwayHowLong `json:"howLong" doc:"How long a jam here lasts, the middle half of its runs; null under eight runs."`
}

// MotorwayTrend is a jam's trend.
type MotorwayTrend struct {
	Direction string    `json:"direction" enum:"growing,shrinking,stable"`
	Metric    string    `json:"metric" enum:"length,delay"`
	EarlierAt time.Time `json:"earlierAt"`
	Earlier   float64   `json:"earlier"`
	LatestAt  time.Time `json:"latestAt"`
	Latest    float64   `json:"latest"`
	Ticks     int       `json:"ticks"`
}

// MotorwayUsual is a jam's history verdict.
type MotorwayUsual struct {
	Verdict  string `json:"verdict" enum:"usual,unusual"`
	Weeks    int    `json:"weeks"`
	JamWeeks int    `json:"jamWeeks"`
	Weekday  int    `json:"weekday" doc:"ISO weekday in Berlin, Monday 1."`
	Hour     int    `json:"hour"`
}

// MotorwayHowLong is how long a jam here lasts.
type MotorwayHowLong struct {
	Cases     int    `json:"cases"`
	FromHours int    `json:"fromHours"`
	ToHours   int    `json:"toHours"`
	DayKind   string `json:"dayKind" enum:"weekday,weekend"`
	Weeks     int    `json:"weeks"`
}

// motorwayBlock is data.motorway of m; nil for none.
func motorwayBlock(m *roads.MotorwayAnswer) *MotorwayBlock {
	if m == nil {
		return nil
	}
	k := m.Counts
	out := &MotorwayBlock{AsOf: m.Tick, LampReasonsTotal: m.LampReasonsTotal,
		RoadWide: MotorwayRoadWide{Jams: k.Jams, Warnings: k.Warnings, Closures: k.Closures, Roadworks: k.Roadworks,
			Announced: MotorwayAnnounced{Closures: k.AnnouncedClosures, Roadworks: k.AnnouncedRoadworks, Reports: k.AnnouncedReports, From: k.AnnouncedFrom}},
		PlannedNow: make([]MotorwayPlan, len(m.Plans)), Events: make([]MotorwayEntryView, len(m.Entries))}
	if v := m.Measured; v != nil {
		out.Measured = &MotorwayMeasured{Sites: v.Sites, Standstill: v.Standstill, Slow: v.Slow, Heavy: v.Heavy, Free: v.Free, LaneSites: v.LaneSites,
			MeasuredAt: v.MeasuredAt, SlowNear: append([]string{}, v.SlowNear...)}
	}
	for i, p := range m.Plans {
		out.PlannedNow[i] = MotorwayPlan{Family: p.Family, ID: p.ID, Kind: p.Kind, Name: p.Name, Section: p.Section, Cause: p.Cause, From: p.From, To: p.To}
	}
	for i, e := range m.Entries {
		out.Events[i] = motorwayEntry(e)
	}
	return out
}

func motorwayEntry(e roads.MotorwayEntry) MotorwayEntryView {
	v := MotorwayEntryView{ID: e.ID, Kind: e.Kind, Title: e.Title, Subtitle: e.Subtitle, TrafficType: e.TrafficType, AverageKmh: averageKmh(e.AverageKmh), Extent: e.Extent,
		Blocked: e.Blocked != nil && *e.Blocked == "true", Lat: e.Lat, Lon: e.Lon, Since: since(e.StartsAt, e.StartText), Segments: e.Segments,
		SiteIDs: append([]string{}, e.SiteIDs...), DescriptionShown: append([]string{}, e.DescriptionShown...),
		Phase: MotorwayPhase{Start: e.Phase.Start, End: e.Phase.End, MeasureEnd: e.Phase.MeasureEnd}}
	if e.DelayS != nil {
		m := int(math.Round(float64(*e.DelayS) / 60))
		v.DelayMinutes = &m
	}
	if e.Jam != nil {
		v.Jam = motorwayJam(*e.Jam)
	}
	return v
}

// averageKmh is the publisher's average speed as a number; nil for none.
func averageKmh(s *string) *float64 {
	if s == nil || *s == "" {
		return nil
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(*s), 64)
	if err != nil {
		return nil
	}
	return &v
}

// since is the entry's start: its instant, else its stated start.
func since(at *time.Time, text *string) *time.Time {
	if at != nil || text == nil {
		return at
	}
	t, err := time.Parse(time.RFC3339Nano, *text)
	if err != nil {
		return nil
	}
	return &t
}

func motorwayJam(j roads.JamDetail) *MotorwayJam {
	out := &MotorwayJam{Causes: append([]string{}, j.Causes...), Detours: append([]string{}, j.Detours...)}
	if j.LengthM != nil {
		l := int(math.Round(*j.LengthM))
		out.LengthM = &l
	}
	if e := j.End; e != nil {
		out.EndText, out.EndKm, out.EndSide, out.EndPlace = &e.Text, e.Km, e.Side, e.Place
	}
	if t := j.Trend; t != nil {
		out.Trend = &MotorwayTrend{Direction: t.Direction, Metric: t.Metric, EarlierAt: t.EarlierAt, Earlier: t.Earlier, LatestAt: t.LatestAt, Latest: t.Latest, Ticks: t.Ticks}
	}
	if u := j.Usual; u != nil {
		out.Usual = &MotorwayUsual{Verdict: u.Verdict, Weeks: u.Weeks, JamWeeks: u.JamWeeks, Weekday: u.Weekday, Hour: u.Hour}
	}
	if h := j.HowLong; h != nil {
		out.HowLong = &MotorwayHowLong{Cases: h.Cases, FromHours: h.FromHours, ToHours: h.ToHours, DayKind: h.DayKind, Weeks: h.Weeks}
	}
	return out
}
