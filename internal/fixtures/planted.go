package fixtures

import (
	"fmt"
	"io"
	"time"
)

// Planted writes the rows the integration tests plant beside a generated
// set, placed on the same world (its roads, Länder and sources), to be
// loaded after the set's seed.sql. They are kept out of Generate: the set's
// rows are what the parity bundles were answered on, so they never change
// for a planted row. Every planted row is drawn without a random number and
// is active over the whole window.
func Planted(out io.Writer, profile string, seed uint64, contractVersion string) error {
	g, _, err := generate(io.Discard, profile, seed, contractVersion)
	if err != nil {
		return err
	}
	g.w = newSQLWriter(out)
	g.w.write("-- Planted rows, written by fixtures.Planted (integration tests only), viafrei-api fixturegen " + GeneratorVersion +
		". Fictional data, CC0 1.0. Load it after the seed.sql of profile " + profile + ", seed " + fmt.Sprint(seed) + ".\n")
	g.plantRoads()
	g.plantWeather()
	g.w.end()
	if _, err := g.w.close(); err != nil {
		return fmt.Errorf("fixtures: write: %w", err)
	}
	return nil
}

// fresh is the first fresh source of a family, chosen without a random draw.
func (g *gen) fresh(domain, family string) string {
	for _, s := range g.sources[domain] {
		if s.family == family && !s.stale {
			return s.ref
		}
	}
	panic("fixtures: no source " + family + " for " + domain)
}

// TwoPartRef is the reference of the planted road in two parts.
const TwoPartRef = "A990"

// twoPartRoad is the line of the road in two parts, each a straight
// segment: the first across land 06 from west to east, the second across
// land 07, with a gap between them.
func (g *gen) twoPartRoad() [][]lonLat {
	part := func(c bbox) []lonLat {
		lat := (c.s + c.n) / 2
		return []lonLat{{c.w + (c.e-c.w)*0.2, lat}, {c.w + (c.e-c.w)*0.8, lat}}
	}
	return [][]lonLat{part(g.land("06").cell), part(g.land("07").cell)}
}

// PlantedEvent is the contract id of a planted traffic event.
func PlantedEvent(name string) string { return eventID(name) }

// plantRoads plants the road in two parts (a motorway with a gap) with one
// situation in the middle of each part, and, on road 1, an event of a type
// the platform does not know (kind other) and one on a road of no known
// class (ENV-051). The platform reads the severity and the direction of
// every traffic message as other and unknown, so no value of either is
// planted.
func (g *gen) plantRoads() {
	g.w.begin("road", "road_ref")
	g.w.row(str(TwoPartRef))

	g.w.begin("traffic_event", eventColumns...)
	plant := func(name, kind, roadRef, class string, at lonLat) {
		g.w.row(str(eventID(name)), str(kind), str(roadRef), str(class), point(at),
			ts(g.day(0)), null, str(g.fresh("roads", "alpha")), str(name), ts(g.day(0)), ts(g.day(0)), ts(g.day(0)), null)
	}
	rd := g.roads[1]
	plant("ev-u-kind", "other", rd.ref, "motorway", rd.along(0.2))
	plant("ev-u-class", "restriction", "K990", "other", rd.along(0.8))
	for i, part := range g.twoPartRoad() {
		mid := lonLat{(part[0].lon + part[1].lon) / 2, (part[0].lat + part[1].lat) / 2}
		plant(fmt.Sprintf("ev-two-%d", i+1), "roadworks", TwoPartRef, "motorway", mid)
	}
}

// PlantedWarning is the contract id of a planted weather warning.
func PlantedWarning(name string) string {
	return map[string]string{"issued": "dwd:900001", "cancelled": "dwd:900002", "withdrawn": "dwd:900003"}[name]
}

// plantWeather plants, in one warn cell of the first Land (its western
// district) over the first two days, a warning in force, the same warning cancelled, and one withdrawn
// from publication; only the first answers.
func (g *gen) plantWeather() {
	l := g.lands[0]
	ref := g.fresh("weather", "eta")
	from := g.day(0)
	cell, cellName, area := warnCell(l, l.code+"001")
	g.w.begin("weather_warning", warningColumns...)
	for _, w := range []struct{ name, status, withdrawn string }{
		{"issued", "actual", null},
		{"cancelled", "cancelled", null},
		{"withdrawn", "actual", ts(from.Add(time.Hour))},
	} {
		g.w.row(str(PlantedWarning(w.name)), str("ice"), num(2), str("Stufe 2"), str(w.status),
			str("Warnung vor Glätte (Stufe 2)"), ts(from), ts(from.Add(48*time.Hour)),
			ts(from.Add(-30*time.Minute)), str(l.code+"001"), str(ref), ts(from), w.withdrawn, str(cell), str(cellName), area, ts(from))
	}
}

// Report is one charge point report ExamplePlants writes: the point, how
// long before the clock it was made, and the state it reported.
type Report struct {
	Point string
	Age   time.Duration
	State string
}

// ExamplePlants writes the rows the documented examples are answered on
// beside the generated set, to be seeded in the same seed as its rows: every
// Land a junction lies in is covered by the first fresh roads source of
// family alpha (the generated set carries no message coverage), and each of
// reports is a report of its point made Age before at, credited to the
// point's own source. An unknown point is an error.
func ExamplePlants(out io.Writer, profile string, seed uint64, contractVersion string, at time.Time, reports []Report) error {
	g, _, err := generate(io.Discard, profile, seed, contractVersion)
	if err != nil {
		return err
	}
	g.w = newSQLWriter(out)
	g.w.write("-- Example rows, written by fixtures.ExamplePlants (integration tests only). Fictional data, CC0 1.0.\n")
	g.w.begin("message_coverage", "state_code", "source_ref", "coverage_role")
	ref := g.fresh("roads", "alpha")
	for _, code := range g.junctionLands {
		g.w.row(str(code), str(ref), str("own"))
	}
	if len(reports) > 0 {
		g.w.begin("charge_point_state_observation", "point_id", "observed_at", "availability", "source_ref")
		for _, r := range reports {
			src, ok := g.pointRef[r.Point]
			if !ok {
				return fmt.Errorf("fixtures: no charge point %q in profile %s, seed %d", r.Point, profile, seed)
			}
			g.w.row(str(r.Point), ts(at.Add(-r.Age)), str(r.State), str(src))
		}
	}
	g.w.end()
	if _, err := g.w.close(); err != nil {
		return fmt.Errorf("fixtures: write: %w", err)
	}
	return nil
}
