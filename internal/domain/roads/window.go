package roads

import (
	"errors"
	"time"
)

// The window bounds of the platform's road tools: road status looks at most
// MaxHorizonDays ahead; roadworks ahead looks DefaultWindowDays past its
// first day unless told, and at most MaxWindowDays.
const (
	MaxHorizonDays    = 14
	DefaultWindowDays = 7
	MaxWindowDays     = 92
)

// The refusals of a window.
var (
	ErrWindowTwice    = errors.New("give horizon_days or from and to, not both")
	ErrWindowDate     = errors.New("a window day is a calendar date, YYYY-MM-DD")
	ErrWindowInverted = errors.New("the window's last day is before its first")
	ErrWindowWide     = errors.New("the window spans more than 92 days")
)

// berlin is the zone a window's calendar days are read in.
var berlin, _ = time.LoadLocation("Europe/Berlin")

// DayWindow is the window a road question asks for at now, as the bounds a
// situation's validity must overlap: horizonDays ahead of now (the road
// status horizon), or the German calendar days from to to, both inclusive
// (the roadworks window: from defaults to today, to to DefaultWindowDays
// after from). Nothing asked is the zero window, the situations in force now.
func DayWindow(now time.Time, horizonDays int, from, to string) (start, end time.Time, err error) {
	if horizonDays > 0 && (from != "" || to != "") {
		return start, end, ErrWindowTwice
	}
	if horizonDays > 0 {
		return now, now.Add(time.Duration(horizonDays) * 24 * time.Hour), nil
	}
	if from == "" && to == "" {
		return start, end, nil
	}
	first := now.In(berlin)
	first = time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, berlin)
	if from != "" {
		if first, err = time.ParseInLocation(time.DateOnly, from, berlin); err != nil {
			return start, end, ErrWindowDate
		}
	}
	last := first.AddDate(0, 0, DefaultWindowDays)
	if to != "" {
		if last, err = time.ParseInLocation(time.DateOnly, to, berlin); err != nil {
			return start, end, ErrWindowDate
		}
	}
	switch days := last.Sub(first).Hours() / 24; {
	case days < 0:
		return start, end, ErrWindowInverted
	case days > MaxWindowDays+0.1:
		return start, end, ErrWindowWide
	}
	return first.UTC(), last.AddDate(0, 0, 1).UTC(), nil
}
