package rules

import "github.com/mavrovde/viafrei-api/internal/domain/geo"

// The platform's reach of a low-emission-zone check:
// a zone or released area within ZoneReachM of the point is named, an
// exempt road within ExemptRoadReachM.
const (
	ZoneReachM       = 25000
	ExemptRoadReachM = 60
	// FreshCadences is how many of its own cadences a zone source's last check
	// may lie back before its rows drop out; DefaultCadenceS when it states
	// none (30 days).
	FreshCadences   = 3
	DefaultCadenceS = 2592000
)

// ZoneQuery asks for the zone rows at a point, or of a city.
type ZoneQuery struct {
	At   *geo.Point
	City string
}

// Zone is one published zone row: a zone, a road it does not apply to
// (exempt_road) or an area released at its edge (transfer).
type Zone struct {
	Key, ZoneID, Role, Name string
	City, Publisher         *string
	// Inside is whether the point lies in it (on its boundary counts);
	// DistanceM 0 when inside, else the distance to it.
	Inside    bool
	DistanceM int
	// SameZone are the other publishers' rows of the same zone in this read.
	SameZone []string
	Family   string
}

// Verdict is the zone answer of a point: inside a zone, in a released area,
// outside (the nearest zone named) or unknown (none within reach).
type Verdict struct {
	Kind       string // inside, transfer, outside, unknown
	Zone       *Zone
	Transfer   *Zone
	ExemptRoad *Zone
}

// VerdictOf judges the zone rows of a point as the platform's verdictOf
// does: the first zone it is inside, with its own publisher's released area
// and exempt road; else a released area it is inside; else the nearest zone.
// rows are ordered inside first, then by distance.
func VerdictOf(rows []Zone) Verdict {
	for i := range rows {
		z := &rows[i]
		if z.Role != "zone" || !z.Inside {
			continue
		}
		v := Verdict{Kind: "inside", Zone: z}
		for j := range rows {
			r := &rows[j]
			if r.Family != z.Family {
				continue
			}
			if r.Role == "transfer" && r.Inside && v.Transfer == nil {
				v.Transfer = r
			}
			if r.Role == "exempt_road" && v.ExemptRoad == nil {
				v.ExemptRoad = r
			}
		}
		return v
	}
	for i := range rows {
		if rows[i].Role == "transfer" && rows[i].Inside {
			return Verdict{Kind: "transfer", Transfer: &rows[i]}
		}
	}
	var nearest *Zone
	for i := range rows {
		if rows[i].Role == "zone" && (nearest == nil || rows[i].DistanceM < nearest.DistanceM) {
			nearest = &rows[i]
		}
	}
	if nearest == nil {
		return Verdict{Kind: "unknown"}
	}
	return Verdict{Kind: "outside", Zone: nearest}
}
