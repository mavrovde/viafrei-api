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
	// Station returns the station with this id, held by one of the given
	// families, or transit.ErrNotFound.
	Station(ctx context.Context, id string, families []string) (transit.Station, error)
	// Departures returns the departing calls of q's station planned in q's
	// window, of the given families: calls a passenger can board, of trips
	// that call outside the station after them, by planned instant.
	Departures(ctx context.Context, q transit.DepartureQuery, families []string) ([]transit.Departure, error)
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

// Board is a station's departure board (FR-TRANSIT-002).
type Board struct {
	Answer[transit.Departure]
	Station transit.Station
	// Matched and MatchedAll count the departures of the window of the
	// asked modes and categories, and of every one; Dropped counts what the
	// filter left out, per route type.
	Matched    int
	MatchedAll int
	Dropped    map[int]int
}

// Departures answers "what leaves this station" for key: the departures
// planned in the window, as the platform's departure board shows them
// (transit.Board). An id no permitted family holds is transit.ErrNotFound.
func (s *Service) Departures(ctx context.Context, key access.Record, q transit.DepartureQuery) (Board, error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return Board{}, err
	}
	permitted, partial, err := gate.Answer(access.Answering(reg, "transit"))
	if err != nil {
		return Board{}, err
	}
	st, err := s.repos.Transit.Station(ctx, q.StationID, permitted)
	if err != nil {
		return Board{}, err
	}
	calls, err := s.repos.Transit.Departures(ctx, q, permitted)
	if err != nil {
		return Board{}, err
	}
	b := transit.Board(calls, q)
	contributed := []string{st.Family}
	for _, d := range b.Rows {
		contributed = append(contributed, d.Families...)
	}
	if err := gate.Check(contributed); err != nil {
		return Board{}, err
	}
	fams, err := s.provenanceOf(ctx, contributed, permitted)
	if err != nil {
		return Board{}, err
	}
	return Board{
		Answer:  Answer[transit.Departure]{Items: b.Rows, HasMore: b.Matched > len(b.Rows), Families: fams, Partial: partial, At: s.now()},
		Station: st, Matched: b.Matched, MatchedAll: b.MatchedAll, Dropped: b.Dropped,
	}, nil
}
