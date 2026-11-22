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
	// RoadLamps returns, from one snapshot and by road, what the lamp of
	// each road states over the whole road in both directions, as
	// SituationsOnRoad's page carries it (Messages, Warnings, Sources,
	// Confirmations, States, Coverage); a page lists no situation.
	RoadLamps(ctx context.Context, refs []string, fams roads.Families, now time.Time) (map[string]roads.Page, error)
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
	v, meta, err := listed(ctx, s, key, "roads", "", func(permitted []string) ([]roads.Motorway, []string, error) {
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
// read for every motorway listed in one read (a conditions read per
// motorway cost the list most of its budget, #141).
func (s *Service) motorwayLamps(ctx context.Context, key access.Record, ms []roads.Motorway) error {
	if len(ms) == 0 {
		return nil
	}
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return err
	}
	fams, partial, weatherRead, err := roadFamilies(reg, gate, false, s.set.PlannedWorks)
	if err != nil {
		return err
	}
	now := s.now()
	refs := make([]string, len(ms))
	for i, m := range ms {
		refs[i] = m.Road
	}
	pages, err := s.repos.Catalogue.RoadLamps(ctx, refs, fams, now)
	if err != nil {
		return err
	}
	for i := range ms {
		p := pages[ms[i].Road]
		ms[i].Lamp, _ = roads.LampOf(p.Messages, warningLevels(p), sights(p, fams, partial, weatherRead, now))
	}
	return nil
}
