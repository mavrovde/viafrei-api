package features

import (
	"context"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// Transit is the port to the public-transport store.
type Transit interface {
	// Stations returns up to q.Limit stations of the given families that
	// answer q, and whether more do. A name search (q.Text) keeps the
	// stations whose folded name contains the folded text, those that start
	// with it first; near a position (q.At) keeps those within the radius,
	// nearest first; with both, a name search within the radius, nearest
	// first. Ties go by name, then id.
	Stations(ctx context.Context, q transit.Query, families []string) ([]transit.Station, bool, error)
}

// Stations answers "which station is this" and "which stations are near
// here" for key (FR-TRANSIT-001): the station search and the suggestions a
// client shows as the user types.
func (s *Service) Stations(ctx context.Context, key access.Record, q transit.Query) (Answer[transit.Station], error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return Answer[transit.Station]{}, err
	}
	permitted, partial, err := gate.Answer(access.Answering(reg, "transit"))
	if err != nil {
		return Answer[transit.Station]{}, err
	}
	stations, more, err := s.repos.Transit.Stations(ctx, q, permitted)
	if err != nil {
		return Answer[transit.Station]{}, err
	}
	contributed := make([]string, len(stations))
	for i, st := range stations {
		contributed[i] = st.Family
	}
	if err := gate.Check(contributed); err != nil {
		return Answer[transit.Station]{}, err
	}
	fams, err := s.provenanceOf(ctx, contributed, permitted)
	if err != nil {
		return Answer[transit.Station]{}, err
	}
	return Answer[transit.Station]{Items: stations, HasMore: more, Families: fams, Partial: partial, At: s.now()}, nil
}
