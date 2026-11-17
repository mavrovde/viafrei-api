package httpapi

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
)

// The on-demand fuel refresh (G8): the site forwards a person's view of a
// fuel card, and the API forwards it to the refresher that holds the
// provider key (fuel.refresh_url), relaying its answer as the platform
// gives it today.

// FuelRefreshIn is the ask, as the site sends it.
type FuelRefreshIn struct {
	At     string `json:"at" maxLength:"40" pattern:"^(ags:[0-9]{8}|pt:-?[0-9]{1,2}(\\.[0-9]{1,6})?,-?[0-9]{1,3}(\\.[0-9]{1,6})?)$" example:"ags:09162000" doc:"The area: a Gemeinde's eight-digit AGS (ags:09162000) or a point inside Germany of at most six decimals (pt:48.137,11.575)."`
	Snap   bool   `json:"snap,omitempty" required:"false" doc:"Answer the point as its nearest Gemeinde's centroid, as a place card is built; default false. Not with anchor." example:"false"`
	Anchor bool   `json:"anchor,omitempty" required:"false" doc:"The point is a motorway page's public anchor, refreshed about as it is; default false. Not with snap." example:"false"`
}

// FuelRefreshInput is the input of requestFuelRefresh.
type FuelRefreshInput struct {
	Body FuelRefreshIn
}

// Resolve refuses an ask the platform would not read: a point outside
// Germany's box, or both snap and anchor (huma calls it after the schema
// checks).
func (in *FuelRefreshInput) Resolve(huma.Context) []error {
	switch err := in.ask().Validate(); {
	case errors.Is(err, energy.ErrRefreshMode):
		return []error{&huma.ErrorDetail{Location: "body.anchor", Message: err.Error(), Value: in.Body.Anchor}}
	case err != nil:
		return []error{&huma.ErrorDetail{Location: "body.at", Message: err.Error(), Value: in.Body.At}}
	}
	return nil
}

func (in *FuelRefreshInput) ask() energy.RefreshAsk {
	return energy.RefreshAsk{At: in.Body.At, Snap: in.Body.Snap, Anchor: in.Body.Anchor}
}

// FuelRefreshQueue is where a pending ask stands.
type FuelRefreshQueue struct {
	Position   int `json:"position" minimum:"0" doc:"1 is next; 0 while it runs."`
	EtaSeconds int `json:"etaSeconds" minimum:"0" doc:"About how long until it starts, in whole seconds (rounded up)."`
}

// FuelRefreshData is the outcome of the ask.
type FuelRefreshData struct {
	State     string            `json:"state" enum:"refreshed,pending,failed,paused,none_due,refused" doc:"refreshed: the prices were refreshed (settledAt says when; read the fuel answer again). pending: queued or running, its rows stored when it settles (queue, when given, says where; ask again later). failed: the provider did not answer. paused: refreshing is paused. none_due: every price of the area is inside its cache window. refused: no refresh is made (reason, when given, says why)."`
	SettledAt *time.Time        `json:"settledAt" nullable:"true" doc:"When the ask settled; null while it has not."`
	Reused    bool              `json:"reused" doc:"The answer is the area's remembered outcome: no request or read was made."`
	Queue     *FuelRefreshQueue `json:"queue" doc:"On pending, where the ask stands in the queue of person asks; null otherwise or when the refresher did not say."`
	Reason    *string           `json:"reason" nullable:"true" doc:"Why a refusal was made, when known: refresh not configured on an instance with no refresher."`
}

// FuelRefreshMeta is the meta member of requestFuelRefresh.
type FuelRefreshMeta struct {
	ResponseMeta
}

// FuelRefreshEnvelope is the enveloped answer of requestFuelRefresh.
type FuelRefreshEnvelope struct {
	Data  FuelRefreshData `json:"data"`
	Meta  FuelRefreshMeta `json:"meta"`
	Links Links           `json:"links"`
}

// FuelRefreshOutput is the response of requestFuelRefresh.
type FuelRefreshOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         FuelRefreshEnvelope
}

func fuelRefreshData(a energy.RefreshAnswer) FuelRefreshData {
	out := FuelRefreshData{State: string(a.State), Reused: a.Reused}
	if a.SettledAt != nil {
		t := a.SettledAt.UTC()
		out.SettledAt = &t
	}
	if q := a.Queue; q != nil {
		out.Queue = &FuelRefreshQueue{Position: q.Position, EtaSeconds: int(math.Ceil(q.ETA.Seconds()))}
	}
	if a.Reason != "" {
		r := a.Reason
		out.Reason = &r
	}
	return out
}

func registerFuelRefresh(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Refresh the fuel prices about an area on a person's view"
	op.Description = "Forwards one person's view of a fuel card to the refresher that holds the provider key, as the platform's site asks it today: the refresher refreshes the prices about the area under its own guards and stores them, and the fuel answers serve them. Only a key of ViaFrei's own audience may call it; any other key gets 403 (scope-denied). The refresher is given about 1.2 s (fuel.refresh_deadline): an ask it has not answered by then is pending. An instance with no refresher (fuel.refresh_url unset) answers refused with the reason refresh not configured. An area the refresher does not know (no such Gemeinde) is a 400 naming body.at; a refresher that cannot answer is a 503 (upstream-unavailable)."
	featureResponses(api, &op, "Not used by this operation.")
	huma.Register(api, op, func(ctx context.Context, in *FuelRefreshInput) (*FuelRefreshOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		a, err := s.features.FuelRefresh(ctx, principalFrom(ctx).Key, in.ask())
		switch {
		case err == nil:
			return &FuelRefreshOutput{CacheControl: noStore, Body: FuelRefreshEnvelope{
				Data:  fuelRefreshData(a),
				Meta:  FuelRefreshMeta{ResponseMeta: s.envelopeMeta(ctx)},
				Links: s.selfLink(ctx),
			}}, nil
		case errors.Is(err, features.ErrOwnKeyOnly):
			return nil, newProblem(ctx, SlugScopeDenied, "Only ViaFrei's own keys may ask for a fuel refresh.", 0)
		case errors.Is(err, energy.ErrRefreshArea), errors.Is(err, energy.ErrRefreshAt), errors.Is(err, energy.ErrRefreshMode):
			p := newProblem(ctx, SlugInvalidParameter, "The refresher does not know this area; see errors.", 0)
			p.Errors = append(p.Errors, ParamError{Parameter: "body.at", Code: "invalid", Detail: err.Error()})
			return nil, p
		default:
			return nil, newProblem(ctx, SlugUpstreamUnavailable, "The fuel refresher did not answer; ask again.", 1)
		}
	})
}
