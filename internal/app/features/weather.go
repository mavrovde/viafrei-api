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
	// WarningsAt returns every warning of the given families in force at
	// now whose area contains q.At, at or above q.MinLevel, of the asked
	// hazards: level descending, then start, then id.
	WarningsAt(ctx context.Context, q weather.Query, families []string, now time.Time) ([]weather.Warning, error)
}

// Rules is the port to the driving-rules store.
type Rules interface {
	// Topic returns one rule topic with its rules in order, or
	// rules.ErrUnknownTopic.
	Topic(ctx context.Context, code string) (rules.Topic, error)
}

// WeatherRisk is the answer about the warnings at a position.
type WeatherRisk struct {
	Answer[weather.Warning]
	Lamp weather.Lamp
}

// WeatherRisk answers "is there a weather warning here" for key
// (FR-WEATHER-001).
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
	ws, err := s.repos.Weather.WarningsAt(ctx, q, permitted, now)
	if err != nil {
		return WeatherRisk{}, err
	}
	contributed := make([]string, len(ws))
	for i, w := range ws {
		contributed[i] = w.Family
	}
	if err := gate.Check(contributed); err != nil {
		return WeatherRisk{}, err
	}
	fams, err := s.provenanceOf(ctx, contributed, permitted)
	if err != nil {
		return WeatherRisk{}, err
	}
	return WeatherRisk{
		Answer: Answer[weather.Warning]{Items: ws, Families: fams, Partial: partial, At: now},
		Lamp:   weather.LampOf(ws),
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
	topic, err := s.repos.Rules.Topic(ctx, code)
	if err != nil {
		return rules.Topic{}, Answer[rules.Rule]{}, err
	}
	fams, err := s.provenanceOf(ctx, nil, permitted)
	if err != nil {
		return rules.Topic{}, Answer[rules.Rule]{}, err
	}
	return topic, Answer[rules.Rule]{Items: topic.Rules, Families: fams, Partial: partial, At: s.now()}, nil
}
