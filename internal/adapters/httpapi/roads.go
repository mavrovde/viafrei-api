package httpapi

import (
	"context"
	"time"

	_ "time/tzdata" // the Europe/Berlin rendering never depends on the host

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// The road-conditions operations: what is happening on a motorway, on the
// roads near a position, and one situation by its id (FR-ROAD).

// berlin is the zone every *Local member is rendered in (CONV-004); the
// zone database is embedded, so loading it cannot fail.
var berlin, _ = time.LoadLocation("Europe/Berlin")

// local renders an instant in Europe/Berlin time, RFC 3339 with its offset.
func local(t time.Time) string { return t.In(berlin).Format(time.RFC3339) }

// TimeWindow is a validity: an instant in UTC and its Europe/Berlin
// rendering (CONV-004); until is null when open-ended.
type TimeWindow struct {
	From       time.Time  `json:"from" doc:"Start of the validity."`
	FromLocal  string     `json:"fromLocal" format:"date-time" doc:"from in Europe/Berlin time."`
	Until      *time.Time `json:"until" nullable:"true" doc:"End of the validity; null when open-ended."`
	UntilLocal *string    `json:"untilLocal" nullable:"true" format:"date-time" doc:"until in Europe/Berlin time; null when until is."`
}

func timeWindow(from time.Time, until *time.Time) TimeWindow {
	w := TimeWindow{From: from.UTC(), FromLocal: local(from)}
	if until != nil {
		u, l := until.UTC(), local(*until)
		w.Until, w.UntilLocal = &u, &l
	}
	return w
}

// Lanes are the closed and total lanes of a carriageway, where published.
type Lanes struct {
	Closed *int `json:"closed" nullable:"true" doc:"Lanes closed; null when not published."`
	Total  *int `json:"total" nullable:"true" doc:"Lanes in total; null when not published."`
}

// RoadSituation is anything that affects travel on a road.
type RoadSituation struct {
	ID             string     `json:"id"`
	Kind           string     `json:"kind" enum:"closure,roadworks,jam,accident,hazard,restriction,weather,other" doc:"What it is. Open enumeration; treat an unknown value as other."`
	Severity       string     `json:"severity" enum:"info,minor,lane_closed,major,road_closed,other" doc:"Effect on traffic, ordered info < minor < lane_closed < major < road_closed; other when it cannot be graded. Open enumeration."`
	Road           *string    `json:"road" nullable:"true" doc:"The road reference without spaces (A8); null when the situation is on no numbered road."`
	RoadType       string     `json:"roadType" enum:"motorway,federal,state,urban,other" doc:"The class of the road. Open enumeration; treat an unknown value as other."`
	Direction      string     `json:"direction" enum:"north,east,south,west,both,unknown" doc:"The affected carriageway."`
	DirectionText  *string    `json:"directionText" nullable:"true" doc:"The signed destination of the carriageway, as published."`
	Location       *Position  `json:"location" doc:"Where it is; null when not published."`
	Validity       TimeWindow `json:"validity"`
	Lanes          *Lanes     `json:"lanes" doc:"Closed and total lanes; null when neither is published."`
	DelayMin       *int       `json:"delayMin" nullable:"true" doc:"The published delay in minutes; null when not published."`
	LengthM        *int       `json:"lengthM" nullable:"true" doc:"The published length in metres; null when not published."`
	Headline       string     `json:"headline" doc:"Plain text, in textLanguage."`
	Description    *string    `json:"description" nullable:"true" doc:"Plain text, in textLanguage; null when not published."`
	TextLanguage   string     `json:"textLanguage" doc:"The language of headline and description (BCP 47)."`
	UpdatedAt      time.Time  `json:"updatedAt" doc:"When the situation last changed."`
	UpdatedAtLocal string     `json:"updatedAtLocal" format:"date-time" doc:"updatedAt in Europe/Berlin time."`
	AtKm           *float64   `json:"atKm,omitempty" doc:"Position along the road in kilometres, one decimal. Present on a road's conditions when the situation can be placed on the road."`
	DistanceM      *int       `json:"distanceM,omitempty" doc:"Distance from the asked position in metres. Present on the conditions near a position."`
	SourceFamily   string     `json:"sourceFamily" doc:"The source family of the item; its attribution is in meta.attribution."`
}

func roadSituation(v roads.Situation) RoadSituation {
	out := RoadSituation{
		ID: v.ID, Kind: string(v.Kind), Severity: string(v.Severity), Road: v.Road, RoadType: string(v.Class),
		Direction: string(v.Direction), DirectionText: v.Towards, Validity: timeWindow(v.From, v.Until),
		DelayMin: v.DelayMin, LengthM: v.LengthM, Headline: v.Headline, Description: v.Description,
		TextLanguage: v.TextLanguage, UpdatedAt: v.UpdatedAt.UTC(), UpdatedAtLocal: local(v.UpdatedAt),
		AtKm: v.AtKm, DistanceM: v.DistanceM, SourceFamily: v.Family,
	}
	if v.Location != nil {
		p := position(*v.Location)
		out.Location = &p
	}
	if v.LanesClosed != nil || v.LanesTotal != nil {
		out.Lanes = &Lanes{Closed: v.LanesClosed, Total: v.LanesTotal}
	}
	return out
}

func roadSituations(list []roads.Situation) []RoadSituation {
	out := make([]RoadSituation, len(list))
	for i, v := range list {
		out[i] = roadSituation(v)
	}
	return out
}

func lampReason(r *roads.Severity) *string {
	if r == nil {
		return nil
	}
	s := string(*r)
	return &s
}

// SituationFilters are the filters of the conditions operations.
type SituationFilters struct {
	Kinds       []string `query:"kinds" enum:"closure,roadworks,jam,accident,hazard,restriction,weather,other" doc:"Only these kinds, comma-separated; other keeps the kinds that cannot be mapped. Default: every kind."`
	MinSeverity string   `query:"min_severity" enum:"info,minor,lane_closed,major,road_closed" default:"info" doc:"Only situations at or above this severity; info also keeps those that cannot be graded."`
}

func (f SituationFilters) filter() roads.Filter {
	out := roads.Filter{MinSeverity: roads.Severity(f.MinSeverity)}
	for _, k := range f.Kinds {
		out.Kinds = append(out.Kinds, roads.Kind(k))
	}
	return out
}

// FilterQuery is the filter part of meta.query of a conditions answer.
type FilterQuery struct {
	Kinds       []string `json:"kinds" doc:"The kinds asked for; empty for every kind."`
	MinSeverity string   `json:"minSeverity"`
}

func filterQuery(f roads.Filter) FilterQuery {
	out := FilterQuery{Kinds: []string{}, MinSeverity: string(f.MinSeverity)}
	for _, k := range f.Kinds {
		out.Kinds = append(out.Kinds, string(k))
	}
	return out
}

// ------------------------------------------------------- motorway conditions

// MotorwayInput is the input of getMotorwayConditions.
type MotorwayInput struct {
	Road      string `path:"road" maxLength:"6" pattern:"^[AaBb] ?[0-9]{1,4}$" example:"A8" doc:"The road reference, case-insensitive, with an optional space (A8, a 8)."`
	Direction string `query:"direction" enum:"north,east,south,west,both" default:"both" doc:"The carriageway; both keeps every situation. A direction keeps that carriageway's situations, those on both and those whose direction is unknown."`
	SituationFilters
	Limit int `query:"limit" minimum:"1" maximum:"100" doc:"Most situations to return. Default: pagination.default_limit."`
}

// Corridor is the road a conditions answer is about.
type Corridor struct {
	Road      string   `json:"road" doc:"The road reference without spaces."`
	RoadType  string   `json:"roadType" enum:"motorway,federal,state,urban,other" doc:"Open enumeration; treat an unknown value as other."`
	Direction string   `json:"direction" enum:"north,east,south,west,both"`
	States    []string `json:"states" doc:"The ISO 3166-2 codes of the states the road crosses."`
}

// MotorwayData is the answer of getMotorwayConditions.
type MotorwayData struct {
	Corridor   Corridor        `json:"corridor"`
	Lamp       string          `json:"lamp" enum:"green,amber,red,unknown" doc:"The lamp over every situation that answers the question, not only this page: red when one closes the road; amber when the worst is major or lane_closed, or one cannot be graded; green otherwise, also when there is none."`
	LampReason *string         `json:"lampReason" nullable:"true" enum:"info,minor,lane_closed,major,road_closed,other" doc:"The severity that decided the lamp; null for green with no situation."`
	Situations []RoadSituation `json:"situations" doc:"Ordered by position along the road (those that cannot be placed last), then severity descending, then id."`
}

// MotorwayQuery is meta.query of getMotorwayConditions.
type MotorwayQuery struct {
	Road      string `json:"road"`
	Direction string `json:"direction"`
	FilterQuery
}

// MotorwayMeta is the meta member of getMotorwayConditions.
type MotorwayMeta struct {
	DataMeta
	Page  Page          `json:"page"`
	Query MotorwayQuery `json:"query"`
}

// MotorwayEnvelope is the enveloped answer of getMotorwayConditions.
type MotorwayEnvelope struct {
	Data  MotorwayData `json:"data"`
	Meta  MotorwayMeta `json:"meta"`
	Links Links        `json:"links"`
}

// MotorwayOutput is the response of getMotorwayConditions.
type MotorwayOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         MotorwayEnvelope
}

func registerMotorwayConditions(api huma.API, s *server, op huma.Operation) {
	op.Summary = "What is happening on a motorway"
	op.Description = "The active situations on one road, ordered by their position along it, with the lamp over every situation that answers the question. MCP counterpart: road status. Needs the scope roads."
	featureResponses(api, &op, "No road with this reference (not-found), or the operation is switched off on this instance (feature-disabled).")
	huma.Register(api, op, func(ctx context.Context, in *MotorwayInput) (*MotorwayOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		road, _ := roads.ParseRoad(in.Road) // the path pattern admits only references ParseRoad reads
		q := roads.CorridorQuery{Road: road, Direction: roads.Direction(in.Direction), Filter: in.filter(), Limit: s.limit(in.Limit)}
		c, err := s.features.MotorwayConditions(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		return &MotorwayOutput{CacheControl: noStore, Body: MotorwayEnvelope{
			Data: MotorwayData{
				Corridor:   Corridor{Road: c.Road.Ref, RoadType: string(c.Road.Class), Direction: string(q.Direction), States: append([]string{}, c.Road.States...)},
				Lamp:       string(c.Lamp),
				LampReason: lampReason(c.Reason),
				Situations: roadSituations(c.Items),
			},
			Meta: MotorwayMeta{
				DataMeta: s.dataMeta(ctx, c.Families, c.At, c.Partial, partialDetail),
				Page:     Page{Limit: q.Limit, HasMore: c.HasMore},
				Query:    MotorwayQuery{Road: c.Road.Ref, Direction: string(q.Direction), FilterQuery: filterQuery(q.Filter)},
			},
			Links: s.selfLink(ctx),
		}}, nil
	})
}

// ------------------------------------------------------------ area conditions

// RoadConditionsInput is the input of listRoadConditions.
type RoadConditionsInput struct {
	Near
	SituationFilters
	RoadClass []string `query:"road_class" enum:"motorway,federal,state,urban,other" doc:"Only situations on roads of these classes, comma-separated; other keeps the classes that cannot be mapped. Default: every class held."`
}

// Area is the area a conditions answer is about.
type Area struct {
	At      Position `json:"at"`
	RadiusM int      `json:"radiusM" doc:"The radius applied, in metres."`
}

// RoadConditionsData is the answer of listRoadConditions.
type RoadConditionsData struct {
	Area       Area            `json:"area"`
	Lamp       string          `json:"lamp" enum:"green,amber,red,unknown" doc:"The lamp over every situation that answers the question, not only this page: red when one closes the road; amber when the worst is major or lane_closed, or one cannot be graded; green otherwise, also when there is none."`
	LampReason *string         `json:"lampReason" nullable:"true" enum:"info,minor,lane_closed,major,road_closed,other" doc:"The severity that decided the lamp; null for green with no situation."`
	Situations []RoadSituation `json:"situations" doc:"Ordered by severity descending, then distance, then id."`
}

// RoadConditionsQuery is meta.query of listRoadConditions.
type RoadConditionsQuery struct {
	NearQuery
	FilterQuery
	RoadClasses []string `json:"roadClasses" doc:"The road classes asked for; empty for every class."`
}

// RoadConditionsMeta is the meta member of listRoadConditions.
type RoadConditionsMeta struct {
	DataMeta
	Page  Page                `json:"page"`
	Query RoadConditionsQuery `json:"query"`
}

// RoadConditionsEnvelope is the enveloped answer of listRoadConditions.
type RoadConditionsEnvelope struct {
	Data  RoadConditionsData `json:"data"`
	Meta  RoadConditionsMeta `json:"meta"`
	Links Links              `json:"links"`
}

// RoadConditionsOutput is the response of listRoadConditions.
type RoadConditionsOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         RoadConditionsEnvelope
}

func registerRoadConditions(api huma.API, s *server, op huma.Operation) {
	op.Summary = "What is happening on the roads near a position"
	op.Description = "The active situations within the radius of a position on every road class held, by severity descending, then distance, then id, with the lamp over every situation that answers the question. MCP counterpart: road status (area form). Needs the scope roads."
	featureResponses(api, &op, featureOffDoc)
	huma.Register(api, op, func(ctx context.Context, in *RoadConditionsInput) (*RoadConditionsOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := roads.AreaQuery{At: in.point, RadiusM: s.radius(in.RadiusM), Filter: in.filter(), Limit: s.limit(in.Limit)}
		types := []string{}
		for _, c := range in.RoadClass {
			q.Classes = append(q.Classes, roads.Class(c))
			types = append(types, c)
		}
		c, err := s.features.RoadConditions(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		return &RoadConditionsOutput{CacheControl: noStore, Body: RoadConditionsEnvelope{
			Data: RoadConditionsData{
				Area:       Area{At: position(q.At), RadiusM: q.RadiusM},
				Lamp:       string(c.Lamp),
				LampReason: lampReason(c.Reason),
				Situations: roadSituations(c.Items),
			},
			Meta: RoadConditionsMeta{
				DataMeta: s.dataMeta(ctx, c.Families, c.At, c.Partial, partialDetail),
				Page:     Page{Limit: q.Limit, HasMore: c.HasMore},
				Query:    RoadConditionsQuery{NearQuery: NearQuery{At: position(q.At), RadiusM: q.RadiusM}, FilterQuery: filterQuery(q.Filter), RoadClasses: types},
			},
			Links: s.selfLink(ctx),
		}}, nil
	})
}

// ------------------------------------------------------------- one situation

// SituationInput is the input of getRoadSituation.
type SituationInput struct {
	SituationID string `path:"situationId" maxLength:"64" pattern:"^[A-Za-z0-9_.-]+$" example:"ev-00001" doc:"The situation's id."`
}

// SituationEnvelope is the enveloped answer of getRoadSituation.
type SituationEnvelope struct {
	Data  RoadSituation `json:"data"`
	Meta  DataMeta      `json:"meta"`
	Links Links         `json:"links"`
}

// SituationOutput is the response of getRoadSituation.
type SituationOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         SituationEnvelope
}

func registerRoadSituation(api huma.API, s *server, op huma.Operation) {
	op.Summary = "One road situation"
	op.Description = "One situation by its id, active or ended. A situation that was withdrawn, or that belongs to a source family the key may not read, is not found. Needs the scope roads."
	featureResponses(api, &op, "No situation with this id (not-found), or the operation is switched off on this instance (feature-disabled).")
	huma.Register(api, op, func(ctx context.Context, in *SituationInput) (*SituationOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		a, err := s.features.RoadSituation(ctx, principalFrom(ctx).Key, in.SituationID)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		return &SituationOutput{CacheControl: noStore, Body: SituationEnvelope{
			Data:  roadSituation(a.Items[0]),
			Meta:  s.dataMeta(ctx, a.Families, a.At, a.Partial, partialDetail),
			Links: s.selfLink(ctx),
		}}, nil
	})
}
