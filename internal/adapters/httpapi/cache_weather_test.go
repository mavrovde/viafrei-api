package httpapi_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/weather"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// A weather answer rests on every weather family its lamp read, not only on
// those it attributes, and on the clock: a warning placed at the position
// starting or ending changes it. A driving-rules answer rests on the rule
// rows, which carry no freshness version. None of these may be answered with
// a 304, or replayed, once it moved; and a weather answer is never served
// stale past the next start or end of a warning.

const (
	weatherURL = "/v1/weather-risk?at=52.52,13.405"
	rulesURL   = "/v1/driving-rules?topic=winter_tyres"
)

// newWeatherRig is the cache rig answering the weather and rules
// operations: the lamp read the attributed weather family and a quiet one
// with no warning in scope, which it does not attribute.
func newWeatherRig(t *testing.T, cfg *config.Config) *cacheRig {
	t.Helper()
	r := newCacheRig(t, cfg, "v1")
	r.versions.change(func(v *fakeVersions) {
		v.vs["src-alpha-weather"], v.vs[quietWeather], v.vs["src-alpha-rules"] = "a1", "w1", "k1"
		v.rulesRef = "u1"
	})
	a := riskAnswer()
	a.Read = []string{"src-alpha-weather", quietWeather}
	r.features.risk = a
	r.features.topic = topicAnswer()
	return r
}

func TestAWarningInAWeatherFamilyTheLampReadEndsAWeatherAnswer(t *testing.T) {
	req.Covers(t, "NFR-PERF-005", "FR-WEATHER-001")
	r := newWeatherRig(t, config.Defaults())
	etag := revalidates(t, r, weatherURL)
	// The quiet family contributed nothing and is not attributed; a warning
	// coming into force there gives it a new version.
	r.clock.add(2 * time.Second)
	r.versions.change(func(v *fakeVersions) { v.vs[quietWeather] = "w2" })
	answersAnew(t, r, weatherURL, etag, "a new version of a family the lamp read")
}

func TestAFirstPublicationOfAWeatherFamilyTheLampReadEndsItsAnswer(t *testing.T) {
	req.Covers(t, "NFR-PERF-005", "FR-WEATHER-001")
	r := newWeatherRig(t, config.Defaults())
	r.versions.change(func(v *fakeVersions) { delete(v.vs, quietWeather) })
	etag := revalidates(t, r, weatherURL)
	r.clock.add(2 * time.Second)
	r.versions.change(func(v *fakeVersions) { v.vs[quietWeather] = "w1" })
	answersAnew(t, r, weatherURL, etag, "the family's first data")
}

// TestAWarningStartingOrEndingEndsAWeatherAnswer: the answer is the stored
// one until the next start or end of a warning at the position, and a read
// from that instant on, whatever the versions say; it is never served stale
// from then on either.
func TestAWarningStartingOrEndingEndsAWeatherAnswer(t *testing.T) {
	req.Covers(t, "NFR-PERF-005", "NFR-RES-004", "FR-WEATHER-001")
	r := newWeatherRig(t, config.Defaults())
	a := r.features.risk
	a.WarningChange = tp(fixedNow.Add(30 * time.Second))
	a.ChangesAt = a.WarningChange
	r.features.risk = a
	first := r.road(t, weatherURL, "")
	if cc := first.Header().Get("Cache-Control"); cc != "private, max-age=30" {
		t.Fatalf("the max-age reaches past the warning's change: %q", cc)
	}
	etag := first.Header().Get("ETag")
	r.clock.add(30*time.Second - time.Nanosecond)
	if w := r.road(t, weatherURL, etag); w.Code != http.StatusNotModified {
		t.Fatalf("before the change: %d", w.Code)
	}
	// Storage down just before the change: the answer is served stale.
	r.failStorage(errFake)
	if w := r.road(t, weatherURL, ""); w.Code != http.StatusOK || w.Header().Get("Age") == "" {
		t.Fatalf("before the change, storage down: %d %v", w.Code, w.Header())
	}
	// At the change it is no longer: it would state a warning that ended,
	// or miss one that started.
	r.clock.add(time.Nanosecond)
	if w := r.road(t, weatherURL, ""); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("at the change, storage down: %d %s", w.Code, w.Body)
	}
	// Read at the change, the warning has ended: the lamp is another.
	r.failStorage(nil)
	later := riskAnswer()
	later.Read, later.Items, later.Lamp = a.Read, nil, weather.LampGreen
	r.features.risk = later
	if w := r.road(t, weatherURL, etag); w.Code != http.StatusOK || w.Header().Get("ETag") == etag {
		t.Fatalf("at the change: %d", w.Code)
	}
}

// TestAWeatherFamilyTheLampReadBoundsTheStaleAllowance: a weather answer is
// never served stale when a family its lamp read is never to be, though the
// answer does not attribute that family.
func TestAWeatherFamilyTheLampReadBoundsTheStaleAllowance(t *testing.T) {
	req.Covers(t, "NFR-PERF-005", "NFR-RES-004", "FR-WEATHER-001")
	for name, tc := range map[string]struct {
		noStale []string
		want    int
	}{
		"nothing forbids it":                        {want: http.StatusOK},
		"a family the lamp read never served stale": {noStale: []string{quietWeather}, want: http.StatusServiceUnavailable},
	} {
		cfg := config.Defaults()
		cfg.Cache.MaxAge.Weather = time.Minute
		cfg.Cache.NoStale = tc.noStale
		r := newWeatherRig(t, cfg)
		if w := r.road(t, weatherURL, ""); w.Code != http.StatusOK {
			t.Fatalf("%s: first answer: %d", name, w.Code)
		}
		// Past the max-age, inside the stale allowance.
		r.clock.add(2 * time.Minute)
		r.failStorage(errFake)
		if w := r.road(t, weatherURL, ""); w.Code != tc.want {
			t.Errorf("%s: %d, want %d", name, w.Code, tc.want)
		}
	}
}

// TestAFamilyTheLampReadTurningStaleEndsAWeatherAnswer: a family the lamp
// read, but the answer does not attribute, turns stale 10 s on with no
// warning starting or ending: from then on the lamp is amber, so the stored
// answer must not answer, though no version moved.
func TestAFamilyTheLampReadTurningStaleEndsAWeatherAnswer(t *testing.T) {
	req.Covers(t, "NFR-PERF-005", "FR-WEATHER-001")
	r := newWeatherRig(t, config.Defaults())
	a := r.features.risk
	a.Items, a.Lamp = nil, weather.LampGreen
	a.ChangesAt, a.WarningChange = tp(fixedNow.Add(10*time.Second)), nil
	r.features.risk = a
	first := r.road(t, weatherURL, "")
	if cc := first.Header().Get("Cache-Control"); cc != "private, max-age=10" {
		t.Fatalf("the max-age reaches past the turn: %q", cc)
	}
	etag := first.Header().Get("ETag")
	later := a
	later.Lamp, later.ChangesAt = weather.LampAmber, nil
	r.features.risk = later
	r.clock.add(10*time.Second - time.Nanosecond)
	if w := r.road(t, weatherURL, etag); w.Code != http.StatusNotModified {
		t.Fatalf("before the turn: %d", w.Code)
	}
	if w := r.road(t, weatherURL, ""); w.Header().Get("Age") == "" {
		t.Fatalf("before the turn the answer was read again: %s", w.Body)
	}
	r.clock.add(time.Nanosecond)
	w := r.road(t, weatherURL, etag)
	if w.Code != http.StatusOK || w.Header().Get("ETag") == etag {
		t.Fatalf("at the turn: %d", w.Code)
	}
	if data, _ := decode(t, w)["data"].(map[string]any); data["lamp"] != "amber" {
		t.Fatalf("at the turn the lamp is %v", data["lamp"])
	}
}

func TestAnEditedRuleEndsADrivingRulesAnswer(t *testing.T) {
	req.Covers(t, "NFR-PERF-005", "FR-RULES-001")
	r := newWeatherRig(t, config.Defaults())
	etag := revalidates(t, r, rulesURL)
	// The rules family's data version did not move: the rule rows did.
	r.clock.add(2 * time.Second)
	r.versions.change(func(v *fakeVersions) { v.rulesRef = "u2" })
	answersAnew(t, r, rulesURL, etag, "an edited rule")
}
