package features

import (
	"context"
	"slices"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// Disruptions is the transit-disruption port.
type Disruptions interface {
	// Disruptions returns the service alerts in force of q's area and line,
	// of the given families, at most q.Limit with their count before it,
	// and the area's punctuality when there is an area.
	Disruptions(ctx context.Context, q transit.DisruptionQuery, families []string) (transit.Disruptions, error)
}

// DisruptionAnswer is the answer to a disruption question.
type DisruptionAnswer struct {
	transit.Disruptions
	Meta Answer[struct{}]
}

// Disruptions answers "is public transport disrupted here" for key as the
// platform's disruption check answers it, at the answer's instant.
func (s *Service) Disruptions(ctx context.Context, key access.Record, q transit.DisruptionQuery) (DisruptionAnswer, error) {
	q.Now = s.now()
	// As the platform's check credits: each alert listed, and the realtime
	// source read for the area's punctuality, "no reports" included; an
	// answer that listed no alert and read no punctuality credits nothing.
	v, meta, err := listedOwn(ctx, s, key, "transit", func(permitted []string) (transit.Disruptions, []string, error) {
		v, err := s.repos.Disruptions.Disruptions(ctx, q, permitted)
		contributed := slices.Clone(v.PunctualityRead)
		for _, a := range v.Alerts {
			contributed = append(contributed, a.Family)
		}
		if v.Punctuality != nil {
			contributed = append(contributed, v.Punctuality.Family)
		}
		return v, contributed, err
	})
	return DisruptionAnswer{Disruptions: v, Meta: meta}, err
}
