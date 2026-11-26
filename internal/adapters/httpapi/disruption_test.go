package httpapi_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func (f *fakeFeatures) Disruptions(_ context.Context, key access.Record, q transit.DisruptionQuery) (features.DisruptionAnswer, error) {
	f.gotKey, f.gotDisrupt = key, q
	return f.disruption, f.err
}

func TestDisruptionsAnswer(t *testing.T) {
	req.Covers(t, "FR-TRANSIT-001", "ENV-040")
	f := everyAnswer()
	f.disruption = features.DisruptionAnswer{
		Disruptions: transit.Disruptions{
			Alerts:      []transit.Alert{{ID: "vrr:1", Header: sp("Bauarbeiten"), ActiveFrom: &fixedNow, PlacedBy: "route", Family: "src-alpha-transit"}},
			AlertsTotal: 2,
			Punctuality: &transit.Punctuality{AreaLevel: "kreis", AreaCode: "05315", TripsObserved: 40, TripsLate: 4, Family: "src-alpha-transit"},
			Coverage:    &transit.AlertsCoverage{Covered: true, LookbackDays: 14, Lands: []transit.LandCoverage{{Land: "05", AlertsSeen: 3, LastSeenAt: &fixedNow}}},
		},
		Meta: features.Answer[struct{}]{Families: []provenance.Family{transitFamily}, At: fixedNow},
	}
	h := featureHandler(t, config.Defaults(), f, nil)
	w := do(t, h, http.MethodGet, "/v1/transit-disruptions?area=05315&line=re%201&limit=1", bearer)
	body := w.Body.String()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, body)
	}
	if f.gotDisrupt.Line != "RE1" || f.gotDisrupt.WindowMin != 60 || f.gotDisrupt.Limit != 1 || f.gotDisrupt.Area != "05315" {
		t.Errorf("asked %+v", f.gotDisrupt)
	}
	for _, want := range []string{`"id":"vrr:1"`, `"lines":[]`, `"alertsTotal":2`, `"late":4`, `"windowMin":30`, `"windowMin":60`, `"hasMore":true`,
		`"alertsCoverage":{"covered":true,"lookbackDays":14,"lands":[{"land":"05","alertsSeen":3,"newestAlertSeenAt":"2026-03-29T01:30:00Z"}]}`} {
		if !strings.Contains(body, want) {
			t.Errorf("answer lacks %s: %s", want, body)
		}
	}
	if !strings.Contains(body, `"line":null`) {
		t.Errorf("a line the timetable was not asked about is null: %s", body)
	}
	f.disruption = features.DisruptionAnswer{Disruptions: transit.Disruptions{Line: &transit.LineCheck{Checked: true}}, Meta: features.Answer[struct{}]{At: fixedNow}}
	w = do(t, h, http.MethodGet, "/v1/transit-disruptions?line=S%201&window_min=120", bearer)
	if body = w.Body.String(); w.Code != http.StatusOK || !strings.Contains(body, `"punctuality":null`) || f.gotDisrupt.Limit != 6 || f.gotDisrupt.WindowMin != 120 ||
		!strings.Contains(body, `"line":{"key":"S1","checked":true,"inTimetable":false}`) {
		t.Errorf("line only: %d %s %+v", w.Code, body, f.gotDisrupt)
	}
	f.disruption.Line = &transit.LineCheck{Checked: true, InTimetable: true, Families: []string{"src-alpha-transit"}}
	if body = do(t, h, http.MethodGet, "/v1/transit-disruptions?line=re1", bearer).Body.String(); !strings.Contains(body, `"line":{"key":"RE1","checked":true,"inTimetable":true}`) {
		t.Errorf("a line in the timetable: %s", body)
	}
	assertProblem(t, do(t, h, http.MethodGet, "/v1/transit-disruptions", bearer), 400, "invalid-parameter")
	assertProblem(t, do(t, h, http.MethodGet, "/v1/transit-disruptions?area=05315&window_min=121", bearer), 400, "invalid-parameter")
	assertProblem(t, do(t, featureHandler(t, config.Defaults(), nil, nil), http.MethodGet, "/v1/transit-disruptions?area=05", bearer), 404, "feature-disabled")
	f.err = access.ErrNonePermitted
	assertProblem(t, do(t, h, http.MethodGet, "/v1/transit-disruptions?area=05", bearer), 403, "source-not-permitted")
}
