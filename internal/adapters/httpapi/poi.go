package httpapi

import (
	"context"
	"strconv"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// The search for a place of interest, as the platform's: 10 km around a
// position by default and at most 50 km; 10 matches a page by default and at
// most 25; at most 50 in all, through the cursor.
const (
	poiDefaultRadiusM = 10000
	poiDefaultLimit   = 10
)

// PoiInput is the input of listPois.
type PoiInput struct {
	Name     string `query:"name" required:"true" minLength:"2" maxLength:"80" example:"Apotheke" doc:"The name, or part of it, or a brand; case, umlauts and punctuation do not matter."`
	In       string `query:"in" maxLength:"80" doc:"A town to search in. Use this or at, not both."`
	At       string `query:"at" pattern:"^-?[0-9]{1,3}(\\.[0-9]+)?,-?[0-9]{1,3}(\\.[0-9]+)?$" example:"52.52,13.405" doc:"A position to search around: lat,lon (WGS 84). Use this or in, not both."`
	RadiusM  int    `query:"radius_m" minimum:"1000" maximum:"50000" doc:"Search radius around at in metres (1000 to 50000); default 10000."`
	Category string `query:"category" enum:"office,amenity,shop,tourism,healthcare,leisure,industrial,historic,natural,building" doc:"Only this OpenStreetMap family."`
	Limit    int    `query:"limit" minimum:"1" maximum:"25" doc:"Matches per page, best first; default 10."`
	Cursor   string `query:"cursor" pattern:"^[0-9]{1,2}$" doc:"The meta.page.nextCursor of the page before; omit for the first page."`
	point    *geo.Point
	offset   int
}

// Resolve reads the position and the cursor (huma calls it after the schema
// checks): in and at are two different questions, and a cursor lies inside
// the reach of 50.
func (p *PoiInput) Resolve(huma.Context) []error {
	if p.In != "" && p.At != "" {
		return []error{&huma.ErrorDetail{Location: "query.in", Message: "give in or at, not both", Value: p.In}}
	}
	if p.At != "" {
		pt, err := geo.ParsePoint(p.At)
		if err != nil {
			return []error{&huma.ErrorDetail{Location: "query.at", Message: err.Error(), Value: p.At}}
		}
		p.point = &pt
	}
	if p.Cursor != "" {
		p.offset, _ = strconv.Atoi(p.Cursor)
		if p.offset >= places.PoiReach {
			return []error{&huma.ErrorDetail{Location: "query.cursor", Message: "past the reach of 50", Value: p.Cursor}}
		}
	}
	return nil
}

// Poi is a place of interest of an answer.
type Poi struct {
	Name         *string  `json:"name" nullable:"true" doc:"The name; null when not mapped."`
	Category     string   `json:"category" doc:"The OpenStreetMap family."`
	Kind         *string  `json:"kind" nullable:"true" doc:"The value in the family (pharmacy, hotel); null when not mapped."`
	Brand        *string  `json:"brand" nullable:"true" doc:"The brand; null when not mapped."`
	Operator     *string  `json:"operator" nullable:"true" doc:"The operator; null when not mapped."`
	Street       *string  `json:"street" nullable:"true" doc:"The street; null when not mapped."`
	Number       *string  `json:"number" nullable:"true" doc:"The house number; null when not mapped."`
	Postcode     *string  `json:"postcode" nullable:"true" doc:"The postcode; null when not mapped."`
	Place        *string  `json:"place" nullable:"true" doc:"The town; null when not mapped."`
	Location     Position `json:"location"`
	DistanceM    *int     `json:"distanceM" nullable:"true" doc:"Distance from at in metres; null when no position was asked."`
	DataAsOf     *string  `json:"dataAsOf" nullable:"true" doc:"The day of the OpenStreetMap extract (YYYY-MM-DD)."`
	SourceFamily string   `json:"sourceFamily"`
}

// PoiPage is the page of a places-of-interest answer.
type PoiPage struct {
	Limit      int     `json:"limit"`
	Total      int     `json:"total" doc:"Matches, at most 50."`
	NextCursor *string `json:"nextCursor" nullable:"true" doc:"The cursor of the next page; null on the last."`
}

// PoiMeta is the meta of listPois.
type PoiMeta struct {
	DataMeta
	Page PoiPage `json:"page"`
}

// PoiEnvelope is the enveloped answer of listPois.
type PoiEnvelope struct {
	Data  []Poi   `json:"data"`
	Meta  PoiMeta `json:"meta"`
	Links Links   `json:"links"`
}

// PoiOutput is the response of listPois.
type PoiOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         PoiEnvelope
}

func registerPois(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Places of interest by name"
	op.Description = "The places of interest OpenStreetMap maps whose name or brand holds the text: in a town, around a position, or anywhere; around a position the nearest first, else the name spelt as asked first, then one that begins with it. Pages of up to 25, at most 50 in all. Needs the scope places."
	featureResponses(api, &op, "Not used by this operation.")
	op.Extensions[counterpartExtension] = "place of interest search"
	huma.Register(api, op, func(ctx context.Context, in *PoiInput) (*PoiOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := places.PoiQuery{Name: in.Name, In: in.In, At: in.point, RadiusM: in.RadiusM, Category: in.Category, Limit: in.Limit, Offset: in.offset}
		if q.RadiusM == 0 {
			q.RadiusM = poiDefaultRadiusM
		}
		if q.Limit == 0 {
			q.Limit = poiDefaultLimit
		}
		a, err := s.features.Pois(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		return &PoiOutput{CacheControl: noStore, Body: poiEnvelope(s, ctx, a, q)}, nil
	})
}

func poiEnvelope(s *server, ctx context.Context, a features.PoiAnswer, q places.PoiQuery) PoiEnvelope {
	data := make([]Poi, len(a.Pois))
	for i, p := range a.Pois {
		data[i] = Poi{Name: p.Name, Category: p.Category, Kind: p.CategoryValue, Brand: p.Brand, Operator: p.Operator,
			Street: p.Street, Number: p.HouseNumber, Postcode: p.Postcode, Place: p.Place, Location: position(p.Location),
			DistanceM: p.DistanceM, DataAsOf: day(p.DataAsOf), SourceFamily: p.Family}
	}
	page := PoiPage{Limit: q.Limit, Total: a.Total}
	if end := q.Offset + len(a.Pois); end < a.Total {
		next := strconv.Itoa(end)
		page.NextCursor = &next
	}
	return PoiEnvelope{Data: data, Meta: PoiMeta{DataMeta: s.dataMeta(ctx, a.Meta.Families, a.Meta.At, a.Meta.Partial, partialDetail), Page: page},
		Links: s.selfLink(ctx)}
}
