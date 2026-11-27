// Package places is the places context: a named place a feature can be asked
// around.
package places

import (
	"errors"
	"regexp"
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
	// NameEn is the place's English name where it differs from Name; nil
	// else.
	NameEn *string
	// Kreis is the Kreis it lies in, in German (KreisOf); nil when none can
	// be named. KreisFamily is the family that named it, empty when none did.
	Kreis       *string
	KreisFamily string
	// DistanceM is the distance from the asked position; nil without one.
	DistanceM *int
	Family    string
	// PartOfKey is the Gemeinde key of the municipality a city district
	// (kind locality) is part of, and PartOf that municipality's name; nil
	// for any other place or when not recorded.
	PartOfKey *string
	PartOf    *string
	// Population is the place's population where the gazetteer holds one
	// above zero; nil else.
	Population *int
}

// GemeindeKey is the key the place's Kreis is named from: its own official
// key, else the key of the municipality it is part of.
func (p Place) GemeindeKey() *string {
	if p.OfficialKey != nil {
		return p.OfficialKey
	}
	return p.PartOfKey
}

// agsForm is the form of an official key that is an AGS, as the platform
// tells one (a fixture's "HE" is not).
var agsForm = regexp.MustCompile(`^[0-9]{2,12}$`)

// AGS is the place's official key when it is an AGS (Amtlicher
// Gemeindeschlüssel, or the leading part a Land or a district carries), as
// the platform states a place's key; nil else.
func (p Place) AGS() *string {
	if p.OfficialKey == nil || !agsForm.MatchString(*p.OfficialKey) {
		return nil
	}
	return p.OfficialKey
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
