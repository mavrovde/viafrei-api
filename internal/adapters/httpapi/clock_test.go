package httpapi_test

import (
	"bytes"
	"net/http"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/buildinfo"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// On a fixed answer clock (clock.fixed_now, #165) one request asked at two
// wall times is answered with the same bytes, while caching keeps the wall
// clock: the stored answer is served while the answer's clock has not
// reached the instant it changes, expires by wall time and carries its true
// Age.
func TestAFixedAnswerClockAnswersTheSameBytesAtAnyWallTime(t *testing.T) {
	req.Covers(t, "TEST-009", "FR-GEN-002")
	answer := chargingAnswer()
	fam := fresh // stale at fixedNow+55m
	fam.Version = "v1"
	answer.Families = []provenance.Family{fam}
	fixed := func() time.Time { return fixedNow }
	f := &countingFeatures{fakeFeatures: &fakeFeatures{charging: answer}, now: fixed}
	wall := &clock{t: fixedNow.Add(2 * time.Hour)} // past the instant the answer turns stale
	h, err := httpapi.NewHandler(httpapi.Deps{
		Config: config.Defaults(), Build: buildinfo.Info{Version: "t", Commit: "c"}, ContractVersion: "0.1",
		Now: wall.now, At: fixed,
		Auth: &lockedAuth{p: featureScopes()}, Sources: &fakeSources{register: fixtureRegister}, Features: f,
		Versions: &fakeVersions{vs: map[string]string{"src-alpha-energy": "v1"}, register: "reg-1"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ask := func() (int, http.Header, []byte) {
		w := do(t, h, http.MethodGet, chargingURL, http.Header{"Authorization": {"Bearer x"}, httpapi.RequestIDHeader: {"parity-run-0001"}})
		return w.Code, w.Header(), w.Body.Bytes()
	}
	code, head, first := ask()
	if code != http.StatusOK || head.Get("Cache-Control") != "private, max-age=60" || !bytes.Contains(first, []byte(`"generatedAt":"2026-03-29T01:30:00Z"`)) ||
		!bytes.Contains(first, []byte(`"stale":false`)) {
		t.Fatalf("first: %d %v %s", code, head, first)
	}
	wall.add(10 * time.Second)
	if _, head, again := ask(); !bytes.Equal(again, first) || head.Get("Age") != "10" || f.calls.Load() != 1 {
		t.Fatalf("10 s later: age %q, %d reads, body equal %v", head.Get("Age"), f.calls.Load(), bytes.Equal(again, first))
	}
	wall.add(time.Hour)
	if _, head, later := ask(); !bytes.Equal(later, first) || head.Get("Age") != "" || f.calls.Load() != 2 {
		t.Fatalf("an hour later: age %q, %d reads, body equal %v", head.Get("Age"), f.calls.Load(), bytes.Equal(later, first))
	}
}

// Without an answer clock the answers are computed at Now, as before.
func TestTheAnswerClockDefaultsToNow(t *testing.T) {
	req.Covers(t, "TEST-009")
	h := newHandler(t, config.Defaults(), nil)
	w := do(t, h, http.MethodGet, "/v1/meta", nil)
	if !bytes.Contains(w.Body.Bytes(), []byte(`"generatedAt":"2026-03-29T01:30:00Z"`)) {
		t.Fatalf("meta: %s", w.Body)
	}
}

// The place overview assesses its families' freshness on the answers' clock:
// fresh's data is five minutes old at At, whatever the wall clock says.
func TestTheOverviewAssessesFreshnessAtTheAnswerClock(t *testing.T) {
	req.Covers(t, "TEST-009")
	f := &fakeFeatures{overview: overviewAnswer()}
	w := do(t, featureHandler(t, config.Defaults(), f, nil), http.MethodGet, "/v1/places/pl-01-0001/overview", bearer)
	if !bytes.Contains(w.Body.Bytes(), []byte(`"family":"src-alpha-energy","dataAsOf":"2026-03-29T01:25:00Z","ageS":300`)) {
		t.Fatalf("overview freshness: %s", w.Body)
	}
}

// A family whose data last changed 2 848 s ago but was confirmed unchanged
// 169 s ago is current to its confirmation, as the platform judges it: its
// freshness entry is dated at the confirmation, is not stale against its
// 990 s threshold, and the answer carries no data_stale warning (#222).
func TestConfirmedUnchangedDataDoesNotReadAsStale(t *testing.T) {
	req.Covers(t, "ENV-003")
	confirmed := fresh
	confirmed.StaleAfter = 990 * time.Second
	confirmed.DataAsOf, confirmed.ReadAt = tp(fixedNow.Add(-2848*time.Second)), tp(fixedNow.Add(-169*time.Second))
	a := overviewAnswer()
	a.Families = []provenance.Family{confirmed}
	w := do(t, featureHandler(t, config.Defaults(), &fakeFeatures{overview: a}, nil), http.MethodGet, "/v1/places/pl-01-0001/overview", bearer)
	if !bytes.Contains(w.Body.Bytes(), []byte(`"family":"src-alpha-energy","dataAsOf":"2026-03-29T01:27:11Z","ageS":169,"staleAfterS":990,"stale":false`)) {
		t.Fatalf("overview freshness: %s", w.Body)
	}
	if bytes.Contains(w.Body.Bytes(), []byte(`"data_stale"`)) {
		t.Fatalf("a confirmed family reads as stale: %s", w.Body)
	}
}
