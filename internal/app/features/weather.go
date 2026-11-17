package features

import (
	"context"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/rules"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
)

// Weather is the port to the weather-warning store.
type Weather interface {
	// WarningsAt returns, from one snapshot, the warnings of the given
	// families in force at now whose area contains q.At, at or above
	// q.MinLevel, of the asked hazards: level descending, then start, then
	// id; at most q.Limit, and whether more answer. It also
	// returns the level of every warning of the families in force at q.At,
	// whatever the floor and the hazards keep, for the lamp, and the earliest
	// instant after now at which any warning of the families placed at q.At
	// starts or ends.
	WarningsAt(ctx context.Context, q weather.Query, families []string, now time.Time) (weather.Scope, error)
	// CellWarnings resolves q to its warn cell and returns the cell's
	// warnings of the given families in force at now, at or above
	// q.MinLevel, worst first; weather.ErrNoCell when no cell answers.
	CellWarnings(ctx context.Context, q weather.CellQuery, families []string, now time.Time) (weather.Cell, error)
}

// Rules is the port to the driving-rules store.
type Rules interface {
	// Topic returns one rule topic with its rules in order, or
	// rules.ErrUnknownTopic.
	Topic(ctx context.Context, code, lang string) (rules.Topic, error)
	// Zones reads the low-emission-zone rows at a point or of a city.
	Zones(ctx context.Context, q rules.ZoneQuery, families []string, now time.Time) ([]rules.Zone, error)
	ZonePlaques
}

// WeatherRisk is the answer about the warnings at a position.
type WeatherRisk struct {
	Answer[weather.Warning]
	Lamp weather.Lamp
	// Read are every family the lamp read, whether or not it contributed
	// (Families names only those): a new version of any of them may change
	// the lamp.
	Read []string
	// WarningChange is the earliest instant after At at which a warning
	// placed at the position starts or ends: the answer is wrong from then
	// on, so it is never served past it, not even stale. Nil when none
	// starts or ends.
	WarningChange *time.Time
}

// WeatherRisk answers "is there a weather warning here" for key
// (FR-WEATHER-001). The lamp reads every warning in force at the position,
// whatever the floor and the hazards keep, and the freshness of every
// permitted weather family.
func (s *Service) WeatherRisk(ctx context.Context, key access.Record, q weather.Query) (WeatherRisk, error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return WeatherRisk{}, err
	}
	permitted, partial, err := gate.Answer(access.Answering(reg, "weather"))
	if err != nil {
		return WeatherRisk{}, err
	}
	now := s.now()
	q.Limit = s.set.MaxWarnings
	sc, err := s.repos.Weather.WarningsAt(ctx, q, permitted, now)
	if err != nil {
		return WeatherRisk{}, err
	}
	contributed := make([]string, len(sc.Warnings))
	for i, w := range sc.Warnings {
		contributed[i] = w.Family
	}
	if err := gate.Check(contributed); err != nil {
		return WeatherRisk{}, err
	}
	fams, stale, turns, err := s.sight(ctx, permitted, contributed, now)
	if err != nil {
		return WeatherRisk{}, err
	}
	// The answer changes with the clock alone when a warning starts or ends,
	// or when a family the lamp read turns stale.
	changes := sc.NextChange
	if turns != nil && (changes == nil || turns.Before(*changes)) {
		changes = turns
	}
	return WeatherRisk{
		Answer: Answer[weather.Warning]{Items: sc.Warnings, HasMore: sc.HasMore, Families: fams, Partial: partial, At: now,
			ChangesAt: changes},
		Lamp: weather.LampOf(sc.Levels, partial, stale), Read: permitted, WarningChange: sc.NextChange,
	}, nil
}

// DrivingRules answers "what are the rules on this topic" for key. The
// rules are curated content carried by the permitted rules families, which
// the answer is attributed to.
func (s *Service) DrivingRules(ctx context.Context, key access.Record, code string) (rules.Topic, Answer[rules.Rule], error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return rules.Topic{}, Answer[rules.Rule]{}, err
	}
	permitted, partial, err := gate.Answer(access.Answering(reg, "rules"))
	if err != nil {
		return rules.Topic{}, Answer[rules.Rule]{}, err
	}
	topic, err := s.repos.Rules.Topic(ctx, code, s.set.Language)
	if err != nil {
		return rules.Topic{}, Answer[rules.Rule]{}, err
	}
	// Rule topics carry no source of their own: the curated content is
	// published under every rules family, so the answer is attributed to
	// each family the key may read (nil contributed means all permitted).
	fams, err := s.provenanceOf(ctx, nil, permitted)
	if err != nil {
		return rules.Topic{}, Answer[rules.Rule]{}, err
	}
	return topic, Answer[rules.Rule]{Items: topic.Rules, Families: fams, Partial: partial, At: s.now()}, nil
}

// ZonesAnswer is the zone part of a rules answer: the rows read and, for a
// point, the platform's verdict and what its answer states beside it.
type ZonesAnswer struct {
	Zones   []rules.Zone
	Verdict *rules.Verdict
	Answer  *rules.ZoneAnswer
	Meta    Answer[struct{}]
}

// LowEmissionZones answers "am I in a low-emission zone" for a point, or
// "which zone does this city publish", from the zone sources the key may read
// (domain roads) that are alive now.
func (s *Service) LowEmissionZones(ctx context.Context, key access.Record, q rules.ZoneQuery) (ZonesAnswer, error) {
	v, meta, err := listed(ctx, s, key, "roads", func(permitted []string) ([]rules.Zone, []string, error) {
		zs, err := s.repos.Rules.Zones(ctx, q, permitted, s.now())
		var fams []string
		for _, z := range zs {
			fams = append(fams, z.Family)
		}
		return zs, fams, err
	})
	out := ZonesAnswer{Zones: v, Meta: meta}
	if q.At == nil || err != nil {
		return out, err
	}
	vd := rules.VerdictOf(v)
	out.Verdict = &vd
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return ZonesAnswer{}, err
	}
	a, extra, err := s.zoneAnswer(ctx, reg, gate, vd, q.At, nil)
	if err != nil {
		return ZonesAnswer{}, err
	}
	out.Answer = &a
	if len(extra) > 0 {
		fams, err := s.creditsOf(ctx, extra)
		if err != nil {
			return ZonesAnswer{}, err
		}
		out.Meta.Families = append(out.Meta.Families, fams...)
	}
	return out, nil
}
