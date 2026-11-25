package httpapi

import (
	"math"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/parking"
)

// NearbyAmenity is an amenity OpenStreetMap maps near a parking site.
type NearbyAmenity struct {
	Kind        string   `json:"kind" enum:"toilet,shower,restaurant,snack,cafe,shop,drinking_water" doc:"Open enumeration; treat an unknown value as other."`
	Name        string   `json:"name"`
	DistanceM   int      `json:"distanceM" doc:"Distance from the site, in metres."`
	Location    Position `json:"location"`
	OSMDataAsOf *string  `json:"osmDataAsOf" nullable:"true" format:"date" doc:"The day of the OpenStreetMap extract the entry comes from."`
}

// OSMCarPark is a car park OpenStreetMap maps near the asked position.
type OSMCarPark struct {
	ID          string   `json:"id" doc:"The OpenStreetMap element (node/1, way/2, relation/3)."`
	Name        *string  `json:"name" nullable:"true"`
	Operator    *string  `json:"operator" nullable:"true"`
	Street      *string  `json:"street" nullable:"true"`
	HouseNumber *string  `json:"houseNo" nullable:"true"`
	Postcode    *string  `json:"zip" nullable:"true"`
	Place       *string  `json:"town" nullable:"true"`
	Location    Position `json:"location"`
	DistanceM   int      `json:"distanceM" doc:"Distance from the asked position, in metres."`
}

// ParkingContextItem is a coach place or an on-street bay rule near the
// asked position.
type ParkingContextItem struct {
	Kind         string         `json:"kind" enum:"coach_parking,street_parking" doc:"Open enumeration; treat an unknown value as other."`
	SourceFamily string         `json:"sourceFamily"`
	ID           string         `json:"id"`
	Variant      *string        `json:"variant" nullable:"true" doc:"The publisher's variant of the kind."`
	Name         *string        `json:"name" nullable:"true"`
	Ref          *string        `json:"ref" nullable:"true"`
	Detail       *string        `json:"detail" nullable:"true"`
	Facts        map[string]any `json:"facts" nullable:"true" doc:"The publisher's facts as published (its period, a coach count, the bays' rules)."`
	Location     Position       `json:"location" doc:"The point of the entry nearest the asked position."`
	DistanceM    int            `json:"distanceM"`
	SeenAt       time.Time      `json:"publishedSeenAt" doc:"When the source last published the entry."`
	KindTotal    int            `json:"kindTotal" doc:"The entries of this kind in its radius (2 km for coach places, 300 m for bays), the bays counted one per bay inside it."`
	KindVariants map[string]int `json:"kindVariants" nullable:"true" doc:"kindTotal by variant."`
	PartsInside  int            `json:"partsInside" doc:"The entry's own bays inside the radius; 1 for an entry not counted by bay."`
}

// ParkingAround is meta.around of listParkingOptions: what the platform's
// parking search shows beside its list.
type ParkingAround struct {
	OSMCarParks []OSMCarPark         `json:"osmCarParks" nullable:"true" doc:"OpenStreetMap's car parks (ODbL; no capacity, no occupancy) within the radius that are none of the listed sites, nearest first, same-named ones as one, at most the limit; null when not read: a lorry or rest-area question, or a key that may not read OpenStreetMap. Credited in meta.attribution, with its share-alike offer, only when one is listed."`
	OSMDataAsOf *string              `json:"osmDataAsOf" nullable:"true" format:"date" doc:"The newest OpenStreetMap extract day of the car parks listed; null when none is listed."`
	RoadContext []ParkingContextItem `json:"roadContext" nullable:"true" doc:"The city's coach places within 2 km and on-street bay rules within 300 m, three of each at most, the coach places for parking first; empty where no city publishes them; null when not read: a question with a kind, free_min or facilities."`
}

func nearbyAmenities(n []parking.Nearby) []NearbyAmenity {
	out := make([]NearbyAmenity, len(n))
	for i, v := range n {
		out[i] = NearbyAmenity{Kind: v.Amenity, Name: v.Name, DistanceM: v.Metres, Location: position(v.At), OSMDataAsOf: v.ExtractDate}
	}
	return out
}

func osmCarParks(rows []parking.OSMCarPark) []OSMCarPark {
	out := make([]OSMCarPark, len(rows))
	for i, v := range rows {
		out[i] = OSMCarPark{ID: v.ID, Name: v.Name, Operator: v.Operator, Street: v.Street, HouseNumber: v.HouseNumber, Postcode: v.Postcode,
			Place: v.Place, Location: position(v.At), DistanceM: int(math.Round(v.DistanceM))}
	}
	return out
}

// parkingAround is meta.around of an answer; nil when nothing beside the
// list was asked.
func parkingAround(a *parking.Around) *ParkingAround {
	if a == nil {
		return nil
	}
	out := &ParkingAround{}
	if a.OSMAsked {
		out.OSMCarParks = osmCarParks(a.OSMCarParks)
		out.OSMDataAsOf = parking.OSMDataAsOf(a.OSMCarParks)
	}
	if a.ContextRead {
		out.RoadContext = make([]ParkingContextItem, len(a.Context))
		for i, v := range a.Context {
			out.RoadContext[i] = ParkingContextItem{Kind: v.Kind, SourceFamily: v.Family, ID: v.ID, Variant: v.Variant, Name: v.Name, Ref: v.Ref, Detail: v.Detail,
				Facts: v.Facts, Location: position(v.At), DistanceM: int(math.Round(v.DistanceM)), SeenAt: v.LastSeenAt, KindTotal: v.Total,
				KindVariants: v.Variants, PartsInside: v.PartsIn}
		}
	}
	return out
}

// siteNearby is the amenities near the i-th listed site; nil when not read.
func siteNearby(a *parking.Around, i int) []NearbyAmenity {
	if a == nil || a.Nearby == nil {
		return nil
	}
	return nearbyAmenities(a.Nearby[i])
}
