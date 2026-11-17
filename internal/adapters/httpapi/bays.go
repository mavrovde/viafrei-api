package httpapi

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
)

// The on-street bays (G4): the bays a municipality publishes as reserved for
// a disabled parking permit or for charging, as the platform's parking
// search answers kind=disabled_badge.

const (
	baysDefaultRadiusM = 10000
	baysDefaultLimit   = 5
)

// BaysInput is the input of listParkingBays.
type BaysInput struct {
	AtPosition
	Kind     string `query:"kind" enum:"disabled,ev_charging" doc:"Which bays: disabled = reserved for a disabled parking permit, ev_charging = reserved for charging. Default: disabled."`
	RadiusM  int    `query:"radius_m" minimum:"1000" maximum:"25000" doc:"Search radius in metres (1000 to 25000); default 10000."`
	Limit    int    `query:"limit" minimum:"1" maximum:"20" doc:"Most bays to list, nearest first; default 5. The counts cover every bay in the radius."`
	OnlyFree string `query:"only_free" enum:"true,false" doc:"List only the bays a sensor reports free now; default false. A sensor-less site is never free."`
}

// ParkingBay is one bay or sensor-less site.
type ParkingBay struct {
	ID            string     `json:"id"`
	Kind          string     `json:"kind" doc:"disabled or ev_charging."`
	PublisherKind *string    `json:"publisherKind" nullable:"true" doc:"The publisher's own word for the bay, verbatim."`
	Street        *string    `json:"street" nullable:"true"`
	HouseNumber   *string    `json:"houseNumber" nullable:"true"`
	Postcode      *string    `json:"postcode" nullable:"true"`
	City          *string    `json:"city" nullable:"true"`
	Lat           float64    `json:"lat"`
	Lon           float64    `json:"lon"`
	DistanceM     int        `json:"distanceM"`
	Sensor        bool       `json:"sensor" doc:"Whether a sensor reports the bay; false at a sensor-less site, which states spaces instead."`
	State         *string    `json:"state" nullable:"true" doc:"free or occupied while the newest message is under three hours old; null otherwise (no current reading) and at a sensor-less site."`
	MessageAt     *time.Time `json:"messageAt" nullable:"true" doc:"When the sensor last reported its state, fresh or not; null at a sensor-less site."`
	Spaces        *int       `json:"spaces" nullable:"true" doc:"A sensor-less site's count of reserved spaces; null on a sensor bay."`
	ReservedWhen  *string    `json:"reservedWhen" nullable:"true" doc:"A sensor-less site's words for when the spaces are reserved, verbatim in German."`
	SourceFamily  string     `json:"sourceFamily"`
}

// ParkingBayCounts counts every bay of the kind within the radius.
type ParkingBayCounts struct {
	Total        int `json:"total"`
	Sensors      int `json:"sensors" doc:"Bays a sensor reports."`
	Free         int `json:"free"`
	Occupied     int `json:"occupied"`
	Unknown      int `json:"unknown" doc:"Bays a sensor reports with no current reading."`
	StaticSites  int `json:"staticSites" doc:"Sensor-less sites."`
	StaticSpaces int `json:"staticSpaces" doc:"The reserved spaces the sensor-less sites stand for."`
}

// ParkingBaysData is the data of listParkingBays.
type ParkingBaysData struct {
	Kind   string           `json:"kind"`
	Counts ParkingBayCounts `json:"counts"`
	Bays   []ParkingBay     `json:"bays"`
}

// ParkingBaysEnvelope is the enveloped answer of listParkingBays.
type ParkingBaysEnvelope struct {
	Data  ParkingBaysData `json:"data"`
	Meta  PagedMeta       `json:"meta"`
	Links Links           `json:"links"`
}

// ParkingBaysOutput is the response of listParkingBays.
type ParkingBaysOutput struct {
	CacheControl string `header:"Cache-Control"`
	Body         ParkingBaysEnvelope
}

func registerParkingBays(api huma.API, s *server, op huma.Operation) {
	op.Summary = "On-street bays near a position"
	op.Description = "The on-street bays a municipality publishes as open data, reserved for a disabled parking permit (kind=disabled) or for charging, nearest first: a sensor's free or occupied while its newest message is under three hours old, else no current reading; a sensor-less site with the publisher's count of reserved spaces, never free. The counts cover every bay of the kind in the radius. A bay is not a car park: listParkingOptions never lists one. Needs the scope parking."
	featureResponses(api, &op, "Not used by this operation.")
	op.Extensions[counterpartExtension] = "parking search, kind disabled_badge"
	huma.Register(api, op, func(ctx context.Context, in *BaysInput) (*ParkingBaysOutput, error) {
		if err := s.featuresOff(ctx); err != nil {
			return nil, err
		}
		q := parking.BayQuery{At: in.point, Kind: in.Kind, RadiusM: in.RadiusM, Limit: in.Limit, OnlyFree: in.OnlyFree == "true"}
		if q.Kind == "" {
			q.Kind = parking.BayKinds[0]
		}
		if q.RadiusM == 0 {
			q.RadiusM = baysDefaultRadiusM
		}
		if q.Limit == 0 {
			q.Limit = baysDefaultLimit
		}
		a, err := s.features.ParkingBays(ctx, principalFrom(ctx).Key, q)
		if err != nil {
			return nil, s.featureProblem(ctx, err)
		}
		return &ParkingBaysOutput{CacheControl: noStore, Body: baysEnvelope(s, ctx, a, q)}, nil
	})
}

func baysEnvelope(s *server, ctx context.Context, a features.BaysAnswer, q parking.BayQuery) ParkingBaysEnvelope {
	c := a.Counts
	data := ParkingBaysData{Kind: q.Kind, Bays: make([]ParkingBay, len(a.Items)), Counts: ParkingBayCounts{Total: c.Total, Sensors: c.Sensors,
		Free: c.Free, Occupied: c.Occupied, Unknown: c.Unknown(), StaticSites: c.StaticSites, StaticSpaces: c.StaticSpaces}}
	for i, b := range a.Items {
		data.Bays[i] = ParkingBay{ID: b.ID, Kind: b.Kind, PublisherKind: b.PublisherKind, Street: b.Street, HouseNumber: b.HouseNumber,
			Postcode: b.Postcode, City: b.City, Lat: b.Location.Lat, Lon: b.Location.Lon, DistanceM: b.DistanceM, Sensor: b.Sensor(),
			State: b.State, MessageAt: b.MessageAt, Spaces: b.Spaces, ReservedWhen: b.ReservedWhen, SourceFamily: b.Family}
	}
	return ParkingBaysEnvelope{Data: data,
		Meta:  PagedMeta{DataMeta: s.dataMeta(ctx, a.Meta.Families, a.Meta.At, a.Meta.Partial, partialDetail), Page: Page{Limit: q.Limit, HasMore: c.Total > len(a.Items)}},
		Links: s.selfLink(ctx)}
}
