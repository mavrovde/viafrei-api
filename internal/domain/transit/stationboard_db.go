package transit

import (
	"cmp"
	"slices"
	"strings"
	"time"
)

// railTravels are the travel modes of the rows DB's board can speak for:
// the trains of the station's own board (the platform's BOARD_GROUP), never
// a bus, tram or underground row.
var railTravels = []string{TravelLong, TravelRegional, TravelSBahn}

// WithTrainBoard is a station page's board composed with DB's board of the
// station, as the platform's station page composes them (matchDbBoard,
// dbRowOf): each departure of DB's board takes the train row of the
// timetable it is (the same line, or the train's number, within two
// minutes, a row to the same town first, then the nearest), and stands in
// its place with DB's figures and platform; a DB departure no row is is
// added when it is planned within [from, until]. A board that is degraded
// or absent speaks for nothing: rows come back as they are and spoke is
// false. Before what a stored board covers only its departures are read.
func WithTrainBoard(rows []Departure, b *TrainBoard, from, until time.Time) (out []Departure, spoke bool) {
	if b == nil || b.Degraded {
		return rows, false
	}
	covered := func(t time.Time) bool { return b.CoversUntil == nil || t.Before(*b.CoversUntil) }
	var rail []int
	for i, d := range rows {
		if slices.Contains(railTravels, TravelOf(d)) && (b.CoversUntil == nil || d.PlannedAt.Before(b.CoversUntil.Add(railTwinWindow))) {
			rail = append(rail, i)
		}
	}
	deps := slices.Clone(b.Departures)
	slices.SortStableFunc(deps, func(x, y TrainDeparture) int { return x.PlannedAt.Compare(y.PlannedAt) })
	spoken := map[int]TrainDeparture{}
	var only []TrainDeparture
	for _, x := range deps {
		if !covered(x.PlannedAt) {
			continue
		}
		label := TrainLabel(x)
		town := townWord(x.Destination)
		var cands []int
		for _, i := range rail {
			d := rows[i]
			if _, taken := spoken[i]; taken || d.PlannedAt.Sub(x.PlannedAt).Abs() > railTwinWindow {
				continue
			}
			if sameLine(label, d.Line) || (d.TrainNumber != nil && sameLine(label, d.TrainNumber)) {
				cands = append(cands, i)
			}
		}
		if len(cands) > 0 {
			slices.SortStableFunc(cands, func(a, c int) int {
				return cmp.Or(cmp.Compare(townRank(town, rows[a]), townRank(town, rows[c])),
					cmp.Compare(rows[a].PlannedAt.Sub(x.PlannedAt).Abs(), rows[c].PlannedAt.Sub(x.PlannedAt).Abs()))
			})
			spoken[cands[0]] = x
			continue
		}
		if !x.PlannedAt.Before(from) && !x.PlannedAt.After(until) {
			only = append(only, x)
		}
	}
	if len(spoken) == 0 && len(only) == 0 {
		return rows, false
	}
	out = make([]Departure, 0, len(rows)+len(only))
	for i, d := range rows {
		if x, ok := spoken[i]; ok {
			out = append(out, dbRow(x, &d))
			continue
		}
		out = append(out, d)
	}
	for _, x := range only {
		out = append(out, dbRow(x, nil))
	}
	slices.SortStableFunc(out, func(x, y Departure) int { return x.PlannedAt.Compare(y.PlannedAt) })
	return out, true
}

func townRank(town *string, d Departure) int {
	if town != nil && sameKey(town, townWord(d.Destination)) {
		return 0
	}
	return 1
}

// TrainLabel is what a person reads on DB's board for a departure: its
// line where the board names one (RE 1, S 3), else its category and number
// (ICE 571); nil when the board names neither.
func TrainLabel(x TrainDeparture) *string {
	if l := strings.TrimSpace(deref(x.Line)); l != "" {
		return &l
	}
	l := strings.TrimSpace(strings.TrimSpace(deref(x.Category)) + " " + strings.TrimSpace(deref(x.Number)))
	if l == "" {
		return nil
	}
	return &l
}

// dbRow is a departure of DB's board as the station page's row
// (dbRowOf): DB's planned time, figure, platform and cancellation, credited
// to DB Timetables; the timetable's row it speaks for, where there is one,
// lends its journey, route type and mode, never a figure. A row no
// timetable row is is a train, or a bus when DB's board says one.
func dbRow(x TrainDeparture, d *Departure) Departure {
	fam := TrainBoardFamily
	src := fam
	out := Departure{PlannedAt: x.PlannedAt, Line: TrainLabel(x), Category: x.Category, TrainNumber: x.Number, Destination: x.Destination,
		Headsign: x.Destination, PlannedPlatform: x.Platform, Platform: x.Platform, PlatformChanged: x.PlatformChanged(),
		Live: true, Source: &src, Cancelled: x.Cancelled, Family: fam, Families: []string{fam}}
	if x.ChangedPlatform != nil {
		out.Platform = x.ChangedPlatform
	}
	if !x.Cancelled {
		n := x.DelayMin()
		e := x.ExpectedAt()
		out.DelayMin, out.ExpectedAt = &n, &e
	}
	if d != nil {
		out.JourneyID, out.StopPointID, out.CallOrder, out.RouteType, out.Mode = d.JourneyID, d.StopPointID, d.CallOrder, d.RouteType, d.Mode
		return out
	}
	out.JourneyID = fam + ":" + x.PlannedAt.UTC().Format(time.RFC3339) + ":" + deref(out.Line)
	out.RouteType, out.Mode = 2, "rail"
	if strings.EqualFold(strings.TrimSpace(deref(x.Category)), "BUS") {
		out.RouteType, out.Mode = 3, "bus"
	}
	if TravelOf(out) == TravelSBahn {
		out.Mode = "sbahn"
	}
	return out
}
