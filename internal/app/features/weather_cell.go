package features

import (
	"context"
	"errors"
	"slices"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
)

// ErrAreaNotCell means a weather watch named a Land, a Regierungsbezirk or a
// Kreis: an area takes no single warn cell, and the platform refuses it.
var ErrAreaNotCell = errors.New("features: an area takes no single warn cell")

// areaKinds are the place kinds that are an area, not a point.
var areaKinds = []places.Kind{"district", "region", "state"}

// CellAnswer is the warn cell a weather watch resolved to, its warnings, and
// the place a name resolved to on the way.
type CellAnswer struct {
	weather.Cell
	Place *places.Place
	Meta  Answer[struct{}]
}

// WeatherCell answers a weather watch's read for key as the platform keys
// and reads it: a position is the cell it lies in; a target with a digit and
// no space is a cell key as given; a name is the place it names (the first
// the gazetteer answers, when the key may read places), the cell that
// place's point lies in, else a cell held under that name. An area is
// refused with ErrAreaNotCell; no cell with weather.ErrNoCell.
func (s *Service) WeatherCell(ctx context.Context, key access.Record, target string, at *geo.Point, minLevel int) (CellAnswer, error) {
	var out CellAnswer
	q := weather.CellQuery{Cell: target, At: at, MinLevel: minLevel}
	var placeFams []string
	area := false
	if at == nil && !weather.LooksLikeCell(target) {
		p, fams, err := s.placeOf(ctx, key, target)
		if err != nil {
			return out, err
		}
		if p != nil {
			out.Place, placeFams = p, fams
			if area = slices.Contains(areaKinds, p.Kind); !area {
				q.At = &p.Location
			}
		}
	}
	v, meta, err := listed(ctx, s, key, "weather", func(permitted []string) (weather.Cell, []string, error) {
		c, err := s.repos.Weather.CellWarnings(ctx, q, permitted, s.now())
		if errors.Is(err, weather.ErrNoCell) && q.At != nil && at == nil {
			// no cell held at the place: a cell held under the name given
			// stays a key, as the platform keeps it
			q.At = nil
			c, err = s.repos.Weather.CellWarnings(ctx, q, permitted, s.now())
		}
		if errors.Is(err, weather.ErrNoCell) && area {
			err = ErrAreaNotCell
		}
		contributed := slices.Clone(placeFams)
		for _, w := range c.Warnings {
			contributed = append(contributed, w.Family)
		}
		return c, contributed, err
	})
	out.Cell, out.Meta = v, meta
	return out, err
}

// placeOf is the first place the gazetteer answers name with, of the places
// families key may read; nil when it may read none or none answers.
func (s *Service) placeOf(ctx context.Context, key access.Record, name string) (*places.Place, []string, error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return nil, nil, err
	}
	permitted, _, err := gate.Answer(access.Answering(reg, "places"))
	if err != nil || !key.Plan.Has("places") {
		return nil, nil, nil
	}
	ps, _, err := s.repos.Places.Places(ctx, places.Query{Text: name, Limit: 1}, permitted, nil, nil)
	if err != nil || len(ps) == 0 {
		return nil, nil, err
	}
	return &ps[0], []string{ps[0].Family}, nil
}
