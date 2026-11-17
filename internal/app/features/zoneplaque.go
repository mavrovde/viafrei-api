package features

import (
	"context"
	"regexp"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/rules"
)

// ZonePlaques is the part of the Rules port that reads the curated zone
// list and the Gemeinde that finds its entry for a point.
type ZonePlaques interface {
	// ZoneEntries reads the list entries that speak for any of codes.
	ZoneEntries(ctx context.Context, codes []string) ([]rules.ListEntry, error)
	// GemeindeAt reads the nearest municipality of the families within
	// reachM metres of at, nil when none.
	GemeindeAt(ctx context.Context, at geo.Point, reachM int, families []string) (*places.Place, error)
}

// gemeindeKey is an eight-digit Gemeinde key; a Kreis or Land key finds no
// list entry.
var gemeindeKey = regexp.MustCompile(`^[0-9]{8}$`)

// zoneAnswer states what the platform's zone answer says beside v
// (rules.AnswerOf): the plaque from the list entry of the deciding source's
// Gemeinde or of the point's. The point's Gemeinde is gemeinde when the
// caller knows it, else, with a position at, the nearest municipality the
// key may read within rules.GemeindeReachM. The Gemeinde's family is
// returned when it found the entry, as the platform credits it then.
func (s *Service) zoneAnswer(ctx context.Context, reg []access.Terms, gate access.Gate, v rules.Verdict, at *geo.Point, gemeinde *places.Place) (rules.ZoneAnswer, []string, error) {
	d := v.Decider()
	if d == nil {
		return rules.AnswerOf(v, nil, nil), nil, nil
	}
	if gemeinde == nil && at != nil {
		if fams, _, err := gate.Answer(access.Answering(reg, "places")); err == nil {
			g, err := s.repos.Rules.GemeindeAt(ctx, *at, rules.GemeindeReachM, fams)
			if err != nil {
				return rules.ZoneAnswer{}, nil, err
			}
			gemeinde = g
		}
	}
	var point *string
	if gemeinde != nil && gemeinde.OfficialKey != nil && gemeindeKey.MatchString(*gemeinde.OfficialKey) {
		point = gemeinde.OfficialKey
	}
	var codes []string
	for _, c := range []*string{d.PlaceCode, point} {
		if c != nil {
			codes = append(codes, *c)
		}
	}
	var entries []rules.ListEntry
	if len(codes) > 0 {
		var err error
		if entries, err = s.repos.Rules.ZoneEntries(ctx, codes); err != nil {
			return rules.ZoneAnswer{}, nil, err
		}
	}
	a := rules.AnswerOf(v, point, entries)
	if a.FoundByPoint {
		return a, []string{gemeinde.Family}, nil
	}
	return a, nil, nil
}
