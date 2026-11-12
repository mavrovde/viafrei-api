package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// The road answers rest on more than the families they attribute: the lamp
// reads every permitted roads family and weather family, judges them by
// their confirmations and by the lamp's reference rows, and changes with
// the clock when a confirmation lapses or a situation or warning starts or
// ends. None of these may be answered with a 304, or replayed, once it
// moved.

const (
	motorwayURL  = "/v1/motorways/A8/conditions"
	situationURL = "/v1/road-situations/ev-01-0001"
	quietWeather = "src-omega-weather"
)

// newRoadRig is the cache rig answering the road operations: the lamp read
// the attributed roads family and a weather family with no warning in
// scope, which it does not attribute.
func newRoadRig(t *testing.T) *cacheRig {
	t.Helper()
	return newRoadRigWith(t, config.Defaults())
}

func newRoadRigWith(t *testing.T, cfg *config.Config) *cacheRig {
	t.Helper()
	r := newCacheRig(t, cfg, "v1")
	r.versions.change(func(v *fakeVersions) {
		v.vs["src-alpha-roads"], v.vs[quietWeather] = "r1", "w1"
		v.confirmed = map[string]string{"src-alpha-roads": "c1", quietWeather: "c1"}
		v.lampRef = "l1"
	})
	c := conditionsAnswer()
	c.Read.Roads, c.Read.Weather = []string{"src-alpha-roads"}, []string{quietWeather}
	r.features.conditions = c
	r.features.situation = situationAnswer()
	return r
}

func (r *cacheRig) road(t *testing.T, url, inm string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, r.h, http.MethodGet, url, http.Header{"Authorization": {"Bearer x"}, "If-None-Match": {inm}})
}

// revalidates asks url once and again with the validator it was given,
// which must be a 304 while nothing moved; it returns the validator.
func revalidates(t *testing.T, r *cacheRig, url string) string {
	t.Helper()
	first := r.road(t, url, "")
	etag := first.Header().Get("ETag")
	if first.Code != http.StatusOK || etag == "" {
		t.Fatalf("%s: %d, etag %q", url, first.Code, etag)
	}
	if w := r.road(t, url, etag); w.Code != http.StatusNotModified {
		t.Fatalf("%s: nothing moved and the repeat is %d", url, w.Code)
	}
	return etag
}

// answersAnew asserts that a revalidation with etag is a full answer with
// another validator, and that the request without one is no replay of the
// stored answer (it carries no Age).
func answersAnew(t *testing.T, r *cacheRig, url, etag, what string) {
	t.Helper()
	w := r.road(t, url, etag)
	if w.Code != http.StatusOK || w.Header().Get("ETag") == etag {
		t.Fatalf("after %s: %d, etag %q (was %q)", what, w.Code, w.Header().Get("ETag"), etag)
	}
	if again := r.road(t, url, ""); again.Header().Get("Age") != "" && again.Header().Get("ETag") == etag {
		t.Fatalf("after %s the stored answer was replayed", what)
	}
}

func TestAWarningInAWeatherFamilyTheLampReadEndsItsAnswer(t *testing.T) {
	req.Covers(t, "NFR-PERF-005", "FR-ROAD-004")
	r := newRoadRig(t)
	etag := revalidates(t, r, motorwayURL)
	// The weather family contributed nothing and is not attributed; a
	// warning coming into force there gives it a new version.
	r.clock.add(2 * time.Second)
	r.versions.change(func(v *fakeVersions) { v.vs[quietWeather] = "w2" })
	answersAnew(t, r, motorwayURL, etag, "a new weather version")
}

func TestAFirstPublicationOfAFamilyTheLampReadEndsItsAnswer(t *testing.T) {
	req.Covers(t, "NFR-PERF-005", "FR-ROAD-004")
	r := newRoadRig(t)
	r.versions.change(func(v *fakeVersions) {
		delete(v.vs, quietWeather)
		delete(v.confirmed, quietWeather)
	})
	etag := revalidates(t, r, "/v1/road-conditions?at=48.1,11.6")
	r.clock.add(2 * time.Second)
	r.versions.change(func(v *fakeVersions) { v.vs[quietWeather] = "w1" })
	answersAnew(t, r, "/v1/road-conditions?at=48.1,11.6", etag, "the family's first data")
}

func TestAConfirmationOfAFamilyTheLampReadEndsItsAnswer(t *testing.T) {
	req.Covers(t, "NFR-PERF-005", "FR-ROAD-004")
	r := newRoadRig(t)
	etag := revalidates(t, r, motorwayURL)
	// The family is confirmed again (a stale sight recovers): no data
	// version moved.
	r.clock.add(2 * time.Second)
	r.versions.change(func(v *fakeVersions) { v.confirmed["src-alpha-roads"] = "c2" })
	answersAnew(t, r, motorwayURL, etag, "a new confirmation")
}

func TestAChangedCoverageEndsALampAnswer(t *testing.T) {
	req.Covers(t, "NFR-PERF-005", "FR-ROAD-004")
	r := newRoadRig(t)
	etag := revalidates(t, r, motorwayURL)
	r.clock.add(2 * time.Second)
	r.versions.change(func(v *fakeVersions) { v.lampRef = "l2" })
	answersAnew(t, r, motorwayURL, etag, "a changed message coverage")
}

func TestALampChangingWithTheClockEndsItsAnswer(t *testing.T) {
	req.Covers(t, "NFR-PERF-005", "FR-ROAD-004")
	r := newRoadRig(t)
	// A confirmation lapses, or a situation's validity ends, 40 s on: the
	// lamp read after it is another (green here, with nothing else
	// moving).
	c := r.features.conditions
	c.ChangesAt = tp(fixedNow.Add(40 * time.Second))
	r.features.conditions = c
	first := r.road(t, motorwayURL, "")
	if cc := first.Header().Get("Cache-Control"); cc != "private, max-age=40" {
		t.Fatalf("the max-age reaches past the change: %q", cc)
	}
	etag := first.Header().Get("ETag")
	later := c
	later.Lamp, later.Reason, later.ChangesAt = "green", nil, nil
	r.features.conditions = later
	// Before the change the stored answer answers: a read would have been
	// the green one.
	r.clock.add(40*time.Second - time.Nanosecond)
	if w := r.road(t, motorwayURL, etag); w.Code != http.StatusNotModified {
		t.Fatalf("before the change: %d", w.Code)
	}
	if w := r.road(t, motorwayURL, ""); w.Header().Get("Age") == "" {
		t.Fatalf("before the change the answer was read again: %s", w.Body)
	}
	r.clock.add(time.Nanosecond)
	w := r.road(t, motorwayURL, etag)
	if w.Code != http.StatusOK || w.Header().Get("ETag") == etag {
		t.Fatalf("at the change: %d", w.Code)
	}
	if data, _ := decode(t, w)["data"].(map[string]any); data["lamp"] != "green" {
		t.Fatalf("at the change the lamp is %v", data["lamp"])
	}
}

func TestAnEndedSituationLeavingTheRetentionEndsItsAnswer(t *testing.T) {
	req.Covers(t, "NFR-PERF-005", "FR-ROAD-005")
	r := newRoadRig(t)
	a := r.features.situation
	a.ChangesAt = tp(fixedNow.Add(30 * time.Second))
	r.features.situation = a
	etag := revalidates(t, r, situationURL)
	// Read after the retention, the situation is not found.
	r.features.err = roads.ErrUnknownSituation
	r.clock.add(30*time.Second - time.Nanosecond)
	if w := r.road(t, situationURL, etag); w.Code != http.StatusNotModified {
		t.Fatalf("inside the retention: %d", w.Code)
	}
	r.clock.add(time.Nanosecond)
	if w := r.road(t, situationURL, etag); w.Code != http.StatusNotFound {
		t.Fatalf("past the retention: %d", w.Code)
	}
}

func TestTheMaxAgeOfAFamilyTheLampReadBoundsTheAnswer(t *testing.T) {
	req.Covers(t, "NFR-PERF-005", "FR-ROAD-004")
	cfg := config.Defaults()
	cfg.Cache.MaxAge.Weather = 20 * time.Second
	r := newRoadRigWith(t, cfg)
	// The answer attributes a roads family only; the weather family it read
	// has the shorter max-age.
	if cc := r.road(t, motorwayURL, "").Header().Get("Cache-Control"); cc != "private, max-age=20" {
		t.Fatalf("Cache-Control %q", cc)
	}
}
