package places

import (
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// PoiCategories are the OpenStreetMap families a place of interest is
// searched in, as on the platform.
var PoiCategories = []string{"office", "amenity", "shop", "tourism", "healthcare", "leisure", "industrial", "historic", "natural", "building"}

// PoiReach is how far a search pages: the platform lists at most 50 matches.
const PoiReach = 50

// Poi is a place of interest OpenStreetMap maps.
type Poi struct {
	ID            string
	Name          *string
	Category      string
	CategoryValue *string
	Brand         *string
	Operator      *string
	Street        *string
	HouseNumber   *string
	Postcode      *string
	Place         *string
	Location      geo.Point
	// DistanceM is the distance from the position asked; nil when none was.
	DistanceM *int
	DataAsOf  *time.Time
	Family    string
}

// PoiQuery asks for the places of interest whose name or brand holds Name:
// in the town In, or within RadiusM of At, or anywhere; in Category when
// set; Limit of them from Offset, best match first.
type PoiQuery struct {
	Name     string
	In       string
	At       *geo.Point
	RadiusM  int
	Category string
	Limit    int
	Offset   int
}
