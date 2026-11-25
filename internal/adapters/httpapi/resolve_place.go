package httpapi

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// ResolvePlaceInput is the input of resolvePlace.
type ResolvePlaceInput struct {
	Q string `query:"q" required:"true" minLength:"2" maxLength:"80" example:"Neustadt" doc:"The place as typed: a town, a district, a station or stop, a landmark or other place of interest, an address, a motorway junction; case, umlauts written out (koeln for Köln), accents and punctuation do not matter; any of its names (an English one too) answers."`
}

// ResolvedAlternative is another place of the same name the label names.
type ResolvedAlternative struct {
	Name string `json:"name" doc:"How the label names it."`
	Kind string `json:"kind" doc:"Its kind."`
}

// ResolvedPlace is the one place a text names.
type ResolvedPlace struct {
	Name         string                `json:"name" doc:"The place's name: the label without the other places of the same name."`
	Label        string                `json:"label" doc:"What the answer says of the place, with its kind and the alternatives where the name is shared."`
	Kind         string                `json:"kind" doc:"The kind of the place."`
	Location     Position              `json:"location"`
	DistrictCode *string               `json:"districtCode" nullable:"true" doc:"The municipality key (AGS) of a town; null for any other place."`
	Alternatives []ResolvedAlternative `json:"alternatives" doc:"The other places of the same name the label names; empty when none."`
}

// NullablePlace is a resolved place or null.
type NullablePlace struct{ Place *ResolvedPlace }

// MarshalJSON writes the place, or null.
func (n NullablePlace) MarshalJSON() ([]byte, error) { return json.Marshal(n.Place) }

// Schema is the place's schema, inline, with null allowed.
func (NullablePlace) Schema(r huma.Registry) *huma.Schema {
	s := huma.SchemaFromType(r, reflect.TypeFor[ResolvedPlace]())
	s.Nullable = true
	return s
}

// ResolvePlaceEnvelope is the enveloped answer of resolvePlace.
type ResolvePlaceEnvelope struct {
	Data  NullablePlace `json:"data" doc:"The place; null when the text names no one place (none, or several alike)."`
	Meta  DataMeta      `json:"meta"`
	Links Links         `json:"links"`
}

// ResolvePlaceOutput is the response of resolvePlace.
type ResolvePlaceOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         ResolvePlaceEnvelope
}

func registerResolvePlace(api huma.API, s *server, op huma.Operation) {
	op.Summary = "The one place a text names"
	op.Description = "The one place a free text names, as the platform's resolver reads it: a town by its gazetteer point, a stop, station, landmark or other place of interest, an address or a motorway junction where the text names one ('Berlin Alexanderplatz'), the other places of the same name said as alternatives ('Sylt (town), also: Sylt (island)'). The label is what the answer says; meta.attribution credits the sources the place came from. data is null when the text names no one place. Needs the scope places."
	featureResponses(api, &op, "Not used by this operation.")
	op.Extensions[counterpartExtension] = "place resolver"
	huma.Register(api, op, func(ctx context.Context, in *ResolvePlaceInput) (*ResolvePlaceOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		a, err := s.features.ResolvePlace(ctx, principalFrom(ctx).Key, in.Q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		return &ResolvePlaceOutput{CacheControl: noStore, Body: resolvePlaceEnvelope(s, ctx, a)}, nil
	})
}

func resolvePlaceEnvelope(s *server, ctx context.Context, a features.ResolveAnswer) ResolvePlaceEnvelope {
	env := ResolvePlaceEnvelope{Meta: s.dataMeta(ctx, a.Meta.Families, a.Meta.At, a.Meta.Partial, partialDetail), Links: s.selfLink(ctx)}
	r := a.Resolution
	if r.Kind != places.ResolvedPoint {
		return env
	}
	p := &ResolvedPlace{Name: places.ChosenName(r, a.Lang), Label: r.Label, Kind: places.ResolvedKind(r.PlaceKind, r.Via, r.AGS),
		Location: position(geo.Point{Lat: r.Lat, Lon: r.Lon}), Alternatives: []ResolvedAlternative{}}
	if r.AGS != "" {
		p.DistrictCode = &r.AGS
	}
	for _, o := range r.Alternatives {
		p.Alternatives = append(p.Alternatives, ResolvedAlternative{Name: o.Label, Kind: places.ResolvedKind(o.Kind, "", "")})
	}
	env.Data = NullablePlace{Place: p}
	return env
}
