package features

import (
	"context"
	"slices"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/national"
)

// National is the port of the national picture.
type National interface {
	// National returns one domain's figures from the given families at now;
	// fuelWindow is how young a price must be to count.
	National(ctx context.Context, domain string, families []string, now time.Time, fuelWindow time.Duration) ([]national.Row, error)
}

// NationalAnswer is the national picture.
type NationalAnswer struct {
	Rows []national.Row
	Meta Answer[struct{}]
}

// National answers the national picture for key (the site's home tiles, its
// live map and its national pages): every domain of national.Domains the key
// has the scope of, each from the families it may read there. A domain whose
// families are all withheld is left out and the answer is partial; one the
// key has no scope for is not asked.
func (s *Service) National(ctx context.Context, key access.Record, fuelWindow time.Duration) (NationalAnswer, error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return NationalAnswer{}, err
	}
	now := s.now()
	var rows []national.Row
	var consulted []string
	partial, asked := false, false
	for _, d := range national.Domains {
		if !key.Plan.Has(access.Scope(d)) {
			continue
		}
		asked = true
		permitted, part, err := gate.Answer(access.Answering(reg, d))
		if err != nil {
			partial = true
			continue
		}
		partial = partial || part
		consulted = append(consulted, permitted...)
		got, err := s.repos.National.National(ctx, d, permitted, now, fuelWindow)
		if err != nil {
			return NationalAnswer{}, err
		}
		if err := gate.Check(national.Families(got)); err != nil {
			return NationalAnswer{}, err
		}
		rows = append(rows, got...)
	}
	if !asked || len(consulted) == 0 {
		return NationalAnswer{}, access.ErrNonePermitted
	}
	fams, err := s.provenanceOf(ctx, national.Families(rows), consulted)
	if err != nil {
		return NationalAnswer{}, err
	}
	slices.SortStableFunc(rows, func(a, b national.Row) int {
		return slices.Index(sections, a.Section) - slices.Index(sections, b.Section)
	})
	return NationalAnswer{Rows: rows, Meta: Answer[struct{}]{Families: fams, Partial: partial, At: now}}, nil
}

// sections is the answer order of the sections.
var sections = []string{national.SectionSituations, national.SectionStates, national.SectionFuel, national.SectionCharging,
	national.SectionParking, national.SectionFacilities, national.SectionPunctuality, national.SectionWarnings}
