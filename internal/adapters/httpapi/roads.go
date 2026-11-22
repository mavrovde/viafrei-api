package httpapi

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	_ "time/tzdata" // the Europe/Berlin rendering never depends on the host

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// The road-conditions operations: what is happening on a motorway, on the
// roads near a position, and one situation by its id (FR-ROAD).

// berlin is the zone every *Local member is rendered in (CONV-004); the
// zone database is embedded, so loading it cannot fail.
var berlin, _ = time.LoadLocation("Europe/Berlin")

// local renders an instant in Europe/Berlin time, RFC 3339 with its offset.
func local(t time.Time) string { return t.In(berlin).Format(time.RFC3339) }

// TimeWindow is a validity: an instant in UTC and its Europe/Berlin
// rendering (CONV-004); until is null when open-ended.
type TimeWindow struct {
	From       time.Time  `json:"from" doc:"Start of the validity."`
	FromLocal  string     `json:"fromLocal" format:"date-time" doc:"from in Europe/Berlin time."`
	Until      *time.Time `json:"until" nullable:"true" doc:"End of the validity; null when open-ended."`
	UntilLocal *string    `json:"untilLocal" nullable:"true" format:"date-time" doc:"until in Europe/Berlin time; null when until is."`
}

func timeWindow(from time.Time, until *time.Time) TimeWindow {
	w := TimeWindow{From: from.UTC(), FromLocal: local(from)}
	if until != nil {
		u, l := until.UTC(), local(*until)
		w.Until, w.UntilLocal = &u, &l
	}
	return w
}

// Lanes are the closed and total lanes of a carriageway, where published.
type Lanes struct {
	Closed *int `json:"closed" nullable:"true" doc:"Lanes closed; null when not published."`
	Total  *int `json:"total" nullable:"true" doc:"Lanes in total; null when not published."`
}

// RoadPosition is where a situation lies along its road (chainage).
type RoadPosition struct {
	FromKm       *float64 `json:"fromKm" nullable:"true" doc:"Kilometre of the situation along the road, one decimal. A projection of its location onto the road's line, measured over the road's parts in their order and scaled to the road's published length; not a signed chainage."`
	ToKm         *float64 `json:"toKm" nullable:"true" doc:"Kilometre where the situation ends; null while the service holds no extent along the road."`
	JunctionFrom *string  `json:"junctionFrom" nullable:"true" doc:"The junction before the situation; null while the service does not name junctions."`
	JunctionTo   *string  `json:"junctionTo" nullable:"true" doc:"The junction after the situation; null while the service does not name junctions."`
}

// RoadSituation is anything that affects travel on a road.
type RoadSituation struct {
	ID             string        `json:"id"`
	Kind           string        `json:"kind" enum:"closure,roadworks,jam,accident,hazard,restriction,weather,other" doc:"What it is. Open enumeration; treat an unknown value as other."`
	Severity       string        `json:"severity" enum:"info,minor,lane_closed,major,road_closed,other" doc:"Effect on traffic, ordered info < minor < lane_closed < major < road_closed; other when it cannot be graded. Open enumeration."`
	Road           *string       `json:"road" nullable:"true" doc:"The road reference without spaces (A8); null when the situation is on no numbered road."`
	RoadType       string        `json:"roadType" enum:"motorway,federal,state,urban,other" doc:"The class of the road. Open enumeration; treat an unknown value as other."`
	Direction      string        `json:"direction" enum:"north,east,south,west,both,unknown" doc:"The affected carriageway."`
	DirectionText  *string       `json:"directionText" nullable:"true" doc:"The signed destination of the carriageway, as published."`
	Location       *Position     `json:"location" doc:"Where it is; null when not published."`
	Validity       TimeWindow    `json:"validity"`
	Lanes          *Lanes        `json:"lanes" doc:"Closed and total lanes; null when neither is published."`
	DelayMin       *int          `json:"delayMin" nullable:"true" doc:"The published delay in minutes; null when not published."`
	LengthM        *int          `json:"lengthM" nullable:"true" doc:"The published length in metres; null when not published."`
	Headline       string        `json:"headline" doc:"Plain text, in textLanguage."`
	Description    *string       `json:"description" nullable:"true" doc:"Plain text, in textLanguage; null when not published."`
	TextLanguage   string        `json:"textLanguage" doc:"The language of headline and description (BCP 47)."`
	UpdatedAt      time.Time     `json:"updatedAt" doc:"When the situation last changed."`
	UpdatedAtLocal string        `json:"updatedAtLocal" format:"date-time" doc:"updatedAt in Europe/Berlin time."`
	Position       *RoadPosition `json:"position" doc:"Chainage along the road; null when the situation cannot be placed on its road, and on answers that are not about one road."`
	DistanceM      *int          `json:"distanceM,omitempty" doc:"Distance from the asked position in metres. Present on the conditions near a position."`
	SourceFamily   string        `json:"sourceFamily" doc:"The source family of the item; its attribution is in meta.attribution."`
}

func roadSituation(v roads.Situation) RoadSituation {
	out := RoadSituation{
		ID: v.ID, Kind: string(v.Kind), Severity: string(v.Severity), Road: v.Road, RoadType: string(v.Class),
		Direction: string(v.Direction), DirectionText: v.Towards, Validity: timeWindow(v.From, v.Until),
		DelayMin: v.DelayMin, LengthM: v.LengthM, Headline: v.Headline, Description: v.Description,
		TextLanguage: v.TextLanguage, UpdatedAt: v.UpdatedAt.UTC(), UpdatedAtLocal: local(v.UpdatedAt),
		DistanceM: v.DistanceM, SourceFamily: v.Family,
	}
	if v.AtKm != nil {
		out.Position = &RoadPosition{FromKm: v.AtKm}
	}
	if v.Location != nil {
		p := position(*v.Location)
		out.Location = &p
	}
	if v.LanesClosed != nil || v.LanesTotal != nil {
		out.Lanes = &Lanes{Closed: v.LanesClosed, Total: v.LanesTotal}
	}
	return out
}

// SourceStamp is one source family's Stand.
type SourceStamp struct {
	Family         string     `json:"family" doc:"The source family; its attribution is in meta.attribution."`
	CheckedAt      time.Time  `json:"lastCheckedAt" doc:"When the family was last checked: its newest confirmation, or its data's instant when that is newer or it was never confirmed."`
	UnchangedSince *time.Time `json:"unchangedSince" nullable:"true" doc:"The instant of the family's data (meta.freshness dataAsOf); null when unknown."`
	PossiblyStale  bool       `json:"possiblyStale" doc:"True when checkedAt is more than three of the family's own cycles before the answer's instant, as the platform judges a source; never for a family whose cycle no register states."`
}

// sourceStamps states the Stand of each stamp at the answer's instant.
func sourceStamps(stamps []roads.Stamp, at time.Time) []SourceStamp {
	out := make([]SourceStamp, len(stamps))
	for i, s := range stamps {
		out[i] = SourceStamp{Family: s.Family, CheckedAt: s.CheckedAt.UTC(), UnchangedSince: utcOrNil(s.UnchangedSince), PossiblyStale: s.PossiblyStale(at)}
	}
	return out
}

// utcOrNil is t in UTC, nil for nil.
func utcOrNil(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

func roadSituations(list []roads.Situation) []RoadSituation {
	out := make([]RoadSituation, len(list))
	for i, v := range list {
		out[i] = roadSituation(v)
	}
	return out
}

// noteLamp tells the cache what a conditions answer rests on besides the
// families it attributes: every family the lamp read, with its
// confirmation; the lamp's reference rows; the lamp and its reason, which
// the clock alone can change; and the earliest instant it does.
func noteLamp(ctx context.Context, c features.Conditions) {
	noteSight(ctx, "roads", c.Read.Roads)
	noteSight(ctx, "weather", c.Read.Weather)
	reason := ""
	if c.Reason != nil {
		reason = string(*c.Reason)
	}
	shown := "lamp " + string(c.Lamp) + " " + reason
	for _, r := range c.Reasons {
		shown += fmt.Sprintf(" %s=%d", r.Reason, r.Count)
	}
	for _, g := range c.Sight {
		shown += " " + g.Group + ":" + g.Family + ":" + g.Land + "=" + string(g.State)
	}
	noteShown(ctx, shown)
	noteChanges(ctx, c.ChangesAt)
}

// LampReason is one reason of a lamp and how many give it.
type LampReason struct {
	Reason string `json:"reason" enum:"road_closed,ramp_closed,safety,bridge_closed,long_jam,jam,weather_warning,slippery,warning,narrowing,limited_sight" example:"narrowing" doc:"The reason, as lampReason names it. Open enumeration."`
	Lamp   string `json:"lamp" enum:"red,amber" doc:"The colour this reason gives on its own."`
	Count  int    `json:"count" minimum:"1" example:"3" doc:"How many give it: situations or weather warnings, or, for limited_sight, the groups of sight that are not fresh."`
}

// LampSight is the sight of one group the lamp read.
type LampSight struct {
	Group  string  `json:"group" example:"roads" doc:"The group: roads (one entry per roads family read, and one without a family for the roads families the key may not read), weather (the official weather warnings, judged as a whole) or messages (the traffic messages of one Land). Open enumeration."`
	State  string  `json:"state" enum:"fresh,stale,missing" doc:"fresh: current; stale: past its freshness threshold; missing: no data, not readable with the key, or no source covers it. Any state but fresh is a limited_sight reason."`
	Family *string `json:"family" nullable:"true" example:"autobahn" doc:"The roads family judged; null for any other group and for the roads families the key may not read."`
	Land   *string `json:"land" nullable:"true" example:"09" doc:"The Land of the traffic messages judged; null for any other group, and for messages when no Land is in scope."`
}

// lampReasons are the reasons of a conditions answer, empty for green.
func lampReasons(c features.Conditions) []LampReason {
	out := make([]LampReason, len(c.Reasons))
	for i, r := range c.Reasons {
		out[i] = LampReason{Reason: string(r.Reason), Lamp: string(r.Lamp), Count: r.Count}
	}
	return out
}

// lampSight is the sight of a conditions answer.
func lampSight(c features.Conditions) []LampSight {
	out := make([]LampSight, len(c.Sight))
	for i, g := range c.Sight {
		out[i] = LampSight{Group: g.Group, State: string(g.State), Family: optional(g.Family), Land: optional(g.Land)}
	}
	return out
}

// optional is s, or nil when empty.
func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// noteChanges records the instant an answer changes with the clock alone,
// when there is one.
func noteChanges(ctx context.Context, t *time.Time) {
	if t != nil {
		noteChangesAt(ctx, *t)
	}
}

func lampReason(r *roads.Reason) *string {
	if r == nil {
		return nil
	}
	s := string(*r)
	return &s
}

// SituationFilters are the filters of the conditions operations.
type SituationFilters struct {
	Kinds       []string `query:"kinds" enum:"closure,roadworks,jam,accident,hazard,restriction,weather,other" doc:"Only these kinds, comma-separated; other keeps the kinds that cannot be mapped. Default: every kind."`
	MinSeverity string   `query:"min_severity" enum:"info,minor,lane_closed,major,road_closed" default:"info" doc:"Only situations at or above this severity; info also keeps those that cannot be graded."`
}

// SituationWindow asks for the situations whose validity overlaps a window
// instead of those in force now.
type SituationWindow struct {
	HorizonDays int    `query:"horizon_days" minimum:"0" maximum:"14" doc:"Also the situations that start within this many days from now: the window is now to now plus the days, and a situation is kept when its validity overlaps it (the platform's road status horizon). 0, the default, keeps those in force now. Not with from or to."`
	From        string `query:"from" format:"date" doc:"The first day of a window of German calendar days (Europe/Berlin), YYYY-MM-DD: a situation is kept when its validity overlaps the window (the platform's roadworks window). Default: today, when to is given."`
	To          string `query:"to" format:"date" doc:"The last day of the window, inclusive, YYYY-MM-DD. Default: 7 days after from. The window spans at most 92 days."`
}

// check refuses a window that cannot be read.
func (w SituationWindow) check() []error {
	if _, _, err := roads.DayWindow(time.Now(), w.HorizonDays, w.From, w.To); err != nil {
		return []error{&huma.ErrorDetail{Location: "query.from", Message: err.Error(), Value: w.From + ".." + w.To}}
	}
	return nil
}

// asked reports whether a window was asked for.
func (w SituationWindow) asked() bool { return w.HorizonDays > 0 || w.From != "" || w.To != "" }

func (f SituationFilters) filter() roads.Filter {
	out := roads.Filter{MinSeverity: roads.Severity(f.MinSeverity)}
	for _, k := range f.Kinds {
		out.Kinds = append(out.Kinds, roads.Kind(k))
	}
	return out
}

// windowed is the filter with the window at now: an answer over a window
// moves with the clock, so it changes at its read.
func (s *server) windowed(ctx context.Context, f roads.Filter, w SituationWindow) roads.Filter {
	if !w.asked() {
		return f
	}
	now := s.at()
	f.From, f.Until, _ = roads.DayWindow(now, w.HorizonDays, w.From, w.To)
	f.Ahead = true
	noteChangesAt(ctx, now)
	return f
}

// FilterQuery is the filter part of meta.query of a conditions answer.
type FilterQuery struct {
	Kinds       []string   `json:"kinds" doc:"The kinds asked for; empty for every kind."`
	MinSeverity string     `json:"minSeverity"`
	WindowFrom  *time.Time `json:"windowFrom,omitempty" doc:"The start of the window asked for (horizon_days, or from and to); absent for the situations in force now."`
	WindowUntil *time.Time `json:"windowUntil,omitempty" doc:"The end of the window asked for; absent for the situations in force now."`
}

func filterQuery(f roads.Filter) FilterQuery {
	out := FilterQuery{Kinds: []string{}, MinSeverity: string(f.MinSeverity)}
	if !f.Until.IsZero() {
		from, until := f.From.UTC(), f.Until.UTC()
		out.WindowFrom, out.WindowUntil = &from, &until
	}
	for _, k := range f.Kinds {
		out.Kinds = append(out.Kinds, string(k))
	}
	return out
}

// ------------------------------------------------------- motorway conditions

// MotorwayInput is the input of getMotorwayConditions.
type MotorwayInput struct {
	Road      string `path:"road" maxLength:"6" pattern:"^[AaBbLlSs] ?[0-9]{1,4}$" example:"A8" doc:"The road reference, case-insensitive, with an optional space (A8, a 8): a motorway (A), a federal road (B), a Landesstraße (L) or a Staatsstraße (S). A Kreisstraße (K) is no reference, its number repeating in every Kreis."`
	Direction string `query:"direction" enum:"north,east,south,west,both" default:"both" doc:"The carriageway; both keeps every situation. A direction keeps that carriageway's situations, those on both and those whose direction is unknown."`
	SituationFilters
	SituationWindow
	Limit  int    `query:"limit" minimum:"1" maximum:"100" doc:"Most situations to return. Default: pagination.default_limit."`
	Cursor string `query:"cursor" pattern:"^[0-9]{1,4}$" doc:"The meta.page.nextCursor of the page before; omit for the first page."`
	road   string
	offset int
}

// Resolve reads the road reference into its canonical form (CONV-011; huma
// calls it after the schema checks).
func (in *MotorwayInput) Resolve(huma.Context) []error {
	road, err := roads.ParseAnyRoad(in.Road)
	if err != nil {
		return []error{&huma.ErrorDetail{Location: "path.road", Message: err.Error(), Value: in.Road}}
	}
	in.road = road
	in.offset, _ = strconv.Atoi(in.Cursor)
	return in.check()
}

// Corridor is the road a conditions answer is about.
type Corridor struct {
	Road      string   `json:"road" doc:"The road reference without spaces."`
	RoadType  string   `json:"roadType" enum:"motorway,federal,state,urban,other" doc:"Open enumeration; treat an unknown value as other."`
	Direction string   `json:"direction" enum:"north,east,south,west,both"`
	States    []string `json:"states" doc:"The ISO 3166-2 codes of the states the road crosses."`
}

// MotorwayData is the answer of getMotorwayConditions.
type MotorwayData struct {
	Corridor    Corridor        `json:"corridor"`
	Lamp        string          `json:"lamp" enum:"green,amber,red" doc:"The lamp over the question's scope (the road and direction, or the radius), whatever kinds and min_severity keep in the list, by the platform's motorway Ampel (amber is its yellow). It reads every situation in the scope as its record is read (the motorway operator's event, any other traffic message, a bridge's state) and every official weather warning in force in an area the road crosses, or within the radius. Red: a closure of the carriageway; a safety message by its words on any kind (a wrong-way driver, an object, a person or an animal on the road, an accident with the road closed); a closed bridge; a jam with a delay of 30 minutes or more; a weather warning of level 3 or 4. Amber: a closed entry, exit or junction link; a shorter jam or one with no delay; a weather warning of level 1 or 2; any other message (an accident without a closure, a restricted bridge, a situation that cannot be mapped); roadworks or a lane restriction on the carriageway (works on a ramp give no colour); or limited sight: a roads family stale or without data, a roads family the key may not read (meta.partial), the weather warnings not readable with the key or their source not current, or a state in scope whose traffic messages no current roads family the key may read covers. A situation's severity does not colour the lamp. Green when nothing gives a colour and every group read is current, which is no guarantee that the road is free."`
	LampReason  *string         `json:"lampReason" nullable:"true" enum:"road_closed,ramp_closed,safety,bridge_closed,long_jam,jam,weather_warning,slippery,warning,narrowing,limited_sight" doc:"Why the lamp has its colour: the first of its reasons, a red one before any amber one, and inside a colour in the platform's order, which is this enumeration's order; null for green. slippery is never given while the read contract holds no road-weather forecast. Open enumeration."`
	LampReasons []LampReason    `json:"lampReasons" doc:"Every reason of the lamp with how many give it, in lampReason's order (lampReason is the first); empty for green."`
	Sight       []LampSight     `json:"sight" doc:"The sight of every group the lamp read; any group not fresh keeps the lamp from green (limited_sight)."`
	Counts      []FamilyCount   `json:"counts" doc:"How many situations the filters keep, by source family, before limit and cursor (the platform's counts per publisher), in family order; empty when none."`
	Stamps      []SourceStamp   `json:"sourceStamps" doc:"The Stand of every attributed source family, as the platform's road status states each source's beside its credit, in family order: when it was last checked, when its data last changed, and whether it is possibly out of date. A family with neither instant has none."`
	Coverage    *RoadCoverage   `json:"coverage" doc:"Null for a road the road register holds. For a road it does not hold (a federal or Land road, or a motorway it lacks), the answer is read from the situations whose reference names the road, none placed along a line and no state named; coverage then says what was read, as the platform's roadworks-ahead answer says it: an empty list is no sites on what the named families publish, never a road with no works."`
	Situations  []RoadSituation `json:"situations" doc:"In the platform's motorway status order: jams, closures, the other reports, then roadworks; within each, by traffic type (queuing, stationary, heavy, slow), delay descending, headline, then the publisher's id."`
}

// RoadCoverage is what a conditions answer about a road the road register
// does not hold read.
type RoadCoverage struct {
	Registered bool     `json:"registered" doc:"Always false: the road register (the motorways of the Autobahn GmbH) does not hold the road."`
	RoadType   string   `json:"roadType" enum:"motorway,federal,state,urban,other" doc:"The class the reference names by its letter: A motorway, B federal, L and S state."`
	Families   []string `json:"families" doc:"The roads families read (those the key may read; with a window ahead, those of planned works) that hold a situation on a road of this class, in name order; meta.attribution names their publishers. Empty when none does: no source read covers roads of this class."`
}

// MotorwayQuery is meta.query of getMotorwayConditions.
type MotorwayQuery struct {
	Road      string `json:"road"`
	Direction string `json:"direction"`
	FilterQuery
}

// CursorPage is a page that a cursor continues.
type CursorPage struct {
	Page
	NextCursor *string `json:"nextCursor" nullable:"true" doc:"The cursor of the next page; null on the last."`
}

// cursorPage is the page of n situations read past offset, limit at most.
func cursorPage(limit, offset int, more bool, n int) CursorPage {
	out := CursorPage{Page: Page{Limit: limit, HasMore: more}}
	if more {
		next := strconv.Itoa(offset + n)
		out.NextCursor = &next
	}
	return out
}

// MotorwayMeta is the meta member of getMotorwayConditions.
type MotorwayMeta struct {
	DataMeta
	Page  CursorPage    `json:"page"`
	Query MotorwayQuery `json:"query"`
}

// MotorwayEnvelope is the enveloped answer of getMotorwayConditions.
type MotorwayEnvelope struct {
	Data  MotorwayData `json:"data"`
	Meta  MotorwayMeta `json:"meta"`
	Links Links        `json:"links"`
}

// MotorwayOutput is the response of getMotorwayConditions.
type MotorwayOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         MotorwayEnvelope
}

func registerMotorwayConditions(api huma.API, s *server, op huma.Operation) {
	op.Summary = "What is happening on a motorway"
	op.Description = "The active situations on one road, in the platform's motorway status order (jams, closures, the other reports, roadworks), with the lamp over every situation on the road in the asked direction and every official weather warning in force in an area the road crosses. MCP counterpart: road status, and, with a window (from, to), roadworks ahead on one road. A federal road, a Landesstraße or a Staatsstraße, or a motorway the road register lacks, is answered from the situations that name it, with data.coverage stating which families hold situations on roads of its class: no sites is a 200 with an empty list, never a 404. Needs the scope roads; the weather warnings are public and read for every key that may read the weather families, which the answer then credits; without them the lamp says its sight is limited."
	featureResponses(api, &op, "The operation is switched off on this instance (feature-disabled). A well-formed road the road register does not hold is no 404: it is answered with data.coverage.")
	huma.Register(api, op, func(ctx context.Context, in *MotorwayInput) (*MotorwayOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := roads.CorridorQuery{Road: in.road, Direction: roads.Direction(in.Direction), Filter: s.windowed(ctx, in.filter(), in.SituationWindow), Limit: s.limit(in.Limit), Offset: in.offset}
		c, err := s.features.MotorwayConditions(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		noteLamp(ctx, c)
		return &MotorwayOutput{CacheControl: noStore, Body: MotorwayEnvelope{
			Data: MotorwayData{
				Corridor:    Corridor{Road: c.Road.Ref, RoadType: string(c.Road.Class), Direction: string(q.Direction), States: append([]string{}, c.Road.States...)},
				Lamp:        string(c.Lamp),
				LampReason:  lampReason(c.Reason),
				LampReasons: lampReasons(c),
				Sight:       lampSight(c),
				Coverage:    roadCoverage(c),
				Counts:      familyCounts(c.Counts),
				Stamps:      sourceStamps(c.Stamps, c.At),
				Situations:  roadSituations(c.Items),
			},
			Meta: MotorwayMeta{
				DataMeta: s.dataMeta(ctx, c.Families, c.At, c.Partial, partialDetail),
				Page:     cursorPage(q.Limit, q.Offset, c.HasMore, len(c.Items)),
				Query:    MotorwayQuery{Road: c.Road.Ref, Direction: string(q.Direction), FilterQuery: filterQuery(q.Filter)},
			},
			Links: s.selfLink(ctx),
		}}, nil
	})
}

// roadCoverage is data.coverage of a conditions answer: nil for a road the
// register holds.
func roadCoverage(c features.Conditions) *RoadCoverage {
	if c.Covering == nil {
		return nil
	}
	return &RoadCoverage{RoadType: string(c.Road.Class), Families: append([]string{}, c.Covering...)}
}

// ------------------------------------------------------------ area conditions

// RoadConditionsInput is the input of listRoadConditions.
type RoadConditionsInput struct {
	Near
	SituationFilters
	SituationWindow
	RoadClass []string `query:"road_class" enum:"motorway,federal,state,urban,other" doc:"Only situations on roads of these classes, comma-separated; other keeps the classes that cannot be mapped. Default: every class held."`
	Cursor    string   `query:"cursor" pattern:"^[0-9]{1,4}$" doc:"The meta.page.nextCursor of the page before; omit for the first page."`
	offset    int
}

// Resolve reads the position and the cursor and refuses a window that
// cannot be read.
func (in *RoadConditionsInput) Resolve(ctx huma.Context) []error {
	in.offset, _ = strconv.Atoi(in.Cursor)
	return append(in.AtPosition.Resolve(ctx), in.check()...)
}

// Area is the area a conditions answer is about.
type Area struct {
	At      Position `json:"at"`
	RadiusM int      `json:"radiusM" doc:"The radius applied, in metres."`
}

// RoadConditionsData is the answer of listRoadConditions. Its lamp and
// lampReason are API-only: the platform's road status around a position
// states none, and the /v1 contract keeps both (VER-001), so the parity
// harness leaves them out (parity.Default).
type RoadConditionsData struct {
	Area        Area            `json:"area"`
	Counts      []FamilyCount   `json:"counts" doc:"How many situations the filters keep, by source family, before limit and cursor (the platform's counts per publisher), in family order; empty when none."`
	Stamps      []SourceStamp   `json:"sourceStamps" doc:"The Stand of every attributed source family, as the platform's road status states each source's beside its credit, in family order: when it was last checked, when its data last changed, and whether it is possibly out of date. A family with neither instant has none."`
	Lamp        string          `json:"lamp" enum:"green,amber,red" doc:"The lamp over the question's scope (the road and direction, or the radius), whatever kinds and min_severity keep in the list, by the platform's motorway Ampel (amber is its yellow). It reads every situation in the scope as its record is read (the motorway operator's event, any other traffic message, a bridge's state) and every official weather warning in force in an area the road crosses, or within the radius. Red: a closure of the carriageway; a safety message by its words on any kind (a wrong-way driver, an object, a person or an animal on the road, an accident with the road closed); a closed bridge; a jam with a delay of 30 minutes or more; a weather warning of level 3 or 4. Amber: a closed entry, exit or junction link; a shorter jam or one with no delay; a weather warning of level 1 or 2; any other message (an accident without a closure, a restricted bridge, a situation that cannot be mapped); roadworks or a lane restriction on the carriageway (works on a ramp give no colour); or limited sight: a roads family stale or without data, a roads family the key may not read (meta.partial), the weather warnings not readable with the key or their source not current, or a state in scope whose traffic messages no current roads family the key may read covers. A situation's severity does not colour the lamp. Green when nothing gives a colour and every group read is current, which is no guarantee that the road is free."`
	LampReason  *string         `json:"lampReason" nullable:"true" enum:"road_closed,ramp_closed,safety,bridge_closed,long_jam,jam,weather_warning,slippery,warning,narrowing,limited_sight" doc:"Why the lamp has its colour: the first of its reasons, a red one before any amber one, and inside a colour in the platform's order, which is this enumeration's order; null for green. slippery is never given while the read contract holds no road-weather forecast. Open enumeration."`
	LampReasons []LampReason    `json:"lampReasons" doc:"Every reason of the lamp with how many give it, in lampReason's order (lampReason is the first); empty for green."`
	Sight       []LampSight     `json:"sight" doc:"The sight of every group the lamp read; any group not fresh keeps the lamp from green (limited_sight)."`
	Situations  []RoadSituation `json:"situations" doc:"What the platform's road status lists around a position: the closures, the roadworks and lane restrictions, and the jams, whatever kinds asks beyond them (a hazard, an accident or a weather report is not listed). Ordered by severity descending, then distance, then id; distanceM is rounded to 100 m, as the platform states it in tenths of a kilometre."`
}

// RoadConditionsQuery is meta.query of listRoadConditions.
type RoadConditionsQuery struct {
	NearQuery
	FilterQuery
	RoadClasses []string `json:"roadClasses" doc:"The road classes asked for; empty for every class."`
}

// RoadConditionsMeta is the meta member of listRoadConditions.
type RoadConditionsMeta struct {
	DataMeta
	Page  CursorPage          `json:"page"`
	Query RoadConditionsQuery `json:"query"`
}

// RoadConditionsEnvelope is the enveloped answer of listRoadConditions.
type RoadConditionsEnvelope struct {
	Data  RoadConditionsData `json:"data"`
	Meta  RoadConditionsMeta `json:"meta"`
	Links Links              `json:"links"`
}

// RoadConditionsOutput is the response of listRoadConditions.
type RoadConditionsOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         RoadConditionsEnvelope
}

func registerRoadConditions(api huma.API, s *server, op huma.Operation) {
	op.Summary = "What is happening on the roads near a position"
	op.Description = "The active closures, roadworks, lane restrictions and jams within the radius of a position on every road class held, what the platform's road status lists there (a hazard, an accident or a weather report is not listed), by severity descending, then distance, then id. With the lamp over every situation within the radius and every official weather warning in force in an area within it (API-only: the platform's road status states no lamp around a position). MCP counterpart: road status (area form). Needs the scope roads; the weather warnings are public and read for every key that may read the weather families, which the answer then credits; without them the lamp says its sight is limited."
	featureResponses(api, &op, featureOffDoc)
	huma.Register(api, op, func(ctx context.Context, in *RoadConditionsInput) (*RoadConditionsOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		f := in.filter()
		f.Listed = roads.AreaKinds
		q := roads.AreaQuery{At: in.point, RadiusM: s.radius(in.RadiusM), Filter: s.windowed(ctx, f, in.SituationWindow), Limit: s.limit(in.Limit), Offset: in.offset}
		types := []string{}
		for _, c := range in.RoadClass {
			q.Classes = append(q.Classes, roads.Class(c))
			types = append(types, c)
		}
		c, err := s.features.RoadConditions(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		noteLamp(ctx, c)
		listed := roadSituations(c.Items)
		for i := range listed {
			if listed[i].DistanceM != nil {
				d := tenthKm(*listed[i].DistanceM)
				listed[i].DistanceM = &d
			}
		}
		return &RoadConditionsOutput{CacheControl: noStore, Body: RoadConditionsEnvelope{
			Data: RoadConditionsData{
				Area:        Area{At: position(q.At), RadiusM: q.RadiusM},
				Lamp:        string(c.Lamp),
				LampReason:  lampReason(c.Reason),
				LampReasons: lampReasons(c),
				Sight:       lampSight(c),
				Counts:      familyCounts(c.Counts),
				Stamps:      sourceStamps(c.Stamps, c.At),
				Situations:  listed,
			},
			Meta: RoadConditionsMeta{
				DataMeta: s.dataMeta(ctx, c.Families, c.At, c.Partial, partialDetail),
				Page:     cursorPage(q.Limit, q.Offset, c.HasMore, len(c.Items)),
				Query:    RoadConditionsQuery{NearQuery: NearQuery{At: position(q.At), RadiusM: q.RadiusM}, FilterQuery: filterQuery(q.Filter), RoadClasses: types},
			},
			Links: s.selfLink(ctx),
		}}, nil
	})
}

// ------------------------------------------------------------- one situation

// SituationInput is the input of getRoadSituation.
type SituationInput struct {
	SituationID string `path:"situationId" maxLength:"64" pattern:"^[A-Za-z0-9_.:-]+$" example:"datex:syn:ev-00001" doc:"The situation's id."`
}

// SituationEnvelope is the enveloped answer of getRoadSituation.
type SituationEnvelope struct {
	Data  RoadSituation `json:"data"`
	Meta  DataMeta      `json:"meta"`
	Links Links         `json:"links"`
}

// SituationOutput is the response of getRoadSituation.
type SituationOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         SituationEnvelope
}

func registerRoadSituation(api huma.API, s *server, op huma.Operation) {
	op.Summary = "One road situation"
	op.Description = "One situation by its id, active or ended. A situation that was withdrawn, or that belongs to a source family the key may not read, is not found. Needs the scope roads."
	featureResponses(api, &op, "No situation with this id (not-found), or the operation is switched off on this instance (feature-disabled).")
	huma.Register(api, op, func(ctx context.Context, in *SituationInput) (*SituationOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		a, err := s.features.RoadSituation(ctx, principalFrom(ctx).Key, in.SituationID)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		noteChanges(ctx, a.ChangesAt)
		return &SituationOutput{CacheControl: noStore, Body: SituationEnvelope{
			Data:  roadSituation(a.Items[0]),
			Meta:  s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail),
			Links: s.selfLink(ctx),
		}}, nil
	})
}

// ------------------------------------------------------------- one Land

// StateConditionsInput is the input of listStateRoadConditions.
type StateConditionsInput struct {
	State string `path:"state" pattern:"^(0[1-9]|1[0-6]|de)$" example:"09" doc:"The Land's official code, 01 to 16 (09 Bayern), as listPlaces or describeLocation give it; de for the whole country (every situation held)."`
	SituationFilters
	SituationWindow
	RoadClass []string `query:"road_class" enum:"motorway,federal,state,urban,other" doc:"Only situations on roads of these classes, comma-separated; motorway for a Land's motorways. Default: every class held."`
	Limit     int      `query:"limit" minimum:"1" maximum:"100" doc:"Most situations to return. Default: pagination.default_limit."`
	Cursor    string   `query:"cursor" pattern:"^[0-9]{1,4}$" doc:"The meta.page.nextCursor of the page before; omit for the first page."`
	offset    int
}

// Resolve reads the cursor and refuses a window that cannot be read.
func (in *StateConditionsInput) Resolve(huma.Context) []error {
	in.offset, _ = strconv.Atoi(in.Cursor)
	return in.check()
}

// StateConditionsData is the answer of listStateRoadConditions.
type StateConditionsData struct {
	Counts      []FamilyCount   `json:"counts" doc:"How many situations the filters keep, by source family, before limit and cursor (the platform's counts per publisher), in family order; empty when none."`
	Stamps      []SourceStamp   `json:"sourceStamps" doc:"The Stand of every attributed source family, as the platform's road status states each source's beside its credit, in family order: when it was last checked, when its data last changed, and whether it is possibly out of date. A family with neither instant has none."`
	State       string          `json:"state" doc:"The Land's code."`
	Lamp        string          `json:"lamp" enum:"green,amber,red" doc:"The lamp over every situation of the Land and every official weather warning in force in its areas, by the rules of listRoadConditions."`
	LampReason  *string         `json:"lampReason" nullable:"true" doc:"Why the lamp has its colour, as in listRoadConditions; null for green."`
	LampReasons []LampReason    `json:"lampReasons" doc:"Every reason of the lamp with how many give it, in lampReason's order (lampReason is the first); empty for green."`
	Sight       []LampSight     `json:"sight" doc:"The sight of every group the lamp read; any group not fresh keeps the lamp from green (limited_sight)."`
	Situations  []RoadSituation `json:"situations" doc:"Ordered by severity descending, then road, then id. A situation is in the Land of the nearest municipality centre (BKG, within 20 km) to its point; near a Land border that can be the neighbouring Land."`
}

// StateConditionsQuery is meta.query of listStateRoadConditions.
type StateConditionsQuery struct {
	State string `json:"state"`
	FilterQuery
	RoadClasses []string `json:"roadClasses" doc:"The road classes asked for; empty for every class."`
}

// StateConditionsEnvelope is the enveloped answer of listStateRoadConditions.
type StateConditionsEnvelope struct {
	Data StateConditionsData `json:"data"`
	Meta struct {
		DataMeta
		Page  CursorPage           `json:"page"`
		Query StateConditionsQuery `json:"query"`
	} `json:"meta"`
	Links Links `json:"links"`
}

// StateConditionsOutput is the response of listStateRoadConditions.
type StateConditionsOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         StateConditionsEnvelope
}

func registerStateConditions(api huma.API, s *server, op huma.Operation) {
	op.Summary = "What is happening on the roads of one Land"
	op.Description = "The active situations of one Land (each placed in the Land of the nearest municipality centre to its point, the platform's Land rule), by severity descending, then road, then id, with the lamp over the Land. road_class=motorway answers a Land's motorways. MCP counterpart: motorway traffic (Land form). Needs the scope roads."
	featureResponses(api, &op, featureOffDoc)
	huma.Register(api, op, func(ctx context.Context, in *StateConditionsInput) (*StateConditionsOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := roads.AreaQuery{State: in.State, Filter: s.windowed(ctx, in.filter(), in.SituationWindow), Limit: s.limit(in.Limit), Offset: in.offset}
		types := []string{}
		for _, c := range in.RoadClass {
			q.Classes = append(q.Classes, roads.Class(c))
			types = append(types, c)
		}
		c, err := s.features.RoadConditions(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		noteLamp(ctx, c)
		out := &StateConditionsOutput{CacheControl: noStore}
		out.Body.Data = StateConditionsData{State: q.State, Lamp: string(c.Lamp), LampReason: lampReason(c.Reason),
			LampReasons: lampReasons(c), Sight: lampSight(c), Counts: familyCounts(c.Counts), Stamps: sourceStamps(c.Stamps, c.At), Situations: roadSituations(c.Items)}
		out.Body.Meta.DataMeta = s.dataMeta(ctx, c.Families, c.At, c.Partial, partialDetail)
		out.Body.Meta.Page = cursorPage(q.Limit, q.Offset, c.HasMore, len(c.Items))
		out.Body.Meta.Query = StateConditionsQuery{State: q.State, FilterQuery: filterQuery(q.Filter), RoadClasses: types}
		out.Body.Links = s.selfLink(ctx)
		return out, nil
	})
}

// FamilyCount is how many situations of one source family an answer's
// filters keep, before the limit.
type FamilyCount struct {
	SourceFamily string `json:"sourceFamily" doc:"The source family; its attribution is in meta.attribution."`
	Situations   int    `json:"situations" minimum:"1" doc:"How many of its situations the filters keep, the listed and those past the limit."`
}

// familyCounts are the counts in family order, never nil.
func familyCounts(m map[string]int) []FamilyCount {
	out := make([]FamilyCount, 0, len(m))
	for _, f := range slices.Sorted(maps.Keys(m)) {
		out = append(out, FamilyCount{SourceFamily: f, Situations: m[f]})
	}
	return out
}
