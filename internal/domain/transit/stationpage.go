package transit

import (
	"cmp"
	"slices"
	"time"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// The station page, as today's describe_station builds it from the page's
// reads: the register's record, the rail lamp over the board, the
// step-free platforms now, the lifts, the platforms, the board, the alerts
// and DB's facility flags.
const (
	PageDepartures    = 10
	PageDeparturesMax = 50
	// StationAlertsLimit is how many alerts the page reads (STATION_ALERTS_LIMIT).
	StationAlertsLimit = 12
)

// StationRecord is DB's register row of a station:
// its address, category, Land, EVA numbers, page slug, stepless access,
// facility flags and point, and the family that lists it.
type StationRecord struct {
	StationNo      string
	Name           string
	EVANo          *string
	EVANumbers     []string
	Slug           *string
	Street         *string
	Zipcode        *string
	City           *string
	Land           *string
	Municipality   *string
	Category       *int
	SteplessAccess *string
	StopPlaceID    *string
	At             *geo.Point
	SeenAt         time.Time
	Family         string
	Flags          []string
}

// FacilityFlags are DB's facility flags in the page's order, as the
// register names them.
var FacilityFlags = []string{"parking", "bicycleParking", "localPublicTransport", "publicFacilities", "lockerSystem", "taxiRank",
	"travelNecessities", "wifi", "travelCentre", "railwayMission", "dbLounge", "lostAndFound", "carRental"}

// StationAlertRow is one alert in force near a station: its message key,
// headline, the lines and stops it names, its end and the families that
// carry the message.
type StationAlertRow struct {
	ID         string
	MessageKey string
	Header     string
	Until      *time.Time
	Lines      []string
	StopIDs    []string
	CarriedBy  []string
	Family     string
}

// StationAlertsQuery asks for the alerts in force at Now naming one of the
// stops IDs, or one of Lines within the Kreis.
type StationAlertsQuery struct {
	Now   time.Time
	IDs   []string
	Lines []string
	Kreis []string
}

// StationAlert is one message as the station is told it (today's
// stationAlertMessages): the primary source's words, the station's lines it
// names, whether it names one of the station's stops.
type StationAlert struct {
	Header    string
	Lines     []string
	AtStation bool
	Until     *time.Time
	CarriedBy []string
}

// StationAlerts folds the rows into messages: a row naming neither a stop
// nor a line of the station is not the station's; rows sharing a message
// key are one message, the key's own row's words; at-station messages first.
func StationAlerts(rows []StationAlertRow, ids, lines []string) []StationAlert {
	type msg struct {
		StationAlert
		primary bool
	}
	var order []string
	by := map[string]*msg{}
	for _, a := range rows {
		at := slices.ContainsFunc(a.StopIDs, func(s string) bool { return slices.Contains(ids, s) })
		var named []string
		for _, l := range a.Lines {
			if slices.Contains(lines, l) {
				named = append(named, l)
			}
		}
		if !at && len(named) == 0 {
			continue
		}
		own := a.ID == a.MessageKey
		m, ok := by[a.MessageKey]
		if !ok {
			m = &msg{StationAlert{Header: a.Header, Until: a.Until, Lines: []string{}}, own}
			by[a.MessageKey] = m
			order = append(order, a.MessageKey)
		}
		if own && !m.primary {
			m.Header, m.Until, m.primary = a.Header, a.Until, true
		}
		m.AtStation = m.AtStation || at
		for _, l := range named {
			if !slices.Contains(m.Lines, l) {
				m.Lines = append(m.Lines, l)
			}
		}
		for _, c := range a.CarriedBy {
			if !slices.Contains(m.CarriedBy, c) {
				m.CarriedBy = append(m.CarriedBy, c)
			}
		}
	}
	german := collate.New(language.German, collate.Numeric)
	out := make([]StationAlert, 0, len(order))
	for _, k := range order {
		m := by[k].StationAlert
		slices.SortFunc(m.Lines, german.CompareString)
		slices.Sort(m.CarriedBy)
		out = append(out, m)
	}
	slices.SortStableFunc(out, func(a, b StationAlert) int { return cmp.Compare(boolInt(b.AtStation), boolInt(a.AtStation)) })
	return out
}
