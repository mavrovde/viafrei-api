package roads

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
	"time"
)

// The platform's road status by road number: its register's sources, in its
// order, and the lists its statements branch on.
var (
	// RoadStatusFamilies are the sources the road status reads, in the
	// order it names them.
	RoadStatusFamilies = []string{"autobahn", "roadworks_bast", "ald_14d", "bab_messages", "nms_events", "roadworks_ald_ni", "roadworks_ald_sn",
		"roadworks_ald_th", "roadworks_nlstbv", "traffic_bw", "traffic_sh", "traffic_berlin", "traffic_koeln_events", "traffic_hannover",
		"roadworks_bremen", "traffic_ruesselsheim", "traffic_kassel", "traffic_leipzig", "bridges_sh", "bemas_bw", "hh_bauweiser",
		"kiel_baustellen", "sh_baustellen", "bb_baustellen", "freiburg_baustellen", "stuttgart_baustellen", "karlsruhe_baustellen",
		"karlsruhe_veranstaltungen", "frankfurt_verkehr", "berlin_planb", "duisburg_baustellen", "aachen_baustellen", "ingolstadt_baustellen",
		"koeln_baustellen", "muenster_baustellen", "dortmund_baustellen", "muenchen_baustellen", "roadworks_bayern", "roadworks_mv",
		"detours_nrw", "traffic_lms_berlin", "viz_berlin", "sperrinfosys_sn", "roadworks_nrw_lvz", "roadworks_mv_planned", "traffic_nrw",
		"traffic_hamburg", "roadworks_akd", "roadworks_he", "roadworks_he_gesammelt"}
	// RoadStatusMessageFamilies are the message sources (their words name
	// the place); RoadStatusClosesTextFamilies add the works source whose
	// words may say a closure.
	RoadStatusMessageFamilies = []string{"nms_events", "bab_messages", "traffic_bw", "traffic_berlin", "viz_berlin", "traffic_koeln_events",
		"traffic_sh", "traffic_hannover", "roadworks_bremen", "traffic_ruesselsheim", "traffic_kassel", "traffic_leipzig", "bridges_sh",
		"roadworks_bayern", "detours_nrw", "traffic_lms_berlin", "traffic_nrw", "traffic_nrw_einzel", "traffic_hamburg"}
	RoadStatusClosesTextFamilies = append(slices.Clone(RoadStatusMessageFamilies), "bemas_bw")
	// RoadStatusPlanFamilies are the motorway plan sources: one in force on a
	// road the road operator's live sweep answers is current works.
	RoadStatusPlanFamilies = []string{"roadworks_bast", "ald_14d", "roadworks_akd"}
	// RoadStatusProgrammeFamilies are the works programmes: a started
	// record is no restriction in force.
	RoadStatusProgrammeFamilies = []string{"roadworks_ald_ni"}
	// PlansOnlyFamilies list plans alone: their entries are planned, and
	// their silence answers nothing.
	PlansOnlyFamilies = []string{"roadworks_akd"}
	// StrategyFamilies are the road operators' switched strategies;
	// HeartbeatEndFamilies those whose stated end is the next pull.
	StrategyFamilies     = []string{"routing_bab", "routing_bayern", "routing_dvl_ni"}
	HeartbeatEndFamilies = []string{"routing_bayern"}
	// StrategyEventTypes are the strategies' kinds.
	StrategyEventTypes = []string{"generalNetworkManagement", "reroutingManagement", "reroutingManagementEnhanced"}
	// PublicationTimeFamilies state their change as the publication's time.
	PublicationTimeFamilies = []string{"bemas_bw"}
)

// The road status's bounds, as the platform's tool sets them.
const (
	RoadStatusHorizonDays = 3
	RoadStatusMaxLimit    = 20
	RoadStatusBodyLines   = 12
	StrategyPlanDays      = 7
	StrategyPlanMax       = 60
	// StrategyCadences and StrategyDefaultCadenceS judge a strategy
	// source current: checked within three of its cycles.
	StrategyCadences        = 3
	StrategyDefaultCadenceS = 600
)

// RoadStatusQuery asks the road status of one road.
type RoadStatusQuery struct {
	Road      string
	Now       time.Time
	WindowEnd time.Time
	// Open are the sources the key may read, in RoadStatusFamilies' order;
	// Strategies the strategy sources, on a motorway only.
	Open       []string
	Strategies []string
	Limit      int
}

// RoadStatusQueryOf is the road status of road, of class, asked at now up to
// until (zero: the tool's default horizon), by a key that may read
// permitted; false when it may read none of the sources.
func RoadStatusQueryOf(road string, class Class, limit int, now, until time.Time, permitted []string) (RoadStatusQuery, bool) {
	in := func(list []string) []string {
		return slices.DeleteFunc(slices.Clone(list), func(f string) bool { return !slices.Contains(permitted, f) })
	}
	q := RoadStatusQuery{Road: road, Now: now, WindowEnd: until, Open: in(RoadStatusFamilies), Limit: min(max(limit, 1), RoadStatusMaxLimit)}
	if until.IsZero() {
		q.WindowEnd = now.Add(RoadStatusHorizonDays * 24 * time.Hour)
	}
	if class == ClassMotorway {
		q.Strategies = in(StrategyFamilies)
	}
	return q, len(q.Open) > 0
}

// RoadStatusLiveRow is one row of the road operator's closures and works.
type RoadStatusLiveRow struct {
	Family, Road, Kind, ID                   string
	Title, Subtitle, Extent, Blocked, Start  *string
	Lat, Lon                                 *float64
	Description                              any
	ObservedAt                               time.Time
	DistanceM                                *int
	Segments, Total, Closed, Near, NearClose int
	Families                                 []string
}

// RoadStatusPlannedRow is one row of the plan, message and works sources.
type RoadStatusPlannedRow struct {
	Family                                               string
	PublicationID, Road, EventType, Publisher            *string
	From, To                                             *string
	DistanceFromM, DistanceToM, DistanceM                *int
	Start, End                                           *time.Time
	LastSeenAt                                           time.Time
	ClosesText                                           *bool
	TypeCloses                                           *bool
	ClosureFrom, ClosureTo                               *time.Time
	WindowStarts, WindowEnds                             []*time.Time
	WindowCount                                          *int
	Windowed, WindowNow                                  *bool
	PublishedAt                                          *time.Time
	SiteName, SectionName, Cause, PlaceText, DetailText  *string
	DetourVia                                            *string
	DetourVehicles                                       *bool
	Total, Planned, Current, Closed, Programme, Near, NC int
	Families, CurrentFamilies, AlsoFamilies              []string
}

// StrategyRow is one row of the switched strategies' statement.
type StrategyRow struct {
	Family, SourceID, PublicationID, Road   string
	EventType, From, To, Name, Section, Via *string
	Words                                   *string
	CauseValues, CommentValues              any
	WindowFrom, WindowTo                    *time.Time
	NowInForce                              bool
}

// SourceCheck is a source's last check.
type SourceCheck struct {
	Family    string
	CadenceS  *int
	CheckedAt *time.Time
	Paused    *bool
}

// RoadStatusRead is what the store reads for a RoadStatusQuery.
type RoadStatusRead struct {
	// Tick is the road operator's newest sweep of the road in the last
	// half hour; zero when none (its live closures are then not read).
	Tick    time.Time
	Live    []RoadStatusLiveRow
	Planned []RoadStatusPlannedRow
	// Checked are the planned rows' sources' last good runs.
	Checked map[string]time.Time
	// Strategies are the current strategy sources' rows; StrategiesStale
	// the strategy sources checked longer than their cycles ago.
	Strategies      []StrategyRow
	StrategiesStale []SourceCheck
}

// CurrentStrategyFamilies are the strategy sources checked within their
// cycles (a paused one keeps its sight), and those checked longer ago.
func CurrentStrategyFamilies(families []string, checks []SourceCheck, now time.Time) (current []string, stale []SourceCheck) {
	for _, f := range families {
		i := slices.IndexFunc(checks, func(c SourceCheck) bool { return c.Family == f })
		if i < 0 || checks[i].CheckedAt == nil {
			continue
		}
		c := checks[i]
		cadence := StrategyDefaultCadenceS
		if c.CadenceS != nil {
			cadence = *c.CadenceS
		}
		if (c.Paused != nil && *c.Paused) || now.Sub(*c.CheckedAt) <= time.Duration(StrategyCadences*cadence)*time.Second {
			current = append(current, f)
		} else {
			stale = append(stale, c)
		}
	}
	return current, stale
}

// RoadStatusItem is one entry of the road status.
type RoadStatusItem struct {
	// Status is closed, restricted, current, programme or planned; Term
	// what it is called (closure, roadworks, restriction).
	Status, Term, Family, Road, ID string
	// The road operator's entry: its kind, title, subtitle, extent, the
	// cleaned lines of its description and the sections it is published as.
	Kind                    *string
	Title, Subtitle, Extent *string
	Description             []string
	Segments                int
	// A source's entry: its type, publisher, publication, the place its
	// words name and their detail, its site, section and cause, its
	// referents, its validity windows and closure dates, a detour's roads.
	EventType, Publisher, PublicationID, PlaceText, DetailText *string
	SiteName, SectionName, Cause, RefFrom, RefTo, DetourVia    *string
	Detour, DetourVehicles                                     bool
	WindowStarts, WindowEnds                                   []*time.Time
	WindowCount                                                int
	ClosureFrom, ClosureTo                                     *time.Time
	From, To, MeasureTo                                        *time.Time
	Lat, Lon                                                   *float64
	DistanceM                                                  *int
}

// RoadStatusCounts are the road status's totals.
type RoadStatusCounts struct {
	Closed, Restricted, Planned, Programme, Current, Total int
	// CurrentFamilies are the plan sources behind Current.
	CurrentFamilies []string
}

// SourceAnswered is a source that answered, with when it was last seen
// current (nil when no row of it was shown).
type SourceAnswered struct {
	Family string
	AsOf   *time.Time
}

// Strategy is a switched strategy of the road operator, said as its
// recommendation.
type Strategy struct {
	Road, Family, ID, Kind              string
	Via, Words, Title, TitleEn, WordsEn *string
	From, To                            *string
	Since, Until                        *time.Time
}

// RoadStatusAnswer is the platform's road status of one road.
type RoadStatusAnswer struct {
	WindowEnd time.Time
	Counts    RoadStatusCounts
	Items     []RoadStatusItem
	// Hidden are the entries counted and not listed, HiddenClosed the
	// closures among them; CanRaise says a higher limit would list them all.
	Hidden, HiddenClosed int
	CanRaise             bool
	Answered             []SourceAnswered
	Strategies           []Strategy
	StrategiesStale      []SourceCheck
}

var statusRank = map[string]int{"closed": 0, "restricted": 1, "current": 2, "programme": 3, "planned": 4}

var (
	closureType = regexp.MustCompile(`(?i)closure|closed|blocked|sperrung`)
	worksType   = regexp.MustCompile(`(?i)works|roadworks|baustelle|construction|maintenance`)
	detourWord  = regexp.MustCompile(`(?i)umleitung|detour|diversion`)
)

// RoadEventTerm is what an entry of eventType from family is called.
func RoadEventTerm(eventType *string, family string) string {
	switch {
	case eventType != nil && closureType.MatchString(*eventType):
		return "closure"
	case slices.Contains(RoadStatusProgrammeFamilies, family):
		return "roadworks"
	case eventType != nil && worksType.MatchString(*eventType):
		return "roadworks"
	}
	return "restriction"
}

// plannedStatus is the platform's status of a source's entry.
func plannedStatus(r RoadStatusPlannedRow, now time.Time, live bool) string {
	closes := isTrue(r.ClosesText)
	if r.TypeCloses != nil {
		closes = closes || *r.TypeCloses
	} else if r.EventType != nil {
		closes = closes || closureType.MatchString(*r.EventType)
	}
	switch {
	case r.Start != nil && r.Start.After(now), slices.Contains(PlansOnlyFamilies, r.Family), isTrue(r.Windowed) && !isTrue(r.WindowNow):
		return "planned"
	case closes:
		return "closed"
	case live && slices.Contains(RoadStatusPlanFamilies, r.Family):
		return "current"
	case slices.Contains(RoadStatusProgrammeFamilies, r.Family):
		return "programme"
	}
	return "restricted"
}

func liveItem(r RoadStatusLiveRow) RoadStatusItem {
	status, term := "restricted", "roadworks"
	if r.Kind == "closure" || (r.Blocked != nil && *r.Blocked == "true") {
		status, term = "closed", "closure"
	}
	ph := PhaseOf(r.Description)
	kind := r.Kind
	return RoadStatusItem{Status: status, Term: term, Family: r.Family, Road: OwnRoad(r.Title, r.Road), ID: r.ID, Kind: &kind,
		Title: r.Title, Subtitle: r.Subtitle, Extent: r.Extent, Description: CleanDescription(r.Description), Segments: r.Segments,
		From: startOf(r.Start), To: laterOf(ph.End, ph.MeasureEnd), MeasureTo: ph.MeasureEnd, Lat: r.Lat, Lon: r.Lon, DistanceM: r.DistanceM,
		WindowStarts: []*time.Time{}, WindowEnds: []*time.Time{}}
}

// laterOf is the phase's end, or the measure's when that is later.
func laterOf(end, measure *time.Time) *time.Time {
	if end == nil || (measure != nil && !measure.Before(*end)) {
		return measure
	}
	return end
}

// startOf is the road operator's stated start; nil when none or unreadable.
func startOf(s *string) *time.Time {
	if s == nil {
		return nil
	}
	t, err := time.Parse(time.RFC3339Nano, *s)
	if err != nil {
		return nil
	}
	return &t
}

var titleRoad = regexp.MustCompile(`^[` + jsSpace + `]*A[` + jsSpace + `]?([0-9]+)`)

// OwnRoad is the motorway the title names first (A and at most three
// digits), else road.
func OwnRoad(title *string, road string) string {
	if title != nil {
		if m := titleRoad.FindStringSubmatch(*title); m != nil && len(m[1]) <= 3 {
			return "A" + m[1]
		}
	}
	return road
}

func plannedItem(r RoadStatusPlannedRow, now time.Time, live bool) RoadStatusItem {
	status := plannedStatus(r, now, live)
	term := RoadEventTerm(r.EventType, r.Family)
	if status == "closed" {
		term = "closure"
	}
	road := ""
	if r.Road != nil {
		road = *r.Road
	}
	// A planned entry is keyed by its source and its first reference marker;
	// one without a marker by its publication id (as the platform keys a
	// local message), so none carries a placeholder. Every planned row is a
	// stored road event, whose publication id the pinned viafrei-database
	// release declares not null and unique within its source (the baseline's
	// unique constraint on source and publication id): a row without a
	// marker always has one.
	ref := r.Family
	switch {
	case r.From != nil:
		ref += ":" + *r.From
	case r.PublicationID != nil:
		ref += ":" + *r.PublicationID
	}
	return RoadStatusItem{Status: status, Term: term, Family: r.Family, Road: road, ID: ref, EventType: r.EventType, Publisher: r.Publisher,
		PublicationID: r.PublicationID, PlaceText: r.PlaceText, DetailText: r.DetailText, SiteName: r.SiteName, SectionName: r.SectionName, Cause: r.Cause,
		RefFrom: r.From, RefTo: r.To, DetourVia: r.DetourVia, Detour: r.EventType != nil && *r.EventType == "reroutingManagement", DetourVehicles: isTrue(r.DetourVehicles),
		WindowStarts: nonNil(r.WindowStarts), WindowEnds: nonNil(r.WindowEnds), WindowCount: deref(r.WindowCount), ClosureFrom: r.ClosureFrom, ClosureTo: r.ClosureTo,
		From: r.Start, To: r.End, DistanceM: r.DistanceM, Description: []string{}}
}

func isTrue(b *bool) bool { return b != nil && *b }

func deref(n *int) int {
	if n == nil {
		return 0
	}
	return *n
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// SortRoadStatus orders the entries as the road path does: by severity,
// then distance, start, road and id.
func SortRoadStatus(items []RoadStatusItem) {
	far := func(i RoadStatusItem) int {
		if i.DistanceM == nil {
			return int(^uint(0) >> 1)
		}
		return *i.DistanceM
	}
	from := func(i RoadStatusItem) int64 {
		if i.From == nil {
			return 0
		}
		return i.From.UnixMilli()
	}
	slices.SortStableFunc(items, func(a, b RoadStatusItem) int {
		return cmp.Or(statusRank[a.Status]-statusRank[b.Status], cmp.Compare(far(a), far(b)), cmp.Compare(from(a), from(b)),
			strings.Compare(a.Road, b.Road), strings.Compare(a.ID, b.ID))
	})
}

// PickPage takes slots entries of sorted, the closures first up to a cap
// that leaves room for the rest, in sorted's order.
func PickPage(sorted []RoadStatusItem, slots int) []RoadStatusItem {
	if slots <= 0 {
		return []RoadStatusItem{}
	}
	if len(sorted) <= slots {
		return slices.Clone(sorted)
	}
	closed := 0
	for _, i := range sorted {
		if i.Status == "closed" {
			closed++
		}
	}
	limit := slots
	if closed < len(sorted) {
		limit = max((slots+1)/2, slots-3)
	}
	take := make([]bool, len(sorted))
	n := 0
	for k, i := range sorted {
		if i.Status == "closed" && n < min(limit, slots) {
			take[k] = true
			n++
		}
	}
	for k := range sorted {
		if n >= slots {
			break
		}
		if !take[k] {
			take[k] = true
			n++
		}
	}
	out := make([]RoadStatusItem, 0, slots)
	for k, i := range sorted {
		if take[k] {
			out = append(out, i)
		}
	}
	return out
}

// RoadStatusAnswerOf is the road status of r, asked by q.
func RoadStatusAnswerOf(q RoadStatusQuery, r RoadStatusRead) RoadStatusAnswer {
	live := !r.Tick.IsZero()
	items := make([]RoadStatusItem, 0, len(r.Live)+len(r.Planned))
	answered := []string{}
	var k RoadStatusCounts
	if len(r.Live) > 0 {
		t := r.Live[0]
		k.Total, k.Closed, k.Restricted = t.Total, t.Closed, t.Total-t.Closed
		answered = append(answered, t.Families...)
	}
	for _, row := range r.Live {
		items = append(items, liveItem(row))
	}
	if len(r.Planned) > 0 {
		t := r.Planned[0]
		k.Total += t.Total
		k.Closed += t.Closed
		k.Restricted += t.Total - t.Closed - t.Planned - t.Programme - t.Current
		k.Planned, k.Programme, k.Current = t.Planned, t.Programme, t.Current
		k.CurrentFamilies = t.CurrentFamilies
		answered = append(answered, t.Families...)
		for _, f := range t.AlsoFamilies {
			if slices.Contains(q.Open, f) {
				answered = append(answered, f)
			}
		}
		if t.Current > 0 {
			answered = append(answered, "autobahn")
		}
	}
	for _, row := range r.Planned {
		items = append(items, plannedItem(row, q.Now, live && row.Road != nil && *row.Road == q.Road))
	}
	if live {
		answered = append(answered, "autobahn")
	}
	k.CurrentFamilies = nonNil(k.CurrentFamilies)
	SortRoadStatus(items)
	shown := PickPage(items, min(q.Limit, RoadStatusBodyLines-1))
	closedShown := 0
	for _, i := range shown {
		if i.Status == "closed" {
			closedShown++
		}
	}
	a := RoadStatusAnswer{WindowEnd: q.WindowEnd, Counts: k, Items: shown, Hidden: k.Total - len(shown), HiddenClosed: max(0, k.Closed-closedShown),
		Answered: answeredOf(q, r, answered), Strategies: StrategiesOf(r.Strategies), StrategiesStale: nonNil(r.StrategiesStale)}
	a.CanRaise = a.Hidden > 0 && q.Limit < RoadStatusMaxLimit && k.Total <= RoadStatusMaxLimit
	return a
}

// answeredOf are the sources that answered, in the register's order, each
// with its newest stamp: the sweep for the road operator, else the newest
// of its shown rows (its last good run when that is as new).
func answeredOf(q RoadStatusQuery, r RoadStatusRead, answered []string) []SourceAnswered {
	keys := slices.DeleteFunc(slices.Clone(q.Open), func(f string) bool { return !slices.Contains(answered, f) })
	if len(keys) == 0 {
		can := slices.DeleteFunc(slices.Clone(q.Open), func(f string) bool { return slices.Contains(PlansOnlyFamilies, f) })
		keys = slices.DeleteFunc(slices.Clone(can), func(f string) bool { return f == "autobahn" && r.Tick.IsZero() })
		if len(keys) == 0 {
			keys = can
		}
	}
	stamp := map[string]time.Time{}
	if !r.Tick.IsZero() {
		stamp["autobahn"] = r.Tick
	}
	for _, row := range r.Planned {
		at := row.LastSeenAt
		if c, ok := r.Checked[row.Family]; ok && !c.Before(at) {
			at = c
		}
		if s, ok := stamp[row.Family]; !ok || at.After(s) {
			stamp[row.Family] = at
		}
	}
	out := make([]SourceAnswered, len(keys))
	for i, f := range keys {
		out[i] = SourceAnswered{Family: f}
		if s, ok := stamp[f]; ok {
			out[i].AsOf = &s
		}
	}
	return out
}

// langText is one text of a published multilingual value.
type langText struct{ lang, text string }

func langTexts(v any) []langText {
	list, _ := v.([]any)
	var out []langText
	for _, x := range list {
		o, _ := x.(map[string]any)
		text, _ := o["#text"].(string)
		if text = jsTrim(text); text == "" {
			continue
		}
		lang, _ := o["@lang"].(string)
		out = append(out, langText{strings.ToLower(lang), text})
	}
	return out
}

func firstIn(vs []langText, lang string) *string {
	for _, v := range vs {
		if v.lang == lang {
			return &v.text
		}
	}
	return nil
}

// StrategiesOf are the switched strategies of rows in force now, with their
// wording: a detour when its type or words say one, else guidance.
func StrategiesOf(rows []StrategyRow) []Strategy {
	out := []Strategy{}
	for _, r := range rows {
		if !slices.Contains(StrategyFamilies, r.Family) || !r.NowInForce || r.EventType == nil || !slices.Contains(StrategyEventTypes, *r.EventType) {
			continue
		}
		causes, comments := langTexts(r.CauseValues), langTexts(r.CommentValues)
		said := []string{}
		if r.Words != nil {
			said = append(said, *r.Words)
		}
		for _, c := range append(slices.Clone(causes), comments...) {
			said = append(said, c.text)
		}
		kind := "guidance"
		if *r.EventType == "reroutingManagement" || slices.ContainsFunc(said, detourWord.MatchString) {
			kind = "detour"
		}
		title := firstIn(causes, "de")
		if title == nil {
			title = firstIn(causes, "")
		}
		s := Strategy{Road: r.Road, Family: r.Family, ID: r.SourceID + "#" + r.PublicationID, Kind: kind, Words: r.Words, Title: title,
			TitleEn: firstIn(causes, "en"), WordsEn: firstIn(comments, "en"), From: r.From, To: r.To, Since: r.WindowFrom, Until: r.WindowTo}
		if r.Via != nil {
			if v := jsTrim(*r.Via); v != "" {
				s.Via = &v
			}
		}
		if slices.Contains(HeartbeatEndFamilies, r.Family) {
			s.Until = nil
		}
		out = append(out, s)
	}
	return out
}
