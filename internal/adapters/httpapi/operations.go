package httpapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/health"
)

const noStore = "no-store"

// HealthBody is the liveness answer.
type HealthBody struct {
	Status string `json:"status" enum:"ok" doc:"Always ok when the process answers."`
}

// HealthOutput is the liveness response.
type HealthOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         HealthBody
}

func registerHealth(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Liveness"
	op.Description = "The process runs and answers. Never touches storage (FR-SVC-004)."
	huma.Register(api, op, func(_ context.Context, _ *struct{}) (*HealthOutput, error) {
		return &HealthOutput{CacheControl: noStore, Body: HealthBody{Status: "ok"}}, nil
	})
}

// ReadinessBody is the readiness answer. It never carries versions,
// addresses or error text.
type ReadinessBody struct {
	Status string            `json:"status" enum:"ready,not_ready" doc:"ready when every check passes."`
	Checks map[string]string `json:"checks,omitempty" doc:"Per check: pass or fail. Present when not ready."`
}

// ReadinessOutput is the readiness response.
type ReadinessOutput struct {
	Status       int
	CacheControl string `header:"Cache-Control"`
	Body         ReadinessBody
}

func registerReadiness(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Readiness"
	op.Description = "Storage answers within the readiness timeout and publishes a compatible read contract (FR-SVC-004)."
	op.Responses = map[string]*huma.Response{
		"503": {Description: "Not ready: a check failed."},
	}
	huma.Register(api, op, func(ctx context.Context, _ *struct{}) (*ReadinessOutput, error) {
		out := &ReadinessOutput{CacheControl: noStore}
		var res health.Result
		if s.ready != nil {
			res = s.ready.Check(ctx)
		}
		if res.Ready {
			out.Status = http.StatusOK
			out.Body = ReadinessBody{Status: "ready"}
			return out, nil
		}
		if res.Checks == nil {
			res.Checks = map[string]string{health.CheckStorage: health.Fail}
		}
		out.Status = http.StatusServiceUnavailable
		out.Body = ReadinessBody{Status: "not_ready", Checks: res.Checks}
		return out, nil
	})
}

// MetaData describes the running service (FR-SVC-003). It contains no secret,
// hostname or address.
type MetaData struct {
	APIVersion      string   `json:"apiVersion" doc:"Version of this build of the service."`
	BuildSha        string   `json:"buildSha" doc:"Source revision the build was made from."`
	ContractVersion string   `json:"contractVersion" doc:"Read-contract version (major.minor) the service is built for."`
	PlatformVersion *string  `json:"platformVersion" nullable:"true" doc:"Platform version the service is kept in step with; null until version sync exists."`
	OpenAPI         string   `json:"openapi" format:"uri" doc:"URL of the OpenAPI document."`
	Languages       []string `json:"languages" doc:"Languages the service answers in."`
}

// MetaEnvelope is the enveloped meta answer (ENV-001).
type MetaEnvelope struct {
	Data  MetaData     `json:"data"`
	Meta  ResponseMeta `json:"meta"`
	Links Links        `json:"links"`
}

// MetaOutput is the meta response.
type MetaOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         MetaEnvelope
}

func registerMeta(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Service metadata"
	op.Description = "The API version, the build revision and the read-contract version."
	huma.Register(api, op, func(ctx context.Context, _ *struct{}) (*MetaOutput, error) {
		return &MetaOutput{
			CacheControl: noStore,
			Body: MetaEnvelope{
				Data: MetaData{
					APIVersion:      s.build.Version,
					BuildSha:        s.build.Commit,
					ContractVersion: s.contractVersion,
					PlatformVersion: nil,
					OpenAPI:         s.cfg.Server.PublicBaseURL + "/v1/openapi.json",
					Languages:       []string{defaultLanguage},
				},
				Meta:  s.envelopeMeta(ctx),
				Links: s.selfLink(ctx),
			},
		}, nil
	})
}
