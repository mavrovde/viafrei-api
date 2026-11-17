package features

import (
	"context"
	"slices"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// Catalogue is the port of the motorway catalogue.
type Catalogue interface {
	// Motorways returns the motorways q keeps, with the situations of
	// families counted, and the families of the situations in force at now
	// on any road of the register (the families its counts state).
	Motorways(ctx context.Context, q roads.CatalogueQuery, families []string, now time.Time) ([]roads.Motorway, []string, error)
}

// MotorwaysAnswer is the motorway catalogue answer.
type MotorwaysAnswer struct {
	Motorways []roads.Motorway
	Meta      Answer[struct{}]
}

// Motorways answers the motorway catalogue for key, from the roads families
// it may read, each motorway with the lamp its own conditions answer states.
func (s *Service) Motorways(ctx context.Context, key access.Record, q roads.CatalogueQuery) (MotorwaysAnswer, error) {
	var counted []string
	v, meta, err := listed(ctx, s, key, "roads", func(permitted []string) ([]roads.Motorway, []string, error) {
		// An answer credits what the site's motorway index credits: the
		// families whose situations its counts state (on any motorway, a
		// count of none included) and the register of the motorways it
		// lists, when the key may read it. With no situation anywhere, the
		// families consulted are named, and none of their conditions is
		// carried (the platform's rule for an answer that states no row).
		ms, c, err := s.repos.Catalogue.Motorways(ctx, q, permitted, s.now())
		counted = c
		return ms, motorwayFamilies(ms, c, permitted), err
	})
	if err != nil {
		return MotorwaysAnswer{}, err
	}
	if len(counted) == 0 {
		meta.Families = provenance.Unconditioned(meta.Families)
	}
	if err := s.motorwayLamps(ctx, key, v); err != nil {
		return MotorwaysAnswer{}, err
	}
	return MotorwaysAnswer{Motorways: v, Meta: meta}, nil
}

// motorwayFamilies are the families counted and the registers of the listed
// motorways (the road's and its junctions') the key may read, each once,
// sorted; empty when none.
func motorwayFamilies(ms []roads.Motorway, counted, permitted []string) []string {
	out := slices.Clone(counted)
	for _, m := range ms {
		for _, f := range append([]string{m.Family}, m.Registers...) {
			if f != "" && slices.Contains(permitted, f) {
				out = append(out, f)
			}
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// motorwayLamps sets each motorway's lamp as MotorwayConditions states it
// for the whole road in both directions: the same families, sight and rule,
// read once per motorway.
func (s *Service) motorwayLamps(ctx context.Context, key access.Record, ms []roads.Motorway) error {
	if len(ms) == 0 {
		return nil
	}
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return err
	}
	fams, partial, weatherRead, err := roadFamilies(reg, gate, false, false, s.set.PlannedWorks)
	if err != nil {
		return err
	}
	now := s.now()
	for i := range ms {
		p, err := s.repos.Roads.SituationsOnRoad(ctx, roads.CorridorQuery{Road: ms[i].Road, Direction: roads.DirectionBoth,
			Filter: roads.Filter{MinSeverity: roads.SeverityInfo}, Limit: 1}, fams, now)
		if err != nil {
			return err
		}
		ms[i].Lamp, _ = roads.LampOf(p.Messages, warningLevels(p), sights(p, fams, partial, weatherRead, now))
	}
	return nil
}
