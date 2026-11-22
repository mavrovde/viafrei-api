package httpapi

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// The place search (FR-PLACE): the place a name means, for every operation
// that asks a position.

const placesDefaultLimit = 5

// PlacesInput is the input of listPlaces.
type PlacesInput struct {
	Q       string `query:"q" required:"true" minLength:"2" maxLength:"80" example:"Neustadt" doc:"The place name as typed: case, umlauts written out (koeln for Köln), accents and punctuation do not matter; any of its names (an English one too) answers."`
	Kind    string `query:"kind" enum:"municipality,district,locality,region,state,station_area,motorway,junction" doc:"Only places of this kind. Default: every kind. motorway and junction are the gazetteer's motorway places; a q that starts with a junction word (AK, AD, AS, Kreuz, Dreieck, Anschlussstelle, interchange, junction, exit ...) asks for a junction by the name after it."`
	At      string `query:"at" pattern:"^-?[0-9]{1,3}(\\.[0-9]+)?,-?[0-9]{1,3}(\\.[0-9]+)?$" example:"53.55,10.0" doc:"A position to tell same-named places apart: lat,lon in decimal degrees (WGS 84). Every place then carries its distance from it."`
	RadiusM int    `query:"radius_m" minimum:"1000" maximum:"200000" doc:"With at, keep only places within this many metres. Default: none dropped."`
	Limit   int    `query:"limit" minimum:"1" maximum:"10" doc:"Most places to return, best match first; default 5."`
	point   *geo.Point
}

// Resolve reads the position (huma calls it after the schema checks).
func (in *PlacesInput) Resolve(huma.Context) []error {
	if in.At == "" {
		return nil
	}
	p, err := geo.ParsePoint(in.At)
	if err != nil {
		return []error{&huma.ErrorDetail{Location: "query.at", Message: err.Error(), Value: in.At}}
	}
	in.point = &p
	return nil
}

// PlaceItem is a place of a search.
type PlaceItem struct {
	PlaceSummary
	DistrictCode *string `json:"districtCode" nullable:"true" doc:"The official key of the district or Land the place lies in; null when not recorded."`
	DistanceM    *int    `json:"distanceM,omitempty" doc:"Distance from at in metres. Present when at is."`
	NameEn       *string `json:"nameEn,omitempty" doc:"The place's English name where it differs from name (Munich for München), as the platform's place search states it. Absent else."`
	AGS          *string `json:"ags,omitempty" doc:"The place's own official key when it is an AGS (Amtlicher Gemeindeschlüssel): a Gemeinde's eight digits, a district's five, a Land's two. Absent when the place has none, as a stop and a motorway have none."`
}

// PlacesQuery is meta.query of listPlaces.
type PlacesQuery struct {
	Q       string    `json:"q"`
	Kind    *string   `json:"kind" nullable:"true"`
	At      *Position `json:"at,omitempty" doc:"The position measured from. Present when at is."`
	RadiusM *int      `json:"radiusM" nullable:"true"`
}

// PlacesMeta is the meta member of listPlaces.
type PlacesMeta struct {
	DataMeta
	Page  Page        `json:"page"`
	Query PlacesQuery `json:"query"`
}

// PlacesEnvelope is the enveloped answer of listPlaces.
type PlacesEnvelope struct {
	Data  []PlaceItem `json:"data"`
	Meta  PlacesMeta  `json:"meta"`
	Links Links       `json:"links"`
}

// PlacesOutput is the response of listPlaces.
type PlacesOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         PlacesEnvelope
}

func registerPlaces(api huma.API, s *server, op huma.Operation) {
	op.Summary = "The places a name means"
	op.Description = "The places one of whose names answers the text, and the timetable stops whose name does (kind station_area, from the stop families the key may read), best match first: a whole name before the start of one, the start before a part, then the more prominent place, then the nearer to at. The id is what getPlaceOverview takes, and the location is what every position operation asks. Needs the scope places."
	featureResponses(api, &op, "Not used by this operation.")
	op.Extensions[counterpartExtension] = "place search"
	huma.Register(api, op, func(ctx context.Context, in *PlacesInput) (*PlacesOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := places.Query{Text: in.Q, Kind: places.Kind(in.Kind), At: in.point, Limit: in.Limit}
		if q.Limit == 0 {
			q.Limit = placesDefaultLimit
		}
		query := PlacesQuery{Q: in.Q}
		if in.Kind != "" {
			query.Kind = &in.Kind
		}
		if in.point != nil {
			p := position(*in.point)
			query.At = &p
			if in.RadiusM != 0 {
				q.RadiusM = in.RadiusM
				query.RadiusM = &q.RadiusM
			}
		}
		a, err := s.features.Places(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		data := make([]PlaceItem, len(a.Items))
		for i, p := range a.Items {
			data[i] = PlaceItem{
				PlaceSummary: PlaceSummary{ID: p.ID, Name: p.Name, Kind: string(p.Kind), Location: position(p.Location), SourceFamily: p.Family},
				DistrictCode: p.RegionCode, DistanceM: p.DistanceM, AGS: p.AGS(), NameEn: p.NameEn,
			}
		}
		return &PlacesOutput{CacheControl: noStore, Body: PlacesEnvelope{
			Data: data,
			Meta: PlacesMeta{
				DataMeta: s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail),
				Page:     Page{Limit: q.Limit, HasMore: a.HasMore},
				Query:    query,
			},
			Links: s.selfLink(ctx),
		}}, nil
	})
}
