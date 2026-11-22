package wire

import (
	"log/slog"
	"time"

	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

// AnswerClock is the clock the answers are computed at: the wall clock, or
// the instant clock.fixed_now names (#165), which it then warns of once, so
// no instance runs on a fixed clock unnoticed. Deadlines, caching, rate
// limits and keys never read it: they keep the wall clock.
func AnswerClock(cfg *config.Config, log *slog.Logger) func() time.Time {
	at, fixed := cfg.Clock.Fixed()
	if !fixed {
		return time.Now
	}
	log.Warn("clock.fixed_now is set: every answer is computed at this instant, not the wall clock", "at", at.UTC().Format(time.RFC3339))
	return func() time.Time { return at }
}
