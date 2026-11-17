package fixtures

import (
	"fmt"
	"time"
)

// streetBays writes, per Land near its first place, the on-street bays the
// platform reads (contract.street_bay): a disabled bay a sensor reports free
// twenty minutes before the end, one whose occupied state is five hours old
// (no current reading), a charging bay, and a sensor-less site of two spaces.
// The bay tables take only their own sources' keys and ids of the form
// <city>:<id>. Written after every other table, with no draw.
func (g *gen) streetBays() {
	const sensors, sites = "dortmund_parksensoren_bays", "frankfurt_behindertenparkplaetze"
	g.w.begin("street_bay", "bay_id", "bay_kind", "publisher_kind", "street", "house_number", "postcode", "city", "spaces", "reserved_when",
		"location", "state", "state_at", "last_message_at", "source_ref", "recorded_at")
	for i, l := range g.lands {
		pl := g.places[l.code][1]
		at := func(dx float64) lonLat { return lonLat{pl.at.lon + dx, pl.at.lat} }
		code := fmt.Sprintf("%05d", 10000+i*5000)
		fresh, old, charge := g.end().Add(-20*time.Minute), g.end().Add(-5*time.Hour), g.end().Add(-5*time.Minute)
		g.w.row(str("dortmund:"+l.code+"1"), str("disabled"), str("Behindertenparkplatz"), str("Hauptstraße"), str("1"), str(code), str(pl.name),
			null, null, point(at(0.001)), str("free"), ts(fresh), ts(fresh), str(sensors), ts(g.day(0)))
		g.w.row(str("dortmund:"+l.code+"2"), str("disabled"), str("Behindertenparkplatz"), str("Hauptstraße"), str("7"), str(code), str(pl.name),
			null, null, point(at(0.002)), str("occupied"), ts(old), ts(old), str(sensors), ts(g.day(0)))
		g.w.row(str("dortmund:"+l.code+"3"), str("ev_charging"), str("E-Ladeplatz"), str("Marktplatz"), null, str(code), str(pl.name),
			null, null, point(at(0.003)), str("free"), ts(charge), ts(charge), str(sensors), ts(g.day(0)))
		g.w.row(str("frankfurt:"+l.code+"4"), str("disabled"), null, str("Bahnhofstraße"), null, str(code), str(pl.name),
			num(2), str("Mo-Fr 8-18 Uhr"), point(at(0.004)), null, null, null, str(sites), ts(g.day(0)))
		for range 3 {
			g.counted(l.code, sensors)
		}
		g.counted(l.code, sites)
	}
}

// TrainBoardEVA is the fictional station number (EVA) of a Land's board.
func TrainBoardEVA(code string) string { return "80" + code + "001" }

// trainBoards writes, per Land, the departure boards the API was handed
// (contract.train_board serves the newest fetch per station): one fetched
// ten seconds before the end with three departures (one late and moved to
// another platform, one cancelled). The seed reads each row back through
// the view, which serves the newest fetch alone, so the set holds no older
// one: the database's own tests hold the newest-fetch rule.
func (g *gen) trainBoards() {
	g.w.begin("train_board", "station_eva", "fetched_at", "planned_departure_at", "changed_departure_at", "platform", "changed_platform",
		"category", "train_number", "line", "destination", "cancelled", "recorded_at")
	for _, l := range g.lands {
		eva, dest := TrainBoardEVA(l.code), g.places[l.code][1].name
		at := g.end().Add(-10 * time.Second)
		g.w.row(str(eva), ts(at), ts(at.Add(4*time.Minute)), ts(at.Add(9*time.Minute)), str("5"), str("7"), str("ICE"), str("571"), null, str(dest), boolean(false), ts(at))
		g.w.row(str(eva), ts(at), ts(at.Add(12*time.Minute)), null, str("2"), null, str("RE"), str("4713"), str("RE 1"), str(dest), boolean(true), ts(at))
		g.w.row(str(eva), ts(at), ts(at.Add(20*time.Minute)), null, null, null, str("S"), null, str("S 3"), str(dest), boolean(false), ts(at))
		g.counted(l.code, TrainBoardRef)
	}
}

// MotorwayPlaceName is the junction place of the first road (G5): the place
// search finds it by the name after the junction word, the road's own
// number names the road itself.
const MotorwayPlaceName = "Kreuz Fixfeld"

// MotorwayPlaceRef is the source of the motorway places: the place table
// takes the Autobahn GmbH source alone for them.
const MotorwayPlaceRef = "autobahn"

// motorwayPlaces writes the gazetteer's motorway junction places the place
// search lists (contract.place, kind junction, under a roads source): one at
// the first junction of each of the first two roads. Written after every
// other table, with no draw.
func (g *gen) motorwayPlaces() {
	g.w.begin("place", "place_id", "place_kind", "name", "location", "region_code", "official_key", "prominence",
		"source_ref", "origin_key", "recorded_at", "changed_at", "withdrawn_at")
	for i, rd := range g.roads[:2] {
		at := rd.along(0.5 / float64(8+g.p.Scale/4))
		name := MotorwayPlaceName
		if i > 0 {
			name = "Dreieck Fixau"
		}
		id := num(900001 + i)
		g.w.row(str(id), str("junction"), str(name+" ("+rd.ref+")"), point(at), null, null, num(0),
			str(MotorwayPlaceRef), str("o-"+id), ts(g.day(0)), ts(g.day(0)), null)
		g.counted(landOf(at), MotorwayPlaceRef)
	}
}

// dbStation writes one DB railway station of several parts (G2), as the
// StaDa crosswalk holds Berlin Hbf: in the first Land near its first place,
// a stop place whose main EVA number is the Land's train board station
// (TrainBoardEVA) and whose two parts carry numbers of their own. Written
// after every other table, with no draw.
func (g *gen) dbStation() {
	l := g.lands[0]
	pl := g.places[l.code][1]
	s := g.sourceByRef("delfi_static")
	code := fmt.Sprintf("de:%s:9001", pl.district)
	main := TrainBoardEVA(l.code)
	g.w.begin("stop_place", "stop_place_id", "name", "location", "national_stop_code", "region_code", "source_ref",
		"origin_key", "eva_no", "eva_numbers")
	g.w.row(str(fmt.Sprintf("%d:%s", s.feedKey, code)), str(pl.name+" Hbf"), point(lonLat{pl.at.lon - 0.002, pl.at.lat}), str(code),
		str(pl.district), str("delfi_static"), str(code), str(main), array(main, "80"+l.code+"002", "80"+l.code+"003"))
	g.counted(l.code, "delfi_static")
}
