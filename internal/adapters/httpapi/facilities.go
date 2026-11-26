package httpapi

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// The station facilities operation (FR-TRANSIT-003).

const facilitiesDefaultLimit = 20

// FacilitiesInput is the input of listStationFacilities.
type FacilitiesInput struct {
	StationID string `path:"stationId" minLength:"3" maxLength:"120" example:"1:de:05315:11201" doc:"The station id, as listStations answers it."`
	Kind      string `query:"kind" enum:"elevator,escalator" doc:"Only lifts (elevator) or only escalators. Default: both."`
	Limit     int    `query:"limit" minimum:"1" maximum:"50" doc:"Most facilities to return, out of service first; default 20. The counts in data.counts cover all of them."`
}

// StationFacility is a lift or escalator.
type StationFacility struct {
	ID           string  `json:"id"`
	Kind         string  `json:"kind" doc:"elevator, escalator or other. Open enumeration."`
	Position     *string `json:"position" nullable:"true" doc:"Where it is, as published (zu Gleis 2); null when not published."`
	State        string  `json:"state" doc:"active, inactive or unknown: a state older than its family's freshness threshold is unknown. Open enumeration."`
	SourceFamily string  `json:"sourceFamily"`
	EquipmentNo  *string `json:"equipmentNo" nullable:"true" doc:"DB's equipment number of the unit, as its source lists it, the number the platform's station facilities check names a unit by; null when the source gives none (the unit is then named by its id)."`
	// StateExplanation and NameShared: the operator's word and a twin.
	StateExplanation *string `json:"stateExplanation" nullable:"true" doc:"The operator's explanation of the state, verbatim (not monitored, under maintenance, or the operator's own words); null when it gives none or one that repeats the state (available, not available)."`
	NameShared       bool    `json:"nameShared" doc:"Another unit of the station has the same kind and position, listed or beyond the limit: name this unit by its number."`
}

// FacilityCounts counts every facility of the asked kind by state.
type FacilityCounts struct {
	Active   int `json:"active"`
	Inactive int `json:"inactive"`
	Unknown  int `json:"unknown"`
}

// FacilityStation is the station a facilities answer is about, named as the
// platform's facilities check names it (docs/parity.md).
type FacilityStation struct {
	Name string `json:"name"`
}

// StationFacilitiesData is the data of listStationFacilities.
type StationFacilitiesData struct {
	Station    FacilityStation   `json:"station"`
	Counts     FacilityCounts    `json:"counts"`
	Facilities []StationFacility `json:"facilities"`
	Platforms  *Platforms        `json:"platforms" doc:"The station's platforms as DB's station inventory (OpenStation) states them, as the platform's facilities check lists them; null for a stop with no EVA number or none the inventory lists."`
}

// Platforms are a station's platforms per DB's station inventory.
type Platforms struct {
	ReleasedOn *string            `json:"releasedOn" nullable:"true" format:"date" doc:"The Berlin date of DB's newest publication of them; null when none is stated."`
	Stations   []PlatformsStation `json:"stations" doc:"One per stop place of the inventory."`
}

// PlatformsStation is one stop place's platforms.
type PlatformsStation struct {
	Name           *string          `json:"name" nullable:"true"`
	Station        *PlatformFacts   `json:"station" doc:"The stop place's own assessment; null when it carries none."`
	StepFreeCounts StepFreeCounts   `json:"stepFreeCounts"`
	Total          int              `json:"total"`
	List           []PlatformAccess `json:"list" doc:"By the number of the first track, then by name."`
}

// PlatformFacts are the facts the inventory states, each yes, partial, no
// or unknown (not stated).
type PlatformFacts struct {
	StepFree        string `json:"stepFree" enum:"yes,partial,no,unknown"`
	Wheelchair      string `json:"wheelchair" enum:"yes,partial,no,unknown"`
	TactileGuidance string `json:"tactileGuidance" enum:"yes,partial,no,unknown"`
	LevelBoarding   string `json:"levelBoarding" enum:"yes,partial,no,unknown"`
}

// StepFreeCounts counts the platforms by their step-free word.
type StepFreeCounts struct {
	Yes     int `json:"yes"`
	Partial int `json:"partial"`
	No      int `json:"no"`
	Unknown int `json:"unknown"`
}

// PlatformAccess is one platform.
type PlatformAccess struct {
	Name     *string       `json:"name" nullable:"true" doc:"The platform's own name (Bahnsteig Gleis 6/7); null when the publisher gives none."`
	Tracks   []string      `json:"tracks" doc:"The numbers of its track edges, in number order."`
	Stated   bool          `json:"stated" doc:"False when the platform carries no assessment: its facts are then unknown, not no."`
	Facts    PlatformFacts `json:"facts"`
	HeightCm *HeightRange  `json:"heightCm" doc:"The lowest and highest height published over the platform, its edges and their sectors; null when none is."`
}

// HeightRange is a range of platform heights in centimetres.
type HeightRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

func platformsOf(sts []transit.StationPlatforms, at *time.Time) *Platforms {
	if len(sts) == 0 {
		return nil
	}
	out := &Platforms{Stations: make([]PlatformsStation, len(sts))}
	if at != nil {
		d := at.In(berlin).Format(time.DateOnly)
		out.ReleasedOn = &d
	}
	facts := func(f *transit.PlatformFacts) PlatformFacts {
		if f == nil {
			return PlatformFacts{"unknown", "unknown", "unknown", "unknown"}
		}
		return PlatformFacts{f.StepFree, f.Wheelchair, f.TactileGuidance, f.LevelBoarding}
	}
	for i, st := range sts {
		ps := PlatformsStation{Name: st.StationName, Total: len(st.Platforms), List: make([]PlatformAccess, len(st.Platforms)),
			StepFreeCounts: StepFreeCounts{st.StepFreeCounts["yes"], st.StepFreeCounts["partial"], st.StepFreeCounts["no"], st.StepFreeCounts["unknown"]}}
		if st.Station != nil {
			f := facts(st.Station)
			ps.Station = &f
		}
		for j, p := range st.Platforms {
			pa := PlatformAccess{Name: p.Name, Tracks: p.Tracks, Stated: p.Facts != nil, Facts: facts(p.Facts)}
			if h := p.Height; h != nil {
				pa.HeightCm = &HeightRange{h.Min, h.Max}
			}
			ps.List[j] = pa
		}
		out.Stations[i] = ps
	}
	return out
}

// FacilitiesEnvelope is the enveloped answer of listStationFacilities.
type FacilitiesEnvelope struct {
	Data  StationFacilitiesData `json:"data"`
	Meta  PagedMeta             `json:"meta"`
	Links Links                 `json:"links"`
}

// PagedMeta is the meta member of a page with no query echo.
type PagedMeta struct {
	DataMeta
	Page Page `json:"page"`
}

// FacilitiesOutput is the response of listStationFacilities.
type FacilitiesOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         FacilitiesEnvelope
}

func registerFacilities(api huma.API, s *server, op huma.Operation) {
	op.Summary = "A station's lifts and escalators"
	op.Description = "The lifts and escalators of the station with their reported state: out of service first, then unknown, then in service, then by position; the counts cover every one of the asked kind, so a shorter list never hides a broken lift. Needs the scope transit."
	featureResponses(api, &op, "No station with this id.")
	op.Extensions[counterpartExtension] = "station facilities"
	huma.Register(api, op, func(ctx context.Context, in *FacilitiesInput) (*FacilitiesOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := transit.FacilityQuery{StationID: in.StationID, Kind: in.Kind, Limit: in.Limit}
		if q.Limit == 0 {
			q.Limit = facilitiesDefaultLimit
		}
		a, err := s.features.Facilities(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		rows := make([]StationFacility, len(a.Items))
		for i, f := range a.Items {
			rows[i] = StationFacility{ID: f.ID, Kind: f.Kind, Position: f.Position, State: f.State, SourceFamily: f.Family, EquipmentNo: f.EquipmentNo,
				StateExplanation: f.StateExplanation, NameShared: f.NameShared}
		}
		return &FacilitiesOutput{CacheControl: noStore, Body: FacilitiesEnvelope{
			Data: StationFacilitiesData{
				Station:    FacilityStation{Name: a.Station.Name},
				Counts:     FacilityCounts{Active: a.Active, Inactive: a.Inactive, Unknown: a.Unknown},
				Facilities: rows,
				Platforms:  platformsOf(a.Platforms, a.PlatformsAt),
			},
			Meta:  PagedMeta{DataMeta: s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail), Page: Page{Limit: q.Limit, HasMore: a.HasMore}},
			Links: s.selfLink(ctx),
		}}, nil
	})
}
