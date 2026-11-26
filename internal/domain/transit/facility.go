package transit

import (
	"cmp"
	"slices"
	"time"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// Facility is a lift or escalator of a station and its reported state.
type Facility struct {
	ID string
	// Kind is elevator, escalator or other.
	Kind     string
	Position *string
	// State is active, inactive or unknown: a state reported longer ago
	// than its family's freshness threshold is unknown, never the last
	// known word.
	State      string
	ObservedAt *time.Time
	Family     string
	// Listing is the family the unit's listing (its kind and position)
	// comes from when its state is another family's; "" when they are one.
	Listing string
	// EquipmentNo is DB's equipment number of the unit, as its source
	// lists it; nil when the source gives none.
	EquipmentNo *string
	// StateExplanation is the operator's explanation of the state ("not
	// monitored"), verbatim; nil when it gives none or one that repeats the
	// state.
	StateExplanation *string
	// NameShared marks a unit another unit of the station shares its kind
	// and position with, so an answer names its number.
	NameShared bool
}

// OnePerUnit keeps one row per DB equipment number, as today's check keeps
// them (dedupeByEquipment): the first, unless it is unknown and a later copy
// has a state; a row without a number is its own unit.
func OnePerUnit(fs []Facility) []Facility {
	at := map[string]int{}
	keep := make([]bool, len(fs))
	for i, f := range fs {
		if f.EquipmentNo == nil {
			keep[i] = true
			continue
		}
		j, seen := at[*f.EquipmentNo]
		if !seen || (fs[j].State == "unknown" && f.State != "unknown") {
			if seen {
				keep[j] = false
			}
			at[*f.EquipmentNo], keep[i] = i, true
		}
	}
	out := []Facility{}
	for i, f := range fs {
		if keep[i] {
			out = append(out, f)
		}
	}
	return out
}

// FacilityQuery asks for the facilities of a station: of one kind (empty:
// every kind), at most Limit.
type FacilityQuery struct {
	StationID string
	Kind      string
	Limit     int
}

// Facilities is a station's facilities shown and the count of every one of
// the asked kind by state.
type Facilities struct {
	Rows                      []Facility
	Active, Inactive, Unknown int
}

// facilityOrder lists out-of-service equipment first: a shorter list never
// hides a broken lift.
var facilityOrder = map[string]int{"inactive": 0, "unknown": 1, "active": 2}

// ListFacilities is the answer of q from a station's facilities, as the
// platform lists them: of the kind asked, out of service first, then
// unknown, then in service, then by position as German sorts it; the
// counts cover all of them, the rows at most q.Limit.
func ListFacilities(all []Facility, q FacilityQuery) Facilities {
	out := Facilities{Rows: []Facility{}}
	for _, f := range all {
		if q.Kind != "" && f.Kind != q.Kind {
			continue
		}
		switch f.State {
		case "active":
			out.Active++
		case "inactive":
			out.Inactive++
		default:
			f.State = "unknown"
			out.Unknown++
		}
		out.Rows = append(out.Rows, f)
	}
	shared := map[string]int{}
	for _, f := range out.Rows {
		shared[f.Kind+"\x00"+text(f.Position)]++
	}
	for i, f := range out.Rows {
		out.Rows[i].NameShared = shared[f.Kind+"\x00"+text(f.Position)] > 1
	}
	german := collate.New(language.German)
	slices.SortStableFunc(out.Rows, func(a, b Facility) int {
		return cmp.Or(cmp.Compare(facilityOrder[a.State], facilityOrder[b.State]), german.CompareString(text(a.Position), text(b.Position)))
	})
	if len(out.Rows) > q.Limit {
		out.Rows = out.Rows[:q.Limit]
	}
	return out
}
