package httpapi

import (
	"context"
	"sort"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// The public-transport operations (FR-TRANSIT).

// ---------------------------------------------------------------- departures

// The board's bounds: the platform's departure board (one day per answer,
// fifteen rows a person reads).
const (
	boardMinMinutes     = 5
	boardMaxMinutes     = 1440
	boardDefaultMinutes = 60
	boardMaxRows        = 15
	boardDefaultRows    = 10
)

// DeparturesInput is the input of listStationDepartures.
type DeparturesInput struct {
	StationID   string    `path:"stationId" minLength:"3" maxLength:"120" example:"1:de:05315:11201" doc:"The station id, as listStations answers it."`
	From        time.Time `query:"from" doc:"Start of the window (RFC 3339 with an offset); default: now. The board lists the departures planned from it."`
	DurationMin int       `query:"duration_min" minimum:"5" maximum:"1440" doc:"Length of the window in minutes; default 60. To look further ahead, move from rather than widen the window."`
	Modes       []string  `query:"modes" maxItems:"7" doc:"Keep only these modes (comma-separated): rail, sbahn, subway, tram, bus, ferry, other. Default: every mode."`
	Categories  []string  `query:"categories" maxItems:"20" doc:"Keep only trains of these categories (comma-separated, as the timetable writes them: ICE, IC, RE, RB, S). Default: every departure."`
	Limit       int       `query:"limit" minimum:"1" maximum:"15" doc:"Most departures to return, earliest first; default 10."`
	modes       []transit.Mode
}

// Resolve reads the modes (huma calls it after the schema checks).
func (in *DeparturesInput) Resolve(huma.Context) []error {
	for _, m := range in.Modes {
		mode := transit.MapMode(m)
		if string(mode) != m {
			return []error{&huma.ErrorDetail{Location: "query.modes", Message: "unknown mode", Value: m}}
		}
		in.modes = append(in.modes, mode)
	}
	return nil
}

// Departure is one departure of a board.
type Departure struct {
	ID              string     `json:"id" doc:"The journey and the call: one train calling twice at the station is two rows."`
	JourneyID       string     `json:"journeyId" doc:"The trip and its service day."`
	Line            *string    `json:"line" nullable:"true" doc:"The line as signed (RE 5, S 6, 120); null when the timetable names none."`
	Mode            string     `json:"mode" doc:"The mode of transport. Open enumeration (rail, sbahn, subway, tram, bus, ferry, other); treat an unknown value as other."`
	RouteType       int        `json:"routeType" doc:"The timetable's route type code, basic or extended (3 bus, 2 and 100-199 rail, 109 S-Bahn)."`
	TrainNumber     *string    `json:"trainNumber" nullable:"true" doc:"The train's number; null off the railway or when not published."`
	Category        *string    `json:"category" nullable:"true" doc:"The train category (ICE, RE, S) where a live source names it; null otherwise."`
	Destination     *string    `json:"destination" nullable:"true" doc:"Where the trip goes: its headsign, else its last stop; null when neither is known."`
	PlannedAt       time.Time  `json:"plannedAt" doc:"The planned departure."`
	PlannedAtLocal  string     `json:"plannedAtLocal" format:"date-time" doc:"plannedAt in Europe/Berlin time."`
	Platform        *string    `json:"platform" nullable:"true" doc:"The live platform where one is reported, else the planned one; null when none is known."`
	PlannedPlatform *string    `json:"plannedPlatform" nullable:"true" doc:"The planned platform; null when the timetable names none."`
	PlatformChanged bool       `json:"platformChanged" doc:"True when a live source reports a platform other than the planned one."`
	Realtime        bool       `json:"realtime" doc:"True when a live figure stands for this departure."`
	RealtimeSource  *string    `json:"realtimeSource" nullable:"true" doc:"The live source family the figure is from; null without one."`
	Cancelled       bool       `json:"cancelled" doc:"True when a live source reports the departure cancelled."`
	DelayMin        *int       `json:"delayMin" nullable:"true" doc:"The live delay in whole minutes (negative: early); null without a figure."`
	ExpectedAt      *time.Time `json:"expectedAt" nullable:"true" doc:"The expected departure; null without a figure."`
	NoForecast      bool       `json:"noForecast" doc:"True when a live source holds the departure but gives no figure for it."`
	SourceFamily    string     `json:"sourceFamily" doc:"The timetable's source family; every family a row draws on is credited in meta.attribution."`
}

// BoardStation is the station a board is of.
type BoardStation struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	Location Position `json:"location"`
	StopCode *string  `json:"stopCode" nullable:"true" doc:"The national stop code (DHID); null when none is recorded."`
}

// BoardWindow is the window a board covers and what it holds.
type BoardWindow struct {
	From       time.Time `json:"from" doc:"Start of the window."`
	FromLocal  string    `json:"fromLocal" format:"date-time" doc:"from in Europe/Berlin time."`
	Until      time.Time `json:"until" doc:"End of the window (excluded)."`
	UntilLocal string    `json:"untilLocal" format:"date-time" doc:"until in Europe/Berlin time."`
	Matched    int       `json:"matched" doc:"Departures of the asked modes and categories in the window; more than listed when meta.page.hasMore."`
	MatchedAll int       `json:"matchedAll" doc:"Departures of every mode and category in the window."`
	Dropped    []Dropped `json:"dropped" doc:"Departures the mode and category filter left out, per route type; empty when it left none out."`
}

// Dropped counts the departures of one route type a filter left out.
type Dropped struct {
	Code  string `json:"code" doc:"The route type code, as text."`
	Count int    `json:"count"`
}

// Board is the data of listStationDepartures.
type Board struct {
	Station    BoardStation `json:"station"`
	Window     BoardWindow  `json:"window"`
	Departures []Departure  `json:"departures"`
}

// DeparturesQuery is meta.query of listStationDepartures.
type DeparturesQuery struct {
	Modes      []string `json:"modes" doc:"The modes asked; empty for every mode."`
	Categories []string `json:"categories" doc:"The categories asked; empty for every departure."`
}

// DeparturesMeta is the meta member of listStationDepartures.
type DeparturesMeta struct {
	DataMeta
	Page  Page            `json:"page"`
	Query DeparturesQuery `json:"query"`
}

// DeparturesEnvelope is the enveloped answer of listStationDepartures.
type DeparturesEnvelope struct {
	Data  Board          `json:"data"`
	Meta  DeparturesMeta `json:"meta"`
	Links Links          `json:"links"`
}

// DeparturesOutput is the response of listStationDepartures.
type DeparturesOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         DeparturesEnvelope
}

func registerDepartures(api huma.API, s *server, op huma.Operation) {
	op.Summary = "A station's departure board"
	op.Description = "The departures planned from the station in the window, as a departure board shows them: calls a passenger can board, of trips that go on beyond the station (a trip's last call is an arrival); one line to one destination at one instant once, whatever stop of the station published it; one destination from one platform at one instant once, whatever the line is called; earliest first, then by line. Each row carries the newest live figure the platform holds for it (delay, cancellation, platform change). Needs the scope transit."
	featureResponses(api, &op, "No station with this id.")
	op.Extensions[counterpartExtension] = "departure board"
	huma.Register(api, op, func(ctx context.Context, in *DeparturesInput) (*DeparturesOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		from := in.From
		if from.IsZero() {
			from = s.now()
		}
		minutes, limit := in.DurationMin, in.Limit
		if minutes == 0 {
			minutes = boardDefaultMinutes
		}
		if limit == 0 {
			limit = boardDefaultRows
		}
		q := transit.DepartureQuery{StationID: in.StationID, From: from, Window: time.Duration(minutes) * time.Minute,
			Modes: in.modes, Categories: in.Categories, Limit: limit}
		b, err := s.features.Departures(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		until := from.Add(q.Window)
		data := Board{
			Station: BoardStation{ID: b.Station.ID, Name: b.Station.Name, Location: position(b.Station.Location), StopCode: b.Station.NationalCode},
			Window: BoardWindow{From: from.UTC(), FromLocal: local(from), Until: until.UTC(), UntilLocal: local(until),
				Matched: b.Matched, MatchedAll: b.MatchedAll, Dropped: dropped(b.Dropped)},
			Departures: make([]Departure, len(b.Items)),
		}
		for i, d := range b.Items {
			data.Departures[i] = departureRow(d)
		}
		modes := make([]string, len(in.modes))
		for i, m := range in.modes {
			modes[i] = string(m)
		}
		cats := in.Categories
		if cats == nil {
			cats = []string{}
		}
		return &DeparturesOutput{CacheControl: noStore, Body: DeparturesEnvelope{
			Data: data,
			Meta: DeparturesMeta{
				DataMeta: s.dataMeta(ctx, b.Families, b.At, b.Partial, partialDetail),
				Page:     Page{Limit: limit, HasMore: b.HasMore},
				Query:    DeparturesQuery{Modes: modes, Categories: cats},
			},
			Links: s.selfLink(ctx),
		}}, nil
	})
}

// dropped lists the filter's counts by route type, in code order.
func dropped(by map[int]int) []Dropped {
	codes := make([]int, 0, len(by))
	for c := range by {
		codes = append(codes, c)
	}
	sort.Ints(codes)
	out := make([]Dropped, len(codes))
	for i, c := range codes {
		out[i] = Dropped{Code: strconv.Itoa(c), Count: by[c]}
	}
	return out
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// ------------------------------------------------------------------ stations

// StationsInput is the input of listStations.
type StationsInput struct {
	Q       string `query:"q" minLength:"2" maxLength:"60" example:"Köln Hbf" doc:"A station name, or part of it, as typed: case, umlauts written out (koeln for Köln), accents and punctuation do not matter."`
	Code    string `query:"code" minLength:"3" maxLength:"40" example:"8000207" doc:"A station code: the railway's EVA number or the national stop code (DHID); the station that carries it. At least one of q, code and at."`
	At      string `query:"at" pattern:"^-?[0-9]{1,3}(\\.[0-9]+)?,-?[0-9]{1,3}(\\.[0-9]+)?$" example:"50.943,6.959" doc:"A position: lat,lon in decimal degrees (WGS 84). With q, the name search within radius_m of it. At least one of q and at."`
	RadiusM int    `query:"radius_m" minimum:"1" maximum:"50000" doc:"Search radius around at, in metres. Default: geo.default_radius_m. Ignored without at."`
	Limit   int    `query:"limit" minimum:"1" maximum:"60" doc:"Most stations to return. Default: pagination.default_limit."`
	point   *geo.Point
}

// Resolve reads the position and requires q or at (huma calls it after the
// schema checks).
func (in *StationsInput) Resolve(huma.Context) []error {
	if in.At == "" {
		if in.Q == "" && in.Code == "" {
			return []error{&huma.ErrorDetail{Location: "query.q", Message: "q, code or at is required"}}
		}
		return nil
	}
	p, err := geo.ParsePoint(in.At)
	if err != nil {
		return []error{&huma.ErrorDetail{Location: "query.at", Message: err.Error(), Value: in.At}}
	}
	in.point = &p
	return nil
}

// Station is one item of listStations.
type Station struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Location     Position `json:"location"`
	StopCode     *string  `json:"stopCode" nullable:"true" doc:"The national stop code (DHID); null when none is recorded."`
	DistrictCode *string  `json:"districtCode" nullable:"true" doc:"The official code (AGS) of the district the station lies in; null when unknown."`
	Modes        []string `json:"modes" doc:"The modes of transport that serve the station. Open enumeration (rail, sbahn, subway, tram, bus, ferry, other: rail is every train but the S-Bahn); treat an unknown value as other."`
	DistanceM    *int     `json:"distanceM" nullable:"true" doc:"Distance from at, in metres; null when the question named no position."`
	SourceFamily string   `json:"sourceFamily" doc:"The source family of the item; its attribution is in meta.attribution."`
}

// StationsQuery is meta.query of listStations: the input actually answered.
type StationsQuery struct {
	Q       *string   `json:"q" nullable:"true" doc:"The text searched for; null for none."`
	Code    *string   `json:"code" nullable:"true" doc:"The station code asked; null for none."`
	At      *Position `json:"at" doc:"The position searched around; null for none."`
	RadiusM *int      `json:"radiusM" nullable:"true" doc:"The radius around at; null without at."`
}

// StationsMeta is the meta member of listStations.
type StationsMeta struct {
	DataMeta
	Page  Page          `json:"page"`
	Query StationsQuery `json:"query"`
}

// StationsEnvelope is the enveloped answer of listStations.
type StationsEnvelope struct {
	Data  []Station    `json:"data"`
	Meta  StationsMeta `json:"meta"`
	Links Links        `json:"links"`
}

// StationsOutput is the response of listStations.
type StationsOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         StationsEnvelope
}

func registerStations(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Find stations by name or near a position"
	op.Description = "The stations whose name contains q, those that start with it first, then by name; or the stations within the radius of at, nearest first; or, with both, the stations named so within the radius, nearest first. Ties go by name, then id. It answers a search field's suggestions as the user types (limit 8) as well as a full search. Needs the scope transit."
	featureResponses(api, &op, featureOffDoc)
	huma.Register(api, op, func(ctx context.Context, in *StationsInput) (*StationsOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := transit.Query{Text: in.Q, Code: in.Code, At: in.point, Limit: s.limit(in.Limit)}
		query := StationsQuery{}
		if in.Q != "" {
			query.Q = &in.Q
		}
		if in.Code != "" {
			query.Code = &in.Code
		}
		if in.point != nil {
			q.RadiusM = s.radius(in.RadiusM)
			p := position(*in.point)
			query.At, query.RadiusM = &p, &q.RadiusM
		}
		a, err := s.features.Stations(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		data := stations(a)
		return &StationsOutput{CacheControl: noStore, Body: StationsEnvelope{
			Data: data,
			Meta: StationsMeta{
				DataMeta: s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail),
				Page:     Page{Limit: q.Limit, HasMore: a.HasMore},
				Query:    query,
			},
			Links: s.selfLink(ctx),
		}}, nil
	})
}

// stations are the stations of a station answer.
func stations(a features.Answer[transit.Station]) []Station {
	data := make([]Station, len(a.Items))
	for i, v := range a.Items {
		data[i] = station(v)
	}
	return data
}

// station is the answer of one station.
func station(v transit.Station) Station {
	modes := make([]string, len(v.Modes))
	for j, m := range v.Modes {
		modes[j] = string(m)
	}
	return Station{
		ID: v.ID, Name: v.Name, Location: position(v.Location), StopCode: v.NationalCode,
		DistrictCode: v.RegionCode, Modes: modes, DistanceM: v.DistanceM, SourceFamily: v.Family,
	}
}

// departureRow is the answer of one departure.
func departureRow(d transit.Departure) Departure {
	return Departure{
		ID: d.JourneyID + "/" + strconv.Itoa(d.CallOrder), JourneyID: d.JourneyID, Line: d.Line, Mode: string(d.Mode),
		RouteType: d.RouteType, TrainNumber: d.TrainNumber, Category: d.Category, Destination: d.Destination,
		PlannedAt: d.PlannedAt.UTC(), PlannedAtLocal: local(d.PlannedAt), Platform: d.Platform,
		PlannedPlatform: d.PlannedPlatform, PlatformChanged: d.PlatformChanged, Realtime: d.Live,
		RealtimeSource: d.Source, Cancelled: d.Cancelled, DelayMin: d.DelayMin, ExpectedAt: utc(d.ExpectedAt),
		NoForecast: d.NoForecast, SourceFamily: d.Family,
	}
}
