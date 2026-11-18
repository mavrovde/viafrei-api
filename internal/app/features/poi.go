package features

import (
	"context"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// Pois is the places-of-interest port.
type Pois interface {
	// Pois returns, best match first, up to places.PoiReach+1 places of
	// interest of the families that q asks for.
	Pois(ctx context.Context, q places.PoiQuery, families []string) ([]places.Poi, error)
}

// PoiAnswer is one page of a places-of-interest search.
type PoiAnswer struct {
	Pois []places.Poi
	// Total is how many matched, at most places.PoiReach.
	Total int
	// Beyond is set when more than places.PoiReach matched.
	Beyond bool
	Meta   Answer[struct{}]
}

// Pois answers "where is the X in Y" for key as the platform's search for a
// place of interest pages it: q.Limit matches from q.Offset.
func (s *Service) Pois(ctx context.Context, key access.Record, q places.PoiQuery) (PoiAnswer, error) {
	v, meta, err := listedOwn(ctx, s, key, "places", func(permitted []string) (PoiAnswer, []string, error) {
		all, err := s.repos.Pois.Pois(ctx, q, permitted)
		if err != nil {
			return PoiAnswer{}, nil, err
		}
		a := PoiAnswer{Beyond: len(all) > places.PoiReach}
		all = all[:min(len(all), places.PoiReach)]
		a.Total = len(all)
		a.Pois = all[min(q.Offset, len(all)):min(q.Offset+q.Limit, len(all))]
		var contributed []string
		for _, p := range a.Pois {
			contributed = append(contributed, p.Family)
		}
		return a, contributed, nil
	})
	v.Meta = meta
	return v, err
}
