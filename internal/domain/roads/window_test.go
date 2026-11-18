package roads_test

import (
	"errors"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestDayWindow(t *testing.T) {
	req.Covers(t, "FR-ROAD-001")
	now := time.Date(2026, 3, 29, 23, 30, 0, 0, time.UTC) // 01:30 on 30 March in Berlin (CEST)
	day := func(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 0, 0, 0, time.UTC) }
	for _, c := range []struct {
		name       string
		horizon    int
		from, to   string
		start, end time.Time
		err        error
	}{
		{name: "nothing asked"},
		{name: "a horizon", horizon: 3, start: now, end: now.Add(72 * time.Hour)},
		{name: "from alone", from: "2026-03-28", start: day(2026, 3, 27, 23), end: day(2026, 4, 4, 22)},
		{name: "both", from: "2026-04-01", to: "2026-04-01", start: day(2026, 3, 31, 22), end: day(2026, 4, 1, 22)},
		{name: "to alone", to: "2026-03-31", start: day(2026, 3, 29, 22), end: day(2026, 3, 31, 22)},
		{name: "92 days", from: "2026-01-01", to: "2026-04-03", start: day(2025, 12, 31, 23), end: day(2026, 4, 3, 22)},
		{name: "93 days", from: "2026-01-01", to: "2026-04-04", err: roads.ErrWindowWide},
		{name: "inverted", from: "2026-04-02", to: "2026-04-01", err: roads.ErrWindowInverted},
		{name: "a bad from", from: "Friday", err: roads.ErrWindowDate},
		{name: "a bad to", to: "2026-02-30", err: roads.ErrWindowDate},
		{name: "twice", horizon: 1, to: "2026-04-01", err: roads.ErrWindowTwice},
	} {
		start, end, err := roads.DayWindow(now, c.horizon, c.from, c.to)
		if !errors.Is(err, c.err) || !start.Equal(c.start) || !end.Equal(c.end) {
			t.Errorf("%s: %v, %v, %v", c.name, start, end, err)
		}
	}
}

func TestFilterWindowDefaultsToNow(t *testing.T) {
	req.Covers(t, "FR-ROAD-001")
	now := time.Date(2026, 3, 29, 23, 30, 0, 0, time.UTC)
	if s, e := (roads.Filter{}).Window(now); !s.Equal(now) || !e.Equal(now) {
		t.Errorf("zero window %v %v", s, e)
	}
	later := now.Add(time.Hour)
	if s, e := (roads.Filter{From: later, Until: later}).Window(now); !s.Equal(later) || !e.Equal(later) {
		t.Errorf("window %v %v", s, e)
	}
}
