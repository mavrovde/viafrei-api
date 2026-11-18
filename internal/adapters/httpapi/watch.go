package httpapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// The platform's watch window (watch_situation): 3 hours by default, at
// least a quarter of an hour, at most 24.
const (
	watchDefaultHours = 3
	watchMaxHours     = 24
)

// WatchInput is the input of getWatch: a watch is a plain read, repeated. The
// API keeps no watch: the same read again, with since, answers what changed.
type WatchInput struct {
	WatchID   string  `query:"watch_id" maxLength:"512" pattern:"^[A-Za-z0-9_-]+$" doc:"A watch's id as an earlier answer gave it: it stands for kind, target and condition. Give it, or kind and target (the platform's key)."`
	Kind      string  `query:"kind" enum:"road,place,station,region,weather,charger" example:"road" doc:"What is watched: road (target A8, B27), place (target lat,lon), station (a station id as listStations answers it), region (an official key: two digits a Land, five a Kreis), weather (target lat,lon) or charger (a charging site id)."`
	Key       string  `query:"target" maxLength:"120" example:"A7" doc:"The thing watched: a road number, a position lat,lon (place, weather), a DWD warn cell id or a place or cell name (weather), a station id, an official region key or a charging site id."`
	Request   string  `query:"request" maxLength:"500" doc:"The person's request in their own words; a jam is watched on a motorway only, and the answer says so."`
	Hours     float64 `query:"hours" minimum:"0.25" maximum:"24" doc:"How long the window runs from now, in hours (default 3). Give hours or until, not both."`
	Until     string  `query:"until" format:"date-time" doc:"The window's end instant, at most 24 hours ahead. Give hours or until, not both."`
	Condition string  `query:"condition" maxLength:"500" doc:"The threshold as JSON: road {\"event\": \"reopen\"|\"any\", \"events\": [\"restrictions\",\"jams\"]}; place {\"radius_km\": 25}; station {\"delay_min\": 10}; region {\"late_share\": 0.25, \"delay_min\": 5, \"min_trips\": 10}; weather {\"min_level\": 2}; charger {\"point_id\": \"...\"}. The values shown are the defaults. A key of another kind is refused by name."`
	Since     string  `query:"since" format:"date-time" doc:"Only the situations changed after this instant: the cursor of a repeated read, the previous answer's generatedAt."`
	cond      watchCondition
	at        *geo.Point
	end       *time.Time
	since     *time.Time
}

type watchCondition struct {
	Event    string   `json:"event,omitempty"`
	Events   []string `json:"events,omitempty"`
	RadiusKm float64  `json:"radius_km,omitempty"`
	DelayMin int      `json:"delay_min,omitempty"`
	// LateShare is the share of observed trips late for a region watch.
	LateShare float64 `json:"late_share,omitempty"`
	MinTrips  int     `json:"min_trips,omitempty"`
	// MinLevel is a pointer: 0 is a level the platform takes (every
	// warning), not an absent key.
	MinLevel *int   `json:"min_level,omitempty"`
	PointID  string `json:"point_id,omitempty"`
}

// watchToken is the id of a watch: kind, key and condition.
type watchToken struct {
	Kind      string         `json:"k"`
	Key       string         `json:"key"`
	Condition watchCondition `json:"c"`
}

func bad(loc, msg, v string) []error {
	return []error{&huma.ErrorDetail{Location: loc, Message: msg, Value: v}}
}

// Resolve reads the watch id, the condition, the window and the key.
func (in *WatchInput) Resolve(huma.Context) []error {
	if in.WatchID != "" {
		raw, err := base64.RawURLEncoding.DecodeString(in.WatchID)
		var t watchToken
		if err != nil || json.Unmarshal(raw, &t) != nil {
			return bad("query.watch_id", "not a watch id", in.WatchID)
		}
		in.Kind, in.Key, in.cond = t.Kind, t.Key, t.Condition
	} else if in.Condition != "" {
		d := json.NewDecoder(strings.NewReader(in.Condition))
		d.DisallowUnknownFields()
		if err := d.Decode(&in.cond); err != nil {
			return bad("query.condition", "condition: "+err.Error(), in.Condition)
		}
	}
	if in.Kind == "" || in.Key == "" {
		return bad("query.kind", "give watch_id, or kind and target", in.Kind)
	}
	if errs := conditionKeysOf(in.Kind, in.Condition); errs != nil {
		return errs
	}
	if errs := in.cond.bounds(in.Condition); errs != nil {
		return errs
	}
	if in.Hours != 0 && in.Until != "" {
		return bad("query.until", "give hours or until, not both", in.Until)
	}
	if in.Until != "" {
		t, err := time.Parse(time.RFC3339, in.Until)
		if err != nil {
			return bad("query.until", err.Error(), in.Until)
		}
		in.end = &t
	}
	if in.Since != "" {
		t, err := time.Parse(time.RFC3339, in.Since)
		if err != nil {
			return bad("query.since", err.Error(), in.Since)
		}
		in.since = &t
	}
	switch in.Kind {
	case "station", "region", "weather", "charger":
		return in.resolveOtherKind()
	case "road":
		r, err := roads.ParseRoad(in.Key)
		if err != nil {
			return bad("query.target", err.Error(), in.Key)
		}
		in.Key = r
	case "place":
		return in.parseAt()
	}
	return nil
}

// parseAt reads the target as a position.
func (in *WatchInput) parseAt() []error {
	p, err := geo.ParsePoint(in.Key)
	if err != nil {
		return bad("query.target", err.Error(), in.Key)
	}
	in.at = &p
	return nil
}

// WatchData is the answer of one read of a watch.
type WatchData struct {
	WatchID    string          `json:"watchId" doc:"Read it again with watch_id and since to see what changed."`
	URI        string          `json:"uri" doc:"The platform's resource uri of this watch."`
	Kind       string          `json:"kind"`
	Key        string          `json:"key"`
	From       time.Time       `json:"from"`
	Until      time.Time       `json:"until" doc:"The end of the window; a read after it answers ended."`
	Ended      bool            `json:"ended"`
	Condition  map[string]any  `json:"condition" doc:"The condition applied, defaults filled in."`
	Note       *string         `json:"note" nullable:"true" doc:"Set when the request asked for something the watch does not report (a jam off a motorway, or around a place)."`
	Situations []RoadSituation `json:"situations" doc:"road, place: the situations in the window, changed after since when given; empty for the other kinds."`
	// The lists below answer the other kinds.
	Departures  []Departure            `json:"departures,omitempty" doc:"station: the departures of the window cancelled or at least delay_min late."`
	Punctuality *DisruptionPunctuality `json:"punctuality,omitempty" doc:"region: the area's punctuality, as listDisruptions reports it."`
	Trips       *WatchTrips            `json:"trips,omitempty" doc:"region: the distinct trips observed at the area's stops in the last half hour and those more than delay_min late at one of them; the condition holds when at least min_trips were observed and at least late_share of them were late. Absent when none was observed."`
	Warnings    []WeatherWarning       `json:"warnings,omitempty" doc:"weather: the official warnings in force of the warn cell of at least min_level."`
	Site        *ChargingSite          `json:"site,omitempty" doc:"charger: the site and its points; the condition holds when a point (point_id when given) is reported available."`
}

// WatchTrips is the trip count a region watch holds on.
type WatchTrips struct {
	Observed   int       `json:"observed" doc:"Distinct trips observed at the area's stops in the last 30 minutes."`
	Late       int       `json:"late" doc:"Those more than delay_min minutes late at one of the area's stops."`
	LateShare  float64   `json:"lateShare" doc:"late over observed, rounded to three decimals."`
	ObservedAt time.Time `json:"observedAt" doc:"The newest observation counted."`
}

// WatchEnvelope is the enveloped answer of getWatch.
type WatchEnvelope struct {
	Data  WatchData `json:"data"`
	Meta  DataMeta  `json:"meta"`
	Links Links     `json:"links"`
}

// WatchOutput is the response of getWatch.
type WatchOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         WatchEnvelope
}

func registerWatch(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Watch a road, a place, a station, a region, the weather or a charger: a repeated read"
	op.Description = "The platform's watch as a plain read: the situations of a road (closures, restrictions and, on a motorway, jams) or around a place (closures and restrictions only) in force in the window, and with since only those changed after it; a station's departures delayed or cancelled, a region's punctuality, the weather warnings at a position or a charging site's free points, with whether the condition holds now. Read it again with watch_id (and since); there is nothing to stop. MCP counterpart: watch situation, stop watch. Needs the scope roads, and for a station or region watch transit, for a weather watch weather, for a charger watch energy."
	featureResponses(api, &op, featureOffDoc)
	huma.Register(api, op, func(ctx context.Context, in *WatchInput) (*WatchOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		now := s.now()
		end := now.Add(time.Duration(float64(time.Hour) * watchDefaultHours))
		if in.Hours != 0 {
			end = now.Add(time.Duration(in.Hours * float64(time.Hour)))
		}
		if in.end != nil {
			end = *in.end
		}
		if end.After(now.Add(watchMaxHours * time.Hour)) {
			return nil, errWatchUntil(ctx, in.Until)
		}
		if !end.After(now) {
			// As the platform's watch: a window must end in the future, so
			// no answer has an until before its from (#150).
			return nil, errWatchPast(ctx, in.Until)
		}
		raw, _ := json.Marshal(watchToken{Kind: in.Kind, Key: in.Key, Condition: in.cond})
		id := base64.RawURLEncoding.EncodeToString(raw)
		if in.Kind != "road" && in.Kind != "place" {
			r, err := s.readOtherKind(ctx, in, now, end)
			if err != nil {
				return nil, err
			}
			d := r.data
			d.WatchID, d.URI, d.Kind, d.Key, d.From, d.Until, d.Ended, d.Situations = id, "viafrei://watch/"+id, in.Kind, in.Key, now, end, !end.After(now), []RoadSituation{}
			return &WatchOutput{CacheControl: noStore, Body: WatchEnvelope{Data: d, Meta: r.meta, Links: s.selfLink(ctx)}}, nil
		}
		// The events watched, as the platform infers them from the person's
		// words when the condition names none: a jam is watched on a
		// motorway only.
		events := in.cond.Events
		if in.Kind == "road" {
			events = roads.WatchEvents(in.cond.Events, roads.AskOf(true, in.Request, in.Key))
		}
		motorway := in.Kind == "road" && strings.HasPrefix(in.Key, "A")
		var note *string
		if !motorway && (slices.Contains(events, "jams") || in.Kind == "place" && roads.AskOf(false, in.Request, "") != roads.AskPlain) {
			n := "A jam is watched on a motorway only; this watch reports closures and restrictions."
			note = &n
			events = slices.DeleteFunc(slices.Clone(events), func(e string) bool { return e == "jams" })
		}
		jams := motorway && slices.Contains(events, "jams")
		restrictions := len(events) == 0 || slices.Contains(events, "restrictions")
		var kinds []roads.Kind
		if restrictions {
			kinds = append(kinds, roads.KindClosure, roads.KindRoadworks, roads.KindRestriction)
		}
		if jams {
			kinds = append(kinds, roads.KindJam)
		}
		event := in.cond.Event
		if event == "" {
			event = "any"
		}
		filter := roads.Filter{Kinds: kinds, Until: end}
		cond := map[string]any{}
		var (
			list []roads.Situation
			fams []provenance.Family
			at   time.Time
			part bool
		)
		if in.Kind == "road" {
			cond["event"] = event
			if events != nil {
				cond["events"] = events
			}
			c, err := s.features.MotorwayConditions(ctx, principalFrom(ctx).Key, roads.CorridorQuery{Road: in.Key, Direction: roads.DirectionBoth, Filter: filter, Limit: 100})
			if err != nil {
				return nil, s.featureProblem(ctx, err)
			}
			list, fams, at, part = c.Items, c.Families, c.At, c.Partial
		} else {
			r := in.cond.RadiusKm
			if r == 0 {
				r = 25
			}
			cond["radius_km"] = r
			c, err := s.features.RoadConditions(ctx, principalFrom(ctx).Key, roads.AreaQuery{At: *in.at, RadiusM: int(r * 1000), Filter: filter, Limit: 100})
			if err != nil {
				return nil, s.featureProblem(ctx, err)
			}
			list, fams, at, part = c.Items, c.Families, c.At, c.Partial
		}
		if in.since != nil {
			list = slices.DeleteFunc(slices.Clone(list), func(x roads.Situation) bool { return !x.UpdatedAt.After(*in.since) })
		}
		// The watch credits the families of the situations it reports, as the
		// platform's watch credits the data behind each report: a watch that
		// reports none credits none.
		fams = slices.DeleteFunc(slices.Clone(fams), func(f provenance.Family) bool {
			return !slices.ContainsFunc(list, func(x roads.Situation) bool { return x.Family == f.Source.Family })
		})
		meta := s.dataMeta(ctx, fams, at, part, partialDetail)
		return &WatchOutput{CacheControl: noStore, Body: WatchEnvelope{
			Data: WatchData{WatchID: id, URI: "viafrei://watch/" + id, Kind: in.Kind, Key: in.Key, From: now, Until: end, Ended: !end.After(now),
				Condition: cond, Note: note, Situations: roadSituations(list)},
			Meta: meta, Links: s.selfLink(ctx)}}, nil
	})
}
