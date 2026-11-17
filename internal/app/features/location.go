package features

import (
	"context"
	"slices"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/rules"
)

// Locations is the reverse-description port.
type Locations interface {
	// Location returns what of the families lies at q.At.
	Location(ctx context.Context, q places.LocationQuery, families []string) (places.Location, error)
}

// LocationAnswer is the answer to "where is this position".
type LocationAnswer struct {
	places.Location
	// Zone is the low-emission-zone verdict at the position, as the
	// platform's description of a position gives it; nil when the key may
	// read no zone source.
	Zone *rules.Verdict
	Meta Answer[struct{}]
}

// Location answers "where is this position" for key as the platform's
// description of a position does: independent facts, each nil when none is
// within its reach, and the low-emission zone the position lies in. The
// register and gate are read once for both.
func (s *Service) Location(ctx context.Context, key access.Record, q places.LocationQuery) (LocationAnswer, error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return LocationAnswer{}, err
	}
	permitted, partial, err := gate.Answer(access.Answering(reg, "places"))
	if err != nil {
		return LocationAnswer{}, err
	}
	v, err := s.repos.Locations.Location(ctx, q, permitted)
	if err != nil {
		return LocationAnswer{}, err
	}
	var contributed []string
	if v.Address != nil {
		contributed = append(contributed, v.Address.Family)
	}
	for _, p := range []*places.Place{v.Municipality, v.District, v.State} {
		if p != nil {
			contributed = append(contributed, p.Family)
		}
	}
	if err := gate.Check(contributed); err != nil {
		return LocationAnswer{}, err
	}
	zone, zoneFams, err := s.zoneAt(ctx, reg, gate, q)
	if err != nil {
		return LocationAnswer{}, err
	}
	// Without a fact the attribution names the places families consulted
	// (ENV-003); a zone verdict's families stand beside them.
	fams := contributed
	if len(fams) == 0 {
		fams = slices.Clone(permitted)
	}
	fams = append(fams, zoneFams...)
	slices.Sort(fams)
	prov, err := s.repos.Register.Provenance(ctx, slices.Compact(fams), s.set.Language)
	if err != nil {
		return LocationAnswer{}, err
	}
	return LocationAnswer{Location: v, Zone: zone, Meta: Answer[struct{}]{Families: prov, Partial: partial, At: s.now()}}, nil
}

// zoneAt is the zone verdict at q.At from the zone sources (domain roads)
// the key may read, with the families of the rows the verdict names: an
// unknown verdict names none, so it adds no attribution. A key that may read
// no zone source gets no verdict, not an error: the zone is one fact of the
// description, not its subject.
func (s *Service) zoneAt(ctx context.Context, reg []access.Terms, gate access.Gate, q places.LocationQuery) (*rules.Verdict, []string, error) {
	permitted, _, err := gate.Answer(access.Answering(reg, "roads"))
	if err != nil {
		// ErrNonePermitted or ErrTermsNotAccepted, the only refusals.
		return nil, nil, nil
	}
	at := q.At
	zs, err := s.repos.Rules.Zones(ctx, rules.ZoneQuery{At: &at}, permitted, s.now())
	if err != nil {
		return nil, nil, err
	}
	vd := rules.VerdictOf(zs)
	var contributed []string
	for _, z := range []*rules.Zone{vd.Zone, vd.Transfer, vd.ExemptRoad} {
		if z != nil {
			contributed = append(contributed, z.Family)
		}
	}
	if err := gate.Check(contributed); err != nil {
		return nil, nil, err
	}
	return &vd, contributed, nil
}
