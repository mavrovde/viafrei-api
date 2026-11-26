package features

import (
	"context"
	"slices"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
	"github.com/mavrovde/viafrei-api/internal/domain/weather"
)

// States is the port of a Land page's later sections.
type States interface {
	// StateWarnings returns the warnings of the families in force at now in
	// the Land, one per event, worst first, then by event.
	StateWarnings(ctx context.Context, land string, families []string, now time.Time) ([]weather.StateWarning, error)
	// StateEventsEnglish returns the issuer's English name of each event
	// the Land's warnings in force at now name, by its German name, and the
	// family the names come from; none while that family is not among the
	// families.
	StateEventsEnglish(ctx context.Context, land string, families []string, now time.Time) (map[string]string, string, error)
	// StateAlerts returns the alert rows of the families' messages in force
	// at now that name a stop in the Land.
	StateAlerts(ctx context.Context, land string, families []string, now time.Time) ([]transit.StateAlertRow, error)
}

// StateWarnings answers "which official warnings are in force in this Land"
// as the platform's Land page lists them (domain weather). An answer in
// English names each event by the issuer's English name where it issued
// one, credited to the family it comes from; the German name otherwise.
func (s *Service) StateWarnings(ctx context.Context, key access.Record, land string) ([]weather.StateWarning, Answer[struct{}], error) {
	return listed(ctx, s, key, "weather", provenance.DatasetWarnings, func(permitted []string) ([]weather.StateWarning, []string, error) {
		v, err := s.repos.States.StateWarnings(ctx, land, permitted, s.now())
		if err != nil {
			return nil, nil, err
		}
		var contributed []string
		for _, w := range v {
			contributed = append(contributed, w.Families...)
		}
		if s.lang(ctx) != "en" || !slices.ContainsFunc(v, func(w weather.StateWarning) bool { return w.Event != nil }) {
			return v, contributed, nil
		}
		names, family, err := s.repos.States.StateEventsEnglish(ctx, land, permitted, s.now())
		if err != nil {
			return nil, nil, err
		}
		for i, w := range v {
			if w.Event == nil {
				continue
			}
			if en, ok := names[*w.Event]; ok {
				v[i].Event = &en
				v[i].Families = append(slices.Clone(w.Families), family)
				contributed = append(contributed, family)
			}
		}
		return v, contributed, nil
	})
}

// StateAlertsAnswer is the answer to "which service alerts are in force in
// this Land": at most the asked number of messages, and how many there are.
type StateAlertsAnswer struct {
	Alerts []transit.StateAlert
	Total  int
	Meta   Answer[struct{}]
}

// StateAlerts answers "which service alerts are in force in this Land" as
// the platform's Land page tells them (domain transit): one per message, the
// messages naming most lines first; at most limit.
func (s *Service) StateAlerts(ctx context.Context, key access.Record, land string, limit int) (StateAlertsAnswer, error) {
	total := 0
	v, meta, err := listed(ctx, s, key, "transit", provenance.DatasetAlerts, func(permitted []string) ([]transit.StateAlert, []string, error) {
		rows, err := s.repos.States.StateAlerts(ctx, land, permitted, s.now())
		v := transit.StateAlertsOf(rows, land)
		total = len(v)
		v = v[:min(limit, len(v))]
		var contributed []string
		for _, a := range v {
			contributed = append(contributed, a.CarriedBy...)
		}
		return v, contributed, err
	})
	return StateAlertsAnswer{Alerts: v, Total: total, Meta: meta}, err
}
