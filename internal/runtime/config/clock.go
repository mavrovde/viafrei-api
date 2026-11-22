package config

import "time"

// Clock holds the instant the answers are computed at. Unset, it is the
// wall clock; a parity run (TEST-009) sets it, so two runs of one request
// at different wall times answer the same bytes (#165).
type Clock struct {
	FixedNow string `koanf:"fixed_now" default:"" validate:"omitempty,rfc3339" desc:"An RFC 3339 instant, for example 2026-11-22T10:00:00Z, at which every answer is computed instead of the wall clock: what is in force, ages and freshness, the generated time. For parity runs only: refused unless auth.environment is test. Deadlines, caching, rate limits and keys keep the wall clock. Empty (the default): the wall clock."`
}

// Fixed is the instant clock.fixed_now names, and whether it is set. A
// loaded configuration holds a valid instant or none.
func (c Clock) Fixed() (time.Time, bool) {
	at, err := time.Parse(time.RFC3339, c.FixedNow)
	return at, err == nil
}

// crossFieldClock keeps a fixed clock off an instance that accepts live keys.
func crossFieldClock(c *Config) Errors {
	if c.Clock.FixedNow != "" && c.Auth.Environment != "test" {
		return Errors{"clock.fixed_now: only with auth.environment test; an instance that accepts live keys answers on the wall clock"}
	}
	return nil
}
