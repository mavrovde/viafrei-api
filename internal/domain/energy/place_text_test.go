package energy

import (
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func sp(s string) *string { return &s }

func deref(s *string) string {
	if s == nil {
		return "<nil>"
	}
	return *s
}

func TestAddressTextAsThePlatformPrintsIt(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	for _, c := range []struct {
		in   *string
		want string
	}{{nil, "<nil>"}, {sp(" "), "<nil>"}, {sp("0"), "<nil>"}, {sp(" 12a "), "12a"}} {
		if got := deref(houseNumberShown(c.in)); got != c.want {
			t.Errorf("houseNumberShown(%q) = %q, want %q", deref(c.in), got, c.want)
		}
	}
	for _, c := range []struct {
		street, no *string
		want       string
	}{
		{nil, sp("1"), "<nil>"},
		{sp(" ** "), sp("1"), "<nil>"},
		{sp("Haupt**str."), nil, "Haupt str."},
		{sp("Hauptstr."), sp("0"), "Hauptstr."},
		{sp("Hauptstr. 1 2"), sp("12"), "Hauptstr. 1 2"},
		{sp("Hauptstr12"), sp("12"), "Hauptstr12 12"},
		{sp("Adlerstraße"), sp("4-8"), "Adlerstraße 4-8"},
	} {
		if got := deref(streetLine(c.street, c.no)); got != c.want {
			t.Errorf("streetLine(%q, %q) = %q, want %q", deref(c.street), deref(c.no), got, c.want)
		}
	}
	for _, c := range []struct {
		in   *string
		want string
	}{{nil, "<nil>"}, {sp(" 10117 "), "10117"}, {sp("1067"), "01067"}, {sp("D-10117 Berlin"), "10117"},
		{sp("10117 / 10119"), "<nil>"}, {sp("abc"), "<nil>"}, {sp(" 10117"), "10117"}} {
		if got := deref(postcodeShown(c.in)); got != c.want {
			t.Errorf("postcodeShown(%q) = %q, want %q", deref(c.in), got, c.want)
		}
	}
	if got := townLine(nil, nil); got != "" {
		t.Errorf("townLine(nil, nil) = %q", got)
	}
	if got := townLine(sp("10117"), sp(" Berlin ")); got != "10117 Berlin" {
		t.Errorf("townLine = %q", got)
	}
	if got := townLine(sp("x"), sp(" ")); got != "" {
		t.Errorf("townLine(x, blank) = %q", got)
	}
	for name, want := range map[string]bool{
		"": false, "DE-ABC-123": true, "bnetza_lsr_52.5_13.4 (Berlin)": true, "Parkhaus Adler 2": false, "Station_A": false,
	} {
		if got := isRecordIDName(name); got != want {
			t.Errorf("isRecordIDName(%q) = %v", name, got)
		}
	}
	for _, c := range []struct {
		in   *string
		want string
	}{{nil, "<nil>"}, {sp("  "), "<nil>"}, {sp("DE-ABC-123"), "<nil>"}, {sp(" Q-Park "), "Q-Park"}} {
		if got := deref(siteNameShown(c.in)); got != c.want {
			t.Errorf("siteNameShown(%q) = %q", deref(c.in), got)
		}
	}
	if got := addressFold("Adlerstr. 4, Groß"); got != " adlerstrasse 4 gross " {
		t.Errorf("addressFold = %q", got)
	}
	if got := transliterateDigraphs("koeln muenchen baer"); got != "koln munchen bar" {
		t.Errorf("transliterateDigraphs = %q", got)
	}
	for _, c := range []struct {
		in   *string
		want string
	}{{nil, ""}, {sp("x"), ""}, {sp("D-10117 Berlin"), "10117"}} {
		if got := postcodeOf(c.in); got != c.want {
			t.Errorf("postcodeOf(%q) = %q", deref(c.in), got)
		}
	}
}

func TestAnswerFoldKeys(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	for _, c := range []struct {
		in   *string
		want string
	}{{nil, "<nil>"}, {sp("--"), "<nil>"}, {sp("Hauptstraße"), "hauptstr"}, {sp("Allee Str 11"), "alestr11"}} {
		if got := deref(foldWordKey(c.in)); got != c.want {
			t.Errorf("foldWordKey(%q) = %q, want %q", deref(c.in), got, c.want)
		}
	}
	for _, c := range []struct {
		in   *string
		want string
	}{{nil, "<nil>"}, {sp(" "), "<nil>"}, {sp("4 – 8"), "4-8"}, {sp("12A"), "12a"}} {
		if got := deref(houseFoldKey(c.in)); got != c.want {
			t.Errorf("houseFoldKey(%q) = %q, want %q", deref(c.in), got, c.want)
		}
	}
	if addressFoldKey(nil, sp("1")) != nil || addressFoldKey(sp("A"), nil) != nil {
		t.Error("addressFoldKey of a half address is not nil")
	}
	if got := deref(addressFoldKey(sp("Hauptstraße"), sp("1"))); got != "hauptstr#1" {
		t.Errorf("addressFoldKey = %q", got)
	}
	if operatorWords(nil) != nil {
		t.Error("operatorWords(nil) is not nil")
	}
	if got := operatorWords(sp("EnBW Energie Deutschland GmbH & Co. KG")); len(got) != 2 || got[0] != "enbw" || got[1] != "energie" {
		t.Errorf("operatorWords = %q", got)
	}
	for _, c := range []struct {
		a, b answerFacts
		want bool
	}{
		{answerFacts{}, answerFacts{operator: sp("EnBW")}, true},
		{answerFacts{operator: sp("EnBW AG")}, answerFacts{operator: sp("EnBW Energie")}, true},
		{answerFacts{operator: sp("Ionity")}, answerFacts{operator: sp("Allego"), name: sp("Ionity Ladepark")}, true},
		{answerFacts{operator: sp("Allego"), name: sp("Ionity Ladepark")}, answerFacts{operator: sp("Ionity")}, true},
		{answerFacts{operator: sp("Ionity")}, answerFacts{operator: sp("Allego")}, false},
	} {
		if got := operatorsAgree(c.a, c.b); got != c.want {
			t.Errorf("operatorsAgree(%q, %q) = %v", deref(c.a.operator), deref(c.b.operator), got)
		}
	}
	here, near, far := geo.Point{Lat: 52.52, Lon: 13.405}, geo.Point{Lat: 52.5202, Lon: 13.405}, geo.Point{Lat: 52.53, Lon: 13.405}
	addr := func(at geo.Point, name, op *string) answerFacts {
		return answerFacts{at: at, name: name, street: sp("Hauptstraße"), house: sp("1"), postcode: sp("10117"), operator: op}
	}
	other := addr(near, nil, sp("Allego"))
	other.postcode = sp("10119")
	noHouse := addr(near, sp("B"), nil)
	noHouse.house = nil
	for name, c := range map[string]struct {
		a, b answerFacts
		want bool
	}{
		"far":               {addr(here, sp("A"), nil), addr(far, sp("A"), nil), false},
		"other postcode":    {addr(here, sp("A"), nil), other, false},
		"one name":          {addr(here, sp("Q-Park"), nil), addr(near, sp("Q Park"), nil), true},
		"one address":       {addr(here, sp("A"), sp("EnBW")), addr(near, nil, sp("EnBW AG")), true},
		"address, no house": {noHouse, addr(here, sp("A"), nil), false},
		"other has none":    {addr(here, sp("A"), nil), noHouse, false},
		"operators differ":  {addr(here, nil, sp("Ionity")), addr(near, nil, sp("Allego")), false},
	} {
		if got := sameAnswerRow(c.a, c.b); got != c.want {
			t.Errorf("%s: sameAnswerRow = %v", name, got)
		}
	}
}

func TestPlugNamesAndNumbers(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	for _, c := range []struct{ in, lang, want string }{
		{"IEC62196T2COMBO", "de", "CCS2"}, {" iec62196T2 ", "de", "Typ 2"}, {"iec62196T2", "en", "Type 2"},
		{"domesticB", "de", "Haushaltsstecker Typ B"}, {"domesticB", "en", "household plug type B"}, {"x_plug", "de", "x_plug"},
	} {
		if got := ConnectorLabel(c.in, c.lang); got != c.want {
			t.Errorf("ConnectorLabel(%q, %s) = %q, want %q", c.in, c.lang, got, c.want)
		}
	}
	for _, c := range []struct {
		x      float64
		digits int
		want   string
	}{
		{1.005, 2, "1.00"}, {0.565, 2, "0.56"}, {0.575, 2, "0.57"}, {2.5, 0, "3"}, {0.045, 2, "0.04"}, {0.0451, 2, "0.05"},
		{9.995, 2, "9.99"}, {9.9951, 2, "10.00"}, {-0.001, 2, "-0.00"}, {-1.25, 1, "-1.3"}, {52.520007, 5, "52.52001"}, {3, 2, "3.00"},
	} {
		if got := toFixed(c.x, c.digits); got != c.want {
			t.Errorf("toFixed(%v, %d) = %q, want %q", c.x, c.digits, got, c.want)
		}
	}
	for _, c := range []struct {
		kw         float64
		lang, want string
	}{{22, "de", "22 kW"}, {3.7, "de", "3,7 kW"}, {3.75, "en", "3.8 kW"}, {350, "en", "350 kW"}} {
		if got := fmtKw(c.kw, c.lang); got != c.want {
			t.Errorf("fmtKw(%v, %s) = %q", c.kw, c.lang, got)
		}
	}
	for _, c := range []struct {
		eur        float64
		vat        bool
		lang, want string
	}{{0.57, false, "de", "0,57 €/kWh netto"}, {0.57, false, "en", "0.57 €/kWh net"}, {0.599, true, "de", "0,60 €/kWh"}} {
		if got := fmtEur(c.eur, c.vat, c.lang); got != c.want {
			t.Errorf("fmtEur(%v, %v, %s) = %q", c.eur, c.vat, c.lang, got)
		}
	}
}
