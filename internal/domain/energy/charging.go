// Package energy is the charging context: charging sites near a position,
// their charge points and the last reported availability of each.
package energy

import (
	"slices"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// Other is the value of every open enumeration for a stored code the service
// cannot map (ENV-051): a raw storage code never reaches an answer.
const Other = "other"

// Access says who may use a charging site.
type Access string

// The known access values.
const (
	AccessPublic     Access = "public"
	AccessRestricted Access = "restricted"
	AccessUnknown    Access = "unknown"
	AccessOther      Access = Other
)

// Accesses lists every access value an answer can carry.
var Accesses = []Access{AccessPublic, AccessRestricted, AccessUnknown, AccessOther}

// MapAccess maps a stored access code.
func MapAccess(raw string) Access {
	return mapCode(raw, Accesses[:3], AccessOther)
}

// Current is the kind of current a charge point delivers.
type Current string

// The known currents.
const (
	CurrentAC    Current = "ac"
	CurrentDC    Current = "dc"
	CurrentOther Current = Other
)

// Currents lists every current an answer can carry.
var Currents = []Current{CurrentAC, CurrentDC, CurrentOther}

// MapCurrent maps a stored current code; nil (not published) stays nil.
func MapCurrent(raw *string) *Current {
	if raw == nil {
		return nil
	}
	c := mapCode(*raw, Currents[:2], CurrentOther)
	return &c
}

// State is the reported availability of a charge point.
type State string

// The known availability states.
const (
	StateAvailable State = "available"
	StateOccupied  State = "occupied"
	// StateReserved stays in the open enumeration, because removing a value
	// a response may carry is a breaking change (VER-002), but it is never
	// sent: a reserved report is answered occupied (MapState).
	StateReserved   State = "reserved"
	StateOutOfOrder State = "out_of_order"
	StateUnknown    State = "unknown"
	StateOther      State = Other
	// StateStale is the state of a report too old to count (Availability.Live):
	// an answer states it instead of the reported code. No stored code maps
	// to it.
	StateStale State = "stale"
)

// States lists every availability state of the contract. reserved, unknown
// and other stay in the enum but are never sent: MapState answers reserved
// occupied, and a report of unknown or other says nothing
// (Availability.Says), so its point is answered as availability: null.
var States = []State{StateAvailable, StateOccupied, StateReserved, StateOutOfOrder, StateUnknown, StateOther, StateStale}

// MapState maps a stored availability code; reserved is answered occupied.
func MapState(raw string) State {
	// A reserved point is not free to charge at, so it is answered
	// occupied: the MCP capability's rule (#18). No answer states reserved.
	if raw == string(StateReserved) {
		return StateOccupied
	}
	return mapCode(raw, States[:5], StateOther)
}

func mapCode[T ~string](raw string, known []T, other T) T {
	if i := slices.Index(known, T(raw)); i >= 0 {
		return known[i]
	}
	return other
}

// Availability is the last reported state of a charge point.
type Availability struct {
	State      State
	ObservedAt time.Time
	// Family is the source family of the report.
	Family string
}

// StatusLimit is how old a report may be and still count: the shorter of
// the status age limit (charging.status_max_age, the MCP capability's own
// limit) and the design threshold of the report's family. This is the MCP
// capability's rule for a point's status: a report older than either is
// stale.
func StatusLimit(maxAge, staleAfter time.Duration) time.Duration {
	return min(maxAge, staleAfter)
}

// Says reports whether the report states anything: a report of unknown, or
// of a code the service cannot map (other), says nothing about the point and
// is answered as no report at all. The MCP capability's rule: a status whose
// own word is unknown is no status, stale or otherwise.
func (a Availability) Says() bool {
	return a.State != StateUnknown && a.State != StateOther
}

// Live reports whether the report still counts at now: no older than limit
// (StatusLimit). A report exactly at the limit is live.
func (a Availability) Live(now time.Time, limit time.Duration) bool {
	return now.Sub(a.ObservedAt) <= limit
}

// Shown is the state an answer states at now: the reported one while the
// report is live, StateStale once it is not.
func (a Availability) Shown(now time.Time, limit time.Duration) State {
	if a.Live(now, limit) {
		return a.State
	}
	return StateStale
}

// ChargePoint is one point of a site.
type ChargePoint struct {
	ID           string
	Current      *Current
	MaxPowerKw   *float64
	Availability *Availability
}

// Site is a charging site near the asked position.
type Site struct {
	ID        string
	Family    string
	Name      *string
	Operator  *string
	Access    Access
	Location  geo.Point
	DistanceM int
	Points    []ChargePoint
}

// Families returns the source family of the site and of every availability
// report it carries that says something (Availability.Says): the families
// that contributed a fact, which the answer credits and the licence gate's
// post-check verifies. A report that says nothing is answered as no report,
// so its family contributed nothing (the MCP capability's rule).
func (s Site) Families() []string {
	out := []string{s.Family}
	for _, p := range s.Points {
		if p.Availability != nil && p.Availability.Says() {
			out = append(out, p.Availability.Family)
		}
	}
	return out
}

// Summary sums up a site's points.
type Summary struct {
	ChargePoints int
	// AvailableNow counts the points whose live report is available; nil
	// when no point has a live report.
	AvailableNow *int
	// MaxPowerKw is the highest power of any point; nil when none is known.
	MaxPowerKw *float64
}

// Summarise sums up the site at now. A report counts while it says something
// (Availability.Says) and is live (Availability.Live) by the limit of the
// report's own family, which may differ from the site's; an older one is
// shown on its point as stale and not counted.
func (s Site) Summarise(now time.Time, limit func(family string) time.Duration) Summary {
	out := Summary{ChargePoints: len(s.Points)}
	for _, p := range s.Points {
		if a := p.Availability; a != nil && a.Says() && a.Live(now, limit(a.Family)) {
			n := 0
			if out.AvailableNow != nil {
				n = *out.AvailableNow
			}
			if a.State == StateAvailable {
				n++
			}
			out.AvailableNow = &n
		}
		if p.MaxPowerKw != nil && (out.MaxPowerKw == nil || *p.MaxPowerKw > *out.MaxPowerKw) {
			out.MaxPowerKw = p.MaxPowerKw
		}
	}
	return out
}

// AccessFilter is the access the caller asks for.
type AccessFilter string

// The access filters.
const (
	FilterPublic     AccessFilter = "public"
	FilterRestricted AccessFilter = "restricted"
	FilterAll        AccessFilter = "all"
)

// Codes returns the access values a filter keeps; nil keeps every site.
func (f AccessFilter) Codes() []Access {
	if f == FilterAll {
		return nil
	}
	return []Access{Access(f)}
}

// Query asks for charging sites near a position.
type Query struct {
	At      geo.Point
	RadiusM int
	// MinKw keeps sites with at least one point of at least this power, and
	// of each site only those points.
	MinKw  float64
	Access AccessFilter
	Limit  int
}
