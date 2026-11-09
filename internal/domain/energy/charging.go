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
	StateAvailable  State = "available"
	StateOccupied   State = "occupied"
	StateReserved   State = "reserved"
	StateOutOfOrder State = "out_of_order"
	StateUnknown    State = "unknown"
	StateOther      State = Other
)

// States lists every availability state an answer can carry.
var States = []State{StateAvailable, StateOccupied, StateReserved, StateOutOfOrder, StateUnknown, StateOther}

// MapState maps a stored availability code.
func MapState(raw string) State {
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
// report it carries: what the licence gate's post-check verifies.
func (s Site) Families() []string {
	out := []string{s.Family}
	for _, p := range s.Points {
		if p.Availability != nil {
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

// Summarise sums up the site at now. A report is live when it is no older
// than staleAfter of the report's own family, which may differ from the
// site's; an older one is shown on its point but not counted.
func (s Site) Summarise(now time.Time, staleAfter func(family string) time.Duration) Summary {
	out := Summary{ChargePoints: len(s.Points)}
	for _, p := range s.Points {
		if a := p.Availability; a != nil && now.Sub(a.ObservedAt) <= staleAfter(a.Family) {
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
	// MinKw keeps sites with at least one point of at least this power.
	MinKw  float64
	Access AccessFilter
	Limit  int
}
