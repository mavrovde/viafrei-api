package features

import (
	"context"
	"time"
	_ "time/tzdata" // the Berlin day never depends on the host

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// Speed is the speed-check port.
type Speed interface {
	// SpeedCameras returns the official locations and OpenStreetMap's of the
	// given families in q's circle, each block at most q.Limit long with
	// its count before the limit.
	SpeedCameras(ctx context.Context, q roads.SpeedQuery, families []string) (roads.SpeedCameras, error)
}

// SpeedAnswer is the answer to a speed-check question.
type SpeedAnswer struct {
	roads.SpeedCameras
	Meta Answer[struct{}]
	// Notice is the § 23 Abs. 1c StVO notice every answer carries as a
	// condition, listed or not, in the answers' language.
	Notice string
}

var berlin, _ = time.LoadLocation("Europe/Berlin")

// SpeedCameras answers "where are speed cameras around here" for key as the
// platform's speed-camera search answers it, on the Europe/Berlin day of
// the answer's instant.
func (s *Service) SpeedCameras(ctx context.Context, key access.Record, q roads.SpeedQuery) (SpeedAnswer, error) {
	n := s.now().In(berlin)
	q.Today = time.Date(n.Year(), n.Month(), n.Day(), 0, 0, 0, 0, time.UTC)
	v, meta, err := listed(ctx, s, key, "roads", func(permitted []string) (roads.SpeedCameras, []string, error) {
		v, err := s.repos.Speed.SpeedCameras(ctx, q, permitted)
		var contributed []string
		for _, c := range append(v.Official, v.OSM...) {
			contributed = append(contributed, c.Family)
		}
		return v, contributed, err
	})
	if err != nil {
		return SpeedAnswer{}, err
	}
	if len(v.Official)+len(v.OSM) == 0 {
		// The platform's speed-camera search credits the registers whose
		// rows it lists: an answer listing none credits none.
		meta.Families = nil
	}
	return SpeedAnswer{SpeedCameras: v, Meta: meta, Notice: roads.SpeedNotice(s.lang(ctx))}, nil
}
