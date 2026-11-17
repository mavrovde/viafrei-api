package httpapi

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// SourcesInput is the input of listSources.
type SourcesInput struct {
	Domain string `query:"domain" maxLength:"32" pattern:"^[a-z_]+$" example:"roads" doc:"Only the sources of this domain (roads, energy, parking, transit, weather, places, addresses, rules, ...); open enumeration. Without it, every domain."`
}

// SourceRow is one source the key may read.
type SourceRow struct {
	Family          string     `json:"family"`
	Domain          string     `json:"domain"`
	Publisher       string     `json:"publisher"`
	AttributionText string     `json:"attribution" doc:"The attribution text: show it with any data of the source."`
	Licence         string     `json:"licence"`
	LicenceURL      string     `json:"licenceUrl" format:"uri"`
	ShareAlike      bool       `json:"shareAlike"`
	TermsURL        *string    `json:"termsUrl" nullable:"true"`
	DataAsOf        *time.Time `json:"dataAsOf" nullable:"true" doc:"The instant up to which the source's data was last known current; null when never."`
	StaleAfterS     *int64     `json:"staleAfterS,omitempty" doc:"Its design threshold; absent for a source with no fixed interval."`
	Status          string     `json:"status" enum:"green,red,grey" doc:"green: inside its freshness window (or no fixed interval and some data); red: stale; grey: never known current."`
}

// SourcesEnvelope is the enveloped answer of listSources.
type SourcesEnvelope struct {
	Data  []SourceRow `json:"data"`
	Meta  DataMeta    `json:"meta"`
	Links Links       `json:"links"`
}

// SourcesOutput is the response of listSources.
type SourcesOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         SourcesEnvelope
}

func registerSources(api huma.API, s *server, op huma.Operation) {
	op.Summary = "The sources this key may read: attribution, licence, freshness"
	op.Description = "Every source family the key may read, of one domain or all: its publisher, attribution text, licence and terms, and whether its data is still arriving (green inside its freshness window, red stale, grey never known current). The platform's attribution, coverage and source-status resources. By family."
	featureResponses(api, &op, featureOffDoc)
	huma.Register(api, op, func(ctx context.Context, in *SourcesInput) (*SourcesOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		fams, a, err := s.features.Sources(ctx, principalFrom(ctx).Key, in.Domain)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		out := make([]SourceRow, 0, len(fams))
		for _, f := range fams {
			r := SourceRow{Family: f.Source.Family, Domain: f.Source.Domain, Publisher: f.Source.Publisher, AttributionText: f.Source.AttributionText,
				Licence: f.Source.Licence, LicenceURL: f.Source.LicenceURL, ShareAlike: f.Source.ShareAlike, TermsURL: f.Source.TermsURL,
				DataAsOf: f.DataAsOf, Status: "grey"}
			if !f.NeverStale {
				sec := int64(f.StaleAfter / time.Second)
				r.StaleAfterS = &sec
			}
			if f.DataAsOf != nil {
				r.Status = "green"
				if !f.NeverStale && a.At.Sub(*f.DataAsOf) > f.StaleAfter {
					r.Status = "red"
				}
			}
			out = append(out, r)
		}
		return &SourcesOutput{CacheControl: noStore, Body: SourcesEnvelope{Data: out,
			Meta: s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail), Links: s.selfLink(ctx)}}, nil
	})
}
