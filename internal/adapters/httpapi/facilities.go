package httpapi

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// The station facilities operation (FR-TRANSIT-003).

const facilitiesDefaultLimit = 20

// FacilitiesInput is the input of listStationFacilities.
type FacilitiesInput struct {
	StationID string `path:"stationId" minLength:"3" maxLength:"120" example:"1:de:05315:11201" doc:"The station id, as listStations answers it."`
	Kind      string `query:"kind" enum:"elevator,escalator" doc:"Only lifts (elevator) or only escalators. Default: both."`
	Limit     int    `query:"limit" minimum:"1" maximum:"50" doc:"Most facilities to return, out of service first; default 20. The counts in data.counts cover all of them."`
}

// StationFacility is a lift or escalator.
type StationFacility struct {
	ID              string     `json:"id"`
	Kind            string     `json:"kind" doc:"elevator, escalator or other. Open enumeration."`
	Position        *string    `json:"position" nullable:"true" doc:"Where it is, as published (zu Gleis 2); null when not published."`
	State           string     `json:"state" doc:"active, inactive or unknown: a state older than its family's freshness threshold is unknown. Open enumeration."`
	ObservedAt      *time.Time `json:"observedAt" nullable:"true" doc:"When the state was reported; null when never."`
	ObservedAtLocal *string    `json:"observedAtLocal" nullable:"true" format:"date-time" doc:"observedAt in Europe/Berlin time."`
	SourceFamily    string     `json:"sourceFamily"`
}

// FacilityCounts counts every facility of the asked kind by state.
type FacilityCounts struct {
	Active   int `json:"active"`
	Inactive int `json:"inactive"`
	Unknown  int `json:"unknown"`
}

// StationFacilitiesData is the data of listStationFacilities.
type StationFacilitiesData struct {
	Station    BoardStation      `json:"station"`
	Counts     FacilityCounts    `json:"counts"`
	Facilities []StationFacility `json:"facilities"`
}

// FacilitiesEnvelope is the enveloped answer of listStationFacilities.
type FacilitiesEnvelope struct {
	Data  StationFacilitiesData `json:"data"`
	Meta  PagedMeta             `json:"meta"`
	Links Links                 `json:"links"`
}

// PagedMeta is the meta member of a page with no query echo.
type PagedMeta struct {
	DataMeta
	Page Page `json:"page"`
}

// FacilitiesOutput is the response of listStationFacilities.
type FacilitiesOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         FacilitiesEnvelope
}

func registerFacilities(api huma.API, s *server, op huma.Operation) {
	op.Summary = "A station's lifts and escalators"
	op.Description = "The lifts and escalators of the station with their reported state: out of service first, then unknown, then in service, then by position; the counts cover every one of the asked kind, so a shorter list never hides a broken lift. Needs the scope transit."
	featureResponses(api, &op, "No station with this id.")
	op.Extensions[counterpartExtension] = "station facilities"
	huma.Register(api, op, func(ctx context.Context, in *FacilitiesInput) (*FacilitiesOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := transit.FacilityQuery{StationID: in.StationID, Kind: in.Kind, Limit: in.Limit}
		if q.Limit == 0 {
			q.Limit = facilitiesDefaultLimit
		}
		a, err := s.features.Facilities(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		rows := make([]StationFacility, len(a.Items))
		for i, f := range a.Items {
			rows[i] = StationFacility{ID: f.ID, Kind: f.Kind, Position: f.Position, State: f.State, ObservedAt: utc(f.ObservedAt), SourceFamily: f.Family}
			if f.ObservedAt != nil {
				l := local(*f.ObservedAt)
				rows[i].ObservedAtLocal = &l
			}
		}
		return &FacilitiesOutput{CacheControl: noStore, Body: FacilitiesEnvelope{
			Data: StationFacilitiesData{
				Station:    BoardStation{ID: a.Station.ID, Name: a.Station.Name, Location: position(a.Station.Location), StopCode: a.Station.NationalCode},
				Counts:     FacilityCounts{Active: a.Active, Inactive: a.Inactive, Unknown: a.Unknown},
				Facilities: rows,
			},
			Meta:  PagedMeta{DataMeta: s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail), Page: Page{Limit: q.Limit, HasMore: a.HasMore}},
			Links: s.selfLink(ctx),
		}}, nil
	})
}
