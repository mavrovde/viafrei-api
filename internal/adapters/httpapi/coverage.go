package httpapi

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// PlaceKindCount is how many places of one kind one family holds.
type PlaceKindCount struct {
	Kind         string `json:"kind" doc:"The place kind (city, town, village, suburb, admin, station, junction, ...). Open enumeration."`
	Count        int    `json:"count"`
	EnglishNames int    `json:"englishNames" doc:"How many of them carry an English name, the names a search in English finds."`
	// NewestLoad is when the newest place of the kind was last loaded.
	NewestLoad   *time.Time `json:"newestLoad" nullable:"true" doc:"When the newest place of this kind and family was last loaded (UTC); null when unknown."`
	SourceFamily string     `json:"sourceFamily" doc:"Its family; when the family was last loaded is in meta.freshness."`
}

// CoverageLand is a Land the gazetteer holds.
type CoverageLand struct {
	Code         string `json:"code" doc:"The Land's two-digit official key."`
	Name         string `json:"name"`
	SourceFamily string `json:"sourceFamily"`
}

// AddressCoverage is a Land the addresses cover.
type AddressCoverage struct {
	Land         *string    `json:"land" nullable:"true" doc:"The Land's name as the address source writes it; null for the addresses placed in no Land."`
	DataAsOf     *time.Time `json:"dataAsOf" nullable:"true" doc:"The extract date of its newest address; null when unknown."`
	SourceFamily string     `json:"sourceFamily"`
}

// PlacesCoverageData is what the gazetteer and the addresses hold.
type PlacesCoverageData struct {
	Kinds     []PlaceKindCount  `json:"kinds" doc:"The places per family and kind."`
	Lands     []CoverageLand    `json:"lands" doc:"The Länder the gazetteer holds, by key."`
	Addresses []AddressCoverage `json:"addresses" doc:"The Länder the addresses cover, by name, each with its extract date; the addresses placed in no Land last."`
}

// PlacesCoverageEnvelope is the enveloped answer of getPlacesCoverage.
type PlacesCoverageEnvelope struct {
	Data  PlacesCoverageData `json:"data"`
	Meta  DataMeta           `json:"meta"`
	Links Links              `json:"links"`
}

// PlacesCoverageOutput is the response of getPlacesCoverage.
type PlacesCoverageOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         PlacesCoverageEnvelope
}

func registerPlacesCoverage(api huma.API, s *server, op huma.Operation) {
	op.Summary = "What the gazetteer and the addresses hold"
	op.Description = "What place and address search can find: the places per family and kind with how many carry an English name (each family's last load is in meta.freshness), the Länder the gazetteer holds, and the Länder the addresses cover with each one's extract date. Read from the places families the key may read. Needs the scope places."
	featureResponses(api, &op, featureOffDoc)
	huma.Register(api, op, func(ctx context.Context, _ *struct{}) (*PlacesCoverageOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		c, a, err := s.features.PlacesCoverage(ctx, principalFrom(ctx).Key)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		d := PlacesCoverageData{Kinds: []PlaceKindCount{}, Lands: []CoverageLand{}, Addresses: []AddressCoverage{}}
		for _, k := range c.Kinds {
			d.Kinds = append(d.Kinds, PlaceKindCount{Kind: k.Kind, Count: k.Rows, EnglishNames: k.EnglishNames, NewestLoad: utc(k.NewestLoad), SourceFamily: k.Family})
		}
		for _, l := range c.Lands {
			d.Lands = append(d.Lands, CoverageLand{Code: l.Code, Name: l.Name, SourceFamily: l.Family})
		}
		for _, x := range c.Addresses {
			d.Addresses = append(d.Addresses, AddressCoverage{Land: x.Land, DataAsOf: x.DataAsOf, SourceFamily: x.Family})
		}
		return &PlacesCoverageOutput{CacheControl: noStore, Body: PlacesCoverageEnvelope{Data: d,
			Meta: s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail), Links: s.selfLink(ctx)}}, nil
	})
}
