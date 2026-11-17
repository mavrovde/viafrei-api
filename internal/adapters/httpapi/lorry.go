package httpapi

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// LorryInput is the input of listLorryRules.
type LorryInput struct {
	Vehicle string `query:"vehicle" required:"true" enum:"lorry,long_lorry" example:"long_lorry" doc:"lorry: the height, weight, width and lorry-ban restrictions and the lorry priority routes around the position, where a publisher holds them. long_lorry: the long-lorry positive network, on the road or around the position."`
	Road    string `query:"road" maxLength:"6" pattern:"^[AaBb] ?[0-9]{1,4}$" example:"A7" doc:"With long_lorry: the road whose network rows are asked for (is the road in the long-lorry network)."`
	At      string `query:"at" pattern:"^-?[0-9]{1,3}(\\.[0-9]+)?,-?[0-9]{1,3}(\\.[0-9]+)?$" example:"51.2,6.98" doc:"A position: lat,lon. Give road or at."`
	RadiusM int    `query:"radius_m" minimum:"100" maximum:"50000" doc:"The radius around at in metres; default 5000 for lorry, 25000 for long_lorry (the platform's reach)."`
	Limit   int    `query:"limit" minimum:"1" maximum:"100" doc:"Most rows; default pagination.default_limit."`
	point   *geo.Point
	road    string
}

// Resolve reads the position and the road; one of them is needed.
func (in *LorryInput) Resolve(huma.Context) []error {
	if in.At != "" {
		p, err := geo.ParsePoint(in.At)
		if err != nil {
			return []error{&huma.ErrorDetail{Location: "query.at", Message: err.Error(), Value: in.At}}
		}
		in.point = &p
	}
	if in.Road != "" {
		r, err := roads.ParseRoad(in.Road)
		if err != nil {
			return []error{&huma.ErrorDetail{Location: "query.road", Message: err.Error(), Value: in.Road}}
		}
		in.road = r
	}
	if in.point == nil && in.road == "" {
		return []error{&huma.ErrorDetail{Location: "query.at", Message: "give road or at", Value: in.At}}
	}
	return nil
}

// LorryRule is one published lorry row. It states what the platform's road
// status states of its layer (docs/parity.md): no identifier, a publisher's
// file date only for a priority route or the long-lorry network, and no
// direction for the long-lorry network.
type LorryRule struct {
	Layer         string   `json:"layer" doc:"long_lorry (the positive network), restriction, priority_route or zone; open enumeration."`
	Kind          *string  `json:"kind" nullable:"true" doc:"What is restricted (height, weight, width, ban, ...), as published."`
	Sign          *string  `json:"sign" nullable:"true"`
	Value         *float64 `json:"value" nullable:"true" doc:"The figure, as published; null where the publisher's words state none (then the signs on site bind)."`
	Unit          *string  `json:"unit" nullable:"true"`
	PublishedText *string  `json:"publishedText" nullable:"true" doc:"The publisher's own words, never rewritten as a number."`
	Road          *string  `json:"road" nullable:"true"`
	Street        *string  `json:"street" nullable:"true"`
	Municipality  *string  `json:"municipality" nullable:"true"`
	Direction     *string  `json:"direction" nullable:"true" doc:"The direction a restriction or a priority route applies in, the publisher's words; null for the long-lorry network."`
	Exceptions    []string `json:"exceptions"`
	Times         *string  `json:"times" nullable:"true"`
	Days          *string  `json:"days" nullable:"true"`
	Category      *string  `json:"category" nullable:"true"`
	NetworkState  *string  `json:"networkState" nullable:"true"`
	VersionDate   *string  `json:"versionDate" nullable:"true" doc:"The date of the publisher's file (YYYY-MM-DD), for a priority route and the long-lorry network; null for a restriction or a zone."`
	DistanceM     *int     `json:"distanceM" nullable:"true" doc:"From the position; null without one."`
	SourceFamily  string   `json:"sourceFamily"`
}

// LorryEnvelope is the enveloped answer of listLorryRules.
type LorryEnvelope struct {
	Data  []LorryRule `json:"data"`
	Meta  PagedMeta   `json:"meta"`
	Links Links       `json:"links"`
}

// LorryOutput is the response of listLorryRules.
type LorryOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         LorryEnvelope
}

func registerLorry(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Where a lorry or a long lorry may go"
	op.Description = "The long-lorry positive network (BASt's extract of the ordinance's annex, which binds; not complete and not fit for navigation) on a road or around a position, or the lorry restrictions and priority routes the publishers hold around a position (NRW, Frankfurt RheinMain, Bremen and the cities' own files); outside their regions none is held, which is no all-clear. By distance, then the publisher's row. MCP counterpart: road status (vehicle form). Needs the scope roads."
	featureResponses(api, &op, featureOffDoc)
	huma.Register(api, op, func(ctx context.Context, in *LorryInput) (*LorryOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := roads.LorryQuery{Vehicle: in.Vehicle, Road: in.road, At: in.point, RadiusM: in.RadiusM, Limit: s.limit(in.Limit)}
		if q.RadiusM == 0 {
			q.RadiusM = roads.LorryRadiusM
			if q.Vehicle == "long_lorry" {
				q.RadiusM = roads.LongLorryRadiusM
			}
		}
		q.Limit++
		a, err := s.features.LorryRules(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		q.Limit--
		rs := a.Rules
		more := len(rs) > q.Limit
		if more {
			rs = rs[:q.Limit]
		}
		out := make([]LorryRule, len(rs))
		for i, r := range rs {
			out[i] = LorryRule{Layer: r.Layer, Kind: r.Kind, Sign: r.Sign, Value: r.Value, Unit: r.Unit, PublishedText: r.PublishedText,
				Road: r.Road, Street: r.Street, Municipality: r.Municipality, Direction: r.Direction, Exceptions: append([]string{}, r.Exceptions...),
				Times: r.Times, Days: r.Days, Category: r.Category, NetworkState: r.NetworkState, VersionDate: day(r.VersionDate),
				DistanceM: r.DistanceM, SourceFamily: r.Family}
			if r.Layer != "priority_route" && r.Layer != "long_lorry" {
				out[i].VersionDate = nil
			}
			if r.Layer == "long_lorry" {
				out[i].Direction = nil
			}
		}
		return &LorryOutput{CacheControl: noStore, Body: LorryEnvelope{Data: out,
			Meta:  PagedMeta{DataMeta: s.dataMeta(ctx, a.Meta.Families, a.Meta.At, a.Meta.Partial, partialDetail), Page: Page{Limit: q.Limit, HasMore: more}},
			Links: s.selfLink(ctx)}}, nil
	})
}
