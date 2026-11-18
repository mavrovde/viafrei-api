package httpapi

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// StateInput names one Land.
type StateInput struct {
	State string `path:"state" pattern:"^(0[1-9]|1[0-6])$" example:"09" doc:"The Land's official code, 01 to 16 (09 Bayern), as listPlaces or describeLocation give it."`
}

// ------------------------------------------------------- weather warnings

// StateWarning is one event of the official warnings in force in a Land.
type StateWarning struct {
	Event   *string    `json:"event" nullable:"true" doc:"The publisher's event name (for the DWD in German capitals, e.g. STURMBÖEN); null when the warning names none."`
	Level   int        `json:"level" minimum:"1" maximum:"4" doc:"The highest level of the event's warnings in the Land."`
	Notices int        `json:"notices" doc:"How many warnings of the event are in force (by the publisher's identifier)."`
	Cells   int        `json:"cells" doc:"How many warn cells of the Land they cover."`
	Until   *time.Time `json:"until" nullable:"true" doc:"The latest end of the event's warnings (UTC); null when none states one."`
}

// StateWarningsData is the answer of listStateWeatherWarnings.
type StateWarningsData struct {
	State  string         `json:"state" doc:"The Land's code."`
	Events []StateWarning `json:"events" doc:"One per event, worst first, then by event."`
}

// StateWarningsEnvelope is the enveloped answer of listStateWeatherWarnings.
type StateWarningsEnvelope struct {
	Data  StateWarningsData `json:"data"`
	Meta  DataMeta          `json:"meta"`
	Links Links             `json:"links"`
}

// StateWarningsOutput is the response of listStateWeatherWarnings.
type StateWarningsOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         StateWarningsEnvelope
}

func registerStateWarnings(api huma.API, s *server, op huma.Operation) {
	op.Summary = "The official weather warnings in force in one Land"
	op.Description = "The warnings in force in one Land at level 1 or above, one per event, as the site's Land page lists them: the highest level, how many warnings and how many warn cells. A warning is in the Land of its warn cell's own key (a municipality cell or a coast or district cell, digits 2 and 3 the Land); no warning text is given. Needs the scope weather."
	featureResponses(api, &op, featureOffDoc)
	huma.Register(api, op, func(ctx context.Context, in *StateInput) (*StateWarningsOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		v, a, err := s.features.StateWarnings(ctx, principalFrom(ctx).Key, in.State)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		events := make([]StateWarning, len(v))
		for i, w := range v {
			events[i] = StateWarning{Event: w.Event, Level: w.Level, Notices: w.Notices, Cells: w.Cells, Until: utc(w.Until)}
		}
		return &StateWarningsOutput{CacheControl: noStore, Body: StateWarningsEnvelope{
			Data: StateWarningsData{State: in.State, Events: events},
			Meta: s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail), Links: s.selfLink(ctx)}}, nil
	})
}

// --------------------------------------------------------- service alerts

// StateAlertsInput is the input of listStateServiceAlerts.
type StateAlertsInput struct {
	StateInput
	Limit int `query:"limit" minimum:"1" maximum:"100" doc:"Most messages; default 5 (the site's Land page names 5)."`
}

// StateAlert is one message of a Land's networks.
type StateAlert struct {
	Header         string     `json:"header" doc:"The publisher's headline, verbatim (usually German); the primary source's wording where it carries the message."`
	Lines          []string   `json:"lines" doc:"Every line the message names, in natural order."`
	Until          *time.Time `json:"until" nullable:"true" doc:"When the message ends (UTC); null when it states no end."`
	SourceFamilies []string   `json:"sourceFamilies" doc:"Every family that carries the message, sorted; each is credited in meta.attribution."`
}

// StateAlertsData is the answer of listStateServiceAlerts.
type StateAlertsData struct {
	State  string       `json:"state" doc:"The Land's code."`
	Alerts []StateAlert `json:"alerts" doc:"One per message, the messages naming most lines first, then by headline in German order."`
	Total  int          `json:"total" doc:"Messages in force in the Land before the limit."`
}

// StateAlertsEnvelope is the enveloped answer of listStateServiceAlerts.
type StateAlertsEnvelope struct {
	Data  StateAlertsData `json:"data"`
	Meta  DataMeta        `json:"meta"`
	Links Links           `json:"links"`
}

// StateAlertsOutput is the response of listStateServiceAlerts.
type StateAlertsOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         StateAlertsEnvelope
}

// stateAlertsDefault is how many messages a Land's answer names unasked: the
// site's Land page names 5.
const stateAlertsDefault = 5

func registerStateAlerts(api huma.API, s *server, op huma.Operation) {
	op.Summary = "The public-transport service alerts in force in one Land"
	op.Description = "The service alerts in force that name a stop in one Land, one per message, as the site's Land page tells them: a message carried by several sources is one, with the primary source's headline, every line it names and every family that carries it. A message is in each Land a district of its stops lies in; one naming no stop is in none. Needs the scope transit."
	featureResponses(api, &op, featureOffDoc)
	huma.Register(api, op, func(ctx context.Context, in *StateAlertsInput) (*StateAlertsOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		limit := in.Limit
		if limit == 0 {
			limit = stateAlertsDefault
		}
		a, err := s.features.StateAlerts(ctx, principalFrom(ctx).Key, in.State, limit)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		alerts := make([]StateAlert, len(a.Alerts))
		for i, x := range a.Alerts {
			alerts[i] = StateAlert{Header: x.Header, Lines: x.Lines, Until: utc(x.Until), SourceFamilies: x.CarriedBy}
		}
		return &StateAlertsOutput{CacheControl: noStore, Body: StateAlertsEnvelope{
			Data: StateAlertsData{State: in.State, Alerts: alerts, Total: a.Total},
			Meta: s.dataMeta(ctx, a.Meta.Families, a.Meta.At, a.Meta.Partial, partialDetail), Links: s.selfLink(ctx)}}, nil
	})
}
