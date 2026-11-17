package httpapi

import (
	"context"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// addressDefaultLimit is the platform's: three addresses, more than one
// when the same address carries more than one OpenStreetMap object.
const addressDefaultLimit = 3

// AddressInput is the input of listAddresses.
type AddressInput struct {
	Q     string `query:"q" required:"true" minLength:"5" maxLength:"120" example:"Marktweg 1, 10001 Neustadt" doc:"The address as typed: street and house number, then a postcode or a town or both (\"Hauptstr. 12, 50667 Köln\")."`
	Limit int    `query:"limit" minimum:"1" maximum:"5" doc:"Most addresses to return; default 3."`
	query places.AddressQuery
}

// Resolve reads the address (huma calls it after the schema checks): a
// street and a number with no postcode and no town is refused.
func (a *AddressInput) Resolve(huma.Context) []error {
	q, ok := places.ParseAddress(a.Q)
	if !ok {
		return []error{&huma.ErrorDetail{Location: "query.q", Message: "give a street and a house number with a postcode or a town", Value: a.Q}}
	}
	a.query = q
	return nil
}

// Address is an address of an answer.
type Address struct {
	Street       *string  `json:"street" nullable:"true" doc:"The street; null when not mapped."`
	HouseNumber  *string  `json:"number" nullable:"true" doc:"The house number; null when not mapped."`
	Postcode     *string  `json:"postcode" nullable:"true" doc:"The postcode; null when not mapped."`
	Place        *string  `json:"place" nullable:"true" doc:"The town; null when not mapped."`
	Land         *string  `json:"land" nullable:"true" doc:"The Land; null when not known."`
	Location     Position `json:"location"`
	DataAsOf     *string  `json:"dataAsOf" nullable:"true" doc:"The day of the OpenStreetMap extract (YYYY-MM-DD)."`
	SourceFamily string   `json:"sourceFamily"`
}

// AddressData is the data of listAddresses: the addresses, as the
// platform's address search states them (no identifier, no reading of the
// typed text: docs/parity.md).
type AddressData struct {
	Addresses []Address `json:"addresses"`
}

// AddressEnvelope is the enveloped answer of listAddresses.
type AddressEnvelope struct {
	Data  AddressData `json:"data"`
	Meta  PagedMeta   `json:"meta"`
	Links Links       `json:"links"`
}

// AddressOutput is the response of listAddresses.
type AddressOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         AddressEnvelope
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func registerAddress(api huma.API, s *server, op huma.Operation) {
	op.Summary = "An address"
	op.Description = "The addresses OpenStreetMap maps for a typed street and house number in a postcode or a town, those in the postcode first. Needs the scope places."
	featureResponses(api, &op, "Not used by this operation.")
	op.Extensions[counterpartExtension] = "address search"
	huma.Register(api, op, func(ctx context.Context, in *AddressInput) (*AddressOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := in.query
		q.Limit = in.Limit
		if q.Limit == 0 {
			q.Limit = addressDefaultLimit
		}
		a, err := s.features.Addresses(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		return &AddressOutput{CacheControl: noStore, Body: addressEnvelope(s, ctx, a, q)}, nil
	})
}

func addressEnvelope(s *server, ctx context.Context, a features.AddressAnswer, q places.AddressQuery) AddressEnvelope {
	data := AddressData{Addresses: make([]Address, len(a.Addresses))}
	for i, v := range a.Addresses {
		data.Addresses[i] = Address{Street: v.Street, HouseNumber: v.HouseNumber, Postcode: v.Postcode, Place: v.Place, Land: v.Land,
			Location: position(v.Location), DataAsOf: day(v.DataAsOf), SourceFamily: v.Family}
	}
	return AddressEnvelope{Data: data,
		Meta:  PagedMeta{DataMeta: s.dataMeta(ctx, a.Meta.Families, a.Meta.At, a.Meta.Partial, partialDetail), Page: Page{Limit: q.Limit}},
		Links: s.selfLink(ctx)}
}
