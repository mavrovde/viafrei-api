package httpapi

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// The train boards: the live refresher stores each DB Timetables board it
// fetches (on a person's ask through createRefresh, source train_board),
// the stored-boards worker writes the board of every large station every
// ten minutes, and the API serves a station's board while one is fresh
// (getTrainBoard), DB Timetables alone (CC BY 4.0), with the station's
// U-Bahn and tram under it from the DELFI timetable.

// TrainBoardInput is the input of getTrainBoard.
type TrainBoardInput struct {
	EVA         string `path:"eva" pattern:"^[0-9]{7}$" example:"8000105" doc:"The station's EVA number."`
	Limit       int    `query:"limit" minimum:"1" maximum:"50" default:"5" doc:"How many departures to show, the next to leave first (1 to 50)."`
	DurationMin int    `query:"duration_min" minimum:"5" maximum:"120" default:"60" doc:"How far ahead to look, in minutes (5 to 120)."`
}

// TrainBoardDeparture is one departure of a served board, as the MCP
// server's train departures show it.
type TrainBoardDeparture struct {
	Line            *string   `json:"line" nullable:"true" doc:"The line, when the board names one."`
	Category        *string   `json:"category" nullable:"true" doc:"The train category."`
	Number          *string   `json:"number" nullable:"true" doc:"The train number; null when the board stated none (a stored board states the number in the line)."`
	Destination     *string   `json:"destination" nullable:"true" doc:"The final destination; null when the board names none."`
	PlannedAt       time.Time `json:"plannedAt"`
	ExpectedAt      time.Time `json:"expectedAt" doc:"The changed time where the board stated one, else the planned time."`
	DelayMin        int       `json:"delayMin" doc:"expectedAt less plannedAt, in whole minutes."`
	Platform        *string   `json:"platform" nullable:"true" doc:"The platform it leaves from: the changed one where the board stated one."`
	PlannedPlatform *string   `json:"plannedPlatform" nullable:"true" doc:"The planned platform."`
	PlatformChanged bool      `json:"platformChanged"`
	Cancelled       bool      `json:"cancelled"`
	StationPart     *string   `json:"stationPart" nullable:"true" doc:"The part of the station it leaves from, when the station's EVA numbers name several (Frankfurt (Main) Hbf (tief)); null for the main part, and on a board that does not state the part."`
	SourceFamily    string    `json:"sourceFamily"`
}

// TrainBoardCap is what the limit cut from a board.
type TrainBoardCap struct {
	More  int       `json:"more" doc:"How many more departures the window holds."`
	Until time.Time `json:"until" doc:"The planned time of the last of them."`
}

// InterchangeDeparture is one U-Bahn or tram departure of a station's
// interchange.
type InterchangeDeparture struct {
	Mode         string     `json:"mode" enum:"subway,tram"`
	Line         *string    `json:"line" nullable:"true"`
	RouteType    int        `json:"routeType" doc:"The timetable's route type code."`
	Destination  *string    `json:"destination" nullable:"true"`
	Platform     *string    `json:"platform" nullable:"true"`
	PlannedAt    time.Time  `json:"plannedAt"`
	ExpectedAt   *time.Time `json:"expectedAt" nullable:"true" doc:"The time a live figure expects; null without one."`
	DelayMin     *int       `json:"delayMin" nullable:"true" doc:"Minutes late (negative early); null when cancelled or no live figure stands."`
	Cancelled    bool       `json:"cancelled"`
	Live         bool       `json:"live" doc:"Whether a live figure stands for it; false: the timetable's time."`
	SourceFamily string     `json:"sourceFamily"`
}

// InterchangeLines is one group of the lines that serve a station: one
// mode, or the modes a line's name could not be told apart between.
type InterchangeLines struct {
	Modes []string `json:"modes" doc:"subway, tram or bus; several when the name could be either."`
	Lines []string `json:"lines"`
}

// TrainInterchange is the U-Bahn and tram under a station's board.
type TrainInterchange struct {
	Departures []InterchangeDeparture `json:"departures" doc:"The next U-Bahn and tram departures, in planned order, at most 10."`
	Lines      []InterchangeLines     `json:"lines" doc:"The U-Bahn, tram and bus lines here."`
}

// TrainBoardData is the data of getTrainBoard.
type TrainBoardData struct {
	EVA         string                `json:"eva"`
	EVAs        []string              `json:"evaNumbers" doc:"The station's EVA numbers the board merges, the asked one first."`
	Fresh       bool                  `json:"fresh" doc:"Whether a fresh board is held: one the live refresher fetched no longer ago than the MCP server's cache lifetime (30 s), or one the stored-boards worker wrote no longer ago than 20 minutes. False is no fresh board."`
	FetchedAt   *time.Time            `json:"fetchedAt" nullable:"true" doc:"When the board served was read; without one, when the newest board held was, fresh or not; null when none is held."`
	AgeSeconds  *int                  `json:"ageSeconds" nullable:"true" doc:"How long ago that was, in seconds; null when none is held."`
	Degraded    bool                  `json:"degraded" doc:"Whether the board served was read in part (a plan hour or the changes missing): its rows are what it read."`
	Departures  []TrainBoardDeparture `json:"departures" doc:"The departures of the window, the next to leave first up to the limit, in planned order; empty without a fresh board."`
	Capped      *TrainBoardCap        `json:"capped" doc:"What the limit cut; null when nothing was."`
	Interchange *TrainInterchange     `json:"interchange" doc:"The U-Bahn and tram at the station, from the DELFI timetable and its realtime; null when the station has neither a departure nor a line to name."`
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
	op.Description = "A station's DB Timetables departure board while a fresh one is held, without a refresh: the newest board the live refresher fetched (no older than the MCP server's cache lifetime, 30 s), else the board the stored-boards worker wrote for the station (no older than 20 minutes), its EVA numbers merged into one list. DB Timetables alone, CC BY 4.0, never blended with a share-alike source; the interchange under it is the DELFI timetable's, with its own sources in meta. Without a fresh board, fresh is false and the answer states when the newest board held was read. Needs the scope transit."
	featureResponses(api, &op, "Not used by this operation.")
	op.Extensions[counterpartExtension] = "train departures"
	huma.Register(api, op, func(ctx context.Context, in *TrainBoardInput) (*TrainBoardOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		a, err := s.features.TrainBoard(ctx, principalFrom(ctx).Key, features.TrainBoardQuery{EVA: in.EVA, Limit: in.Limit, DurationMin: in.DurationMin})
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		data := TrainBoardData{EVA: in.EVA, EVAs: []string{in.EVA}, Departures: []TrainBoardDeparture{}, Interchange: trainInterchange(a.Interchange)}
		if a.AsOf != nil {
			at := a.AsOf.UTC()
			data.FetchedAt = &at
		}
		if b := a.Board; b != nil {
			at := b.FetchedAt.UTC()
			data.Fresh, data.Degraded, data.FetchedAt = true, b.Degraded, &at
			if len(b.EVAs) > 0 {
				data.EVAs = b.EVAs
			}
			for _, d := range b.Departures {
				platform := d.Platform
				if d.ChangedPlatform != nil {
					platform = d.ChangedPlatform
				}
				data.Departures = append(data.Departures, TrainBoardDeparture{Line: d.Line, Category: d.Category, Number: d.Number, Destination: d.Destination,
					PlannedAt: d.PlannedAt.UTC(), ExpectedAt: d.ExpectedAt().UTC(), DelayMin: d.DelayMin(), Platform: platform, PlannedPlatform: d.Platform,
					PlatformChanged: d.PlatformChanged(), Cancelled: d.Cancelled, StationPart: d.StationPart, SourceFamily: transit.TrainBoardFamily})
			}
		}
		if c := a.Cap; c != nil {
			data.Capped = &TrainBoardCap{More: c.More, Until: c.Until.UTC()}
		}
		if data.FetchedAt != nil {
			age := int(s.at().Sub(*data.FetchedAt) / time.Second)
			data.AgeSeconds = &age
		}
		return &TrainBoardOutput{CacheControl: noStore, Body: TrainBoardEnvelope{Data: data,
			Meta: s.dataMeta(ctx, a.Meta.Families, a.Meta.At, a.Meta.Partial, partialDetail), Links: s.selfLink(ctx)}}, nil
	})
}

// trainInterchange is the interchange block; nil when the station has none.
func trainInterchange(ic *transit.Interchange) *TrainInterchange {
	if ic == nil {
		return nil
	}
	out := &TrainInterchange{Departures: []InterchangeDeparture{}, Lines: []InterchangeLines{}}
	for _, d := range ic.Departures {
		rt := d.RouteType
		v := InterchangeDeparture{Mode: string(transit.BoardModeOf(&rt)), Line: d.Line, RouteType: d.RouteType, Destination: d.Destination,
			Platform: d.Platform, PlannedAt: d.PlannedAt.UTC(), DelayMin: d.DelayMin, Cancelled: d.Cancelled, Live: d.Live, SourceFamily: d.Family}
		if d.ExpectedAt != nil {
			e := d.ExpectedAt.UTC()
			v.ExpectedAt = &e
		}
		out.Departures = append(out.Departures, v)
	}
	for _, g := range ic.Lines {
		out.Lines = append(out.Lines, InterchangeLines{Modes: g.Modes, Lines: g.Lines})
	}
	return out
}
