package httpapi

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/refresh"
)

// The on-demand refreshes of live sources (G8): a person's view asks for a
// refresh of one subject of a live source (createRefresh); the API records
// it, the live refresher refreshes it once for everyone asking about that
// subject, and the asker reads the outcome here (getRefresh) and the data
// under the source's own resources. A refresh carries no provider data.

// RefreshIn is a refresh ask.
type RefreshIn struct {
	Source  string         `json:"source" minLength:"1" maxLength:"64" example:"fuel-prices-de" doc:"The live source: an id of the instance's registry (refresh.sources_file, the live refresher's registry), fuel-prices-de and train-board-de on ViaFrei's own."`
	Subject map[string]any `json:"subject" example:"{\"at\":\"ags:09162000\"}" doc:"What to refresh, as the source's registered JSON Schema admits it: fuel-prices-de takes at (ags:<8 digits> or pt:lat,lon) with snap or anchor; train-board-de takes eva or name, exactly one. Only a public id is stored: a position is resolved, in memory, to the AGS of its Gemeinde, and a station's name to its EVA number; neither the position nor the name is kept or echoed."`
}

// RefreshInput is the input of createRefresh.
type RefreshInput struct {
	Body RefreshIn
}

// RefreshQueue is where an open refresh stands.
type RefreshQueue struct {
	Position int `json:"position" minimum:"0" doc:"1 is next; 0 while it runs."`
}

// RefreshData is a refresh as its askers see it.
type RefreshData struct {
	ID        string        `json:"id" pattern:"^[1-9][0-9]{0,17}$" doc:"The refresh's id, decimal digits: GET /v1/refreshes/{id} reads it again."`
	Source    string        `json:"source"`
	State     string        `json:"state" enum:"pending,refreshed,none_due,refused,failed" doc:"pending: queued or running (queue says where; read it again). refreshed: the subject was refreshed, and its data is served fresh under the source's resources. none_due: its stored data was inside its cache window, so none was needed. refused: no refresh was made (reason, when known, says why). failed: the provider did not answer."`
	CreatedAt time.Time     `json:"createdAt" doc:"When the first asker asked."`
	SettledAt *time.Time    `json:"settledAt" nullable:"true" doc:"When it settled; null while pending."`
	Reason    *string       `json:"reason" nullable:"true" doc:"Why it was refused or failed, when known."`
	Reused    bool          `json:"reused" doc:"True when this answer is a refresh asked before: one settled inside the source's freshness window, an open one this ask joined, or one the live refresher answered from a refresh already under way."`
	Queue     *RefreshQueue `json:"queue" doc:"While pending, where it stands; null once settled."`
}

// RefreshEnvelope is the enveloped refresh.
type RefreshEnvelope struct {
	Data  RefreshData  `json:"data"`
	Meta  ResponseMeta `json:"meta"`
	Links Links        `json:"links"`
}

// RefreshOutput is the answer of createRefresh.
type RefreshOutput struct {
	Status       int
	Location     string `header:"Location" doc:"The refresh's own URL."`
	CacheControl string `header:"Cache-Control"`
	Body         RefreshEnvelope
}

// RefreshGetInput is the input of getRefresh.
type RefreshGetInput struct {
	ID string `path:"id" maxLength:"18" example:"1" doc:"The refresh's id, as createRefresh answered it."`
}

// RefreshGetOutput is the answer of getRefresh.
type RefreshGetOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         RefreshEnvelope
}

var reflectRefreshEnvelope = reflect.TypeFor[RefreshEnvelope]()

func refreshData(r refresh.Request) RefreshData {
	out := RefreshData{ID: r.ID, Source: r.Source, State: string(r.State), CreatedAt: r.CreatedAt.UTC(), Reused: r.Reused}
	if r.SettledAt != nil {
		t := r.SettledAt.UTC()
		out.SettledAt = &t
	}
	if r.Reason != "" {
		reason := r.Reason
		out.Reason = &reason
	}
	if r.Position != nil {
		out.Queue = &RefreshQueue{Position: *r.Position}
	}
	return out
}

func (s *server) refreshPath(id string) string {
	return s.cfg.Server.PublicBaseURL + "/v1/refreshes/" + id
}

func (s *server) refreshEnvelope(ctx context.Context, r refresh.Request) RefreshEnvelope {
	return RefreshEnvelope{Data: refreshData(r), Meta: s.envelopeMeta(ctx), Links: Links{Self: s.refreshPath(r.ID)}}
}

// refreshProblem answers the errors both refresh operations share.
func refreshProblem(ctx context.Context, err error) error {
	var se *refresh.SubjectError
	switch {
	case errors.Is(err, features.ErrOwnKeyOnly):
		return newProblem(ctx, SlugScopeDenied, "Only ViaFrei's own keys may ask for a refresh.", 0)
	case errors.Is(err, features.ErrNoRefreshes):
		return newProblem(ctx, SlugFeatureDisabled, "Refreshes are switched off on this instance.", 0)
	case errors.Is(err, refresh.ErrNotFound):
		return newProblem(ctx, SlugNotFound, "No refresh of this id was asked for by this key.", 0)
	case errors.Is(err, refresh.ErrPaused):
		return newProblem(ctx, SlugFeatureDisabled, "This live source is switched off.", 0)
	case errors.Is(err, refresh.ErrSource):
		p := newProblem(ctx, SlugInvalidParameter, "No live source of this name is registered; see errors.", 0)
		p.Errors = append(p.Errors, ParamError{Parameter: "body.source", Code: "invalid", Detail: err.Error()})
		return p
	case errors.As(err, &se):
		p := newProblem(ctx, SlugInvalidParameter, "The source does not admit this subject; see errors.", 0)
		p.Errors = append(p.Errors, ParamError{Parameter: "body." + se.Location, Code: "invalid", Detail: se.Detail})
		return p
	}
	return err
}

func registerCreateRefresh(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Ask for a refresh of a live source on a person's view"
	op.Description = "Records a refresh of one subject of a live source, as a person's view asks it (the fuel prices about an area, a station's train board): the live refresher refreshes it once for everyone asking about the same subject while it is open, under its own guards, and the source's resources then serve it. Only a key of ViaFrei's own audience may call it; any other key gets 403 (scope-denied). The answer waits up to the source's deadline (its registry's deadline_ms, 1.2 s on ViaFrei's own; refresh.wait when it sets none) for the refresh to settle: settled, it is 201 with the outcome; still open, it is 202 with state pending and its place in the queue; either way Location names the refresh, which GET reads again. A refresh of the subject settled inside the source's freshness window answers at once, with no new refresh and reused true. A position is resolved to the AGS of its Gemeinde before anything is recorded, and only that public id is stored; a position no Gemeinde answers is a 400 naming body.subject.at, with nothing recorded. A switched-off source is a 404 (feature-disabled). An unknown source is a 400 naming body.source; a subject the source refuses is a 400 naming the field (body.subject.at, say). No provider data is ever in this answer."
	op.DefaultStatus = http.StatusCreated
	featureResponses(api, &op, "Not used by this operation.")
	created := op.Responses["201"]
	if created == nil {
		created = &huma.Response{}
		op.Responses["201"] = created
	}
	created.Description = "The refresh settled: refreshed, none_due, refused or failed."
	op.Responses["202"] = &huma.Response{
		Description: "The refresh is still open (pending): read it again at Location.",
		Headers: map[string]*huma.Param{
			"Location":      {Description: "The refresh's own URL.", Schema: &huma.Schema{Type: huma.TypeString}},
			"Cache-Control": {Schema: &huma.Schema{Type: huma.TypeString}},
		},
		Content: map[string]*huma.MediaType{"application/json": {
			Schema: api.OpenAPI().Components.Schemas.Schema(reflectRefreshEnvelope, true, "RefreshEnvelope"),
		}},
	}
	huma.Register(api, op, func(ctx context.Context, in *RefreshInput) (*RefreshOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		r, err := s.features.RequestRefresh(ctx, principalFrom(ctx).Key, refresh.Ask{Source: in.Body.Source, Subject: in.Body.Subject})
		if err != nil {
			return nil, refreshProblem(ctx, err)
		}
		status := http.StatusCreated
		if !r.State.Settled() {
			status = http.StatusAccepted
		}
		return &RefreshOutput{Status: status, Location: s.refreshPath(r.ID), CacheControl: noStore, Body: s.refreshEnvelope(ctx, r)}, nil
	})
}

func registerGetRefresh(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Read a refresh"
	op.Description = "A refresh this key asked for, as it now stands; a refresh of another key, or of no such id, is a 404. Only a key of ViaFrei's own audience may call it; any other key gets 403 (scope-denied). Refreshes are kept 30 days after they were asked, in any state."
	featureResponses(api, &op, "No refresh of this id was asked for by this key (not-found).")
	huma.Register(api, op, func(ctx context.Context, in *RefreshGetInput) (*RefreshGetOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		r, err := s.features.Refresh(ctx, principalFrom(ctx).Key, in.ID)
		if err != nil {
			return nil, refreshProblem(ctx, err)
		}
		return &RefreshGetOutput{CacheControl: noStore, Body: s.refreshEnvelope(ctx, r)}, nil
	})
}
