package roads

import (
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// SpeedCamera is a published speed-check location: a town's official
// list (a fixed camera, a section control, an announced mobile check, a
// measuring site) or a fixed camera OpenStreetMap maps.
type SpeedCamera struct {
	ID            string
	Kind          string
	Enforces      *string
	Street        *string
	LocationText  *string
	Municipality  *string
	Direction     *string
	MaxSpeedKmh   *int
	MaxSpeedHGVKm *int
	ValidFrom     *time.Time
	ValidTo       *time.Time
	DataAsOf      *time.Time
	// StaleYear is the year of the publication when it is over twelve
	// months old on the day asked; nil otherwise.
	StaleYear *int
	Location  *geo.Point
	Family    string
}

// SpeedQuery asks for the published locations within RadiusM of At on the
// Europe/Berlin day Today, at most Limit of each block.
type SpeedQuery struct {
	At      geo.Point
	RadiusM int
	Today   time.Time
	Limit   int
}

// SpeedCameras are the two blocks of the answer, each with the number of
// locations before its limit.
type SpeedCameras struct {
	Official      []SpeedCamera
	OfficialTotal int
	OSM           []SpeedCamera
	OSMTotal      int
}
