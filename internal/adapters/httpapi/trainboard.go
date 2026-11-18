package httpapi

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// The train boards: the live refresher stores each DB Timetables board it
// fetches (on a person's ask through createRefresh, source train_board),
// and the API serves a station's newest one while it is
// fresh (getTrainBoard), DB Timetables alone (CC BY 4.0).

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
	op.Description = "The newest DB Timetables departure board the live refresher fetched for the station, while it was fetched no longer ago than the MCP server's own cache lifetime (30 s): DB Timetables alone, CC BY 4.0, never blended with a share-alike source. Without one, fresh is false and the answer states when the newest board held was fetched. Needs the scope transit."
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
