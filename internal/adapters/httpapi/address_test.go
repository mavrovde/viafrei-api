package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func (f *fakeFeatures) Addresses(_ context.Context, key access.Record, q places.AddressQuery) (features.AddressAnswer, error) {
	f.gotKey, f.gotAddress = key, q
	return f.addresses, f.err
}

func TestAddressAnswer(t *testing.T) {
	req.Covers(t, "ENV-040")
	f := everyAnswer()
	f.addresses = features.AddressAnswer{Addresses: []places.Address{{ID: "osm:node1", Street: sp("Hauptstraße"), Family: "src-alpha-places"}},
		Meta: features.Answer[struct{}]{Families: []provenance.Family{transitFamily}, At: fixedNow}}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/addresses?q=Hauptstr.+12,+50667+K%C3%B6ln", bearer)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, body)
	}
	if f.gotAddress.Street != "Hauptstr." || f.gotAddress.Postcode != "50667" || f.gotAddress.Place != "Köln" || f.gotAddress.Limit != 3 {
		t.Errorf("asked %+v", f.gotAddress)
	}
	if !strings.Contains(body, `"street":"Hauptstraße"`) {
		t.Errorf("answer lacks the street: %s", body)
	}
	// The platform's address search states neither an identifier nor its reading of the typed text (docs/parity.md).
	for _, unwanted := range []string{`"id":`, `"query":`} {
		if strings.Contains(body, unwanted) {
			t.Errorf("answer states %s: %s", unwanted, body)
		}
	}
	if w = do(t, h, http.MethodGet, "/v1/addresses?q=Ring+1,+Fulda&limit=5", bearer); w.Code != http.StatusOK || f.gotAddress.Limit != 5 || f.gotAddress.Postcode != "" || f.gotAddress.Place != "Fulda" {
		t.Errorf("town only: %d %s", w.Code, w.Body.String())
	}
	assertProblem(t, do(t, h, http.MethodGet, "/v1/addresses?q=Hauptstra%C3%9Fe+12", bearer), 400, "invalid-parameter")
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/addresses?q=Ring+1,+Fulda", bearer), 404, "feature-disabled")
	f.err = access.ErrNonePermitted
	assertProblem(t, do(t, h, http.MethodGet, "/v1/addresses?q=Ring+1,+Fulda", bearer), 403, "source-not-permitted")
}
