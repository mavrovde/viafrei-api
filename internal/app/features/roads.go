package features

import (
	"context"
	"slices"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// Roads is the port to the road-conditions store.
type Roads interface {
	// Road returns the road with a canonical reference, or
	// roads.ErrUnknownRoad.
	Road(ctx context.Context, ref string) (roads.Road, error)
	// SituationsOnRoad returns up to q.Limit active situations on the road
	// of the given families, in the order of their position along it, the
	// lamp's signals of every situation in the scope (the road and
	// direction, whatever the filters keep), and whether there are more.
	SituationsOnRoad(ctx context.Context, q roads.CorridorQuery, families []string, now time.Time) ([]roads.Situation, []roads.Signal, bool, error)
	// SituationsNear returns up to q.Limit active situations within the
	// radius of the given families, by severity then distance, the lamp's
	// signals of every situation in the radius, whatever the filters keep,
	// and whether there are more.
	SituationsNear(ctx context.Context, q roads.AreaQuery, families []string, now time.Time) ([]roads.Situation, []roads.Signal, bool, error)
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

// conditions reads a page of situations of the permitted roads families,
// post-checks and attributes it, and computes the lamp over every situation
// in the scope, whether the answer is partial and whether any permitted
// family is missing or stale.
func (s *Service) conditions(ctx context.Context, key access.Record, read func(families []string, now time.Time) ([]roads.Situation, []roads.Signal, bool, error)) (Conditions, error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return Conditions{}, err
	}
	permitted, partial, err := gate.Answer(access.Answering(reg, "roads"))
	if err != nil {
		return Conditions{}, err
	}
	now := s.now()
	sits, signals, more, err := read(permitted, now)
	if err != nil {
		return Conditions{}, err
	}
	contributed := make([]string, len(sits))
	for i, v := range sits {
		contributed[i] = v.Family
	}
	if err := gate.Check(contributed); err != nil {
		return Conditions{}, err
	}
	// Every permitted family is read once: all of them for the lamp's
	// freshness, those that contributed (or all, when none did) for the
	// attribution. The post-check above keeps contributed inside permitted.
	consulted := slices.Clone(permitted)
	slices.Sort(consulted)
	consulted = slices.Compact(consulted)
	all, err := s.repos.Register.Provenance(ctx, consulted, s.set.Language)
	if err != nil {
		return Conditions{}, err
	}
	// A permitted family with no provenance row is missing: limited sight.
	stale, fams := len(all) < len(consulted), []provenance.Family{}
	for _, f := range all {
		stale = stale || f.Assess(now).Stale
		if len(contributed) == 0 || slices.Contains(contributed, f.Source.Family) {
			fams = append(fams, f)
		}
	}
	lamp, reason := roads.LampOf(signals, partial, stale)
	return Conditions{
		Answer: Answer[roads.Situation]{Items: sits, HasMore: more, Families: fams, Partial: partial, At: now},
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
	c, err := s.conditions(ctx, key, func(fams []string, now time.Time) ([]roads.Situation, []roads.Signal, bool, error) {
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
	return s.conditions(ctx, key, func(fams []string, now time.Time) ([]roads.Situation, []roads.Signal, bool, error) {
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
