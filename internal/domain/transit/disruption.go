package transit

import (
	"strings"
	"time"
	"unicode"
)

// Alert is a service alert in force, as its network publishes it.
type Alert struct {
	ID          string
	Header      *string
	Description *string
	Cause       *string
	Effect      *string
	URL         *string
	ActiveFrom  *time.Time
	ActiveTo    *time.Time
	Lines       []string
	// PlacedBy is what the alert is attached to: stop, route or network.
	PlacedBy  string
	ChangedAt *time.Time
	Family    string
}

// Punctuality is the share of late trips of an area over the source's
// window, as the source's region-wide totals count it.
type Punctuality struct {
	AreaLevel        string
	AreaCode         string
	AreaName         *string
	TripsObserved    int
	TripsLate        int
	TripsSkipping    int
	TripsUntimed     int
	MedianWorstDelay *int
	StopsSeen        int
	ObservedAt       *time.Time
	Family           string
}

// Disruptions are the service alerts in force of an area or line, at most
// the limit with the count before it, and the area's punctuality.
type Disruptions struct {
	// PunctualityRead are the families whose punctuality of the area was
	// read, whether or not it counted a trip: the platform's disruption
	// check credits its realtime source with "no reports" too.
	PunctualityRead []string
	Alerts          []Alert
	// AlertsTotal are the alerts in force before the limit, duplicates left
	// out; AlertsDuplicates the secondary publication's rows left out as
	// duplicates of a primary alert. AlertsChecked is whether a publication of the families
	// was alive to ask: without one no alert is listed, and none means
	// "could not tell", not "none". AlertFamilies are the families of every
	// row counted, which the platform credits when it lists any.
	AlertsTotal      int
	AlertsDuplicates int
	AlertsChecked    bool
	AlertFamilies    []string
	Punctuality      *Punctuality
	// Trips are the area's observed trips counted against the query's
	// LateOverMin; nil when the query asks for none or none was observed.
	Trips *TripCount
	// Line is what the static timetable says of the line asked; nil when
	// the query names none.
	Line *LineCheck
}

// LineCheck is whether the static timetable knows a line of the asked
// name (folded as FoldLine folds it), so an empty alert list can be told
// from a line that does not exist.
type LineCheck struct {
	// Checked is whether a timetable of the families was there to ask:
	// InTimetable false means no such line only when this is true.
	Checked bool
	// InTimetable is whether a line of the name is in it.
	InTimetable bool
	// Families are the timetable families that hold a line of the name.
	Families []string
}

// TripCount is how many distinct trips were observed at the stops of an area
// in the last half hour, and how many of them ran more than a threshold late
// at one of those stops: the count the platform's region watch reads.
type TripCount struct {
	Observed   int
	Late       int
	ObservedAt time.Time
	Family     string
}

// DisruptionQuery asks for the disruptions of Area (an official region
// code: two digits a Land, five a Kreis, longer a Gemeinde; empty: every
// area) and Line (a line name folded by FoldLine; empty: every line) at Now,
// at most Limit alerts; WindowMin is the window the caller asked for.
type DisruptionQuery struct {
	Area      string
	Line      string
	WindowMin int
	Limit     int
	Now       time.Time
	// LateOverMin, when above zero, asks for the area's trips more than
	// that many minutes late (Disruptions.Trips).
	LateOverMin int
}

// FoldLine folds a line name as the platform compares it: upper case, no
// white space ("RE 1" is "RE1").
func FoldLine(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return -1
		}
		return unicode.ToUpper(r)
	}, s)
}
