package features_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

type locationFake struct {
	*fake
	v   places.Location
	err error
}

func (f *locationFake) Location(context.Context, places.LocationQuery, []string) (places.Location, error) {
	return f.v, f.err
}

func locationService(f *locationFake) *features.Service {
	if f.fake == nil {
		f.fake = &fake{}
	}
	f.reg = registerOf("places")
	return features.New(features.Repositories{Register: f, Locations: f}, features.Settings{Language: "en"}, func() time.Time { return now })
}

func TestLocation(t *testing.T) {
	req.Covers(t, "SEC-030")
	full := key("commercial", []access.Scope{"places"}, "f1")
	v := places.Location{Address: &places.Address{Family: "f1"}, District: &places.Place{Family: "f1"}, State: &places.Place{Family: "f1"}}
	a, err := locationService(&locationFake{v: v}).Location(context.Background(), full, places.LocationQuery{})
	if err != nil || a.Address == nil || a.Municipality != nil || !a.Meta.At.Equal(now) {
		t.Errorf("answer %+v %v", a, err)
	}
	for name, c := range map[string]struct {
		f    *locationFake
		want error
	}{
		"read":       {&locationFake{err: errStore}, errStore},
		"post-check": {&locationFake{v: places.Location{Municipality: &places.Place{Family: "f2"}}}, features.ErrGateViolation},
	} {
		if _, err := locationService(c.f).Location(context.Background(), full, places.LocationQuery{}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}
