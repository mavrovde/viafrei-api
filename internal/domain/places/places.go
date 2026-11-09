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
var Kinds = []Kind{"municipality", "district", "locality", "region", "state", "station_area", Other}

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
	Family   string
}

// ErrNotFound means no place of a permitted family has the id.
var ErrNotFound = errors.New("places: no such place")
