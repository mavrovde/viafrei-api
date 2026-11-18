package transit

import (
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// SharingFreshS is how old, in seconds, a sharing system's last status may
// be for its counts to stand: the platform's rule of the answer (15
// minutes), not a tunable of the service.
const SharingFreshS = 900

// SharingFresh tells whether a status seen at seen is fresh at now.
func SharingFresh(seen *time.Time, now time.Time) bool {
	return seen != nil && now.Sub(*seen).Seconds() <= SharingFreshS
}

// SharingStation is a bike-sharing station with its latest count of the
// last 24 hours, if any.
type SharingStation struct {
	ID         string
	SystemID   string
	SystemName *string
	Name       *string
	Capacity   *int
	IsVirtual  *bool
	Location   geo.Point
	DistanceM  int
	// StatusSeenAt is when the system last published a status.
	StatusSeenAt      *time.Time
	VehiclesAvailable *int
	ElectricAvailable *int
	DocksAvailable    *int
	IsRenting         *bool
	IsReturning       *bool
	CountedAt         *time.Time
	ReportedAt        *time.Time
	Family            string
}

// Current tells whether the station's count stands at now: counted, and its
// system's status fresh.
func (s SharingStation) Current(now time.Time) bool {
	return s.VehiclesAvailable != nil && SharingFresh(s.StatusSeenAt, now)
}

// Available tells whether a bike can be taken at now: current, renting, and
// at least one vehicle counted.
func (s SharingStation) Available(now time.Time) bool {
	return s.Current(now) && (s.IsRenting == nil || *s.IsRenting) && *s.VehiclesAvailable > 0
}

// SharingVehicle is a free-floating vehicle at its latest position.
type SharingVehicle struct {
	FormFactor     *string
	Propulsion     *string
	RangeM         *int
	DistanceM      int
	VehiclesSeenAt *time.Time
	Family         string
}

// Electric tells whether the vehicle is electric or electrically assisted.
func (v SharingVehicle) Electric() bool {
	return v.Propulsion != nil && (*v.Propulsion == "electric" || *v.Propulsion == "electric_assist")
}

// SharingQuery asks for the sharing stations and vehicles within RadiusM of
// At, at Now.
type SharingQuery struct {
	At            geo.Point
	RadiusM       int
	Limit         int
	OnlyAvailable bool
	Now           time.Time
}

// The reads of the platform's sharing search: four times the most stations
// an answer lists, 200 vehicles, and the nearest station within 100 km.
const (
	SharingStationRead = 80
	SharingVehicleRead = 200
	SharingNearestM    = 100000
)
