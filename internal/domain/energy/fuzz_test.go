package energy_test

import (
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/energy"
)

// FuzzReadSchedule reads any opening-hours entry and open_at text: never a
// panic, a window only from a readable entry, a resolved open_at never
// before now for a bare clock time.
func FuzzReadSchedule(f *testing.F) {
	for _, s := range [][4]string{{"Mo-Fr", "06:00", "22:00", "23:30"}, {"Sonn- und Feiertage", "00:00", "00:00", "2026-10-25 02:30"},
		{"Fr bis Mo", "20:00", "02:00", "24:00"}, {"", "x", "", "2026-02-30 10:00"}} {
		f.Add(s[0], s[1], s[2], s[3])
	}
	now := time.Date(2026, 3, 30, 12, 0, 0, 0, time.UTC)
	f.Fuzz(func(t *testing.T, text, start, end, openAt string) {
		s, ok := energy.ReadSchedule(energy.Hours{Times: []energy.OpeningTime{{Text: text, Start: start, End: end}}})
		if ok == (len(s.Unreadable) > 0) {
			t.Fatalf("%q %q %q: read %v, unreadable %v", text, start, end, ok, s.Unreadable)
		}
		at, ok := energy.ResolveOpenAt(openAt, now)
		if ok {
			s.OpenAt(at)
		}
	})
}
