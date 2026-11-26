package features

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// StationPageQuery asks for the page of the station StationID names (a stop
// place id, DB's register number or an EVA number) with Departures rows.
type StationPageQuery struct {
	StationID  string
	Departures int
}

// PageSection is one section's instant (nil when its read had no data) and
// the families it was read from.
type PageSection struct {
	At      *time.Time
	Sources []string
}

// StationPage is a station's page as today's describe_station builds it,
// section by section in the page's order.
type StationPage struct {
	Meta   Answer[struct{}]
	Record transit.StationRecord
	// Sections holds each section's instant and sources by section id.
	Sections   map[string]PageSection
	Ampel      transit.RailAmpel
	StepFree   []transit.StepFreePlatform
	Blocked    []transit.StepFreePlatform
	Lifts      []transit.Facility
	Platforms  []transit.StationPlatforms
	Departures []transit.Departure
	Alerts     []transit.StationAlert
}

// PageSections are the sections of a station page, in its order.
var PageSections = []string{"header", "rail_ampel", "step_free_now", "lifts", "platforms", "departures", "service_alerts", "parking_charging"}

var feedPrefix = regexp.MustCompile(`^[0-9]+:`)

// pageLiftLimit lists every unit of a station: the largest lists a few
// dozen.
const pageLiftLimit = 500

// pageBoardLimit takes every departure of the lamp's hour.
const pageBoardLimit = 1000

// StationPage answers "tell me about this station" for key: the register's
// record, then each section from the reads the page takes. A section whose
// families the key may not read is empty, not dropped.
func (s *Service) StationPage(ctx context.Context, key access.Record, q StationPageQuery) (StationPage, error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return StationPage{}, err
	}
	stations, partial, err := gate.Answer(access.Answering(reg, "transit"))
	if err != nil {
		return StationPage{}, err
	}
	rec, err := s.repos.Transit.StationRecord(ctx, q.StationID, stations)
	if err != nil {
		return StationPage{}, err
	}
	now := s.now()
	out := StationPage{Record: rec, Sections: map[string]PageSection{}}
	// The register's row stands at its last sighting, never after the answer.
	seen := rec.SeenAt
	if seen.After(now) {
		seen = now
	}
	out.Sections["header"] = PageSection{At: &seen, Sources: []string{rec.Family}}
	flags := []string{}
	if len(rec.Flags) > 0 {
		flags = []string{rec.Family}
	}
	out.Sections["parking_charging"] = PageSection{At: &seen, Sources: flags}
	contributed := []string{rec.Family}
	var (
		board  Board
		read   = rec.StopPlaceID != nil
		facil  StationFacilities
		hasFac bool
	)
	if read {
		board, err = s.Departures(ctx, key, transit.DepartureQuery{StationID: *rec.StopPlaceID, From: now, Window: time.Duration(transit.RailWindowMin) * time.Minute, Limit: pageBoardLimit})
		read = err == nil
		if err != nil && !errors.Is(err, access.ErrNonePermitted) && !errors.Is(err, transit.ErrNotFound) {
			return StationPage{}, err
		}
		facil, err = s.Facilities(ctx, key, transit.FacilityQuery{StationID: *rec.StopPlaceID, Limit: pageLiftLimit})
		hasFac = err == nil
		if err != nil && !errors.Is(err, access.ErrNonePermitted) && !errors.Is(err, transit.ErrNotFound) {
			return StationPage{}, err
		}
	}
	// DB's board of the station speaks for the trains it lists, as the
	// platform's station page reads it, where the key reads DB Timetables.
	if rec.EVANo != nil && slices.Contains(stations, transit.TrainBoardFamily) {
		db, _, err := s.freshTrainBoard(ctx, *rec.EVANo, now)
		if err != nil {
			return StationPage{}, err
		}
		var spoke bool
		if board.Items, spoke = transit.WithTrainBoard(board.Items, db, now, now.Add(time.Duration(transit.RailWindowMin)*time.Minute)); spoke {
			if !read {
				board.At, board.Station.Family = now, rec.Family
			}
			read = true
		}
	}
	boardFams := []string{}
	if read {
		boardFams = append(boardFams, board.Station.Family)
		out.Departures = board.Items[:min(q.Departures, len(board.Items))]
		for _, d := range board.Items {
			boardFams = appendNew(boardFams, d.Families...)
		}
		partial = partial || board.Partial
		at := board.At
		out.Sections["departures"] = PageSection{At: &at, Sources: boardFams}
	} else {
		out.Sections["departures"] = PageSection{Sources: []string{}}
	}
	alerts, alertFams, alertsAt, err := s.stationAlerts(ctx, stations, rec, board, now)
	if err != nil {
		return StationPage{}, err
	}
	out.Alerts = alerts
	out.Sections["service_alerts"] = PageSection{At: alertsAt, Sources: alertFams}
	var lines [][]string
	if alertsAt != nil {
		lines = make([][]string, len(alerts))
		for i, a := range alerts {
			lines[i] = a.Lines
		}
	}
	out.Ampel = transit.RailAmpelOf(now, board.Items, read, lines)
	if read {
		at := board.At
		out.Sections["rail_ampel"] = PageSection{At: &at, Sources: appendNew(slices.Clone(boardFams), alertFams...)}
	} else {
		out.Sections["rail_ampel"] = PageSection{Sources: []string{}}
	}
	out.Lifts, out.StepFree, out.Blocked = []transit.Facility{}, []transit.StepFreePlatform{}, []transit.StepFreePlatform{}
	out.Platforms = []transit.StationPlatforms{}
	for _, id := range []string{"lifts", "platforms", "step_free_now"} {
		out.Sections[id] = PageSection{Sources: []string{}}
	}
	if hasFac {
		partial = partial || facil.Partial
		out.Lifts, out.Platforms = facil.Items, facil.Platforms
		out.StepFree, out.Blocked = transit.StepFreeNow(facil.Platforms, facil.Items)
		liftFams, platFams := []string{}, []string{}
		for _, f := range facil.Items {
			liftFams = appendNew(liftFams, f.Family)
			if f.Listing != "" {
				liftFams = appendNew(liftFams, f.Listing)
			}
		}
		at := facil.At
		if len(facil.Items) > 0 {
			out.Sections["lifts"] = PageSection{At: &at, Sources: liftFams}
		}
		if len(facil.Platforms) > 0 {
			for _, p := range facil.Platforms {
				platFams = appendNew(platFams, p.Family)
			}
			out.Sections["platforms"] = PageSection{At: facil.PlatformsAt, Sources: platFams}
		}
		step := slices.Clone(platFams)
		if len(out.Blocked) > 0 || slices.ContainsFunc(out.StepFree, func(p transit.StepFreePlatform) bool { return p.Lift != "none" }) {
			step = appendNew(step, liftFams...)
		}
		if len(platFams) == 0 {
			step = appendNew(step, rec.Family)
		}
		out.Sections["step_free_now"] = PageSection{At: &at, Sources: step}
	}
	for _, sec := range out.Sections {
		contributed = append(contributed, sec.Sources...)
	}
	if err := gate.Check(contributed); err != nil {
		return StationPage{}, err
	}
	fams, err := s.creditsOf(ctx, contributed)
	if err != nil {
		return StationPage{}, err
	}
	out.Meta = Answer[struct{}]{Families: fams, Partial: partial, At: now}
	return out, nil
}

// stationAlerts reads the alerts in force near the station, as the page's
// second round asks them: its stops and DHID, the board's lines within its
// Kreis.
func (s *Service) stationAlerts(ctx context.Context, permitted []string, rec transit.StationRecord, board Board, now time.Time) ([]transit.StationAlert, []string, *time.Time, error) {
	var err error
	q := transit.StationAlertsQuery{Now: now, IDs: []string{}, Lines: []string{}, Kreis: []string{}}
	for _, id := range board.Station.StopIDs {
		q.IDs = appendNew(q.IDs, feedPrefix.ReplaceAllString(id, ""))
	}
	for _, d := range board.Items {
		if d.Line != nil {
			q.Lines = appendNew(q.Lines, *d.Line)
		}
	}
	slices.Sort(q.IDs)
	slices.Sort(q.Lines)
	if m := rec.Municipality; m != nil && len(*m) >= 5 {
		q.Kreis = []string{(*m)[:5]}
	}
	rows := []transit.StationAlertRow{}
	if len(q.IDs) > 0 || len(q.Lines) > 0 {
		if rows, err = s.repos.Transit.StationAlerts(ctx, q, permitted); err != nil {
			return nil, nil, nil, err
		}
	}
	alerts := transit.StationAlerts(rows, q.IDs, q.Lines)
	fams := []string{}
	for _, a := range alerts {
		fams = appendNew(fams, a.CarriedBy...)
	}
	return alerts, fams, &now, nil
}

// appendNew appends each of vs not yet in out.
func appendNew(out []string, vs ...string) []string {
	for _, v := range vs {
		if !slices.Contains(out, v) {
			out = append(out, v)
		}
	}
	return out
}
