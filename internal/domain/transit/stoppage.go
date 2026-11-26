package transit

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
)

// A stop's own page on the platform is one of the stops around a station
// page (/<lang>/station/<slug>/stop/<key>): the bus, tram and U-Bahn stop
// families within StationAroundM of the station's point, StationAroundPerMode
// of each mode, the station's own left out, picked as the stops around a
// position are (PickStops) and at most StationStopsMax of them.
const (
	StationAroundM       = 400
	StationAroundPerMode = 8
	StationStopsMax      = 8
	// StationDHIDRadiusM is how far a zHV stop may lie from a StaDa point of
	// the station's EVA numbers and still be the station's own.
	StationDHIDRadiusM = 300
)

// StationAroundModes are the modes of the stops around a station page, in
// its order.
var StationAroundModes = []string{"subway", "tram", "bus"}

// StationAround is a station page near a stop: its slug, its own DHIDs and
// the families its point's probe found, in the probe's order, with each
// family's stop ids by root.
type StationAround struct {
	Slug  string
	Own   []string
	Near  []StopNear
	Stops map[string][]string
}

// StopLink is a stop's page under a station page: the station's slug and
// the stop's key.
type StopLink struct {
	Slug string
	Key  string
}

var stationLevel = regexp.MustCompile(`^(de:\d{5}:[^:_]+)`)

// StationDHID is the station level of a DHID (de:05315:11201:1:4 is
// de:05315:11201's); "" when id is no DHID.
func StationDHID(id string) string {
	if m := stationLevel.FindStringSubmatch(id); m != nil {
		return m[1]
	}
	return ""
}

// StopKey is a stop family's key in its page's address: its id with the
// colons folded.
func StopKey(id string) string {
	return strings.ReplaceAll(id, ":", "-")
}

// ListedStop is the page of stop (a stop id, without its source prefix) under the
// nearest of stations (nearest first) that lists it among the stops around
// it, keyed by the family that holds it; nil when none lists it.
func ListedStop(stations []StationAround, stop string) *StopLink {
	for _, st := range stations {
		own := map[string]bool{}
		for _, d := range st.Own {
			own[cmp.Or(StationDHID(d), d)] = true
		}
		isOwn := func(id string) bool { return own[id] || own[StationDHID(id)] }
		var rows []StopNear
		for _, r := range st.Near {
			if !isOwn(r.Root) && !slices.ContainsFunc(st.Stops[r.Root], isOwn) {
				rows = append(rows, r)
			}
		}
		for _, f := range PickStops(rows, StationStopsMax) {
			for _, root := range f.Roots {
				if root == stop || slices.Contains(st.Stops[root], stop) {
					return &StopLink{Slug: st.Slug, Key: StopKey(f.Root)}
				}
			}
		}
	}
	return nil
}
