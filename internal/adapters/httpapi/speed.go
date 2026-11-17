package httpapi

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// The speed-camera search, as the platform's: an area, not an order, 5 km
// around the position by default and at most 25 km; 20 locations per block
// by default and at most 50.
const (
	speedDefaultRadiusM = 5000
	speedDefaultLimit   = 20
)

// SpeedInput is the input of listSpeedCameras.
type SpeedInput struct {
	AtPosition
	RadiusM int `query:"radius_m" minimum:"1000" maximum:"25000" doc:"The area around the position in metres (1000 to 25000); default 5000. An area, not an order: the lists are never sorted by distance."`
	Limit   int `query:"limit" minimum:"1" maximum:"50" doc:"Most locations of each block; default 20. Each block's total says how many the area holds."`
}

// SpeedCamera is a published speed check location.
type SpeedCamera struct {
	ID             string    `json:"id"`
	Kind           string    `json:"kind" doc:"fixed, section_control, mobile_announced or measuring_site."`
	Enforces       *string   `json:"enforces" nullable:"true" doc:"What is enforced (speed, red light); null when not published."`
	Street         *string   `json:"street" nullable:"true" doc:"The street; null when not published."`
	LocationText   *string   `json:"locationLabel" nullable:"true" doc:"The published location text, or OpenStreetMap's name; null when none."`
	Municipality   *string   `json:"municipality" nullable:"true" doc:"The town of an official list; null for OpenStreetMap's cameras."`
	Direction      *string   `json:"direction" nullable:"true" doc:"The direction checked; null when not published."`
	MaxSpeedKmh    *int      `json:"limitKmh" nullable:"true" doc:"The limit enforced in km/h; null when not published."`
	MaxSpeedHGVKmh *int      `json:"lorryLimitKmh" nullable:"true" doc:"The limit enforced for lorries in km/h; null when not published."`
	ValidFrom      *string   `json:"firstDay" nullable:"true" doc:"An announced check's first day (YYYY-MM-DD); null otherwise."`
	ValidTo        *string   `json:"lastDay" nullable:"true" doc:"An announced check's last day (YYYY-MM-DD); null otherwise."`
	DataAsOf       *string   `json:"dataAsOf" nullable:"true" doc:"The day the list was published (YYYY-MM-DD); null when not known."`
	StaleYear      *int      `json:"staleYear" nullable:"true" doc:"The year of a publication over twelve months old; null otherwise."`
	Location       *Position `json:"location" doc:"The position; null for a street of a town list."`
	SourceFamily   string    `json:"sourceFamily"`
}

// SpeedData is the data of listSpeedCameras.
type SpeedData struct {
	Official      []SpeedCamera `json:"official" doc:"The towns' official lists, by kind (fixed, section control, announced, measuring site), then first day and street; an announced check only until its last day."`
	OfficialTotal int           `json:"officialTotal" doc:"Official locations in the area before the limit."`
	OSM           []SpeedCamera `json:"osm" doc:"Fixed cameras OpenStreetMap maps, by name; none where an official list of fixed cameras covers the area."`
	OSMTotal      int           `json:"osmTotal" doc:"OpenStreetMap's cameras kept in the area before the limit."`
}

// SpeedEnvelope is the enveloped answer of listSpeedCameras.
type SpeedEnvelope struct {
	Data  SpeedData `json:"data"`
	Meta  PagedMeta `json:"meta"`
	Links Links     `json:"links"`
}

// SpeedOutput is the response of listSpeedCameras.
type SpeedOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         SpeedEnvelope
}

func day(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := t.Format(time.DateOnly)
	return &s
}

func speedCameras(cs []roads.SpeedCamera) []SpeedCamera {
	out := make([]SpeedCamera, len(cs))
	for i, c := range cs {
		out[i] = SpeedCamera{ID: c.ID, Kind: c.Kind, Enforces: c.Enforces, Street: c.Street, LocationText: c.LocationText,
			Municipality: c.Municipality, Direction: c.Direction, MaxSpeedKmh: c.MaxSpeedKmh, MaxSpeedHGVKmh: c.MaxSpeedHGVKm,
			ValidFrom: day(c.ValidFrom), ValidTo: day(c.ValidTo), DataAsOf: day(c.DataAsOf), StaleYear: c.StaleYear, SourceFamily: c.Family}
		if c.Location != nil {
			p := position(*c.Location)
			out[i].Location = &p
		}
	}
	return out
}

func registerSpeed(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Published speed check locations in an area"
	op.Description = "For planning only (§ 23 Abs. 1c StVO): the towns' official lists and OpenStreetMap's fixed cameras in the area, by kind and street, never by distance. Needs the scope roads."
	featureResponses(api, &op, "Not used by this operation.")
	op.Extensions[counterpartExtension] = "speed camera search"
	huma.Register(api, op, func(ctx context.Context, in *SpeedInput) (*SpeedOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := roads.SpeedQuery{At: in.point, RadiusM: in.RadiusM, Limit: in.Limit}
		if q.RadiusM == 0 {
			q.RadiusM = speedDefaultRadiusM
		}
		if q.Limit == 0 {
			q.Limit = speedDefaultLimit
		}
		a, err := s.features.SpeedCameras(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		return &SpeedOutput{CacheControl: noStore, Body: speedEnvelope(s, ctx, a, q.Limit)}, nil
	})
}

func speedEnvelope(s *server, ctx context.Context, a features.SpeedAnswer, limit int) SpeedEnvelope {
	data := SpeedData{Official: speedCameras(a.Official), OfficialTotal: a.OfficialTotal, OSM: speedCameras(a.OSM), OSMTotal: a.OSMTotal}
	more := a.OfficialTotal > len(a.Official) || a.OSMTotal > len(a.OSM)
	m := s.dataMeta(ctx, a.Meta.Families, a.Meta.At, a.Meta.Partial, partialDetail)
	if a.Notice != "" {
		// The § 23 Abs. 1c StVO notice is a condition of every answer.
		m.Notes.Condition = append(m.Notes.Condition, a.Notice)
	}
	return SpeedEnvelope{Data: data,
		Meta:  PagedMeta{DataMeta: m, Page: Page{Limit: limit, HasMore: more}},
		Links: s.selfLink(ctx)}
}
