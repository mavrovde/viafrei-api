package httpapi

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// The sharing search, as the platform's: a shared bike is within walking
// distance, 1 km by default and at most 5 km; 8 stations by default and at
// most 20; the three nearest free-floating vehicles.
const (
	sharingDefaultRadiusM = 1000
	sharingDefaultLimit   = 8
	sharingNearestShown   = 3
)

// SharingInput is the input of listSharingOptions.
type SharingInput struct {
	AtPosition
	RadiusM       int    `query:"radius_m" minimum:"100" maximum:"5000" doc:"Search radius in metres (100 to 5000); default 1000."`
	Limit         int    `query:"limit" minimum:"1" maximum:"20" doc:"Most stations to list, nearest first; default 8."`
	OnlyAvailable string `query:"only_available" enum:"true,false" doc:"List only the stations with a bike to take now; default true."`
}

// SharingStation is a bike-sharing station of an answer.
type SharingStation struct {
	ID                string     `json:"id"`
	SystemID          string     `json:"system"`
	SystemName        *string    `json:"systemLabel" nullable:"true" doc:"The system's name; null when not published."`
	Name              *string    `json:"name" nullable:"true" doc:"The station's name; null when not published."`
	Capacity          *int       `json:"capacity" nullable:"true" doc:"Docks; null when not published."`
	IsVirtual         *bool      `json:"virtual" nullable:"true" doc:"A virtual station (no docks); null when not published."`
	Location          Position   `json:"location"`
	DistanceM         int        `json:"distanceM" doc:"Distance from the asked position in metres."`
	Current           bool       `json:"current" doc:"The count stands: counted, and the system's status at most 15 minutes old."`
	Available         bool       `json:"available" doc:"A bike can be taken: current, renting, and at least one counted."`
	VehiclesAvailable *int       `json:"bikes" nullable:"true" doc:"Bikes counted; null when no count of the last 24 hours."`
	ElectricAvailable *int       `json:"electricBikes" nullable:"true" doc:"Electric bikes counted; null when not published."`
	DocksAvailable    *int       `json:"freeDocks" nullable:"true" doc:"Free docks; null when not published."`
	IsRenting         *bool      `json:"renting" nullable:"true" doc:"The station rents; null when not published."`
	IsReturning       *bool      `json:"takesReturns" nullable:"true" doc:"The station takes returns; null when not published."`
	CountedAt         *time.Time `json:"countedAt" nullable:"true" doc:"When the count was read; null when none."`
	ReportedAt        *time.Time `json:"reportedAt" nullable:"true" doc:"When the system reported the count; null when not published."`
	SourceFamily      string     `json:"sourceFamily"`
}

// SharingVehicle is a free-floating vehicle of an answer.
type SharingVehicle struct {
	DistanceM    int     `json:"distanceM" doc:"Distance from the asked position in metres."`
	FormFactor   *string `json:"vehicleType" nullable:"true" doc:"bicycle, cargo_bicycle, scooter, ...; null when not published."`
	Propulsion   *string `json:"propulsion" nullable:"true" doc:"human, electric_assist, electric, ...; null when not published."`
	RangeM       *int    `json:"rangeLeftM" nullable:"true" doc:"The range left in metres; null when not published."`
	SourceFamily string  `json:"sourceFamily"`
}

// SharingVehicles are the free-floating vehicles in the radius.
type SharingVehicles struct {
	Count    int              `json:"count" doc:"Vehicles whose system's status is at most 15 minutes old."`
	Electric int              `json:"electric" doc:"Of them, electric or electrically assisted."`
	AtLeast  bool             `json:"atLeast" doc:"The read hit its cap of 200: the count is a minimum."`
	Nearest  []SharingVehicle `json:"nearest" doc:"The three nearest."`
}

// SharingData is the data of listSharingOptions.
type SharingData struct {
	Stations []SharingStation `json:"stations"`
	InRadius int              `json:"stationsInRadius" doc:"Every station in the radius, available or not."`
	Vehicles SharingVehicles  `json:"vehicles"`
}

// SharingEnvelope is the enveloped answer of listSharingOptions.
type SharingEnvelope struct {
	Data  SharingData `json:"data"`
	Meta  PagedMeta   `json:"meta"`
	Links Links       `json:"links"`
}

// SharingOutput is the response of listSharingOptions.
type SharingOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         SharingEnvelope
}

func sharingStation(st transit.SharingStation, now time.Time) SharingStation {
	return SharingStation{ID: st.ID, SystemID: st.SystemID, SystemName: st.SystemName, Name: st.Name, Capacity: st.Capacity,
		IsVirtual: st.IsVirtual, Location: position(st.Location), DistanceM: st.DistanceM, Current: st.Current(now), Available: st.Available(now),
		VehiclesAvailable: st.VehiclesAvailable, ElectricAvailable: st.ElectricAvailable, DocksAvailable: st.DocksAvailable,
		IsRenting: st.IsRenting, IsReturning: st.IsReturning, CountedAt: utc(st.CountedAt), ReportedAt: utc(st.ReportedAt), SourceFamily: st.Family}
}

func registerSharing(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Shared bikes near a position"
	op.Description = "The bike-sharing stations within the radius, nearest first (only those with a bike to take now unless only_available=false) and the free-floating vehicles around; no station beyond the radius is listed, as the platform's sharing search lists none. Needs the scope transit."
	featureResponses(api, &op, "Not used by this operation.")
	op.Extensions[counterpartExtension] = "bike sharing search"
	huma.Register(api, op, func(ctx context.Context, in *SharingInput) (*SharingOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := transit.SharingQuery{At: in.point, RadiusM: in.RadiusM, Limit: in.Limit, OnlyAvailable: in.OnlyAvailable != "false"}
		if q.RadiusM == 0 {
			q.RadiusM = sharingDefaultRadiusM
		}
		if q.Limit == 0 {
			q.Limit = sharingDefaultLimit
		}
		a, err := s.features.Sharing(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		return &SharingOutput{CacheControl: noStore, Body: sharingEnvelope(s, ctx, a, q.Limit)}, nil
	})
}

func sharingEnvelope(s *server, ctx context.Context, a features.SharingAnswer, limit int) SharingEnvelope {
	now := a.Meta.At
	data := SharingData{Stations: make([]SharingStation, len(a.Stations)), InRadius: a.InRadius,
		Vehicles: SharingVehicles{Count: len(a.Vehicles), AtLeast: a.VehiclesCapped, Nearest: []SharingVehicle{}}}
	for i, st := range a.Stations {
		data.Stations[i] = sharingStation(st, now)
	}
	for _, v := range a.Vehicles {
		if v.Electric() {
			data.Vehicles.Electric++
		}
		if len(data.Vehicles.Nearest) < sharingNearestShown {
			data.Vehicles.Nearest = append(data.Vehicles.Nearest, SharingVehicle{DistanceM: v.DistanceM, FormFactor: v.FormFactor,
				Propulsion: v.Propulsion, RangeM: v.RangeM, SourceFamily: v.Family})
		}
	}
	return SharingEnvelope{Data: data,
		Meta:  PagedMeta{DataMeta: s.dataMeta(ctx, a.Meta.Families, a.Meta.At, a.Meta.Partial, partialDetail), Page: Page{Limit: limit, HasMore: a.InRadius > len(a.Stations)}},
		Links: s.selfLink(ctx)}
}
