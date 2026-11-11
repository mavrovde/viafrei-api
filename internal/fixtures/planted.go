package fixtures

import (
	"fmt"
	"io"
	"slices"
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
const TwoPartRef = "A 990"

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

// plantRoads plants the road in two parts (a motorway with a gap) with one
// situation in the middle of each part, and one unmapped value in each
// coded column of a situation on road 1 (ENV-051).
func (g *gen) plantRoads() {
	twoPart := g.twoPartRoad()
	var states []string
	length := 0.0
	for _, part := range twoPart {
		for _, p := range part {
			if s := landOf(p); !slices.Contains(states, s) {
				states = append(states, s)
			}
		}
		length += haversineKm(part[0], part[1])
	}
	slices.Sort(states)
	g.w.begin("road", "road_ref", "road_class", "name", "length_km", "geometry", "states")
	g.w.row(str(TwoPartRef), str("motorway"), null, dec(length, 1), lines(twoPart...), array(states...))

	g.w.begin("traffic_event", eventColumns...)
	plant := func(id, kind, sev, roadRef, class, dir, headline string, at lonLat) {
		g.w.row(str(id), str(kind), str(sev), str(roadRef), str(class), str(dir), null, point(at),
			ts(g.day(0)), null, null, null, null, null, str(headline), null, str("de"),
			str(g.fresh("roads", "alpha")), str("o-"+id), ts(g.day(0)), ts(g.day(0)), ts(g.day(0)), null)
	}
	rd := g.roads[1]
	plant("ev-u-kind", unknownCode, "minor", rd.ref, "motorway", "both", "Meldung (planted)", rd.along(0.2))
	plant("ev-u-severity", "restriction", unknownCode, rd.ref, "motorway", "both", "Restriction (planted)", rd.along(0.4))
	plant("ev-u-direction", "restriction", "minor", rd.ref, "motorway", unknownCode, "Restriction (planted)", rd.along(0.6))
	plant("ev-u-class", "restriction", "minor", rd.ref, unknownCode, "both", "Restriction (planted)", rd.along(0.8))
	for i, part := range twoPart {
		mid := lonLat{(part[0].lon + part[1].lon) / 2, (part[0].lat + part[1].lat) / 2}
		plant(fmt.Sprintf("ev-two-%d", i+1), "roadworks", "minor", TwoPartRef, "motorway", "both", "Baustelle (planted)", mid)
	}
}

// plantWeather plants, in one area of the first Land over the first two
// days, a warning in force, the same warning cancelled, and one withdrawn
// from publication; only the first answers.
func (g *gen) plantWeather() {
	l := g.lands[0]
	ref := g.fresh("weather", "eta")
	from := g.day(0)
	g.w.begin("weather_warning", warningColumns...)
	for _, w := range []struct{ id, status, withdrawn string }{
		{"ww-planted-issued", "issued", null},
		{"ww-planted-cancelled", "cancelled", null},
		{"ww-planted-withdrawn", "issued", ts(from.Add(time.Hour))},
	} {
		g.w.row(str(w.id), str("ice"), num(2), str("Stufe 2"), str(w.status), str("Synthetischer Wetterdienst"),
			str("Warnung vor Glätte (Stufe 2)"), str("de"), ts(from), ts(from.Add(48*time.Hour)),
			ts(from.Add(-30*time.Minute)), str(l.code+"001"), str(ref), ts(from), w.withdrawn)
	}
}
