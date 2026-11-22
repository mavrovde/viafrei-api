package wire_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/runtime/wire"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestTheAnswerClockIsTheWallClockUnlessFixed(t *testing.T) {
	req.Covers(t, "TEST-009")
	var out bytes.Buffer
	log := slog.New(slog.NewJSONHandler(&out, nil))
	before := time.Now()
	if got := wire.AnswerClock(config.Defaults(), log)(); got.Before(before) || time.Since(got) > time.Minute {
		t.Fatalf("unset: %s is not the wall clock", got)
	}
	if out.Len() != 0 {
		t.Fatalf("unset logs nothing, got %s", out.String())
	}
	cfg := config.Defaults()
	cfg.Clock.FixedNow = "2026-11-22T11:00:00+01:00"
	clock := wire.AnswerClock(cfg, log)
	want := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	if a, b := clock(), clock(); !a.Equal(want) || !b.Equal(want) {
		t.Fatalf("fixed: %s, %s; want %s twice", a, b, want)
	}
	if s := out.String(); !strings.Contains(s, `"level":"WARN"`) || !strings.Contains(s, `"at":"2026-11-22T10:00:00Z"`) || strings.Count(s, "\n") != 1 {
		t.Fatalf("one warning naming the instant, got %s", s)
	}
}
