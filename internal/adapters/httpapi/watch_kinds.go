package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
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

// regionKey is an official region key: it starts with its Land's key, 01 to
// 16, and runs to at most 12 digits (#152). Every parameter that takes a Land
// or a region key states the same rule in its pattern (state paths,
// listMotorways' state, listTransitDisruptions' area); TestEveryLandParameterTakesTheSixteenLaender
// walks them all.
var regionKey = regexp.MustCompile(`^(0[1-9]|1[0-6])[0-9]{0,10}$`)

// conditionBounds are the bounds the platform takes each numeric condition
// value in (watch_situation's schema); a value outside is refused by name.
var conditionBounds = []struct {
	key    string
	lo, hi float64
	of     func(*watchCondition) (float64, bool)
}{
	{"delay_min", 1, 600, func(c *watchCondition) (float64, bool) { return float64(c.DelayMin), c.DelayMin != 0 }},
	{"late_share", 0.01, 1, func(c *watchCondition) (float64, bool) { return c.LateShare, c.LateShare != 0 }},
	{"min_trips", 1, 10000, func(c *watchCondition) (float64, bool) { return float64(c.MinTrips), c.MinTrips != 0 }},
	{"radius_km", 0.1, 200, func(c *watchCondition) (float64, bool) { return c.RadiusKm, c.RadiusKm != 0 }},
	{"min_level", 0, 4, func(c *watchCondition) (float64, bool) {
		if c.MinLevel == nil {
			return 0, false
		}
		return float64(*c.MinLevel), true
	}},
}

// bounds refuses a condition value outside the platform's bounds. A zero
// given for a key whose bounds exclude it is refused too: raw says which keys
// were given.
func (c *watchCondition) bounds(raw string) []error {
	var given map[string]json.RawMessage
	_ = json.Unmarshal([]byte(raw), &given)
	for _, b := range conditionBounds {
		v, set := b.of(c)
		_, named := given[b.key]
		if !set && !named {
			continue
		}
		if v < b.lo || v > b.hi {
			return bad("query.condition", fmt.Sprintf("condition %s must lie between %g and %g", b.key, b.lo, b.hi), raw)
		}
	}
	return nil
}

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
		if c.LateShare == 0 {
			c.LateShare = watchRegionLateShare
		}
		if c.MinTrips == 0 {
			c.MinTrips = watchRegionMinTrips
		}
	case "weather":
		// a position is the cell it lies in; anything else is a cell or a
		// place, resolved on the read
		if p, err := geo.ParsePoint(in.Key); err == nil {
			in.at = &p
		}
		if c.MinLevel == nil {
			l := watchWeatherMinLevel
			c.MinLevel = &l
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
// until end. A region or weather watch credits no family, as the platform's
// watch states its condition's figures and credits none.
func (s *server) readOtherKind(ctx context.Context, in *WatchInput, now, end time.Time) (watchRead, error) {
	key := principalFrom(ctx).Key
	var r watchRead
	if scope := watchScopes[in.Kind]; !key.Plan.Has(scope) {
		return r, newProblem(ctx, SlugScopeDenied, "A "+in.Kind+" watch needs the scope "+string(scope)+".", 0)
	}
	switch in.Kind {
	case "station":
		return s.readStationWatch(ctx, key, in, now, end)
	case "region":
		return s.readRegionWatch(ctx, key, in)
	case "weather":
		return s.readWeatherWatch(ctx, key, in)
	case "charger":
		return s.readChargerWatch(ctx, key, in)
	}
	return r, nil
}

// readStationWatch reads a station watch: the departures cancelled or at
// least delay_min late, from now until end.
func (s *server) readStationWatch(ctx context.Context, key access.Record, in *WatchInput, now, end time.Time) (watchRead, error) {
	c := in.cond
	var r watchRead
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
	r.meta = s.dataMeta(ctx, b.Families, b.At, b.Partial, partialDetail)
	return r, nil
}

// readRegionWatch reads a region watch: its punctuality and trips. It
// credits no family.
func (s *server) readRegionWatch(ctx context.Context, key access.Record, in *WatchInput) (watchRead, error) {
	c := in.cond
	var r watchRead
	q := transit.DisruptionQuery{Area: in.Key, WindowMin: disruptionDefaultWindowMin, Limit: disruptionDefaultLimit, LateOverMin: c.DelayMin}
	a, err := s.features.Disruptions(ctx, key, q)
	if err != nil {
		return r, s.featureProblem(ctx, err)
	}
	r.data.Condition = map[string]any{"late_share": c.LateShare, "delay_min": c.DelayMin, "min_trips": c.MinTrips}
	env := disruptionEnvelope(s, ctx, a, q)
	r.data.Punctuality = env.Data.Punctuality
	if t := a.Trips; t != nil {
		r.data.Trips = &WatchTrips{Observed: t.Observed, Late: t.Late, LateShare: math.Round(float64(t.Late)/float64(t.Observed)*1000) / 1000, ObservedAt: t.ObservedAt}
	}
	r.meta = env.Meta.DataMeta
	r.meta.Attribution = []Attribution{}
	return r, nil
}

// readWeatherWatch reads a weather watch: the cell's warnings at min_level
// or above. It credits no family.
func (s *server) readWeatherWatch(ctx context.Context, key access.Record, in *WatchInput) (watchRead, error) {
	c := in.cond
	var r watchRead
	a, err := s.features.WeatherCell(ctx, key, in.Key, in.at, *c.MinLevel)
	if err != nil {
		return r, s.featureProblem(ctx, err)
	}
	r.data.Condition = map[string]any{"min_level": *c.MinLevel}
	r.data.Warnings = weatherWarnings(a.Warnings)
	r.meta = s.dataMeta(ctx, nil, a.Meta.At, a.Meta.Partial, partialDetail)
	return r, nil
}

// readChargerWatch reads a charger watch: the site and its points.
func (s *server) readChargerWatch(ctx context.Context, key access.Record, in *WatchInput) (watchRead, error) {
	c := in.cond
	var r watchRead
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
	r.data.Site = &site
	r.meta = s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail)
	return r, nil
}

// errWatchUntil is the refusal of a window more than 24 hours ahead.
func errWatchUntil(until string) error {
	return huma.Error422UnprocessableEntity("until is more than 24 hours ahead", &huma.ErrorDetail{Location: "query.until", Value: until})
}
