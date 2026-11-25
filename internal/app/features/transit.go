package features

import (
	"context"
	"slices"
	"time"

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
	TimetableReach(ctx context.Context) (transit.Reach, error)
	// Facilities returns the lifts and escalators of a station, of the
	// given families, each with its state at now.
	Facilities(ctx context.Context, station string, families []string, now time.Time) ([]transit.Facility, error)
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
		// A railway station is named as DB's station register names it
		// ("Frankfurt (Main) Hbf"), where the key may read the register.
		if q.Railway && st.RegisterName != nil && st.RegisterBy != nil && slices.Contains(permitted, *st.RegisterBy) {
			stations[i].Name = *st.RegisterName
			contributed = append(contributed, *st.RegisterBy)
		}
	}
	if err := gate.Check(contributed); err != nil {
		return Answer[transit.Station]{}, err
	}
	// A name that more than one station answers is told apart by the
	// district and Land each lies in, as the platform's name search labels
	// its candidates with their town and Land: the family that placed them
	// is credited.
	// So is a station found in the town the text names: the district
	// placed it there.
	if q.Text != "" {
		for _, st := range stations {
			if st.PlacedBy != nil && (len(stations) > 1 || st.InTown) {
				contributed = append(contributed, *st.PlacedBy)
			}
			// the gazetteer whose Gemeinde named the town
			if st.TownBy != nil {
				contributed = append(contributed, *st.TownBy)
			}
		}
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
	// Reach is how far the loaded timetable reaches.
	Reach transit.Reach
}

// Departures answers "what leaves this station" for key: the departures
// planned in the window, as the platform's departure board shows them
// (transit.Board). An id no permitted family holds is transit.ErrNotFound.
func (s *Service) Departures(ctx context.Context, key access.Record, q transit.DepartureQuery) (Board, error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return Board{}, err
	}
	permitted, partial, err := gate.Answer(boardFamilies(reg))
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
	reach, err := s.repos.Transit.TimetableReach(ctx)
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
		Station: st, Matched: b.Matched, MatchedAll: b.MatchedAll, Dropped: b.Dropped, Reach: reach,
	}, nil
}

// StationFacilities is a station's lifts and escalators (FR-TRANSIT-003).
type StationFacilities struct {
	Answer[transit.Facility]
	Station                   transit.Station
	Active, Inactive, Unknown int
}

// Facilities answers "do the lifts work at this station" for key, as
// transit.ListFacilities lists them. Facilities are read under the
// accessibility domain, the station under transit.
func (s *Service) Facilities(ctx context.Context, key access.Record, q transit.FacilityQuery) (StationFacilities, error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return StationFacilities{}, err
	}
	stations, stPartial, err := gate.Answer(access.Answering(reg, "transit"))
	if err != nil {
		return StationFacilities{}, err
	}
	permitted, partial, err := gate.Answer(access.Answering(reg, "accessibility"))
	if err != nil {
		return StationFacilities{}, err
	}
	st, err := s.repos.Transit.Station(ctx, q.StationID, stations)
	if err != nil {
		return StationFacilities{}, err
	}
	now := s.now()
	all, err := s.repos.Transit.Facilities(ctx, q.StationID, permitted, now)
	if err != nil {
		return StationFacilities{}, err
	}
	l := transit.ListFacilities(all, q)
	contributed := []string{st.Family}
	for _, f := range l.Rows {
		contributed = append(contributed, f.Family)
		if f.Listing != "" {
			contributed = append(contributed, f.Listing)
		}
	}
	if err := gate.Check(contributed); err != nil {
		return StationFacilities{}, err
	}
	fams, err := s.provenanceOf(ctx, contributed, permitted)
	if err != nil {
		return StationFacilities{}, err
	}
	return StationFacilities{
		Answer:  Answer[transit.Facility]{Items: l.Rows, HasMore: l.Active+l.Inactive+l.Unknown > len(l.Rows), Families: fams, Partial: partial || stPartial, At: now},
		Station: st, Active: l.Active, Inactive: l.Inactive, Unknown: l.Unknown,
	}, nil
}

// boardFamilies are the families a departure board answers from: the
// transit families, and the Trip Updates' family where the register holds
// it, whatever domain the register files it under. This is a bridge: the
// source domains send tripupdates to roads through their catch-all pattern,
// and the root fix is tripupdates in the transit pattern, in the database
// (viafrei-database#110); once that ships, this union adds nothing. A board's call carries
// the Trip Updates' family beside the timetable's (transit.TripUpdatesFamily),
// and a call carrying a family the board did not ask is left out: filed under
// another domain, every call with a Trip Updates figure was left out of
// every board. The licence gate still decides whether the key may read it.
func boardFamilies(reg []access.Terms) []string {
	out := access.Answering(reg, "transit")
	for _, t := range reg {
		if t.Family == transit.TripUpdatesFamily && !slices.Contains(out, t.Family) {
			out = append(out, t.Family)
		}
	}
	slices.Sort(out)
	return out
}
