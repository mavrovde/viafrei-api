package httpapi

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
)

// AffectedInput is the input of listAffectedStations.
type AffectedInput struct {
	Limit int `query:"limit" minimum:"1" maximum:"50" doc:"Most stations; default 10 (the site's board names 5, 10 on a wide screen)."`
}

// AffectedStation is a station and what affects it now.
type AffectedStation struct {
	Station
	Cancelled int `json:"cancelled" doc:"Its departures planned in the next 60 minutes that are cancelled."`
	Late      int `json:"late" doc:"Its departures planned in the next 60 minutes 5 minutes late or more, not cancelled."`
	Alerts    int `json:"alerts" doc:"Service alerts in force that name it."`
	LiftsOut  int `json:"liftsOut" doc:"Its lifts and escalators out of service."`
}

// AffectedEnvelope is the enveloped answer of listAffectedStations.
type AffectedEnvelope struct {
	Data  []AffectedStation `json:"data"`
	Meta  DataMeta          `json:"meta"`
	Links Links             `json:"links"`
}

// AffectedOutput is the response of listAffectedStations.
type AffectedOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         AffectedEnvelope
}

func registerAffected(api huma.API, s *server, op huma.Operation) {
	op.Summary = "The stations most affected now"
	op.Description = "The stations ranked by what affects them now (the site's station board): departures of the next 60 minutes cancelled first, then late ones, then service alerts in force, then lifts and escalators out of service; a station with none is not listed. Needs the scope transit."
	featureResponses(api, &op, featureOffDoc)
	huma.Register(api, op, func(ctx context.Context, in *AffectedInput) (*AffectedOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		limit := in.Limit
		if limit == 0 {
			limit = 10
		}
		v, a, err := s.features.AffectedStations(ctx, principalFrom(ctx).Key, limit)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		out := make([]AffectedStation, len(v))
		for i, x := range v {
			out[i] = AffectedStation{Station: station(x.Station), Cancelled: x.Cancelled, Late: x.Late, Alerts: x.Alerts, LiftsOut: x.LiftsOut}
		}
		return &AffectedOutput{CacheControl: noStore, Body: AffectedEnvelope{Data: out,
			Meta: s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail), Links: s.selfLink(ctx)}}, nil
	})
}
