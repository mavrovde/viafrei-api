// Package parking is the parking context: parking sites near a position and
// the last reported occupancy of each.
package parking

import (
	"slices"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// Other is the value of every open enumeration for a stored code the service
// cannot map (ENV-051).
const Other = "other"

// Kind is the kind of a parking site.
type Kind string

// The known kinds.
const (
	KindGarage       Kind = "garage"
	KindLot          Kind = "lot"
	KindParkAndRide  Kind = "park_and_ride"
	KindOnStreet     Kind = "on_street"
	KindRestArea     Kind = "rest_area"
	KindTruckParking Kind = "truck_parking"
	KindOther        Kind = Other
)

// Kinds lists every kind an answer can carry.
var Kinds = []Kind{KindGarage, KindLot, KindParkAndRide, KindOnStreet, KindRestArea, KindTruckParking, KindOther}

// MapKind maps a stored kind code.
func MapKind(raw string) Kind { return mapCode(raw, Kinds[:6], KindOther) }

// State is the reported occupancy state of a site.
type State string

// The known occupancy states.
const (
	StateFree    State = "free"
	StateFilling State = "filling"
	StateFull    State = "full"
	StateClosed  State = "closed"
	StateUnknown State = "unknown"
	StateOther   State = Other
)

// States lists every occupancy state an answer can carry.
var States = []State{StateFree, StateFilling, StateFull, StateClosed, StateUnknown, StateOther}

// MapState maps a stored occupancy code.
func MapState(raw string) State { return mapCode(raw, States[:5], StateOther) }

func mapCode[T ~string](raw string, known []T, other T) T {
	if i := slices.Index(known, T(raw)); i >= 0 {
		return known[i]
	}
	return other
}

// Occupancy is the last reported occupancy of a site. Free and Total are nil
// where the source published none: an unknown count is never zero (ENV-041).
type Occupancy struct {
	Free, Total *int
	State       State
	ObservedAt  time.Time
	// Family is the source family of the report.
	Family string
}

// Site is a parking site near the asked position.
type Site struct {
	ID        string
	Family    string
	Name      *string
	Operator  *string
	Kind      Kind
	Location  geo.Point
	DistanceM int
	// Unranged is set when the site is stated without a distance, as the
	// platform states it: along a motorway (no point was asked), and a site
	// of a family whose terms need accepting (a Toll Collect site, which
	// the platform names beside its list, never by distance).
	Unranged bool
	// TermsRequired is set when the family's terms must be accepted before
	// its occupancy is given; the occupancy is then withheld.
	TermsRequired bool
	Occupancy     *Occupancy
}

// Withheld returns the site as it is listed before its terms are accepted
// (SEC-030): without its occupancy, marked as needing the terms.
func (s Site) Withheld() Site {
	s.Occupancy = nil
	s.TermsRequired = true
	return s
}

// Query asks for parking sites near a position, or along a motorway.
type Query struct {
	At      geo.Point
	RadiusM int
	// Road, when set, asks for the sites along that motorway instead of
	// near At: the platform's corridor (CorridorM of the road's points, or
	// filed under the road by their source), in name order, with no distance.
	Road string
	// Kind keeps only the sites whose source stores this kind (the
	// platform's car_park, park_and_ride or truck); empty keeps every kind.
	Kind string
	// Amenities keeps only the sites whose own publisher states all of them.
	Amenities []string
	// FreeMin keeps only sites whose live report has at least this many free
	// spaces; zero keeps every site.
	FreeMin int
	Limit   int
}

// CorridorM is how far from one of a motorway's location-code points a
// facility is still on the motorway, in metres: the platform's corridor.
const CorridorM = 1500

// FilterKinds are the kinds a query can keep, as the platform's parking
// search names them; rest_area is accepted and answered unfiltered, because
// no source classifies a rest area.
var FilterKinds = []string{"car_park", "park_and_ride", "truck", "rest_area"}

// FilterKind is the stored kind a requested kind keeps; empty keeps every kind.
func FilterKind(asked string) string {
	if asked == "rest_area" {
		return ""
	}
	return asked
}

// Amenities is the amenity vocabulary of the platform's parking search.
var Amenities = []string{"toilet", "shower", "restaurant", "snack", "cafe", "shop", "fuel", "charging", "drinking_water", "picnic", "playground", "wifi", "security", "accessible_toilet"}
