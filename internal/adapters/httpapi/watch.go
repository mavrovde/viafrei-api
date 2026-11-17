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
	Kind      string  `query:"kind" enum:"road,place,station,region,weather,charger" example:"road" doc:"What is watched: road (key A8, B27) or place (key lat,lon); station, region, weather and charger are answered by their own reads (listStationDepartures, listTransitDisruptions, getWeatherRisk, listChargingOptions)."`
	Key       string  `query:"target" maxLength:"120" example:"A7" doc:"The thing watched: a road number for road, a position lat,lon for place."`
	Request   string  `query:"request" maxLength:"500" doc:"The person's request in their own words; a jam is watched on a motorway only, and the answer says so."`
	Hours     float64 `query:"hours" minimum:"0.25" maximum:"24" doc:"How long the window runs from now, in hours (default 3). Give hours or until, not both."`
	Until     string  `query:"until" format:"date-time" doc:"The window's end instant, at most 24 hours ahead. Give hours or until, not both."`
	Condition string  `query:"condition" maxLength:"500" doc:"The threshold as JSON: road {\"event\": \"reopen\"|\"any\", \"events\": [\"restrictions\",\"jams\"]}; place {\"radius_km\": 10}. Any other key is refused by name."`
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
		return bad("query.kind", "kind "+in.Kind+" is answered by its own read", in.Kind)
	case "road":
		r, err := roads.ParseRoad(in.Key)
		if err != nil {
			return bad("query.target", err.Error(), in.Key)
		}
		in.Key = r
	case "place":
		p, err := geo.ParsePoint(in.Key)
		if err != nil {
			return bad("query.target", err.Error(), in.Key)
		}
		in.at = &p
	}
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
	Lamp       *string         `json:"lamp" nullable:"true"`
	Situations []RoadSituation `json:"situations" doc:"The situations in the window, changed after since when given."`
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
	op.Summary = "Watch a road or a place: a repeated read of what changed"
	op.Description = "The platform's watch as a plain read: the situations of a road (closures, restrictions and, on a motorway, jams) or around a place (closures and restrictions only) in force in the window, and with since only those changed after it. Read it again with watch_id and since; there is nothing to stop. MCP counterpart: watch situation, stop watch. Needs the scope roads."
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
			return nil, huma.Error422UnprocessableEntity("until is more than 24 hours ahead", &huma.ErrorDetail{Location: "query.until", Value: in.Until})
		}
		jams := in.Kind == "road" && strings.HasPrefix(in.Key, "A") && (slices.Contains(in.cond.Events, "jams") || len(in.cond.Events) == 0 && strings.Contains(strings.ToLower(in.Request), "stau"))
		restrictions := len(in.cond.Events) == 0 || slices.Contains(in.cond.Events, "restrictions")
		var kinds []roads.Kind
		if restrictions {
			kinds = append(kinds, roads.KindClosure, roads.KindRoadworks, roads.KindRestriction)
		}
		if jams {
			kinds = append(kinds, roads.KindJam)
		}
		var note *string
		if !jams && (slices.Contains(in.cond.Events, "jams") || strings.Contains(strings.ToLower(in.Request), "stau") || strings.Contains(strings.ToLower(in.Request), "jam")) {
			n := "A jam is watched on a motorway only; this watch reports closures and restrictions."
			note = &n
		}
		event := in.cond.Event
		if event == "" {
			event = "any"
		}
		filter := roads.Filter{Kinds: kinds, Until: end}
		cond := map[string]any{}
		var list []roads.Situation
		var lamp *string
		var meta DataMeta
		if in.Kind == "road" {
			cond["event"], cond["events"] = event, in.cond.Events
			c, err := s.features.MotorwayConditions(ctx, principalFrom(ctx).Key, roads.CorridorQuery{Road: in.Key, Direction: roads.DirectionBoth, Filter: filter, Limit: 100})
			if err != nil {
				return nil, s.featureProblem(ctx, err)
			}
			l := string(c.Lamp)
			list, lamp, meta = c.Items, &l, s.dataMeta(ctx, c.Families, c.At, c.Partial, partialDetail)
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
			l := string(c.Lamp)
			fams := c.Families
			if len(c.Items) == 0 {
				// An answer that states no situation carries no register's
				// condition, as the platform's watch answers it.
				fams = provenance.Unconditioned(fams)
			}
			list, lamp, meta = c.Items, &l, s.dataMeta(ctx, fams, c.At, c.Partial, partialDetail)
		}
		if in.since != nil {
			list = slices.DeleteFunc(slices.Clone(list), func(x roads.Situation) bool { return !x.UpdatedAt.After(*in.since) })
		}
		raw, _ := json.Marshal(watchToken{Kind: in.Kind, Key: in.Key, Condition: in.cond})
		id := base64.RawURLEncoding.EncodeToString(raw)
		return &WatchOutput{CacheControl: noStore, Body: WatchEnvelope{
			Data: WatchData{WatchID: id, URI: "viafrei://watch/" + id, Kind: in.Kind, Key: in.Key, From: now, Until: end, Ended: !end.After(now),
				Condition: cond, Note: note, Lamp: lamp, Situations: roadSituations(list)},
			Meta: meta, Links: s.selfLink(ctx)}}, nil
	})
}
