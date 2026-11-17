package httpapi

import (
	"context"
	"encoding/json"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
)

// The hydrogen search, as the platform's: a wide default radius, since
// hydrogen stations are few, and the nearest one beyond it when none is in.
const (
	hydrogenDefaultRadiusM = 50000
	hydrogenDefaultLimit   = 5
)

// HydrogenInput is the input of listHydrogenOptions.
type HydrogenInput struct {
	AtPosition
	RadiusM int    `query:"radius_m" minimum:"1000" maximum:"150000" doc:"Search radius in metres (1000 to 150000); default 50000."`
	Vehicle string `query:"vehicle" enum:"car,truck" doc:"Only stations with a refill solution for this vehicle. Default: any."`
	Limit   int    `query:"limit" minimum:"1" maximum:"10" doc:"Most stations to return, nearest first; default 5."`
}

// HydrogenStation is a hydrogen filling station of an answer.
type HydrogenStation struct {
	ID                string     `json:"id"`
	Name              *string    `json:"name" nullable:"true" doc:"The station's name; null when not published."`
	Operator          *string    `json:"operator" nullable:"true" doc:"The operator; null when not published."`
	Brand             *string    `json:"brand" nullable:"true" doc:"The brand; null when not published."`
	Location          Position   `json:"location"`
	DistanceM         int        `json:"distanceM" doc:"Distance from the asked position in metres."`
	Address           any        `json:"address" doc:"The published address object, verbatim; null when not published."`
	Refill            any        `json:"refill" doc:"The published refill solutions (pressure, vehicle), verbatim; null when not published."`
	Opening           any        `json:"opening" doc:"The published opening hours, verbatim; null when not published."`
	ProviderUpdatedAt *time.Time `json:"providerUpdatedAt" nullable:"true" doc:"When the provider last changed the station; null when not published."`
	SourceFamily      string     `json:"sourceFamily"`
}

// HydrogenData is the data of listHydrogenOptions.
type HydrogenData struct {
	Stations []HydrogenStation `json:"stations"`
	Nearest  *HydrogenStation  `json:"nearest" doc:"When no station is inside the radius, the nearest one beyond it; otherwise null."`
}

// HydrogenEnvelope is the enveloped answer of listHydrogenOptions.
type HydrogenEnvelope struct {
	Data  HydrogenData `json:"data"`
	Meta  PagedMeta    `json:"meta"`
	Links Links        `json:"links"`
}

// HydrogenOutput is the response of listHydrogenOptions.
type HydrogenOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         HydrogenEnvelope
}

func hydrogenStation(h energy.HydrogenStation) HydrogenStation {
	return HydrogenStation{ID: h.ID, Name: h.Name, Operator: h.Operator, Brand: h.Brand, Location: position(h.Location),
		DistanceM: h.DistanceM, Address: published(h.Address), Refill: published(h.Refill), Opening: published(h.Opening),
		ProviderUpdatedAt: utc(h.ProviderUpdatedAt), SourceFamily: h.Family}
}

// published is a source's published JSON object as it is, or nil when there
// is none: it was valid JSON in the database, so it decodes.
func published(raw json.RawMessage) any {
	var v any
	_ = json.Unmarshal(raw, &v)
	return v
}

func registerHydrogen(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Hydrogen filling stations near a position"
	op.Description = "The hydrogen stations within the radius, nearest first, optionally only those with a refill solution for a car or a truck; when none is inside, data.nearest names the nearest one beyond. Needs the scope energy."
	featureResponses(api, &op, "Not used by this operation.")
	op.Extensions[counterpartExtension] = "hydrogen station search"
	huma.Register(api, op, func(ctx context.Context, in *HydrogenInput) (*HydrogenOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := energy.HydrogenQuery{At: in.point, RadiusM: in.RadiusM, Vehicle: in.Vehicle, Limit: in.Limit}
		if q.RadiusM == 0 {
			q.RadiusM = hydrogenDefaultRadiusM
		}
		if q.Limit == 0 {
			q.Limit = hydrogenDefaultLimit
		}
		a, err := s.features.HydrogenStations(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		return &HydrogenOutput{CacheControl: noStore, Body: hydrogenEnvelope(s, ctx, a, q.Limit)}, nil
	})
}

func hydrogenEnvelope(s *server, ctx context.Context, a features.HydrogenStations, limit int) HydrogenEnvelope {
	data := HydrogenData{Stations: make([]HydrogenStation, len(a.Items))}
	for i, h := range a.Items {
		data.Stations[i] = hydrogenStation(h)
	}
	if a.Nearest != nil {
		n := hydrogenStation(*a.Nearest)
		data.Nearest = &n
	}
	return HydrogenEnvelope{Data: data,
		Meta:  PagedMeta{DataMeta: s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail), Page: Page{Limit: limit, HasMore: a.HasMore}},
		Links: s.selfLink(ctx)}
}
