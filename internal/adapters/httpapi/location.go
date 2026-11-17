package httpapi

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// LocationInput is the input of describeLocation.
type LocationInput struct {
	AtPosition
}

// LocationAddress is the nearest address to a position.
type LocationAddress struct {
	Address
	DistanceM int `json:"distanceM" doc:"Distance from the asked position in metres."`
}

// LocationPlace is a place a position lies in or near.
type LocationPlace struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	Code         *string  `json:"code" nullable:"true" doc:"The official region code; null when none."`
	Location     Position `json:"location"`
	DistanceM    *int     `json:"distanceM" nullable:"true" doc:"Distance from the asked position in metres; null for the Land."`
	SourceFamily string   `json:"sourceFamily"`
}

// LocationData is the data of describeLocation: independent facts, each
// null when none is within its reach.
type LocationData struct {
	Address      *LocationAddress `json:"address" doc:"The nearest address within 5 km; null when none."`
	Municipality *LocationPlace   `json:"municipality" doc:"The nearest municipality within 50 km; null when none."`
	District     *LocationPlace   `json:"district" doc:"The nearest district within 100 km; null when none."`
	State        *LocationPlace   `json:"state" doc:"The Land of the district; null when no district."`
}

// LocationEnvelope is the enveloped answer of describeLocation.
type LocationEnvelope struct {
	Data  LocationData `json:"data"`
	Meta  DataMeta     `json:"meta"`
	Links Links        `json:"links"`
}

// LocationOutput is the response of describeLocation.
type LocationOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         LocationEnvelope
}

func locationPlace(p *places.Place) *LocationPlace {
	if p == nil {
		return nil
	}
	return &LocationPlace{ID: p.ID, Name: p.Name, Code: p.RegionCode, Location: position(p.Location), DistanceM: p.DistanceM, SourceFamily: p.Family}
}

func registerLocation(api huma.API, s *server, op huma.Operation) {
	op.Summary = "What lies at a position"
	op.Description = "A coordinate as a person understands it: the nearest address, municipality and district, and the district's Land; each fact on its own, null when none is within its reach. Needs the scope places."
	featureResponses(api, &op, "Not used by this operation.")
	op.Extensions[counterpartExtension] = "location description"
	huma.Register(api, op, func(ctx context.Context, in *LocationInput) (*LocationOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		a, err := s.features.Location(ctx, principalFrom(ctx).Key, places.LocationQuery{At: in.point})
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		return &LocationOutput{CacheControl: noStore, Body: locationEnvelope(s, ctx, a)}, nil
	})
}

func locationEnvelope(s *server, ctx context.Context, a features.LocationAnswer) LocationEnvelope {
	data := LocationData{Municipality: locationPlace(a.Municipality), District: locationPlace(a.District), State: locationPlace(a.State)}
	if v := a.Address; v != nil {
		data.Address = &LocationAddress{Address: Address{Street: v.Street, HouseNumber: v.HouseNumber, Postcode: v.Postcode, Place: v.Place,
			Land: v.Land, Location: position(v.Location), DataAsOf: day(v.DataAsOf), SourceFamily: v.Family}}
		if v.DistanceM != nil {
			data.Address.DistanceM = *v.DistanceM
		}
	}
	return LocationEnvelope{Data: data, Meta: s.dataMeta(ctx, a.Meta.Families, a.Meta.At, a.Meta.Partial, partialDetail), Links: s.selfLink(ctx)}
}
