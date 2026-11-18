package rules

import (
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

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
	// SameBoundaryM is how far apart two publishers' distances to the point
	// may lie and still be one zone's boundary (LEZ_SAME_BOUNDARY_M).
	SameBoundaryM = 500
	// GemeindeReachM is how far the Gemeinde point that finds a list entry
	// for a point may lie (the platform's bound for describing a position).
	GemeindeReachM = 15000
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
	// ChangedAt is the data's own date where the publisher states one;
	// RecordedAt when the publisher's document was last read.
	ChangedAt  *time.Time
	RecordedAt time.Time
	// Scope is the source's: city (a city's own document, with its Gemeinde
	// key PlaceCode and its SourceCity) or national (the register); nil for
	// a source the zone list does not name, which counts as a city's.
	Scope, PlaceCode, SourceCity *string
}

// National is whether the row is the national register's.
func (z *Zone) National() bool { return z.Scope != nil && *z.Scope == "national" }

// ListEntry is one entry of the curated zone list: the Gemeinden it speaks
// for, the plaque it states (nil: none is stated), whether we may carry the
// zone's data, and the day it was lifted.
type ListEntry struct {
	Key, Name  string
	PlaceCodes []string
	Plaque     *string
	Covered    bool
	LiftedOn   *time.Time
}

// Verdict is the zone answer of a point: inside a zone, in a released area,
// outside (the nearest zone named) or unknown (none within reach).
type Verdict struct {
	Kind       string // inside, transfer, outside, unknown
	Zone       *Zone
	Transfer   *Zone
	ExemptRoad *Zone
	// Also are the register's rows of the same zone that agree with the
	// deciding city row, credited beside it.
	Also []*Zone
}

// Rows are the rows the verdict used, the deciding one first.
func (v Verdict) Rows() []*Zone {
	var out []*Zone
	switch v.Kind {
	case "inside":
		out = append(append(out, v.Zone), v.Also...)
		if v.Transfer != nil {
			out = append(out, v.Transfer)
		}
		if v.ExemptRoad != nil {
			out = append(out, v.ExemptRoad)
		}
	case "transfer":
		out = append(out, v.Transfer)
	case "outside":
		out = append(append(out, v.Zone), v.Also...)
	}
	return out
}

// Decider is the row that decides the verdict, nil for unknown.
func (v Verdict) Decider() *Zone {
	if v.Kind == "transfer" {
		return v.Transfer
	}
	return v.Zone
}

// VerdictOf judges the zone rows of a point as the platform's lezVerdict
// does: a city's own document decides about its own zone, and the
// register's copy of that zone is credited where it agrees; a register zone
// that is no city's answers on its own; with no city zone in reach the
// register decides alone (or, with no register row, whatever answered).
// rows are ordered inside first, then by distance.
func VerdictOf(rows []Zone) Verdict {
	var city, nat []Zone
	cityZones := map[string]bool{}
	for _, r := range rows {
		if r.National() {
			nat = append(nat, r)
			continue
		}
		city = append(city, r)
		if r.Role == "zone" {
			cityZones[r.Key] = true
		}
	}
	if len(cityZones) == 0 {
		if len(nat) == 0 {
			return verdictOf(city)
		}
		return verdictOf(nat)
	}
	v := verdictOf(city)
	if v.Kind == "inside" {
		for i := range nat {
			if nat[i].Role == "zone" && nat[i].Inside && slices.Contains(nat[i].SameZone, v.Zone.Key) {
				v.Also = append(v.Also, &nat[i])
			}
		}
		return v
	}
	var own []Zone
	for _, r := range nat {
		if r.Role == "zone" && !slices.ContainsFunc(r.SameZone, func(k string) bool { return cityZones[k] }) {
			own = append(own, r)
		}
	}
	ov := verdictOf(own)
	if ov.Kind == "inside" {
		return ov
	}
	if v.Kind == "outside" {
		if ov.Kind == "outside" && ov.Zone.DistanceM < v.Zone.DistanceM {
			return ov
		}
		for i := range nat {
			r := &nat[i]
			d := r.DistanceM - v.Zone.DistanceM
			if r.Role == "zone" && !r.Inside && slices.Contains(r.SameZone, v.Zone.Key) && d <= SameBoundaryM && -d <= SameBoundaryM {
				v.Also = append(v.Also, r)
				break
			}
		}
	}
	return v
}

// verdictOf judges one publisher group's rows as the platform's verdictOf
// does: the first zone it is inside, with its own publisher's released area
// and exempt road; else a released area it is inside; else the nearest zone.
func verdictOf(rows []Zone) Verdict {
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

// keyName is a register key standing in for a zone's name ("DE_BW_2").
var keyName = regexp.MustCompile(`^[A-Z]{2}_[A-Z]{2}_[0-9]+$`)

// ZoneName is the zone's name as the platform's answer prints it: the
// register's zone, or one named only by its key, by its city, else by its
// publisher; a city document's by its city where its own name does not say
// it; a bare name gets the word Umweltzone.
func ZoneName(z *Zone) string {
	if z.National() || keyName.MatchString(z.Name) {
		if z.City != nil {
			return "Umweltzone " + *z.City
		}
		pub := ""
		if z.Publisher != nil {
			pub = *z.Publisher
		}
		return "Umweltzone (" + pub + ")"
	}
	if z.SourceCity != nil && !strings.Contains(strings.ToLower(z.Name), strings.ToLower(*z.SourceCity)) {
		return "Umweltzone " + *z.SourceCity
	}
	if strings.Contains(strings.ToLower(z.Name), "umweltzone") {
		return z.Name
	}
	return "Umweltzone " + z.Name
}

// ZoneAnswer is what the platform's zone answer states beside the verdict.
type ZoneAnswer struct {
	// Name is the zone's printed name (ZoneName), the released area's own
	// for a transfer; nil for unknown.
	Name *string
	// Plaque says whether a plaque is stated: only of a zone the point is in
	// and not released from. Entry is the list entry that states it, nil
	// for the national fact that every signposted zone asks for the green
	// plaque; FoundByPoint when the point's Gemeinde found the entry.
	Plaque       bool
	Entry        *ListEntry
	FoundByPoint bool
	// LiftedOn is the day the entry found says the zone was lifted.
	LiftedOn *time.Time
	// DataDate is the deciding row's own date, BoundaryReadAt its last read.
	DataDate, BoundaryReadAt *time.Time
}

// AnswerOf states v as the platform's lezAnswer does: the list entry is
// found by the deciding city source's own Gemeinde first, then by pointCode,
// the Gemeinde the point lies in.
func AnswerOf(v Verdict, pointCode *string, entries []ListEntry) ZoneAnswer {
	var a ZoneAnswer
	d := v.Decider()
	if d == nil {
		return a
	}
	name := d.Name
	if v.Kind != "transfer" {
		name = ZoneName(d)
	}
	a.Name = &name
	a.DataDate = d.ChangedAt
	read := d.RecordedAt
	a.BoundaryReadAt = &read
	find := func(code *string) *ListEntry {
		if code == nil {
			return nil
		}
		for i := range entries {
			if slices.Contains(entries[i].PlaceCodes, *code) {
				return &entries[i]
			}
		}
		return nil
	}
	var sourceCode *string
	if !d.National() {
		sourceCode = d.PlaceCode
	}
	entry := find(sourceCode)
	byPoint := false
	if entry == nil {
		entry = find(pointCode)
		byPoint = entry != nil
	}
	if v.Kind == "inside" && v.Transfer == nil {
		a.Plaque, a.Entry, a.FoundByPoint = true, entry, byPoint
	}
	if entry != nil {
		a.LiftedOn = entry.LiftedOn
	}
	return a
}
