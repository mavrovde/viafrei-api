package weather

import (
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// RoadQuery asks for the road-surface forecast near a position, as the
// platform's weather warnings check reads it (#222): the hours of the
// nearest Stations road-weather stations within RadiusM, valid from From
// to Until.
type RoadQuery struct {
	At          geo.Point
	Stations    int
	RadiusM     float64
	From, Until time.Time
}

// RoadForecast is the road-surface forecast near a position: its
// stations, nearest first, and whether the station catalogue is loaded at
// all, so an empty list can be told: no station within the radius when
// Loaded, the forecast not offered here when not.
type RoadForecast struct {
	Stations []RoadStation
	Loaded   bool
}

// RoadStation is a road-weather station and its forecast hours, as
// issued; nearest first.
type RoadStation struct {
	ID, Name string
	// Segment is the issuer's road segment type and PositionSource how its
	// position was taken, as issued; nil when not given.
	Segment        *int
	PositionSource *string
	Location       geo.Point
	DistanceM      int
	// Family is the source family of the station; ForecastFamily that of
	// its forecast hours.
	Family, ForecastFamily string
	// Hours are the forecast hours, earliest first.
	Hours []RoadHour
}

// RoadHour is one forecast hour of a station, as issued.
type RoadHour struct {
	ValidAt, RunAt time.Time
	// Class is the issuer's road-condition class (1 to 6) and SurfaceTempC
	// the road-surface temperature; nil when not given.
	Class        *int
	SurfaceTempC *float64
}
