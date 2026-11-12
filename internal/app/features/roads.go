package features

import (
	"context"
	"slices"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/ampel"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// Roads is the port to the road-conditions store.
type Roads interface {
	// Road returns the road with a canonical reference, or
	// roads.ErrUnknownRoad.
	Road(ctx context.Context, ref string) (roads.Road, error)
	// SituationsOnRoad returns, from one snapshot, up to q.Limit active
	// situations on the road of the roads families, in the order of their
	// position along it, and whether there are more; the lamp's view of
	// every situation in the scope (the road and direction, whatever the
	// filters keep); the weather warnings of the weather families in force
	// in an area the road crosses (none read when no weather family is
	// given); the freshness of every family given; the states of the road's
	// junctions, the roads families given that cover their traffic messages,
	// and the newest confirmation of those and of the weather families.
	SituationsOnRoad(ctx context.Context, q roads.CorridorQuery, fams roads.Families, now time.Time) (roads.Page, error)
	// SituationsNear is SituationsOnRoad for the radius of a position, by
	// severity then distance; the warnings are those of an area within the
	// radius, and the states those of the junctions within it, else of the
	// junction nearest the position.
	SituationsNear(ctx context.Context, q roads.AreaQuery, fams roads.Families, now time.Time) (roads.Page, error)
	// Situation returns one situation of the given families that is active
	// or ended after endedSince, or roads.ErrUnknownSituation.
	Situation(ctx context.Context, id string, families []string, endedSince time.Time) (roads.Situation, error)
}

// Conditions is the answer about the situations of a road or an area: the
// page of situations and the lamp over every situation that answers.
type Conditions struct {
	Answer[roads.Situation]
	// Road is the road asked about; nil for an area.
	Road   *roads.Road
	Lamp   roads.Lamp
	Reason *roads.Reason
}

// sights are the sight of every group the lamp reads (ampelOf's sight):
// one entry per permitted roads family (missing without a freshness row or
// data, stale past its threshold); one missing entry when a roads family
// the key may not read was left out (partial); the weather warnings' sight,
// fresh only when the read answered (every weather family permitted and
// read) and, for each weather family, a warning is held or the family is
// current by its newest confirmation, missing otherwise; and the traffic
// messages' sight of every state in scope, judged over the roads families
// read that cover it (one missing entry when no state is in scope).
func sights(p roads.Page, fams roads.Families, partial, weatherRead bool, now time.Time) []ampel.SightEntry {
	fresh := map[string]ampel.Sight{}
	for _, f := range p.Sources {
		fresh[f.Source.Family] = roads.SourceSight(f.Assess(now))
	}
	var out []ampel.SightEntry
	for _, f := range fams.Roads {
		s, ok := fresh[f]
		if !ok {
			s = ampel.SightMissing
		}
		out = append(out, ampel.SightEntry{Group: roads.GroupRoads, State: s})
	}
	if partial {
		out = append(out, ampel.SightEntry{Group: roads.GroupRoads, State: ampel.SightMissing})
	}
	weather := ampel.SightMissing
	if len(fams.Weather) > 0 {
		weather = ampel.SightFresh
	}
	for _, f := range fams.Weather {
		var c *ampel.Confirmation
		if v, ok := p.Confirmations[f]; ok {
			c = &v
		}
		if s := ampel.WarningSightOf(weatherRead, len(p.Warnings), c, now); s != ampel.SightFresh {
			weather = s
		}
	}
	out = append(out, ampel.SightEntry{Group: ampel.GroupWeather, State: weather})
	return append(out, ampel.MessageSight(p.States, p.Coverage, p.Confirmations, now)...)
}

// conditions reads a page of situations of the permitted roads families and
// the weather warnings of the permitted weather families in one snapshot,
// post-checks and attributes it, and computes the lamp over every situation
// and warning in the scope with the sight of every group read.
func (s *Service) conditions(ctx context.Context, key access.Record, read func(fams roads.Families, now time.Time) (roads.Page, error)) (Conditions, error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return Conditions{}, err
	}
	permitted, partial, err := gate.Answer(access.Answering(reg, "roads"))
	if err != nil {
		return Conditions{}, err
	}
	// A key without the weather scope, or that may read no weather family,
	// reads no warning: the weather group is then missing, never an error of
	// the roads question.
	var weather []string
	weatherRead := false
	if key.Plan.Has("weather") {
		w, wpartial, werr := gate.Answer(access.Answering(reg, "weather"))
		weather, weatherRead = w, werr == nil && !wpartial
	}
	fams := roads.Families{Roads: permitted, Weather: weather}
	now := s.now()
	p, err := read(fams, now)
	if err != nil {
		return Conditions{}, err
	}
	contributed := make([]string, 0, len(p.Items)+len(p.Warnings))
	for _, v := range p.Items {
		contributed = append(contributed, v.Family)
	}
	for _, w := range p.Warnings {
		contributed = append(contributed, w.Family)
	}
	if err := gate.Check(contributed); err != nil {
		return Conditions{}, err
	}
	// The attribution: the families that contributed a situation or a
	// warning, or every permitted roads family when none did.
	consulted := slices.Clone(contributed)
	if len(consulted) == 0 {
		consulted = slices.Clone(permitted)
	}
	slices.Sort(consulted)
	consulted = slices.Compact(consulted)
	attributed, err := s.repos.Register.Provenance(ctx, consulted, s.set.Language)
	if err != nil {
		return Conditions{}, err
	}
	levels := make([]int, len(p.Warnings))
	for i, w := range p.Warnings {
		levels[i] = w.Level
	}
	lamp, reason := roads.LampOf(p.Messages, levels, sights(p, fams, partial, weatherRead, now))
	return Conditions{
		Answer: Answer[roads.Situation]{Items: p.Items, HasMore: p.HasMore, Families: attributed, Partial: partial, At: now},
		Lamp:   lamp, Reason: reason,
	}, nil
}

// MotorwayConditions answers "what is happening on this road" for key
// (FR-ROAD-003, FR-ROAD-004). The road is resolved first, so an unknown road
// is roads.ErrUnknownRoad whatever the key may read.
func (s *Service) MotorwayConditions(ctx context.Context, key access.Record, q roads.CorridorQuery) (Conditions, error) {
	road, err := s.repos.Roads.Road(ctx, q.Road)
	if err != nil {
		return Conditions{}, err
	}
	q.Road = road.Ref
	c, err := s.conditions(ctx, key, func(fams roads.Families, now time.Time) (roads.Page, error) {
		return s.repos.Roads.SituationsOnRoad(ctx, q, fams, now)
	})
	if err != nil {
		return Conditions{}, err
	}
	c.Road = &road
	return c, nil
}

// RoadConditions answers "what is happening on the roads near here" for key
// (FR-ROAD-006).
func (s *Service) RoadConditions(ctx context.Context, key access.Record, q roads.AreaQuery) (Conditions, error) {
	return s.conditions(ctx, key, func(fams roads.Families, now time.Time) (roads.Page, error) {
		return s.repos.Roads.SituationsNear(ctx, q, fams, now)
	})
}

// RoadSituation answers "what is this situation" for key: one situation of
// a permitted roads family, active or ended less than
// Settings.EndedRetention ago; one ended longer ago is not found.
func (s *Service) RoadSituation(ctx context.Context, key access.Record, id string) (Answer[roads.Situation], error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return Answer[roads.Situation]{}, err
	}
	// One situation is either answered or not found: a family the key may
	// not read makes it unknown, never a partial answer.
	permitted, _, err := gate.Answer(access.Answering(reg, "roads"))
	if err != nil {
		return Answer[roads.Situation]{}, err
	}
	now := s.now()
	sit, err := s.repos.Roads.Situation(ctx, id, permitted, now.Add(-s.set.EndedRetention))
	if err != nil {
		return Answer[roads.Situation]{}, err
	}
	if err := gate.Check([]string{sit.Family}); err != nil {
		return Answer[roads.Situation]{}, err
	}
	fams, err := s.repos.Register.Provenance(ctx, []string{sit.Family}, s.set.Language)
	if err != nil {
		return Answer[roads.Situation]{}, err
	}
	return Answer[roads.Situation]{Items: []roads.Situation{sit}, Families: fams, At: now}, nil
}
