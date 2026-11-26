package energy

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func ip(n int) *int { return &n }

func fp(f float64) *float64 { return &f }

func bp(b bool) *bool { return &b }

// placeReg is a register of an operator source, the register source, a retired
// source in its window and one past it, and two status sources.
var placeReg = SourceRegister{
	Sites: []SiteSource{{Key: "op", Rank: 1}, {Key: "lsr", Rank: 9, TwinRadiusM: ip(100)},
		{Key: "old", Rank: 5, AnswersUntil: ago(-time.Hour)}, {Key: "gone", Rank: 6, AnswersUntil: ago(time.Hour)}},
	Status: []StatusSource{{Key: "s", StaleAfter: 15 * time.Minute, StatementMaxAge: time.Hour, PointsFrom: sp("op")},
		{Key: "t", StaleAfter: 10 * time.Minute, StatementMaxAge: 2 * time.Hour}},
}

// north is a point metres north of at.
func north(at geo.Point, metres float64) geo.Point {
	return geo.Point{Lat: at.Lat + metres/111_195, Lon: at.Lon}
}

var mitte = geo.Point{Lat: 52.52, Lon: 13.405}

func TestTheSourceRegister(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	if got := placeReg.Masters(judgeNow); !reflect.DeepEqual(got, []string{"op", "lsr", "old"}) {
		t.Errorf("Masters = %v", got)
	}
	if got := placeReg.Ended(judgeNow); !reflect.DeepEqual(got, map[string]bool{"old": true}) {
		t.Errorf("Ended = %v", got)
	}
	if twins, radius := placeReg.Twins(); !reflect.DeepEqual(twins, []string{"lsr"}) || radius != 100 {
		t.Errorf("Twins = %v, %v", twins, radius)
	}
	if !placeReg.IsRegister("lsr") || placeReg.IsRegister("op") || placeReg.IsRegister("x") {
		t.Error("IsRegister")
	}
	if keys, ranks := placeReg.Ranks(); !reflect.DeepEqual(keys, []string{"op", "lsr", "old", "gone"}) || !reflect.DeepEqual(ranks, []int{1, 9, 5, 6}) {
		t.Errorf("Ranks = %v, %v", keys, ranks)
	}
	if keys, from := placeReg.StatusKeys(); !reflect.DeepEqual(keys, []string{"s", "t"}) || *from[0] != "op" || from[1] != nil {
		t.Errorf("StatusKeys = %v, %v", keys, from)
	}
	if status, master := placeReg.Pairs(); !reflect.DeepEqual(status, []string{"s"}) || !reflect.DeepEqual(master, []string{"op"}) {
		t.Errorf("Pairs = %v, %v", status, master)
	}
	if w, s := placeReg.WidestWindow(); w != 2*time.Hour || s != 2*time.Hour {
		t.Errorf("WidestWindow = %v, %v", w, s)
	}
	empty := SourceRegister{}
	if twins, radius := empty.Twins(); len(twins) != 0 || twins == nil || radius != 0 {
		t.Error("an empty register's twins are not an empty list")
	}
}

func TestARegisterRowsAddress(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	for _, c := range []struct {
		known   *bool
		nearest *float64
		seen    *bool
		want    bool
	}{
		{bp(true), fp(30_000), bp(false), true}, {nil, fp(30_000), bp(false), false}, {bp(false), fp(30_000), bp(false), false},
		{bp(true), nil, bp(false), false}, {bp(true), fp(20_000), bp(false), false}, {bp(true), fp(30_000), nil, false},
		{bp(true), fp(30_000), bp(true), false},
	} {
		if got := RegisterAddressElsewhere(c.known, c.nearest, c.seen); got != c.want {
			t.Errorf("RegisterAddressElsewhere(%v, %v, %v) = %v", c.known, c.nearest, c.seen, got)
		}
	}
	rec := PlaceRecord{Source: "lsr", Street: sp("Mueller Str."), HouseNumber: sp("12 A"), City: sp("Köln"), Postcode: sp("D-50667")}
	if !Relocatable(rec, placeReg) {
		t.Error("a register row with an address is not relocatable")
	}
	op, blank := rec, rec
	op.Source, blank.HouseNumber = "op", sp(" ")
	if Relocatable(op, placeReg) || Relocatable(blank, placeReg) {
		t.Error("an operator's site, or a row without a house number, is relocatable")
	}
	if got := AddressQueryOf(rec); got != [6]string{"muellerstrasse", "mullerstrasse", "koln", "koln", "12a", "50667"} {
		t.Errorf("AddressQueryOf = %q", got)
	}
	if TownFold(PlaceRecord{}) != "" || TownFold(rec) != "koln" {
		t.Error("TownFold")
	}
	if (PlaceRecord{Source: "a", SiteID: "b"}).Key() == (PlaceRecord{Source: "ab"}).Key() {
		t.Error("two records share a key")
	}
}

func TestRelocationByAddress(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	hamburg, bremen := geo.Point{Lat: 53.55, Lon: 10.0}, geo.Point{Lat: 53.08, Lon: 8.8}
	rec := PlaceRecord{Source: "lsr", Location: mitte, Postcode: sp("10117")}
	hit := func(at geo.Point, postcode, place *string, score int, byPostcode bool) AddressHit {
		return AddressHit{Street: "Hauptstraße", HouseNumber: "1", Postcode: postcode, Place: place, Location: at, LiteralScore: score, ByPostcode: byPostcode}
	}
	for name, c := range map[string]struct {
		hits []AddressHit
		want *geo.Point
	}{
		"no hit":               {nil, nil},
		"by postcode":          {[]AddressHit{hit(hamburg, sp("20095"), nil, 1, true)}, &hamburg},
		"own postcode":         {[]AddressHit{hit(hamburg, sp("20095"), nil, 1, false), hit(bremen, nil, nil, 1, false)}, &bremen},
		"other postcodes only": {[]AddressHit{hit(hamburg, sp("20095"), nil, 1, false)}, nil},
		"the best score":       {[]AddressHit{hit(hamburg, nil, sp("Hamburg"), 1, true), hit(bremen, nil, sp("Bremen"), 2, true)}, &bremen},
		"two towns":            {[]AddressHit{hit(hamburg, nil, sp("Hamburg"), 1, true), hit(bremen, nil, sp("Bremen"), 1, true)}, nil},
		"two postcodes":        {[]AddressHit{hit(hamburg, sp("20095"), nil, 1, true), hit(bremen, sp("28195"), nil, 1, true)}, nil},
		"one object, the fuller hit kept": {[]AddressHit{hit(hamburg, nil, nil, 1, true),
			hit(north(hamburg, 100), sp("20095"), sp("Hamburg"), 1, true)}, func() *geo.Point { p := north(hamburg, 100); return &p }()},
		"two addresses of one town": {[]AddressHit{hit(hamburg, nil, sp("Hamburg"), 1, true),
			{Street: "Nebenweg", HouseNumber: "2", Place: sp("Hamburg"), Location: bremen, LiteralScore: 1, ByPostcode: true}}, &hamburg},
		"at the town point already": {[]AddressHit{hit(north(mitte, 500), nil, nil, 1, true)}, nil},
	} {
		got, ok := RelocationOf(rec, c.hits)
		if ok != (c.want != nil) || (ok && got != *c.want) {
			t.Errorf("%s: RelocationOf = %v, %v", name, got, ok)
		}
	}
	a := hit(hamburg, sp("20095"), sp("Hamburg"), 1, true)
	for name, b := range map[string]AddressHit{
		"other street":   {Street: "Nebenweg", HouseNumber: "1", Location: hamburg},
		"other postcode": hit(hamburg, sp("20097"), nil, 1, true),
		"other place":    hit(hamburg, nil, sp("Altona"), 1, true),
		"far":            hit(north(hamburg, 200), nil, nil, 1, true),
	} {
		if isSameAddress(a, b) {
			t.Errorf("%s: one address", name)
		}
	}
	if !isSameAddress(a, hit(north(hamburg, 100), sp(""), nil, 1, true)) || fold(nil) != "" {
		t.Error("isSameAddress of compatible hits")
	}
}

func TestPlacedAtTheirAddresses(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	rec := func(source, id string, at geo.Point) PlaceRecord {
		return PlaceRecord{Source: source, SiteID: id, Location: at, DistanceM: geo.MetresBetween(mitte, at)}
	}
	op := rec("op", "o1", north(mitte, 3000))
	outside, twin, kept := rec("lsr", "r1", north(mitte, 100)), rec("lsr", "r2", north(mitte, 150)), rec("lsr", "r3", north(mitte, 200))
	recs := []PlaceRecord{outside, twin, kept, op}
	if got := PlaceAtAddresses(recs, nil, mitte, 5000, 2, placeReg); len(got) != 2 || got[0].SiteID != "r1" {
		t.Errorf("nothing moved: %v", got)
	}
	moved := map[string]geo.Point{outside.Key(): north(mitte, 6000), twin.Key(): north(mitte, 3050), kept.Key(): north(mitte, 4000)}
	got := PlaceAtAddresses(recs, moved, mitte, 5000, 5, placeReg)
	if len(got) != 2 || got[0].SiteID != "o1" || got[1].SiteID != "r3" {
		t.Fatalf("PlaceAtAddresses = %v", got)
	}
	if got[1].Reported == nil || *got[1].Reported != kept.Location || got[1].Location != moved[kept.Key()] || got[1].DistanceM < 3999 {
		t.Errorf("the moved record: %+v", got[1])
	}
	if len(PlaceAtAddresses(recs, moved, mitte, 5000, 1, placeReg)) != 1 {
		t.Error("the limit is not kept")
	}
}

func TestJudgedRecords(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	for _, c := range []struct {
		p    PlacePoint
		want *Price
	}{
		{PlacePoint{}, nil},
		{PlacePoint{PricePerKwh: fp(0.59), PriceTaxIncluded: bp(true), PriceTaxRate: fp(19)}, &Price{0.59, true}},
		{PlacePoint{PricePerKwh: fp(0.5), PriceTaxIncluded: bp(false)}, &Price{0.5, false}},
		{PlacePoint{PricePerKwh: fp(0.5), PriceTaxRate: fp(19)}, &Price{0.595, true}},
	} {
		if got := grossPrice(c.p); !reflect.DeepEqual(got, c.want) {
			t.Errorf("grossPrice(%+v) = %+v", c.p, got)
		}
	}
	status := func(p PlacePoint) PlacePoint {
		p.StatusSource, p.Status, p.ObservedAt = sp("s"), sp("available"), ago(time.Minute)
		return p
	}
	points := []PlacePoint{
		status(PlacePoint{ConnectorTypes: []string{"iec62196T2COMBO", "iec62196T2"}, MaxPowerKw: fp(150), PricePerKwh: fp(0.69), PriceTaxIncluded: bp(true)}),
		{ConnectorTypes: []string{"IEC62196T2COMBO"}, MaxPowerKw: fp(50), PricePerKwh: fp(0.5), PriceTaxRate: fp(19)},
		{MaxPowerKw: fp(300)},
	}
	j := Judge([]PlaceRecord{{Source: "op", Points: points}, {Source: "old", Points: points[:1], DistanceM: 10}}, placeReg, nil, judgeNow, "en")
	if !reflect.DeepEqual(j[0].States, []Verdict{VerdictAvailable, VerdictUnknown, VerdictUnknown}) || j[0].Ended {
		t.Errorf("the operator's record: %v, ended %v", j[0].States, j[0].Ended)
	}
	if *j[0].MaxPowerKw != 300 || *j[0].Price != (Price{0.595, true}) || !reflect.DeepEqual(j[0].Connectors, []string{"CCS2", "Type 2"}) {
		t.Errorf("the operator's record: %v %+v %v", *j[0].MaxPowerKw, *j[0].Price, j[0].Connectors)
	}
	if !reflect.DeepEqual(j[1].States, []Verdict{VerdictUnknown}) || !j[1].Ended || j[1].rankM() != 110 || j[0].rankM() != 0 {
		t.Errorf("an ended source's record: %v, ended %v, rank %v", j[1].States, j[1].Ended, j[1].rankM())
	}
	if (Judged{Record: PlaceRecord{}}).MaxPowerKw != nil || j[0].outOfOrder() != 0 {
		t.Error("outOfOrder of a record with a free point")
	}
	if (Judged{JudgedPoints: JudgedPoints{States: []Verdict{VerdictOutOfOrder}}}).outOfOrder() != 1 {
		t.Error("outOfOrder of a record wholly out of order")
	}
}

func TestLabels(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	addr := PlaceRecord{Location: mitte, Street: sp("Spandauer Str."), HouseNumber: sp("1"), Postcode: sp("10178"), City: sp("Berlin")}
	named := func(r PlaceRecord, name string) PlaceRecord { r.Name = sp(name); return r }
	for name, c := range map[string]struct {
		r    PlaceRecord
		lang string
		e    *Elsewhere
		want string
	}{
		"no name, no address": {PlaceRecord{Name: sp("DE-ABC-1")}, "de", nil, "Ladestation"},
		"no name":             {addr, "de", nil, "Spandauer Str. 1, 10178 Berlin"},
		"a name":              {named(addr, "Q-Park Aquarée"), "de", nil, "Q-Park Aquarée, Spandauer Str. 1, 10178 Berlin"},
		"a name of its town":  {named(addr, "Berlin Mitte 10178 Berlin"), "de", nil, "Berlin Mitte 10178 Berlin, Spandauer Str. 1"},
		"a name of its address": {named(addr, "Parkhaus Spandauer Straße 1, 10178 Berlin"), "de", nil,
			"Parkhaus Spandauer Straße 1, 10178 Berlin"},
		"a name alone": {PlaceRecord{Name: sp("Ladepark")}, "de", nil, "Ladepark"},
		"elsewhere, at a coordinate": {addr, "de", &Elsewhere{}, "Standort bei 52,52000; 13,40500 (im Register mit der Adresse 10178 Berlin " +
			"gemeldet, die nicht am Standort liegt — vermutlich die Anschrift des Betreibers)"},
		"elsewhere, at a coordinate, en": {addr, "en", &Elsewhere{}, "site at 52.52000, 13.40500 (filed in the register under an address " +
			"in 10178 Berlin, which is not where the charger is — probably the operator's own address)"},
		"elsewhere, near a town": {named(addr, "Ladepark Nord"), "de", &Elsewhere{LocatedNear: sp("Potsdam")},
			"Ladepark Nord, Standort bei Potsdam (im Register mit der Adresse 10178 Berlin gemeldet, die nicht am Standort liegt — " +
				"vermutlich die Anschrift des Betreibers)"},
		"elsewhere, near a town, en": {addr, "en", &Elsewhere{LocatedNear: sp("Potsdam")}, "site near Potsdam (filed in the register " +
			"under an address in 10178 Berlin, which is not where the charger is — probably the operator's own address)"},
	} {
		if got := SiteLabel(c.r, c.lang, c.e); got != c.want {
			t.Errorf("%s: SiteLabel = %q", name, got)
		}
	}
	release := func(source string, v *string) PlaceRecord { return PlaceRecord{Source: source, SourceVersion: v} }
	if RegisterReleaseOf([]PlaceRecord{release("op", sp("2026-09-01"))}, placeReg) != nil {
		t.Error("an operator's version is a register release")
	}
	recs := []PlaceRecord{release("lsr", sp("2026-09-01")), release("lsr", sp("2026-10-01")), release("lsr", sp("v3")),
		release("op", sp("2027-01-01")), release("lsr", nil), release("lsr", sp("2026-08-01"))}
	if got := RegisterReleaseOf(recs, placeReg); got == nil || *got != "2026-10-01" {
		t.Errorf("RegisterReleaseOf = %v", got)
	}
}

func TestTheLineBody(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	j := Judged{Record: PlaceRecord{Source: "lsr", Operator: sp("Q-Park"), Reported: &mitte}, Connectors: []string{"CCS2", "Typ 2"},
		MaxPowerKw: fp(150), Price: &Price{0.57, false}}
	facts, op := Lines{reg: placeReg, lang: "de"}.body(j)
	if facts != "CCS2/Typ 2, bis 150 kW, 0,57 €/kWh netto, "+statusSlot+", laut Ladesäulenregister, Lage nach Anschrift" || op != " (Q-Park)" {
		t.Errorf("de: %q %q", facts, op)
	}
	j.Record.Source, j.Ended, j.Record.Operator = "old", true, sp(" ")
	facts, op = Lines{reg: placeReg, lang: "en"}.body(j)
	if facts != "CCS2/Typ 2, up to 150 kW, 0.57 €/kWh net, \x00ended\x00old, located by its address" || op != "" {
		t.Errorf("en: %q %q", facts, op)
	}
	facts, _ = Lines{reg: placeReg, lang: "en"}.body(Judged{Record: PlaceRecord{Source: "lsr"}})
	if facts != statusSlot+", per charging point register" {
		t.Errorf("a bare register row: %q", facts)
	}
}

func TestTheLinesOfAnAnswer(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	ccs := []PlacePoint{{ConnectorTypes: []string{"iec62196T2COMBO"}, MaxPowerKw: fp(150)}}
	site := func(source, id, name string, operator *string, at geo.Point, metres float64) PlaceRecord {
		return PlaceRecord{Source: source, SiteID: id, Name: sp(name), Operator: operator, Street: sp("Spandauer Str."), HouseNumber: sp("1"),
			Postcode: sp("10178"), City: sp("Berlin"), Location: at, DistanceM: metres, Points: ccs}
	}
	qpark := sp("Q-Park")
	a := site("op", "a", "Q-Park Aquarée", qpark, mitte, 100)
	a2 := site("op", "a2", "Q-Park Aquarée", qpark, north(mitte, 10), 110)
	r1 := site("lsr", "r1", "bnetza_lsr_52.52_13.40", sp("Q-Park GmbH"), north(mitte, 20), 120)
	r1.Street = sp("Spandauer Straße")
	c := site("op", "c", "Aral pulse", sp("Aral"), north(mitte, 2000), 300)
	c.Street, c.HouseNumber = sp("Karl-Marx-Allee"), sp("90")
	e := site("old", "e", "Altes Netz", nil, north(mitte, 3000), 250)
	b := site("op", "b", "EnBW Hub", sp("EnBW"), north(mitte, 4000), 50)
	b.Points = []PlacePoint{{StatusSource: sp("s"), Status: sp("faulted"), ObservedAt: ago(time.Minute)}}
	judged := Judge([]PlaceRecord{b, a, a2, r1, c, e}, placeReg, nil, judgeNow, "de")

	l := RoughLines(judged, 2, placeReg, "de")
	if !l.More {
		t.Error("four lines over a limit of two: More is false")
	}
	var ids []string
	for _, r := range l.RoughRecords() {
		ids = append(ids, r.SiteID)
	}
	if !reflect.DeepEqual(ids, []string{"a", "a2", "r1", "c"}) {
		t.Errorf("RoughRecords = %v", ids)
	}
	places := l.Places(nil, 2)
	if len(places) != 2 || places[0].Label != "Q-Park Aquarée, Spandauer Str. 1, 10178 Berlin" || places[0].Elsewhere != nil {
		t.Fatalf("Places = %+v", places)
	}
	var members []string
	for _, m := range places[0].Members {
		members = append(members, m.Record.SiteID)
	}
	if !reflect.DeepEqual(members, []string{"a", "a2", "r1"}) || places[1].Members[0].Record.SiteID != "c" {
		t.Errorf("members %v, then %s", members, places[1].Members[0].Record.SiteID)
	}
	if got := l.rough[0].facts; !strings.Contains(got, "; ") || !strings.Contains(got, "laut Ladesäulenregister") {
		t.Errorf("the joined facts: %q", got)
	}
	if one := l.Places(nil, 1); len(one) != 1 {
		t.Errorf("Places(limit 1) = %d", len(one))
	}

	all := RoughLines(judged, 10, placeReg, "de")
	if all.More || len(all.rough) != 4 || all.Listed[len(all.Listed)-1].Record.SiteID != "b" {
		t.Errorf("wholly out of order is not last: %v", all.Listed)
	}
	if p := all.Places(nil, 10); len(p) != 4 || p[3].Members[0].Record.SiteID != "b" {
		t.Errorf("wholly out of order is not the last place: %+v", p)
	}

	// A register row filed elsewhere leads its place, named by where it lies.
	lone := site("lsr", "r9", "bnetza_lsr_1", nil, mitte, 10)
	el := RoughLines(Judge([]PlaceRecord{lone}, placeReg, nil, judgeNow, "en"), 3, placeReg, "en")
	got := el.Places(map[string]Elsewhere{lone.Key(): {LocatedNear: sp("Potsdam")}}, 3)
	if len(got) != 1 || got[0].Elsewhere == nil || !strings.HasPrefix(got[0].Label, "site near Potsdam (") {
		t.Errorf("a register row filed elsewhere: %+v", got)
	}
	// Filed elsewhere but not the lead: the place keeps its lead's label.
	if p := l.Places(map[string]Elsewhere{r1.Key(): {}}, 2); p[0].Elsewhere != nil || p[0].Label != places[0].Label {
		t.Errorf("a member filed elsewhere: %+v", p[0])
	}
}
