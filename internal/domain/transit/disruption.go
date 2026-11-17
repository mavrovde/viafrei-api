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
	AlertsTotal     int
	Punctuality     *Punctuality
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
