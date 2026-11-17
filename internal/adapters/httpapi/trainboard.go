package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// The train boards: the MCP server hands over each DB Timetables board it
// fetches live (recordTrainBoard), and the API serves a station's newest one
// while it is fresh (getTrainBoard), DB Timetables alone (CC BY 4.0).

// TrainBoardDepartureIn is one departure of a recorded board.
type TrainBoardDepartureIn struct {
	PlannedAt       time.Time  `json:"plannedAt" doc:"The planned departure time (RFC 3339)." example:"2026-11-17T15:42:00Z"`
	ChangedAt       *time.Time `json:"changedAt,omitempty" doc:"The changed departure time the board stated; absent when none." example:"2026-11-17T15:47:00Z"`
	Platform        *string    `json:"platform,omitempty" minLength:"1" maxLength:"16" doc:"The planned platform." example:"5"`
	ChangedPlatform *string    `json:"changedPlatform,omitempty" minLength:"1" maxLength:"16" doc:"The changed platform the board stated; absent when none." example:"7"`
	Category        *string    `json:"category,omitempty" minLength:"1" maxLength:"16" doc:"The train category (ICE, RE, S)." example:"ICE"`
	Number          *string    `json:"number,omitempty" minLength:"1" maxLength:"16" doc:"The train number." example:"571"`
	Line            *string    `json:"line,omitempty" minLength:"1" maxLength:"32" doc:"The line, when the board names one." example:"ICE 571"`
	Destination     *string    `json:"destination,omitempty" minLength:"1" maxLength:"120" doc:"The final destination as the board names it." example:"München Hbf"`
	Cancelled       bool       `json:"cancelled,omitempty" required:"false" doc:"Whether the board stated the departure cancelled; default false." example:"false"`
}

// TrainBoardIn is a board the MCP server fetched live from DB Timetables.
type TrainBoardIn struct {
	EVA        string                  `json:"eva" pattern:"^[0-9]{7}$" doc:"The station's EVA number the board was fetched for." example:"8000105"`
	FetchedAt  time.Time               `json:"fetchedAt" doc:"When the board was fetched from DB Timetables (RFC 3339); within the last day, not in the future." example:"2026-11-17T15:30:12Z"`
	Departures []TrainBoardDepartureIn `json:"departures" maxItems:"120" doc:"The board's departures, at most 120; empty when the board listed none."`
}

// RecordTrainBoardInput is the input of recordTrainBoard.
type RecordTrainBoardInput struct {
	Body TrainBoardIn
}

// RecordTrainBoardOutput is the response of recordTrainBoard: no body.
type RecordTrainBoardOutput struct {
	Status       int
	CacheControl string `header:"Cache-Control"`
}

func registerRecordTrainBoard(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Record a DB Timetables board the MCP server fetched"
	op.Description = "Appends one station's DB Timetables departure board as the MCP server fetched it live: one stored row per departure, kept as ticks, CC BY 4.0 with DB's attribution. Only a key of ViaFrei's own audience may call it; any other key gets 403 (scope-denied). A board breaking a rule gets 400 naming the field. Rate-limited per key like every keyed operation. Answers 204."
	op.DefaultStatus = http.StatusNoContent
	featureResponses(api, &op, "Not used by this operation.")
	huma.Register(api, op, func(ctx context.Context, in *RecordTrainBoardInput) (*RecordTrainBoardOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		b := transit.TrainBoard{EVA: in.Body.EVA, FetchedAt: in.Body.FetchedAt, Departures: make([]transit.TrainDeparture, len(in.Body.Departures))}
		for i, d := range in.Body.Departures {
			b.Departures[i] = transit.TrainDeparture{PlannedAt: d.PlannedAt, ChangedAt: d.ChangedAt, Platform: d.Platform, ChangedPlatform: d.ChangedPlatform,
				Category: d.Category, Number: d.Number, Line: d.Line, Destination: d.Destination, Cancelled: d.Cancelled}
		}
		err := s.features.RecordTrainBoard(ctx, principalFrom(ctx).Key, b)
		var fe *transit.BoardFieldError
		switch {
		case err == nil:
			return &RecordTrainBoardOutput{Status: http.StatusNoContent, CacheControl: noStore}, nil
		case errors.Is(err, features.ErrOwnKeyOnly):
			return nil, newProblem(ctx, SlugScopeDenied, "Only ViaFrei's own keys may record a board.", 0)
		case errors.Is(err, features.ErrNoWriter):
			return nil, newProblem(ctx, SlugFeatureDisabled, "Recording boards is switched off on this instance.", 0)
		case errors.As(err, &fe):
			p := newProblem(ctx, SlugInvalidParameter, "The board breaks a rule; see errors.", 0)
			p.Errors = append(p.Errors, ParamError{Parameter: "body." + fe.Field, Code: "invalid", Detail: fe.Detail})
			return nil, p
		default:
			return nil, s.storageProblem(ctx, err)
		}
	})
}

// TrainBoardInput is the input of getTrainBoard.
type TrainBoardInput struct {
	EVA string `path:"eva" pattern:"^[0-9]{7}$" example:"8000105" doc:"The station's EVA number."`
}

// TrainBoardDeparture is one departure of a served board, as the MCP
// server's train departures show it.
type TrainBoardDeparture struct {
	Line            *string   `json:"line" nullable:"true" doc:"The line, when the board names one."`
	Category        *string   `json:"category" nullable:"true" doc:"The train category."`
	Number          *string   `json:"number" nullable:"true" doc:"The train number."`
	Destination     *string   `json:"destination" nullable:"true" doc:"The final destination; null when the board names none."`
	PlannedAt       time.Time `json:"plannedAt"`
	ExpectedAt      time.Time `json:"expectedAt" doc:"The changed time where the board stated one, else the planned time."`
	DelayMin        int       `json:"delayMin" doc:"expectedAt less plannedAt, in whole minutes."`
	Platform        *string   `json:"platform" nullable:"true" doc:"The platform it leaves from: the changed one where the board stated one."`
	PlannedPlatform *string   `json:"plannedPlatform" nullable:"true" doc:"The planned platform."`
	PlatformChanged bool      `json:"platformChanged"`
	Cancelled       bool      `json:"cancelled"`
	SourceFamily    string    `json:"sourceFamily"`
}

// TrainBoardData is the data of getTrainBoard.
type TrainBoardData struct {
	EVA        string                `json:"eva"`
	Fresh      bool                  `json:"fresh" doc:"Whether a board fetched no longer ago than the MCP server's cache lifetime (30 s) is held: false is no fresh board."`
	FetchedAt  *time.Time            `json:"fetchedAt" nullable:"true" doc:"When the newest board held was fetched, fresh or not; null when none is held."`
	AgeSeconds *int                  `json:"ageSeconds" nullable:"true" doc:"How long ago that was, in seconds; null when none is held."`
	Departures []TrainBoardDeparture `json:"departures" doc:"The fresh board's departures by planned time; empty without a fresh board."`
}

// TrainBoardEnvelope is the enveloped answer of getTrainBoard.
type TrainBoardEnvelope struct {
	Data  TrainBoardData `json:"data"`
	Meta  DataMeta       `json:"meta"`
	Links Links          `json:"links"`
}

// TrainBoardOutput is the response of getTrainBoard.
type TrainBoardOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         TrainBoardEnvelope
}

func registerTrainBoard(api huma.API, s *server, op huma.Operation) {
	op.Summary = "A station's DB Timetables board, while fresh"
	op.Description = "The newest DB Timetables departure board the MCP server fetched for the station, while it was fetched no longer ago than the MCP server's own cache lifetime (30 s): DB Timetables alone, CC BY 4.0, never blended with a share-alike source. Without one, fresh is false and the answer states when the newest board held was fetched. Needs the scope transit."
	featureResponses(api, &op, "Not used by this operation.")
	op.Extensions[counterpartExtension] = "train departures"
	huma.Register(api, op, func(ctx context.Context, in *TrainBoardInput) (*TrainBoardOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		a, err := s.features.TrainBoard(ctx, principalFrom(ctx).Key, in.EVA)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		data := TrainBoardData{EVA: in.EVA, Departures: []TrainBoardDeparture{}}
		if a.AsOf != nil {
			at := a.AsOf.UTC()
			age := int(s.now().Sub(at) / time.Second)
			data.FetchedAt, data.AgeSeconds = &at, &age
		}
		if b := a.Board; b != nil {
			data.Fresh = true
			for _, d := range b.Departures {
				platform := d.Platform
				if d.ChangedPlatform != nil {
					platform = d.ChangedPlatform
				}
				data.Departures = append(data.Departures, TrainBoardDeparture{Line: d.Line, Category: d.Category, Number: d.Number, Destination: d.Destination,
					PlannedAt: d.PlannedAt.UTC(), ExpectedAt: d.ExpectedAt().UTC(), DelayMin: d.DelayMin(), Platform: platform, PlannedPlatform: d.Platform,
					PlatformChanged: d.PlatformChanged(), Cancelled: d.Cancelled, SourceFamily: transit.TrainBoardFamily})
			}
		}
		return &TrainBoardOutput{CacheControl: noStore, Body: TrainBoardEnvelope{Data: data,
			Meta: s.dataMeta(ctx, a.Meta.Families, a.Meta.At, a.Meta.Partial, partialDetail), Links: s.selfLink(ctx)}}, nil
	})
}
