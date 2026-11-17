package parking

import (
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// The on-street bays a municipality publishes as open data: a bay reserved
// for a disabled parking permit or for charging, with a sensor's free or
// occupied where one is fitted, or a sensor-less site with the publisher's
// count of reserved spaces. A bay is not a car park: the platform answers
// it only when such a bay is asked for (find_parking kind=disabled_badge).

// BayKinds are the bay kinds a read may ask for.
var BayKinds = []string{"disabled", "ev_charging"}

// BayStateFresh is how long a bay sensor's state counts after the
// publisher's newest message: a sensor reports on a change and otherwise
// only as a heartbeat, so three hours is a sensor still reporting; an older
// state is no reading, never the last one (the platform's BAY_STATE_FRESH_S).
const BayStateFresh = 3 * time.Hour

// BayQuery asks for the bays of a kind within RadiusM of At, at most Limit,
// only the ones reported free when OnlyFree.
type BayQuery struct {
	At       geo.Point
	RadiusM  int
	Kind     string
	Limit    int
	OnlyFree bool
	Now      time.Time
}

// Bay is one bay or sensor-less site.
type Bay struct {
	ID            string
	Kind          string
	PublisherKind *string
	Street        *string
	HouseNumber   *string
	Postcode      *string
	City          *string
	Location      geo.Point
	DistanceM     int
	// State is free or occupied while the newest message is fresh; nil
	// otherwise, and always nil at a sensor-less site.
	State *string
	// MessageAt is when the sensor last reported the state, fresh or not.
	MessageAt *time.Time
	// Spaces and ReservedWhen describe a sensor-less site: the publisher's
	// count of reserved spaces and its words for when they are reserved.
	Spaces       *int
	ReservedWhen *string
	Family       string
}

// Sensor is whether a sensor reports the bay: false exactly at a
// sensor-less site.
func (b Bay) Sensor() bool { return b.Spaces == nil }

// BayCounts counts every bay of the asked kind within the radius, listed
// or not: those a sensor reports, free or occupied now, and the sensor-less
// sites with the spaces they stand for.
type BayCounts struct {
	Total, Sensors, Free, Occupied, StaticSites, StaticSpaces int
}

// Unknown is the bays a sensor reports that have no current reading.
func (c BayCounts) Unknown() int { return c.Sensors - c.Free - c.Occupied }

// Bays is a bay read: the listed bays, nearest first, and the counts over
// the whole radius.
type Bays struct {
	Items  []Bay
	Counts BayCounts
}
