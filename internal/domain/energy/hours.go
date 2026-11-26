package energy

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// OpeningTime is one published opening-hours entry of a filling station: the
// days as written ("Mo-Fr", "täglich") and the clock times.
type OpeningTime struct {
	Text  string `json:"text"`
	Start string `json:"start"`
	End   string `json:"end"`
}

// Hours are a station's published opening hours: open around the clock, or
// its entries.
type Hours struct {
	WholeDay *bool
	Times    []OpeningTime
	// AsOf is when the hours were last read from the provider; nil when
	// never, and then there are none.
	AsOf *time.Time
}

// window is one weekday's opening, in minutes from midnight; an end past
// 24:00 runs into the next day. Day 0 is Monday.
type window struct{ day, start, end int }

// Schedule is the platform's reading of a station's hours.
type Schedule struct {
	WholeDay bool
	windows  []window
	// Unreadable are the entries that could not be read.
	Unreadable []string
}

var dayWords = map[string]int{
	"mo": 0, "mon": 0, "montag": 0, "montags": 0,
	"di": 1, "die": 1, "dienstag": 1, "dienstags": 1,
	"mi": 2, "mit": 2, "mittwoch": 2, "mittwochs": 2,
	"do": 3, "don": 3, "donnerstag": 3, "donnerstags": 3,
	"fr": 4, "fre": 4, "freitag": 4, "freitags": 4,
	"sa": 5, "sam": 5, "samstag": 5, "samstags": 5, "sonnabend": 5, "sonnabends": 5,
	"so": 6, "son": 6, "sonntag": 6, "sonntags": 6,
}

var everyDay = []string{"täglich", "taeglich", "taglich", "alle tage", "jeden tag", "durchgehend", "immer"}

var (
	sundayHoliday = regexp.MustCompile(`sonn-\s*und\s*feiertage?n?`)
	holidayWord   = regexp.MustCompile(`feiertage?n?s?`)
	spaces        = regexp.MustCompile(`\s+`)
	daySplit      = regexp.MustCompile(`\s*(?:,|;|\+|&|/|\bund\b)\s*`)
	dayRange      = regexp.MustCompile(`^([a-zäöü]+)\s*(?:-|–|—|bis)\s*([a-zäöü]+)$`)
	clockRe       = regexp.MustCompile(`^\s*(\d{1,2}):(\d{2})(?::(\d{2}))?\s*$`)
)

// ParseDays reads the days of an entry as the platform does; ok is false
// when it cannot be read.
func ParseDays(text string) (days []int, ok bool) {
	raw := strings.ToLower(strings.TrimSpace(text))
	if raw == "" {
		return nil, false
	}
	cleaned := sundayHoliday.ReplaceAllString(raw, "so")
	cleaned = strings.TrimSpace(spaces.ReplaceAllString(holidayWord.ReplaceAllString(cleaned, ""), " "))
	all := []int{0, 1, 2, 3, 4, 5, 6}
	if slices.Contains(everyDay, cleaned) {
		return all, true
	}
	set := map[int]bool{}
	for _, part := range daySplit.Split(cleaned, -1) {
		token := strings.TrimSpace(strings.ReplaceAll(part, ".", ""))
		if token == "" {
			continue
		}
		if slices.Contains(everyDay, token) {
			for _, d := range all {
				set[d] = true
			}
			continue
		}
		if m := dayRange.FindStringSubmatch(token); m != nil {
			from, okFrom := dayWords[m[1]]
			to, okTo := dayWords[m[2]]
			if !okFrom || !okTo {
				return nil, false
			}
			for d, i := from, 0; i < 7; d, i = (d+1)%7, i+1 {
				set[d] = true
				if d == to {
					break
				}
			}
			continue
		}
		d, ok := dayWords[token]
		if !ok {
			return nil, false
		}
		set[d] = true
	}
	for d := range set {
		days = append(days, d)
	}
	slices.Sort(days)
	return days, len(days) > 0
}

// ParseClock reads "HH:MM" (seconds ignored) as minutes from midnight, 24:00
// included; ok is false otherwise.
func ParseClock(s string) (minutes int, ok bool) {
	m := clockRe.FindStringSubmatch(s)
	if m == nil {
		return 0, false
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	if h > 24 || mi > 59 || (h == 24 && mi != 0) {
		return 0, false
	}
	return h*60 + mi, true
}

// ReadSchedule reads a station's hours as the platform does. ok is false
// when nothing is known: no whole-day flag and no readable entry.
func ReadSchedule(h Hours) (s Schedule, ok bool) {
	if h.WholeDay != nil && *h.WholeDay {
		return Schedule{WholeDay: true}, true
	}
	for _, e := range h.Times {
		label := strings.TrimSpace(e.Text)
		if label == "" {
			label = "(ohne Text)"
		}
		days, okDays := ParseDays(e.Text)
		start, okStart := ParseClock(e.Start)
		end, okEnd := ParseClock(e.End)
		if !okDays || !okStart || !okEnd {
			s.Unreadable = append(s.Unreadable, label)
			continue
		}
		switch {
		case start == end && start == 0:
			end = 24 * 60
		case start == end:
			s.Unreadable = append(s.Unreadable, label)
			continue
		case end < start:
			end += 24 * 60
		}
		for _, d := range days {
			s.windows = append(s.windows, window{day: d, start: start, end: end})
		}
	}
	return s, len(s.windows) > 0
}

// berlin is the zone every open_at text and opening hour is read in; the
// zone database is the runtime image's, as for every other Berlin read.
var berlin, _ = time.LoadLocation("Europe/Berlin")

// OpenAt says whether the schedule has the station open at the instant, by
// the Berlin wall clock; known is false when an unreadable entry might have
// it open.
func (s Schedule) OpenAt(at time.Time) (open, known bool) {
	if s.WholeDay {
		return true, true
	}
	w := at.In(berlin)
	day := (int(w.Weekday()) + 6) % 7
	minutes := w.Hour()*60 + w.Minute()
	yesterday := (day + 6) % 7
	for _, o := range s.windows {
		if o.day == day && minutes >= o.start && minutes < o.end {
			return true, true
		}
		if o.day == yesterday && o.end > 24*60 && minutes+24*60 < o.end {
			return true, true
		}
	}
	return false, len(s.Unreadable) == 0
}

var (
	openAtFull  = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})[T ](\d{1,2}):(\d{2})$`)
	openAtClock = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)
)

// OpenAtPattern is the shape an open_at text has: a Berlin clock time, or a
// Berlin date and clock time.
const OpenAtPattern = `^(\d{4}-\d{2}-\d{2}[T ])?\d{1,2}:\d{2}$`

// ResolveOpenAt is the instant an open_at text names, as the platform reads
// it: "YYYY-MM-DD HH:MM" a Berlin date and time, "HH:MM" the next time the
// Berlin clock shows it from now on. ok is false for a time the calendar
// does not hold. A wall time that falls in the spring gap is moved forward
// by the gap, and one the autumn fold repeats is the later of the two.
func ResolveOpenAt(text string, now time.Time) (at time.Time, ok bool) {
	text = strings.TrimSpace(text)
	if m := openAtFull.FindStringSubmatch(text); m != nil {
		y, _ := strconv.Atoi(m[1])
		mo, _ := strconv.Atoi(m[2])
		d, _ := strconv.Atoi(m[3])
		h, _ := strconv.Atoi(m[4])
		mi, _ := strconv.Atoi(m[5])
		if h > 23 || mi > 59 || mo < 1 || mo > 12 || d < 1 || d > time.Date(y, time.Month(mo)+1, 0, 0, 0, 0, 0, time.UTC).Day() {
			return time.Time{}, false
		}
		return berlinInstant(y, time.Month(mo), d, h, mi), true
	}
	m := openAtClock.FindStringSubmatch(text)
	if m == nil {
		return time.Time{}, false
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	if h > 23 || mi > 59 {
		return time.Time{}, false
	}
	n := now.In(berlin)
	today := berlinInstant(n.Year(), n.Month(), n.Day(), h, mi)
	if !today.Before(now) {
		return today, true
	}
	return berlinInstant(n.Year(), n.Month(), n.Day()+1, h, mi), true
}

// berlinInstant is the Berlin wall time as an instant. Go's time.Date
// reads a wall time in the spring gap with the offset before the gap (moved
// forward by it) and one the autumn fold repeats as the later instant, the
// platform's rule for open_at both times (TestResolveOpenAt holds it).
func berlinInstant(y int, mo time.Month, d, h, mi int) time.Time {
	return time.Date(y, mo, d, h, mi, 0, 0, berlin)
}
