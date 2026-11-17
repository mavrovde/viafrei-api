package httpapi

import (
	"context"

	"github.com/danielgtaylor/huma/v2"
)

// Pressure reports whether the service's reads are saturated: every slot
// taken and a read waiting for one.
type Pressure interface {
	Saturated() bool
}

// PressureData is the answer of getPressure.
type PressureData struct {
	State string `json:"state" enum:"ok,saturated" doc:"saturated while every read slot is taken and a read waits for one; ok otherwise. A client that shows live data can hold back its refreshes while it reads saturated."`
}

// PressureEnvelope is the enveloped answer of getPressure.
type PressureEnvelope struct {
	Data  PressureData `json:"data"`
	Meta  ResponseMeta `json:"meta"`
	Links Links        `json:"links"`
}

// PressureOutput is the response of getPressure.
type PressureOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         PressureEnvelope
}

func registerPressure(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Whether the service is saturated now"
	op.Description = "One word: saturated while every read slot is taken and a read waits for one, ok otherwise (the site's pressure word). It counts slots, never a request, an address or a key."
	huma.Register(api, op, func(ctx context.Context, _ *struct{}) (*PressureOutput, error) {
		state := "ok"
		if s.pressure != nil && s.pressure.Saturated() {
			state = "saturated"
		}
		return &PressureOutput{CacheControl: noStore, Body: PressureEnvelope{Data: PressureData{State: state}, Meta: s.envelopeMeta(ctx), Links: s.selfLink(ctx)}}, nil
	})
}
