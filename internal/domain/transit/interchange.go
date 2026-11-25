package transit

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// The interchange of a train board: the U-Bahn, Stadtbahn and tram that
// leave the station and the lines that serve it, as the MCP server's train
// departures show them under the board (station-interchange.ts).

// The interchange's bounds: at most InterchangeMax departures, read from
// InterchangeBackMin before the window's start (a U-Bahn planned before
// now may not have left).
const (
	InterchangeMax     = 10
	InterchangeBackMin = 30
)

// InterchangeModes are the modes the interchange's departures are of.
var InterchangeModes = []Mode{"subway", "tram"}

// BoardModeOf is a row's board mode from its timetable route type
// (boardModeOf): subway for the U-Bahn (1, 400 to 405), tram for the
// tram and Stadtbahn (0, 900 to 906), rail for any other.
func BoardModeOf(routeType *int) Mode {
	switch {
	case routeType == nil:
		return "rail"
	case *routeType == 1 || (*routeType >= 400 && *routeType <= 405):
		return "subway"
	case *routeType == 0 || (*routeType >= 900 && *routeType <= 906):
		return "tram"
	}
	return "rail"
}

// StopFamilyLines is one stop family of a station: its modes and its
// line names.
type StopFamilyLines struct {
	Modes []string
	Lines []string
}

// LineGroup is one group of "Lines here": one mode, or the modes a line
// name could not be told apart between, and its lines.
type LineGroup struct {
	Modes []string
	Lines []string
}

// lineModes are the modes "Lines here" names, in its order.
var lineModes = []string{"subway", "tram", "bus"}

var uName = regexp.MustCompile(`^U\s?\d`)

// StationLineGroups is today's stationLineGroups: a family's lines are its
// one non-rail mode; a train's name is left out; a "U" name is the U-Bahn
// where the family runs one, else the tram; another name of a family of
// several modes is listed once under them together, never guessed.
func StationLineGroups(rows []StopFamilyLines) []LineGroup {
	groups := map[string][]string{}
	var keys []string
	add := func(modes []string, line string) {
		k := strings.Join(modes, "/")
		if _, ok := groups[k]; !ok {
			keys = append(keys, k)
		}
		if !slices.Contains(groups[k], line) {
			groups[k] = append(groups[k], line)
		}
	}
	for _, f := range rows {
		var modes []string
		for _, m := range lineModes {
			if slices.Contains(f.Modes, m) {
				modes = append(modes, m)
			}
		}
		if len(modes) == 0 {
			continue
		}
		subway, tram := slices.Contains(modes, "subway"), slices.Contains(modes, "tram")
		uNamed := subway && slices.ContainsFunc(f.Lines, func(l string) bool { return uName.MatchString(strings.TrimSpace(l)) })
		for _, raw := range f.Lines {
			line := strings.TrimSpace(raw)
			switch {
			case line == "" || railLineName.MatchString(line):
			case uName.MatchString(line) && (subway || tram):
				add([]string{map[bool]string{true: "subway", false: "tram"}[subway]}, line)
			default:
				could := modes
				if uNamed {
					could = slices.DeleteFunc(slices.Clone(modes), func(m string) bool { return m == "subway" })
				}
				if len(could) > 0 {
					add(could, line)
				}
			}
		}
	}
	single := map[string]bool{}
	for _, k := range keys {
		if !strings.Contains(k, "/") {
			for _, l := range groups[k] {
				single[l] = true
			}
		}
	}
	order := func(k string) int {
		ms := strings.Split(k, "/")
		return len(ms)*10 + slices.Index(lineModes, ms[0])
	}
	slices.SortStableFunc(keys, func(a, b string) int { return order(a) - order(b) })
	out := []LineGroup{}
	for _, k := range keys {
		lines := slices.DeleteFunc(slices.Clone(groups[k]), func(l string) bool { return strings.Contains(k, "/") && single[l] })
		slices.SortFunc(lines, NumericCompare)
		if len(lines) > 0 {
			out = append(out, LineGroup{Modes: strings.Split(k, "/"), Lines: lines})
		}
	}
	return out
}

var digitRun = regexp.MustCompile(`\d+|\D+`)

// NumericCompare orders names as a German numeric collation does for line
// names: runs of digits by their value, the rest case-insensitively.
func NumericCompare(a, b string) int {
	x, y := digitRun.FindAllString(a, -1), digitRun.FindAllString(b, -1)
	for i := 0; i < len(x) && i < len(y); i++ {
		n, errN := strconv.Atoi(x[i])
		m, errM := strconv.Atoi(y[i])
		switch {
		case errN == nil && errM == nil && n != m:
			return n - m
		case errN == nil && errM == nil:
		case errN == nil:
			return -1
		case errM == nil:
			return 1
		default:
			if c := strings.Compare(strings.ToLower(x[i]), strings.ToLower(y[i])); c != 0 {
				return c
			}
		}
	}
	return len(x) - len(y)
}

// StationPairingM is how far from a station's StaDa point a zHV stop is
// the station's (STATION_DHID_RADIUS_M).
const StationPairingM = 300

// InterchangeRead is what the store read of a station's interchange: the
// stop families' lines, the departing calls of its timetable stops, and
// whether the station has any timetable stop.
type InterchangeRead struct {
	Lines []StopFamilyLines
	Calls []Departure
	Stops bool
}

// Interchange is the block under a train board: the U-Bahn and tram
// departures and the lines here.
type Interchange struct {
	Departures []Departure
	Lines      []LineGroup
}

// InterchangeOf is today's readInterchange over the read calls: the calls
// not yet departed at now whose planned or expected time is from or later,
// in planned order, the first InterchangeMax, those of the U-Bahn and the
// tram; nil when the station has no stop, or neither a departure nor a
// line to name.
func InterchangeOf(r InterchangeRead, from, now time.Time) *Interchange {
	if !r.Stops {
		return nil
	}
	var kept []Departure
	for _, d := range r.Calls {
		at := d.PlannedAt
		if d.ExpectedAt != nil {
			at = *d.ExpectedAt
		}
		if at.Before(now) || (d.PlannedAt.Before(from) && at.Before(from)) {
			continue
		}
		kept = append(kept, d)
	}
	slices.SortStableFunc(kept, func(a, b Departure) int { return a.PlannedAt.Compare(b.PlannedAt) })
	out := &Interchange{Departures: []Departure{}, Lines: StationLineGroups(r.Lines)}
	for _, d := range kept[:min(InterchangeMax, len(kept))] {
		rt := d.RouteType
		if BoardModeOf(&rt) != "rail" {
			out.Departures = append(out.Departures, d)
		}
	}
	if len(out.Departures) == 0 && len(out.Lines) == 0 {
		return nil
	}
	return out
}

// InterchangeFamily is the family the interchange's stops and lines are
// read from: the DELFI timetable.
const InterchangeFamily = "delfi_static"
