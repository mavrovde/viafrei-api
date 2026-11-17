package httpapi

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
)

// The platform's watch conditions per kind (watch_situation), their
// defaults filled in when a key is not given.
const (
	watchStationDelayMin = 10
	watchRegionLateShare = 0.25
	watchRegionDelayMin  = 5
	watchRegionMinTrips  = 10
	watchWeatherMinLevel = 2
	// watchMaxDepartures is the most departures of the window a station
	// watch reads.
	watchMaxDepartures = 100
)

// watchConditionKeys are the condition keys each kind takes; any other is
// refused by name, as the platform refuses it.
var watchConditionKeys = map[string][]string{
	"road":    {"event", "events"},
	"place":   {"radius_km"},
	"station": {"delay_min"},
	"region":  {"late_share", "delay_min", "min_trips"},
	"weather": {"min_level"},
	"charger": {"point_id"},
}

// watchScopes are the scopes a watch of each other kind needs besides roads.
var watchScopes = map[string]access.Scope{"station": "transit", "region": "transit", "weather": "weather", "charger": "energy"}

var regionKey = regexp.MustCompile(`^[0-9]{2,12}$`)

// conditionKeysOf refuses a condition key the kind does not take.
func conditionKeysOf(kind, raw string) []error {
	var keys map[string]json.RawMessage
	if raw == "" || json.Unmarshal([]byte(raw), &keys) != nil {
		return nil
	}
	allowed := watchConditionKeys[kind]
	var wrong []string
	for k := range keys {
		if !slices.Contains(allowed, k) {
			wrong = append(wrong, k)
		}
	}
	if len(wrong) == 0 {
		return nil
	}
	sort.Strings(wrong)
	return bad("query.condition", "condition "+strings.Join(wrong, ", ")+" does not fit a "+kind+" watch; that kind takes: "+strings.Join(allowed, ", "), raw)
}

// resolveOtherKind reads the target and condition of a station, region,
// weather or charger watch, its defaults filled in.
func (in *WatchInput) resolveOtherKind() []error {
	c := &in.cond
	switch in.Kind {
	case "station":
		if c.DelayMin == 0 {
			c.DelayMin = watchStationDelayMin
		}
	case "region":
		if !regionKey.MatchString(in.Key) {
			return bad("query.target", "a region is its official key (AGS): two digits a Land, five a Kreis, more a Gemeinde", in.Key)
		}
		if c.DelayMin == 0 {
			c.DelayMin = watchRegionDelayMin
		}
		if c.DelayMin != watchRegionDelayMin {
			return bad("query.condition", "the regional punctuality counts the trips 5 minutes late or more; delay_min must be 5", in.Condition)
		}
		if c.LateShare == 0 {
			c.LateShare = watchRegionLateShare
		}
		if c.MinTrips == 0 {
			c.MinTrips = watchRegionMinTrips
		}
	case "weather":
		if err := in.parseAt(); err != nil {
			return err
		}
		if c.MinLevel == 0 {
			c.MinLevel = watchWeatherMinLevel
		}
	}
	return nil
}

// watchRead is one read of a station, region, weather or charger watch.
type watchRead struct {
	data WatchData
	meta DataMeta
}

// readOtherKind reads a station, region, weather or charger watch at now
// until end, and says whether its condition holds.
func (s *server) readOtherKind(ctx context.Context, in *WatchInput, now, end time.Time) (watchRead, error) {
	key := principalFrom(ctx).Key
	c := in.cond
	var r watchRead
	if scope := watchScopes[in.Kind]; !key.Plan.Has(scope) {
		return r, newProblem(ctx, SlugScopeDenied, "A "+in.Kind+" watch needs the scope "+string(scope)+".", 0)
	}
	holds := false
	switch in.Kind {
	case "station":
		b, err := s.features.Departures(ctx, key, transit.DepartureQuery{StationID: in.Key, From: now, Window: end.Sub(now), Limit: watchMaxDepartures})
		if err != nil {
			return r, s.featureProblem(ctx, err)
		}
		r.data.Condition = map[string]any{"delay_min": c.DelayMin}
		r.data.Departures = []Departure{}
		for _, d := range b.Items {
			if d.Cancelled || d.DelayMin != nil && *d.DelayMin >= c.DelayMin {
				r.data.Departures = append(r.data.Departures, departureRow(d))
			}
		}
		holds = len(r.data.Departures) > 0
		r.meta = s.dataMeta(ctx, b.Families, b.At, b.Partial, partialDetail)
	case "region":
		q := transit.DisruptionQuery{Area: in.Key, WindowMin: disruptionDefaultWindowMin, Limit: disruptionDefaultLimit}
		a, err := s.features.Disruptions(ctx, key, q)
		if err != nil {
			return r, s.featureProblem(ctx, err)
		}
		r.data.Condition = map[string]any{"late_share": c.LateShare, "delay_min": c.DelayMin, "min_trips": c.MinTrips}
		env := disruptionEnvelope(s, ctx, a, q)
		r.data.Punctuality = env.Data.Punctuality
		p := a.Punctuality
		holds = p != nil && p.TripsObserved >= c.MinTrips && float64(p.TripsLate) >= c.LateShare*float64(p.TripsObserved)
		r.meta = env.Meta.DataMeta
	case "weather":
		a, err := s.features.WeatherRisk(ctx, key, weather.Query{At: *in.at, MinLevel: weather.Level(c.MinLevel)})
		if err != nil {
			return r, s.featureProblem(ctx, err)
		}
		r.data.Condition = map[string]any{"min_level": c.MinLevel}
		r.data.Warnings = weatherWarnings(a.Items)
		holds = len(a.Items) > 0
		r.meta = s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail)
	case "charger":
		a, err := s.features.ChargingSite(ctx, key, in.Key, s.cfg.Charging.StatusMaxAge)
		if err != nil {
			return r, s.featureProblem(ctx, err)
		}
		a.Items[0].DistanceM = 0
		site := chargingSites(ctx, a, s.cfg.Charging.StatusMaxAge)[0]
		r.data.Condition = map[string]any{}
		if c.PointID != "" {
			r.data.Condition["point_id"] = c.PointID
		}
		for _, p := range site.ChargePoints {
			if (c.PointID == "" || p.ID == c.PointID) && p.Availability != nil && p.Availability.State == "available" {
				holds = true
			}
		}
		r.data.Site = &site
		r.meta = s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail)
	}
	r.data.Holds = &holds
	return r, nil
}

// errWatchUntil is the refusal of a window more than 24 hours ahead.
func errWatchUntil(until string) error {
	return huma.Error422UnprocessableEntity("until is more than 24 hours ahead", &huma.ErrorDetail{Location: "query.until", Value: until})
}
