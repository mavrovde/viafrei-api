// Package places is the places context: a named place a feature can be asked
// around.
package places

import (
	"errors"
	"slices"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// Kind is the kind of a place.
type Kind string

// Other is the kind of a place whose stored kind the service cannot map.
const Other Kind = "other"

// Kinds lists every kind an answer can carry.
var Kinds = []Kind{"municipality", "district", "locality", "region", "state", "station_area", KindMotorway, KindJunction, Other}

// KindMotorway and KindJunction are the gazetteer's motorway places (G5), as
// the platform's place search lists them: a motorway by its number, a
// junction by its name ("AK Hilden (A3)"). A text that starts with a
// junction word asks for a junction (JunctionCore).
const (
	KindMotorway Kind = "motorway"
	KindJunction Kind = "junction"
)

// MapKind maps a stored kind code (ENV-051).
func MapKind(raw string) Kind {
	if i := slices.Index(Kinds[:len(Kinds)-1], Kind(raw)); i >= 0 {
		return Kinds[i]
	}
	return Other
}

// Place is one place.
type Place struct {
	ID       string
	Name     string
	Kind     Kind
	Location geo.Point
	// RegionCode is the official key of the district or Land it lies in;
	// nil when not recorded.
	RegionCode *string
	// OfficialKey is the place's own official key (a Gemeinde's eight
	// digits); nil when not recorded.
	OfficialKey *string
	// DistanceM is the distance from the asked position; nil without one.
	DistanceM *int
	Family    string
}

// Query asks for the places that answer a name: Text as typed (folded as
// the contract folds it), of one Kind (empty: any), ranked by distance
// from At when given and kept within RadiusM of it when that is set; at
// most Limit.
type Query struct {
	Text    string
	Kind    Kind
	At      *geo.Point
	RadiusM int
	Limit   int
}

// ErrNotFound means no place of a permitted family has the id.
var ErrNotFound = errors.New("places: no such place")
