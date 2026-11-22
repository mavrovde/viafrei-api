package httpapi

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// The disruption check, as the platform's: the last hour by default, at
// most two; six alerts shown.
const (
	disruptionDefaultWindowMin = 60
	disruptionDefaultLimit     = 6
	// punctualityWindowMin is the window of the contract's punctuality:
	// the last half hour of Trip Updates.
	punctualityWindowMin = 30
)

// DisruptionInput is the input of listTransitDisruptions.
type DisruptionInput struct {
	Area      string `query:"area" pattern:"^(0[1-9]|1[0-6])[0-9]{0,10}$" example:"05315" doc:"The official region code of the area (two digits a Land, 01 to 16, five a Kreis; every key starts with its Land's), as listPlaces or describeLocation give it. Give area, line or both."`
	Line      string `query:"line" minLength:"1" maxLength:"16" doc:"A line name as on the vehicle (RB33, S1, RE 1); white space and case do not matter. Without area, the line's alerts anywhere."`
	WindowMin int    `query:"window_min" minimum:"5" maximum:"120" doc:"How many minutes back to look (5 to 120); default 60, echoed as data.windowMin. The punctuality is counted over the source's own window, data.punctuality.windowMin."`
	Limit     int    `query:"limit" minimum:"1" maximum:"20" doc:"Most alerts to return; default 6. data.alertsTotal says how many are in force."`
}

// Resolve refuses a question with neither an area nor a line.
func (in *DisruptionInput) Resolve(huma.Context) []error {
	if in.Area == "" && in.Line == "" {
		return []error{&huma.ErrorDetail{Location: "query.area", Message: "give area, line or both"},
			&huma.ErrorDetail{Location: "query.line", Message: "give area, line or both"}}
	}
	return nil
}

// DisruptionAlert is a service alert in force, with what the platform's
// disruption check states of it: no text beyond the headline, no cause or
// effect, no change time.
type DisruptionAlert struct {
	ID           string     `json:"id"`
	Header       *string    `json:"header" nullable:"true" doc:"The headline as published; null when none."`
	URL          *string    `json:"url" nullable:"true" doc:"The network's page on it; null when none."`
	ActiveFrom   *time.Time `json:"inForceFrom" nullable:"true" doc:"Since when it applies; null when open."`
	ActiveTo     *time.Time `json:"inForceUntil" nullable:"true" doc:"Until when it applies; null when open."`
	Lines        []string   `json:"lines" doc:"The lines it names."`
	PlacedBy     string     `json:"attachedTo" doc:"What it is attached to: stop, route or network."`
	SourceFamily string     `json:"sourceFamily"`
}

// DisruptionPunctuality is the area's punctuality.
type DisruptionPunctuality struct {
	AreaLevel        string     `json:"level"`
	AreaCode         string     `json:"code"`
	AreaName         *string    `json:"name" nullable:"true"`
	WindowMin        int        `json:"windowMin" doc:"The window the source counts over, in minutes."`
	TripsObserved    int        `json:"observed" doc:"Trips with a delay report in the window."`
	TripsLate        int        `json:"late" doc:"Of those, trips late at least once."`
	TripsSkipping    int        `json:"tripsSkippingStops" doc:"Trips with at least one skipped stop."`
	TripsUntimed     int        `json:"untimed" doc:"Trips reported without a delay to count."`
	MedianWorstDelay *int       `json:"medianWorstDelaySeconds" nullable:"true" doc:"The median of each trip's worst delay in seconds; null when none."`
	StopsSeen        int        `json:"stopsReporting"`
	ObservedAt       *time.Time `json:"observedAt" nullable:"true" doc:"The newest report counted."`
	SourceFamily     string     `json:"sourceFamily"`
}

// DisruptionData is the data of listTransitDisruptions.
type DisruptionData struct {
	Alerts      []DisruptionAlert `json:"alerts" doc:"The alerts in force: a stop's first, then a route's, then the network's, each newest first."`
	AlertsTotal int               `json:"alertsTotal" doc:"Alerts in force before the limit, duplicates left out."`
	// AlertsChecked and DuplicatesOmitted are the platform's checked and
	// duplicatesOmitted.
	AlertsChecked     bool                   `json:"alertsChecked" doc:"Whether an alert publication was alive to ask: alertsTotal 0 means none in force only when this is true, and that it could not be told when false."`
	DuplicatesOmitted int                    `json:"duplicatesOmitted" doc:"Alerts of the secondary publication left out as duplicates of an alert of the primary publication in force."`
	Punctuality       *DisruptionPunctuality `json:"punctuality" doc:"The area's punctuality; null without an area or when the source counted no trip in it."`
	WindowMin         int                    `json:"windowMin" doc:"The window asked for, in minutes."`
	Line              *DisruptionLine        `json:"line" doc:"What the static timetable says of the line asked; null without line."`
}

// DisruptionLine is whether the static timetable knows the line asked, so
// no alerts for a line can be told from no such line.
type DisruptionLine struct {
	Key         string `json:"key" example:"RE1" doc:"The line as compared: the name asked, upper case, white space removed."`
	Checked     bool   `json:"checked" doc:"Whether a timetable the key may read was there to ask: inTimetable false means no such line only when this is true, and that it could not be told when false."`
	InTimetable bool   `json:"inTimetable" doc:"Whether a line of this name is in the timetable; the timetable's family is then credited in meta.attribution."`
}

// DisruptionEnvelope is the enveloped answer of listTransitDisruptions.
type DisruptionEnvelope struct {
	Data  DisruptionData `json:"data"`
	Meta  PagedMeta      `json:"meta"`
	Links Links          `json:"links"`
}

// DisruptionOutput is the response of listTransitDisruptions.
type DisruptionOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         DisruptionEnvelope
}

func registerDisruptions(api huma.API, s *server, op huma.Operation) {
	op.Summary = "Public-transport disruption of an area or a line"
	op.Description = "The service alerts in force in the area (a Land or a Kreis) or for the line, and the area's punctuality: the trips late at least once. Needs the scope transit."
	featureResponses(api, &op, "Not used by this operation.")
	op.Extensions[counterpartExtension] = "transit disruption check"
	huma.Register(api, op, func(ctx context.Context, in *DisruptionInput) (*DisruptionOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := transit.DisruptionQuery{Area: in.Area, Line: transit.FoldLine(in.Line), WindowMin: in.WindowMin, Limit: in.Limit}
		if q.WindowMin == 0 {
			q.WindowMin = disruptionDefaultWindowMin
		}
		if q.Limit == 0 {
			q.Limit = disruptionDefaultLimit
		}
		a, err := s.features.Disruptions(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		return &DisruptionOutput{CacheControl: noStore, Body: disruptionEnvelope(s, ctx, a, q)}, nil
	})
}

func disruptionEnvelope(s *server, ctx context.Context, a features.DisruptionAnswer, q transit.DisruptionQuery) DisruptionEnvelope {
	data := DisruptionData{Alerts: make([]DisruptionAlert, len(a.Alerts)), AlertsTotal: a.AlertsTotal, AlertsChecked: a.AlertsChecked,
		DuplicatesOmitted: a.AlertsDuplicates, WindowMin: q.WindowMin}
	for i, v := range a.Alerts {
		lines := v.Lines
		if lines == nil {
			lines = []string{}
		}
		data.Alerts[i] = DisruptionAlert{ID: v.ID, Header: v.Header, URL: v.URL,
			ActiveFrom: utc(v.ActiveFrom), ActiveTo: utc(v.ActiveTo), Lines: lines, PlacedBy: v.PlacedBy, SourceFamily: v.Family}
	}
	if l := a.Line; l != nil {
		data.Line = &DisruptionLine{Key: q.Line, Checked: l.Checked, InTimetable: l.InTimetable}
	}
	if p := a.Punctuality; p != nil {
		data.Punctuality = &DisruptionPunctuality{AreaLevel: p.AreaLevel, AreaCode: p.AreaCode, AreaName: p.AreaName, WindowMin: punctualityWindowMin,
			TripsObserved: p.TripsObserved, TripsLate: p.TripsLate, TripsSkipping: p.TripsSkipping, TripsUntimed: p.TripsUntimed,
			MedianWorstDelay: p.MedianWorstDelay, StopsSeen: p.StopsSeen, ObservedAt: utc(p.ObservedAt), SourceFamily: p.Family}
	}
	return DisruptionEnvelope{Data: data,
		Meta:  PagedMeta{DataMeta: s.dataMeta(ctx, a.Meta.Families, a.Meta.At, a.Meta.Partial, partialDetail), Page: Page{Limit: q.Limit, HasMore: a.AlertsTotal > len(a.Alerts)}},
		Links: s.selfLink(ctx)}
}
