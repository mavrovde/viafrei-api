package httpapi

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// MotorwaysInput is the input of listMotorways.
type MotorwaysInput struct {
	State string `query:"state" pattern:"^[0-9]{2}$" example:"09" doc:"Only the motorways that run through this Land (its two-digit key)."`
	Road  string `query:"road" maxLength:"6" pattern:"^[Aa] ?[0-9]{1,3}$" example:"A7" doc:"Only this motorway, with its junctions (exits) in number order."`
	Limit int    `query:"limit" minimum:"1" maximum:"200" doc:"Most motorways; default 200 (every motorway)."`
	road  string
}

// Resolve reads the road.
func (in *MotorwaysInput) Resolve(huma.Context) []error {
	if in.Road == "" {
		return nil
	}
	r, err := roads.ParseRoad(in.Road)
	if err != nil {
		return []error{&huma.ErrorDetail{Location: "query.road", Message: err.Error(), Value: in.Road}}
	}
	in.road = r
	return nil
}

// MotorwayJunction is one junction of a motorway.
type MotorwayJunction struct {
	ID     string  `json:"id"`
	Number *string `json:"number" nullable:"true" doc:"The junction number as signed."`
	Name   string  `json:"name"`
	Kind   string  `json:"kind" enum:"junction,interchange,triangle"`
	State  *string `json:"state" nullable:"true" doc:"The Land it lies in (two-digit key)."`
	Lat    float64 `json:"lat"`
	Lon    float64 `json:"lon"`
}

// MotorwayRow is one motorway of the catalogue.
type MotorwayRow struct {
	Road         string             `json:"road"`
	Name         string             `json:"name"`
	LengthKm     *float64           `json:"kilometres" nullable:"true" doc:"Its length in km."`
	States       []string           `json:"states" doc:"The Länder it runs through (two-digit keys)."`
	Junctions    int                `json:"junctions"`
	Situations   int                `json:"situations" doc:"Its situations in force now (closures, roadworks, jams, ...)."`
	Exits        []MotorwayJunction `json:"exits,omitempty" doc:"With road: its junctions in number order."`
	SourceFamily string             `json:"sourceFamily"`
}

// MotorwaysEnvelope is the enveloped answer of listMotorways.
type MotorwaysEnvelope struct {
	Data  []MotorwayRow `json:"data"`
	Meta  DataMeta      `json:"meta"`
	Links Links         `json:"links"`
}

// MotorwaysOutput is the response of listMotorways.
type MotorwaysOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         MotorwaysEnvelope
}

func registerMotorways(api huma.API, s *server, op huma.Operation) {
	op.Summary = "The motorways: length, Länder, junctions, situations now"
	op.Description = "The motorway catalogue (the site's motorway index, list, top list and exits): each motorway with its length, the Länder it runs through, its junction count and the count of its situations in force now; with road, its junctions in number order. By road number. MCP counterpart: motorway traffic. Needs the scope roads."
	featureResponses(api, &op, featureOffDoc)
	huma.Register(api, op, func(ctx context.Context, in *MotorwaysInput) (*MotorwaysOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		limit := in.Limit
		if limit == 0 {
			limit = 200
		}
		a, err := s.features.Motorways(ctx, principalFrom(ctx).Key, roads.CatalogueQuery{State: in.State, Road: in.road, Limit: limit})
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		out := make([]MotorwayRow, len(a.Motorways))
		for i, m := range a.Motorways {
			out[i] = MotorwayRow{Road: m.Road, Name: m.Name, LengthKm: m.LengthKm, States: append([]string{}, m.States...),
				Junctions: m.Junctions, Situations: m.Situations, SourceFamily: m.Family}
			for _, j := range m.Exits {
				out[i].Exits = append(out[i].Exits, MotorwayJunction{ID: j.ID, Number: j.Number, Name: j.Name, Kind: j.Kind, State: j.State, Lat: j.Lat, Lon: j.Lon})
			}
		}
		return &MotorwaysOutput{CacheControl: noStore, Body: MotorwaysEnvelope{Data: out,
			Meta: s.dataMeta(ctx, a.Meta.Families, a.Meta.At, a.Meta.Partial, partialDetail), Links: s.selfLink(ctx)}}, nil
	})
}
