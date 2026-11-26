package httpapi

import (
	"cmp"
	"context"
	"math"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// The stops around a position, as today's departures around a place list
// them: within 5 km, eight stops, four departures each, the next hour.
const (
	stopBoardsDefaultStops   = 8
	stopBoardsDefaultPerStop = 4
)

// StopBoardsInput is the input of listStopBoards.
type StopBoardsInput struct {
	AtPosition
	RadiusM     int       `query:"radius_m" minimum:"100" maximum:"5000" doc:"The radius in metres the stops lie within (100 to 5000); default 5000."`
	Limit       int       `query:"limit" minimum:"1" maximum:"20" doc:"Most stops to list; default 8."`
	PerStop     int       `query:"per_stop" minimum:"1" maximum:"15" doc:"Most departures per stop, earliest first; default 4."`
	From        time.Time `query:"from" doc:"Start of the window (RFC 3339 with an offset); default: now."`
	DurationMin int       `query:"duration_min" minimum:"5" maximum:"1440" doc:"Length of the window in minutes; default 60."`
	Modes       []string  `query:"modes" maxItems:"7" doc:"Keep only departures of these modes (comma-separated): rail, sbahn, subway, tram, bus, ferry, other. Default: every mode. The stops listed are the same."`
	modes       []transit.Mode
}

// Resolve reads the position and the modes (huma calls it after the schema
// checks).
func (in *StopBoardsInput) Resolve(ctx huma.Context) []error {
	if errs := in.AtPosition.Resolve(ctx); errs != nil {
		return errs
	}
	d := DeparturesInput{Modes: in.Modes}
	errs := d.Resolve(ctx)
	in.modes = d.modes
	return errs
}

// StopBoard is one stop listed around the position with its departures.
type StopBoard struct {
	ID         string      `json:"id" doc:"The stop family's id, as listStations answers a station's."`
	Name       string      `json:"name"`
	StopID     string      `json:"stopId" doc:"The stop id as the timetable spells it, without the prefix of the timetable it is read from: listStationDepartures takes id, and the timetable stop id is what a passenger information system prints."`
	DistanceM  int         `json:"distanceM"`
	Location   Position    `json:"location"`
	Departures []Departure `json:"departures" doc:"Its departures as its board (listStationDepartures) shows them, at most per_stop; empty when none leaves in the window."`
}

// StopBoardsWindow is the window the boards cover.
type StopBoardsWindow struct {
	From       time.Time `json:"from"`
	FromLocal  string    `json:"fromLocal" format:"date-time"`
	Until      time.Time `json:"until" doc:"End of the window (excluded)."`
	UntilLocal string    `json:"untilLocal" format:"date-time"`
}

// StopBoardsData is the data of listStopBoards.
type StopBoardsData struct {
	Location Position         `json:"location" doc:"The position asked."`
	Window   StopBoardsWindow `json:"window"`
	Stops    []StopBoard      `json:"stops" doc:"The stops, as today's departures around a place pick them: the nearest railway or S-Bahn station first wherever it lies in the radius, then the nearest stops by distance; a stop filed twice under one name within 300 m listed once. Empty when no stop lies in the radius."`
}

// StopBoardsEnvelope is the enveloped answer of listStopBoards.
type StopBoardsEnvelope struct {
	Data  StopBoardsData `json:"data"`
	Meta  DataMeta       `json:"meta"`
	Links Links          `json:"links"`
}

// StopBoardsOutput is the response of listStopBoards.
type StopBoardsOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         StopBoardsEnvelope
}

func registerStopBoards(api huma.API, s *server, op huma.Operation) {
	op.Summary = "The stops around a position with their next departures"
	op.Description = "The stops around the position, as the platform's departures around a place list them, each with its next departures as its departure board shows them, all in one read. Needs the scope transit."
	featureResponses(api, &op, "Not used by this operation.")
	op.Extensions[counterpartExtension] = "departures around a place"
	huma.Register(api, op, func(ctx context.Context, in *StopBoardsInput) (*StopBoardsOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := transit.StopBoardsQuery{At: in.point, RadiusM: cmp.Or(in.RadiusM, transit.StopsRadiusM), Limit: cmp.Or(in.Limit, stopBoardsDefaultStops),
			PerStop: cmp.Or(in.PerStop, stopBoardsDefaultPerStop), From: in.From, Window: time.Duration(cmp.Or(in.DurationMin, boardDefaultMinutes)) * time.Minute,
			Modes: in.modes}
		if q.From.IsZero() {
			q.From = s.at()
		}
		a, err := s.features.StopBoards(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		until := q.From.Add(q.Window)
		data := StopBoardsData{Location: position(q.At),
			Window: StopBoardsWindow{From: q.From.UTC(), FromLocal: local(q.From), Until: until.UTC(), UntilLocal: local(until)},
			Stops:  make([]StopBoard, len(a.Items))}
		for i, b := range a.Items {
			st := StopBoard{ID: b.Stop.Root, Name: b.Stop.Name, StopID: b.Stop.Root[strings.Index(b.Stop.Root, ":")+1:],
				DistanceM: int(math.Round(b.Stop.Metres)), Location: position(b.Stop.At), Departures: make([]Departure, len(b.Board.Rows))}
			for j, d := range b.Board.Rows {
				st.Departures[j] = departureRow(d)
			}
			data.Stops[i] = st
		}
		return &StopBoardsOutput{CacheControl: noStore, Body: StopBoardsEnvelope{Data: data,
			Meta: s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail), Links: s.selfLink(ctx)}}, nil
	})
}
