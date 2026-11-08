package httpapi

import (
	"context"
	"errors"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/metering"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
)

// SourceReader is the port to the source register of the read contract.
type SourceReader interface {
	// Register returns every family's licence terms.
	Register(ctx context.Context) ([]access.Terms, error)
	// Sources returns the attribution of the allowed families only.
	Sources(ctx context.Context, allowed []string) ([]access.Source, error)
}

// KeyPlan is the plan of the presenting key.
type KeyPlan struct {
	Name     string   `json:"name" doc:"The plan's name."`
	UseClass string   `json:"useClass" enum:"consumer,non_commercial,commercial" doc:"The use the plan is for; source terms permit use classes."`
	Tier     string   `json:"tier" enum:"critical,standard,bulk" doc:"Priority under load."`
	Scopes   []string `json:"scopes" doc:"The feature families the key may call."`
}

// Attribution is one source family the key may receive, with the attribution
// its licence requires.
type Attribution struct {
	Family          string  `json:"family" doc:"The source family id."`
	Domain          string  `json:"domain" doc:"The domain the family answers for."`
	Publisher       string  `json:"publisher" doc:"The publisher, as its licence asks to be named."`
	Text            string  `json:"text" doc:"The attribution notice clients displaying the data must show."`
	Licence         string  `json:"licence" doc:"The licence's name."`
	LicenceURL      string  `json:"licenceUrl" format:"uri" doc:"The licence's canonical URL."`
	ShareAlike      bool    `json:"shareAlike" doc:"True for share-alike data."`
	ShareAlikeOffer *string `json:"shareAlikeOffer" nullable:"true" format:"uri" doc:"Where the derived database is offered under the same licence; set when shareAlike."`
	TermsURL        *string `json:"termsUrl" nullable:"true" format:"uri" doc:"The terms a client must follow or accept, when any."`
}

// KeyData describes the presenting key. It never carries the secret or its
// MAC (SEC-012).
type KeyData struct {
	KeyID       string        `json:"keyId" doc:"The key's public id."`
	Environment string        `json:"environment" enum:"live,test" doc:"The environment the key belongs to."`
	Plan        KeyPlan       `json:"plan"`
	ExpiresAt   time.Time     `json:"expiresAt" doc:"When the key stops working."`
	Sources     []Attribution `json:"sources" doc:"The source families the licence gate lets this key receive, sorted by family."`
}

// Warning is one entry of meta.warnings.
type Warning struct {
	Code   string `json:"code" enum:"key_expiring" doc:"Stable warning code."`
	Detail string `json:"detail" doc:"What the warning means for this answer."`
}

// KeyMeta is the meta member of the key answer.
type KeyMeta struct {
	ResponseMeta
	Warnings []Warning `json:"warnings" doc:"Warnings about this answer; empty when there is none."`
}

// KeyEnvelope is the enveloped key answer.
type KeyEnvelope struct {
	Data  KeyData `json:"data"`
	Meta  KeyMeta `json:"meta"`
	Links Links   `json:"links"`
}

// KeyOutput is the key response.
type KeyOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         KeyEnvelope
}

func registerKey(api huma.API, s *server, op huma.Operation) {
	op.Summary = "The presenting key"
	op.Description = "The key's id, plan, expiry and the source families the licence gate lets it receive, with their attribution. Needs a key, no scope."
	huma.Register(api, op, func(ctx context.Context, _ *struct{}) (*KeyOutput, error) {
		p := principalFrom(ctx)
		attr, err := s.permittedSources(ctx, p.Key)
		if err != nil {
			return nil, err
		}
		k := p.Key
		scopes := make([]string, len(k.Plan.Scopes))
		for i, sc := range k.Plan.Scopes {
			scopes[i] = string(sc)
		}
		meta := KeyMeta{ResponseMeta: s.envelopeMeta(ctx), Warnings: []Warning{}}
		if p.Expiring {
			meta.Warnings = append(meta.Warnings, Warning{Code: "key_expiring", Detail: "The key expires at " + k.ExpiresAt.UTC().Format(time.RFC3339) + "; ask for a new one."})
		}
		return &KeyOutput{
			CacheControl: noStore,
			Body: KeyEnvelope{
				Data: KeyData{
					KeyID: k.ID, Environment: string(k.Environment),
					Plan:      KeyPlan{Name: k.Plan.Name, UseClass: string(k.Plan.UseClass), Tier: k.Plan.Tier, Scopes: scopes},
					ExpiresAt: k.ExpiresAt.UTC(), Sources: attr,
				},
				Meta:  meta,
				Links: s.selfLink(ctx),
			},
		}, nil
	})
}

// permittedSources applies the licence gate to the key: the allowed set is
// computed from the register, handed into the read, and every returned item
// is checked against it again (SEC-030 to SEC-032).
func (s *server) permittedSources(ctx context.Context, k access.Record) ([]Attribution, error) {
	if s.sources == nil {
		return nil, newProblem(ctx, SlugUpstreamUnavailable, "Storage is unavailable.", 1)
	}
	register, err := s.sources.Register(ctx)
	if err != nil {
		return nil, s.storageProblem(ctx, err)
	}
	gate := access.NewGate(register, k.Plan, k.Accepted, s.cfg.Sources.Disabled)
	rows, err := s.sources.Sources(ctx, gate.Allowed())
	if err != nil {
		return nil, s.storageProblem(ctx, err)
	}
	families := make([]string, len(rows))
	for i, r := range rows {
		families[i] = r.Family
	}
	if err := gate.Check(families); err != nil {
		gateViolations.Add(1)
		s.log.ErrorContext(ctx, "licence gate post-check aborted an answer", "requestId", RequestIDFrom(ctx), "error", err)
		return nil, newProblem(ctx, SlugInternal, "An unexpected fault occurred; quote the request id.", 0)
	}
	out := make([]Attribution, len(rows))
	for i, r := range rows {
		out[i] = attribution(r)
	}
	return out, nil
}

// storageProblem maps a storage failure to its problem.
func (s *server) storageProblem(ctx context.Context, err error) *Problem {
	if p, ok := s.overloadedProblem(ctx, err); ok {
		return p
	}
	if errors.Is(err, store.ErrDeadlineExhausted) {
		noteRefusal(ctx, metering.DeadlineExhausted)
		return newProblem(ctx, SlugDeadlineExhausted, "The request ran out of time.", 1)
	}
	return newProblem(ctx, SlugUpstreamUnavailable, "Storage is unavailable.", 1)
}
