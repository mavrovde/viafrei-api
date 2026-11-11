package weather_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/weather"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestMapHazardKeepsTheKnownAndFoldsTheRest(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	for _, h := range weather.Hazards {
		if got := weather.MapHazard(string(h)); got != h {
			t.Errorf("%s mapped to %s", h, got)
		}
	}
	for _, raw := range []string{"", "Thunderstorm", "tornado", "other"} {
		if got := weather.MapHazard(raw); got != weather.Other {
			t.Errorf("%q mapped to %s, want other", raw, got)
		}
	}
}

func TestCheckLevelRefusesRatherThanRegrades(t *testing.T) {
	req.Covers(t, "FR-WEATHER-002")
	for raw := 1; raw <= 4; raw++ {
		if l, err := weather.CheckLevel(raw); err != nil || int(l) != raw {
			t.Errorf("%d: %d %v", raw, l, err)
		}
	}
	for _, raw := range []int{-1, 0, 5, 99} {
		if l, err := weather.CheckLevel(raw); !errors.Is(err, weather.ErrLevel) || l != 0 {
			t.Errorf("%d: %d %v, want ErrLevel", raw, l, err)
		}
	}
}

func TestKnownHazards(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	known, other := weather.Query{}.KnownHazards()
	if !other || len(known) != len(weather.Hazards)-1 || slices.Contains(known, weather.Other) {
		t.Errorf("every hazard: %v %v", known, other)
	}
	known[0] = "changed"
	if weather.Hazards[0] == "changed" {
		t.Error("the list of hazards was handed out, not a copy")
	}
	known, other = weather.Query{Hazards: []weather.Hazard{"snow", "ice"}}.KnownHazards()
	if other || !slices.Equal(known, []weather.Hazard{"snow", "ice"}) {
		t.Errorf("snow,ice: %v %v", known, other)
	}
	known, other = weather.Query{Hazards: []weather.Hazard{weather.Other}}.KnownHazards()
	if !other || len(known) != 0 || known == nil {
		t.Errorf("other alone: %v %v", known, other)
	}
}

func TestLampOf(t *testing.T) {
	req.Covers(t, "FR-WEATHER-001")
	for _, c := range []struct {
		levels []weather.Level
		want   weather.Lamp
	}{
		{nil, weather.LampGreen},
		{[]weather.Level{1, 1}, weather.LampGreen},
		{[]weather.Level{1, 2}, weather.LampAmber},
		{[]weather.Level{2, 3}, weather.LampRed},
		{[]weather.Level{4, 1}, weather.LampRed},
	} {
		ws := make([]weather.Warning, len(c.levels))
		for i, l := range c.levels {
			ws[i].Level = l
		}
		if got := weather.LampOf(ws); got != c.want {
			t.Errorf("%v: %s, want %s", c.levels, got, c.want)
		}
	}
}
