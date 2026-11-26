package httpapi_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// placesAnswer holds three places: an operator's site with a register
// row folded in, a register row filed under an address elsewhere with a
// stale status, and a site with no status and a gross price.
func placesAnswer() features.PlacesAnswer {
	twin, seen := 100, fixedNow.Add(-2*time.Minute)
	reg := energy.SourceRegister{
		Sites:  []energy.SiteSource{{Key: "src-alpha-energy", Rank: 1}, {Key: "src-lsr-energy", Rank: 9, TwinRadiusM: &twin}},
		Status: []energy.StatusSource{{Key: "src-alpha-energy", StaleAfter: 15 * time.Minute, StatementMaxAge: time.Hour}, {Key: "x", StaleAfter: time.Hour}},
	}
	at := geo.Point{Lat: 52.52, Lon: 13.405}
	op := energy.PlaceRecord{Source: "src-alpha-energy", SiteID: "a", Name: sp("Q-Park Aquarée"), Operator: sp("Q-Park"), Street: sp("Spandauer Str."),
		HouseNumber: sp("1"), Postcode: sp("10178"), City: sp("Berlin"), Location: at, DistanceM: 12.4,
		Points: []energy.PlacePoint{
			{PointKey: "k1", EvseID: sp("DE*QPK*E1"), ConnectorTypes: []string{"iec62196T2COMBO", "iec62196T2"}, MaxPowerKw: fp(150)},
			{PointKey: "k2", ConnectorTypes: []string{"iec62196T2", "iec62196T2"}, MaxPowerKw: fp(22)},
			{PointKey: "k3", ConnectorTypes: []string{"iec62196T2COMBO"}, MaxPowerKw: fp(300)},
		}}
	lsr := energy.PlaceRecord{Source: "src-lsr-energy", SiteID: "r1", SiteIDs: []string{"r1", "r2", "a"}, Location: at, DistanceM: 20, SourceVersion: sp("2026-09-01"),
		Points: []energy.PlacePoint{{PointKey: "k4", ConnectorTypes: []string{"iec62196T2COMBO"}}}}
	reported := geo.Point{Lat: 52.6, Lon: 13.1}
	far := energy.PlaceRecord{Source: "src-lsr-energy", SiteID: "q", Postcode: sp("80331"), City: sp("München"), Location: at, Reported: &reported, DistanceM: 900,
		SourceVersion: sp("2026-10-01"), Points: []energy.PlacePoint{{PointKey: "k5"}}}
	none := energy.PlaceRecord{Source: "src-alpha-energy", SiteID: "n", Name: sp("Ladepark"), Location: at, DistanceM: 1500, Points: []energy.PlacePoint{{PointKey: "k6"}}}
	t1, t2 := fixedNow.Add(-time.Minute), fixedNow.Add(-3*time.Minute)
	return features.PlacesAnswer{
		Answer: features.Answer[energy.Place]{
			Items: []energy.Place{
				{Label: "Q-Park Aquarée, Spandauer Str. 1, 10178 Berlin", Members: []energy.Judged{
					{Record: op, Price: &energy.Price{Eur: 0.5700001}, JudgedPoints: energy.JudgedPoints{
						States:    []energy.Verdict{energy.VerdictAvailable, energy.VerdictOccupied, energy.VerdictOutOfOrder},
						PointAsOf: []*time.Time{&t1, &t2, &t2}, StatusAsOf: &t1, StaleAsOf: tp(fixedNow.Add(-time.Hour))}},
					{Record: lsr, JudgedPoints: energy.JudgedPoints{States: []energy.Verdict{energy.VerdictUnknown}, PointAsOf: []*time.Time{nil}}},
				}},
				{Label: "Standort bei München", Elsewhere: &energy.Elsewhere{LocatedNear: sp("München")}, Members: []energy.Judged{
					{Record: far, JudgedPoints: energy.JudgedPoints{States: []energy.Verdict{energy.VerdictStale}, PointAsOf: []*time.Time{&seen},
						StaleAsOf: &seen, StaleStates: energy.StaleStates{Occupied: 1}}},
				}},
				{Label: "Ladepark", Members: []energy.Judged{
					{Record: none, Ended: true, Price: &energy.Price{Eur: 0.6, IncludesVat: true}, JudgedPoints: energy.JudgedPoints{
						States: []energy.Verdict{energy.VerdictUnknown}, PointAsOf: []*time.Time{nil}}},
				}},
			},
			HasMore: true, Families: []provenance.Family{fresh}, At: fixedNow,
		},
		Register: reg, Lang: "de",
	}
}

func TestChargingOptionsByPlace(t *testing.T) {
	req.Covers(t, "FR-ENERGY-004", "ENV-040", "ENV-041")
	f := everyAnswer()
	f.chargePlaces = placesAnswer()
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/charging-options?at=52.52,13.405&radius_m=10000&limit=3&group=place&min_kw=50&connector=ccs2&only_available=true", bearer)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	want := features.PlaceQuery{At: geo.Point{Lat: 52.52, Lon: 13.405}, RadiusM: 10000, Limit: 3, MinKw: 50, Connector: "ccs2", OnlyAvailable: true}
	if f.gotPlaceQ != want || f.gotKey.ID != "abcdefghij" {
		t.Errorf("asked %+v", f.gotPlaceQ)
	}
	var body struct {
		Data []struct {
			ID           string
			Name         string
			DistanceM    int
			ChargePoints []struct {
				ID           string
				Availability *struct{ State, ObservedAt string }
			}
			Summary struct {
				ChargePoints int
				AvailableNow *int
				MaxPowerKw   *float64
			}
			Place map[string]any
		}
		Meta struct {
			Query           map[string]any
			PricesPublished map[string]any
			Page            map[string]any
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data) != 3 {
		t.Fatalf("data %s", w.Body)
	}
	d0 := body.Data[0]
	if d0.ID != "src-alpha-energy:a" || d0.Name != "Q-Park Aquarée, Spandauer Str. 1, 10178 Berlin" || d0.DistanceM != 12 || len(d0.ChargePoints) != 4 {
		t.Errorf("the first place: %+v", d0)
	}
	if d0.ChargePoints[0].ID != "DE*QPK*E1" || d0.ChargePoints[1].ID != "src-alpha-energy:k2" || d0.ChargePoints[0].Availability.State != "available" ||
		d0.ChargePoints[2].Availability.State != "out_of_order" || d0.ChargePoints[3].Availability != nil {
		t.Errorf("the points: %+v", d0.ChargePoints)
	}
	if d0.Summary.ChargePoints != 4 || *d0.Summary.AvailableNow != 1 || *d0.Summary.MaxPowerKw != 300 {
		t.Errorf("the summary: %+v", d0.Summary)
	}
	p0 := d0.Place
	if p0["siteId"] != "a" || !reflect.DeepEqual(p0["siteIds"], []any{"a", "r1", "r2"}) || p0["fromRegister"] != true || p0["registerReleasedOn"] != "2026-09-01" ||
		p0["sourceEnded"] != false {
		t.Errorf("the first place's place: %v", p0)
	}
	if got := p0["address"]; !reflect.DeepEqual(got, map[string]any{"street": "Spandauer Str.", "number": "1", "postcode": "10178", "city": "Berlin"}) {
		t.Errorf("the address: %v", got)
	}
	if got := p0["status"]; !reflect.DeepEqual(got, map[string]any{"free": 1.0, "occupied": 1.0, "outOfOrder": 1.0, "total": 4.0,
		"observedAt": "2026-03-29T01:29:00Z", "stale": false}) {
		t.Errorf("the status: %v", got)
	}
	if got := p0["price"]; !reflect.DeepEqual(got, map[string]any{"amount": 0.57, "basis": "net", "currency": "EUR"}) {
		t.Errorf("the price: %v", got)
	}
	if got := p0["connectors"]; !reflect.DeepEqual(got, []any{
		map[string]any{"type": "CCS2", "powerKw": 300.0, "count": 3.0}, map[string]any{"type": "Typ 2", "powerKw": 150.0, "count": 2.0}}) {
		t.Errorf("the connectors: %v", got)
	}
	for _, absent := range []string{"reportedAddress", "locatedNear", "reportedLocation"} {
		if _, ok := p0[absent]; ok {
			t.Errorf("the first place states %s", absent)
		}
	}
	p1 := body.Data[1].Place
	if p1["address"] != nil || p1["locatedNear"] != "München" || p1["registerReleasedOn"] != "2026-10-01" || body.Data[1].Summary.AvailableNow != nil {
		t.Errorf("the place elsewhere: %v", p1)
	}
	if got := p1["reportedAddress"]; !reflect.DeepEqual(got, map[string]any{"street": nil, "number": nil, "postcode": "80331", "city": "München"}) {
		t.Errorf("the reported address: %v", got)
	}
	if got := p1["reportedLocation"]; !reflect.DeepEqual(got, map[string]any{"lat": 52.6, "lon": 13.1}) {
		t.Errorf("the reported location: %v", got)
	}
	if got := p1["status"]; !reflect.DeepEqual(got, map[string]any{"free": 0.0, "occupied": 1.0, "outOfOrder": 0.0, "total": 1.0,
		"observedAt": "2026-03-29T01:28:00Z", "stale": true}) || p1["price"] != nil {
		t.Errorf("the stale status: %v, price %v", got, p1["price"])
	}
	if c := body.Data[1].ChargePoints[0]; c.Availability == nil || c.Availability.State != "stale" {
		t.Errorf("a stale point: %+v", c)
	}
	p2 := body.Data[2].Place
	if p2["status"] != nil || p2["fromRegister"] != false || p2["registerReleasedOn"] != nil || p2["sourceEnded"] != true ||
		!reflect.DeepEqual(p2["price"], map[string]any{"amount": 0.6, "basis": "gross", "currency": "EUR"}) || !reflect.DeepEqual(p2["connectors"], []any{}) {
		t.Errorf("the place with no status: %v", p2)
	}
	q := body.Meta.Query
	if q["group"] != "place" || q["access"] != "public" || q["connector"] != "ccs2" || q["onlyAvailable"] != true || q["minKw"] != 50.0 {
		t.Errorf("query %v", q)
	}
	if !reflect.DeepEqual(body.Meta.PricesPublished, map[string]any{"priced": 2.0, "places": 3.0}) || body.Meta.Page["hasMore"] != true {
		t.Errorf("meta %+v", body.Meta)
	}

	// None: the circle's count, no price line.
	n := 4
	f.chargePlaces = features.PlacesAnswer{Answer: features.Answer[energy.Place]{Items: []energy.Place{}, At: fixedNow, InRadius: &n},
		Register: energy.SourceRegister{}}
	w = do(t, h, http.MethodGet, "/v1/charging-options?at=52.52,13.405&group=place", bearer)
	if b := w.Body.String(); w.Code != http.StatusOK || !strings.Contains(b, `"data":[]`) || !strings.Contains(b, `"inRadius":4`) ||
		!strings.Contains(b, `"pricesPublished":{"priced":0,"places":0}`) {
		t.Errorf("none: %d %s", w.Code, b)
	}
	if f.gotPlaceQ.RadiusM != 2000 || f.gotPlaceQ.Limit != 20 {
		t.Errorf("the defaults: %+v", f.gotPlaceQ)
	}

	f.err = errors.New("down")
	if w := do(t, h, http.MethodGet, "/v1/charging-options?at=52.52,13.405&group=place", bearer); w.Code != http.StatusServiceUnavailable {
		t.Errorf("a failed read: %d", w.Code)
	}
}

func TestChargingOptionsByPlaceRefusesWhatItDoesNotTake(t *testing.T) {
	req.Covers(t, "FR-ENERGY-004")
	h := featureHandler(t, config.Defaults(), everyAnswer(), nil)
	for _, q := range []string{"max_kw=50", "operator=EnBW", "conditions=open247", "access=all"} {
		assertProblem(t, do(t, h, http.MethodGet, "/v1/charging-options?at=52.52,13.405&group=place&"+q, bearer), 400, "invalid-parameter")
	}
	if w := do(t, h, http.MethodGet, "/v1/charging-options?at=52.52,13.405&group=place&access=public", bearer); w.Code != http.StatusOK {
		t.Errorf("access=public: %d", w.Code)
	}
}

func TestNearbyChargingByPlace(t *testing.T) {
	req.Covers(t, "FR-BRIEF-005", "ENV-040")
	f := everyAnswer()
	pa := placesAnswer()
	pa.RegisterReleasedOn = sp("2026-10-01")
	f.nearby = features.NearbyAnswer{ChargingPlaces: &pa, Parts: []features.PartResult{{Part: "charging", Status: features.StatusOK}},
		Meta: features.Answer[struct{}]{Families: []provenance.Family{fresh}, At: fixedNow}}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/nearby?at=52.52,13.405&categories=charging&charging_group=place", bearer)
	body := w.Body.String()
	if w.Code != http.StatusOK || !f.gotNearby.ChargingByPlace {
		t.Fatalf("status %d, asked %+v: %s", w.Code, f.gotNearby, body)
	}
	for _, want := range []string{`"chargingRegister":{"releasedOn":"2026-10-01"}`, `"siteId":"a"`, `"name":"Q-Park Aquarée, Spandauer Str. 1, 10178 Berlin"`} {
		if !strings.Contains(body, want) {
			t.Errorf("answer lacks %s: %s", want, body)
		}
	}
	pa.RegisterReleasedOn = nil
	if body = do(t, h, http.MethodGet, "/v1/nearby?at=52.52,13.405&categories=charging&charging_group=place", bearer).Body.String(); strings.Contains(body, "chargingRegister") {
		t.Errorf("no register row, but a register: %s", body)
	}
	if do(t, h, http.MethodGet, "/v1/nearby?at=52.52,13.405&categories=charging", bearer); f.gotNearby.ChargingByPlace {
		t.Error("by place without charging_group")
	}
}
