package features_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

type poiFake struct {
	*fake
	v   []places.Poi
	err error
}

func (f *poiFake) Pois(context.Context, places.PoiQuery, []string) ([]places.Poi, error) { return f.v, f.err }

func poiService(f *poiFake) *features.Service {
	if f.fake == nil {
		f.fake = &fake{}
	}
	f.reg = registerOf("places")
	return features.New(features.Repositories{Register: f, Pois: f}, features.Settings{Language: "en"}, func() time.Time { return now })
}

func TestPoisPagesWithinTheReach(t *testing.T) {
	req.Covers(t, "SEC-030")
	full := key("commercial", []access.Scope{"places"}, "f1")
	many := make([]places.Poi, places.PoiReach+1)
	for i := range many {
		many[i] = places.Poi{ID: fmt.Sprint(i), Family: "f1"}
	}
	a, err := poiService(&poiFake{v: many}).Pois(context.Background(), full, places.PoiQuery{Limit: 10, Offset: 45})
	if err != nil || len(a.Pois) != 5 || a.Pois[0].ID != "45" || a.Total != places.PoiReach || !a.Beyond {
		t.Errorf("last page %+v %v", a, err)
	}
	if a, err = poiService(&poiFake{v: many[:3]}).Pois(context.Background(), full, places.PoiQuery{Limit: 10, Offset: 5}); err != nil || len(a.Pois) != 0 || a.Total != 3 || a.Beyond {
		t.Errorf("past the end %+v %v", a, err)
	}
	for name, c := range map[string]struct {
		f    *poiFake
		want error
	}{
		"read":       {&poiFake{err: errStore}, errStore},
		"post-check": {&poiFake{v: []places.Poi{{Family: "f2"}}}, features.ErrGateViolation},
	} {
		if _, err := poiService(c.f).Pois(context.Background(), full, places.PoiQuery{Limit: 10}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}
