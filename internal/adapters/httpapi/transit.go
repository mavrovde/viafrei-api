package httpapi

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// The public-transport operations (FR-TRANSIT).

// ------------------------------------------------------------------ stations

// StationsInput is the input of listStations.
type StationsInput struct {
	Q       string `query:"q" minLength:"2" maxLength:"60" example:"Köln Hbf" doc:"A station name, or part of it, as typed: case, umlauts written out (koeln for Köln), accents and punctuation do not matter."`
	At      string `query:"at" pattern:"^-?[0-9]{1,3}(\\.[0-9]+)?,-?[0-9]{1,3}(\\.[0-9]+)?$" example:"50.943,6.959" doc:"A position: lat,lon in decimal degrees (WGS 84). With q, the name search within radius_m of it. At least one of q and at."`
	RadiusM int    `query:"radius_m" minimum:"1" maximum:"50000" doc:"Search radius around at, in metres. Default: geo.default_radius_m. Ignored without at."`
	Limit   int    `query:"limit" minimum:"1" maximum:"60" doc:"Most stations to return. Default: pagination.default_limit."`
	point   *geo.Point
}

// Resolve reads the position and requires q or at (huma calls it after the
// schema checks).
func (in *StationsInput) Resolve(huma.Context) []error {
	if in.At == "" {
		if in.Q == "" {
			return []error{&huma.ErrorDetail{Location: "query.q", Message: "q or at is required"}}
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
	Modes        []string `json:"modes" doc:"The modes of transport that serve the station. Open enumeration (long_distance_rail, regional_rail, suburban_rail, subway, tram, bus, ferry, other); treat an unknown value as other."`
	DistanceM    *int     `json:"distanceM" nullable:"true" doc:"Distance from at, in metres; null when the question named no position."`
	SourceFamily string   `json:"sourceFamily" doc:"The source family of the item; its attribution is in meta.attribution."`
}

// StationsQuery is meta.query of listStations: the input actually answered.
type StationsQuery struct {
	Q       *string   `json:"q" nullable:"true" doc:"The text searched for; null for none."`
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
		q := transit.Query{Text: in.Q, At: in.point, Limit: s.limit(in.Limit)}
		query := StationsQuery{}
		if in.Q != "" {
			query.Q = &in.Q
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
		data := make([]Station, len(a.Items))
		for i, v := range a.Items {
			modes := make([]string, len(v.Modes))
			for j, m := range v.Modes {
				modes[j] = string(m)
			}
			data[i] = Station{
				ID: v.ID, Name: v.Name, Location: position(v.Location), StopCode: v.NationalCode,
				DistrictCode: v.RegionCode, Modes: modes, DistanceM: v.DistanceM, SourceFamily: v.Family,
			}
		}
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
