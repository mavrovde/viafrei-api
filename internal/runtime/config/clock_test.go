package config_test

import (
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestTheClockIsTheWallClockByDefault(t *testing.T) {
	req.Covers(t, "CFG-001", "TEST-009")
	c := config.Defaults()
	if c.Clock.FixedNow != "" {
		t.Fatalf("default %q", c.Clock.FixedNow)
	}
	if at, fixed := c.Clock.Fixed(); fixed || !at.IsZero() {
		t.Fatalf("unset reads as fixed: %s", at)
	}
}

func TestAFixedClockOnlyOnATestInstance(t *testing.T) {
	req.Covers(t, "CFG-004", "TEST-009")
	c, err := config.Load(config.Options{Environ: []string{"VFAPI_CLOCK_FIXED_NOW=2026-11-22T11:00:00+01:00", "VFAPI_AUTH_ENVIRONMENT=test"}})
	if err != nil {
		t.Fatal(err)
	}
	if at, fixed := c.Clock.Fixed(); !fixed || !at.Equal(time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("fixed %v at %s", fixed, at)
	}
	_, err = config.Load(config.Options{Environ: []string{"VFAPI_CLOCK_FIXED_NOW=2026-11-22T10:00:00Z"}})
	assertRefused(t, err, "clock.fixed_now: only with auth.environment test")
	for _, bad := range []string{"2026-11-22", "2026-11-22T10:00:00", "yesterday"} {
		_, err = config.Load(config.Options{Environ: []string{"VFAPI_CLOCK_FIXED_NOW=" + bad, "VFAPI_AUTH_ENVIRONMENT=test"}})
		assertRefused(t, err, "clock.fixed_now: fails the rfc3339 rule")
	}
}
