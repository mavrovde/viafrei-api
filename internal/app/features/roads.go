package features

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/ampel"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// Roads is the port to the road-conditions store.
type Roads interface {
	// Road returns the road with a canonical reference, or
	// roads.ErrUnknownRoad.
	Road(ctx context.Context, ref string) (roads.Road, error)
	// SituationsOnRoad returns, from one snapshot, up to q.Limit active
	// situations on the road of the roads families, in the platform's
	// motorway status order, and whether there are more; the lamp's view of
	// every situation in the scope (the road and direction, whatever the
	// filters keep); the weather warnings of the weather families in force
	// in an area the road crosses (none read when no weather family is
	// given); the freshness of every family given; the states of the road's
	// junctions, the roads families given that cover their traffic messages,
	// and the newest confirmation of those and of the weather families.
	SituationsOnRoad(ctx context.Context, q roads.CorridorQuery, fams roads.Families, now time.Time) (roads.Page, error)
	// SituationsNear is SituationsOnRoad for the radius of a position, by
	// severity then distance; the warnings are those of an area within the
	// radius, and the states those of the junctions within it, else of the
	// junction nearest the position.
	SituationsNear(ctx context.Context, q roads.AreaQuery, fams roads.Families, now time.Time) (roads.Page, error)
	// Situation returns one situation of the given families that is active
	// or ended after endedSince, or roads.ErrUnknownSituation.
	Situation(ctx context.Context, id string, families []string, endedSince time.Time) (roads.Situation, error)
}

// Conditions is the answer about the situations of a road or an area: the
// page of situations and the lamp over every situation that answers.
type Conditions struct {
	Answer[roads.Situation]
	// Road is the road asked about; nil for an area.
	Road   *roads.Road
	Lamp   roads.Lamp
	Reason *roads.Reason
	// Reasons are every reason of the lamp with its count, Sight the sight
	// of every group the lamp read (roads.Account).
	Reasons []roads.ReasonCount
	Sight   []ampel.SightEntry
	// Covering are, for a road the register does not hold, the roads
	// families read that hold a situation on a road of its class; nil for a
	// registered road or an area.
	Covering []string
	// Read are every family the lamp read, whether or not it contributed
	// (Families names only those): a new version of any of them, or of its
	// confirmation, may change the lamp.
	Read roads.Families
}

// sights are the sight of every group the lamp reads (ampelOf's sight):
// one entry per permitted roads family (missing without a freshness row or
// data, stale past its threshold); one missing entry when a roads family
// the key may not read was left out (partial); the weather warnings' sight,
// fresh only when the read answered (every weather family permitted and
// read) and, for each weather family, a warning is held or the family is
// current by its newest confirmation, missing otherwise; and the traffic
// messages' sight of every state in scope, judged over the roads families
// read that cover it (one missing entry when no state is in scope).
func sights(p roads.Page, fams roads.Families, partial, weatherRead bool, now time.Time) []ampel.SightEntry {
	fresh := map[string]ampel.Sight{}
	for _, f := range p.Sources {
		fresh[f.Source.Family] = roads.SourceSight(f.Assess(now))
	}
	var out []ampel.SightEntry
	for _, f := range fams.Roads {
		s, ok := fresh[f]
		if !ok {
			s = ampel.SightMissing
		}
		out = append(out, ampel.SightEntry{Group: roads.GroupRoads, State: s, Family: f})
	}
	if partial {
		out = append(out, ampel.SightEntry{Group: roads.GroupRoads, State: ampel.SightMissing})
	}
	weather := ampel.SightMissing
	if len(fams.Weather) > 0 {
		weather = ampel.SightFresh
	}
	for _, f := range fams.Weather {
		var c *ampel.Confirmation
		if v, ok := p.Confirmations[f]; ok {
			c = &v
		}
		if s := ampel.WarningSightOf(weatherRead, len(p.Warnings), c, now); s != ampel.SightFresh {
			weather = s
		}
	}
	out = append(out, ampel.SightEntry{Group: ampel.GroupWeather, State: weather})
	return append(out, ampel.MessageSight(p.States, p.Coverage, p.Confirmations, now)...)
}

// conditions reads a page of situations of the permitted roads families and
// the weather warnings of the permitted weather families in one snapshot,
// post-checks and attributes it, and computes the lamp over every situation
// and warning in the scope with the sight of every group read.
//
// An answer over a window ahead (ahead) reads only the families of planned
// works (Settings.PlannedWorks) and no weather warning, as the platform's
// roadworks-ahead answer reads the planning data alone.
func (s *Service) conditions(ctx context.Context, key access.Record, ahead bool, read func(fams roads.Families, now time.Time) (roads.Page, error)) (Conditions, error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return Conditions{}, err
	}
	fams, partial, weatherRead, err := roadFamilies(reg, gate, ahead, s.set.PlannedWorks)
	if err != nil {
		return Conditions{}, err
	}
	permitted, weather := fams.Roads, fams.Weather
	now := s.now()
	p, err := read(fams, now)
	if err != nil {
		return Conditions{}, err
	}
	contributed := make([]string, 0, len(p.Items)+len(p.Warnings))
	for _, v := range p.Items {
		contributed = append(contributed, v.Family)
	}
	for _, w := range p.Warnings {
		contributed = append(contributed, w.Family)
	}
	if err := gate.Check(contributed); err != nil {
		return Conditions{}, err
	}
	// The attribution: the families that contributed a situation, or every
	// permitted roads family when none did (a warning found is no
	// situation: the roads answer is still the roads families' "nothing
	// here"); the families of the warnings found; and every weather family
	// the lamp read, whose warnings (or their absence) the lamp states, as
	// the platform credits the DWD on every road status.
	consulted := slices.Clone(contributed[:len(p.Items)])
	if len(consulted) == 0 {
		if consulted, err = s.consultedIn(ctx, provenance.DatasetRoadEvents, permitted); err != nil {
			return Conditions{}, err
		}
	}
	consulted = append(consulted, contributed[len(p.Items):]...)
	// The weather families whose warnings the lamp reads, not those of road
	// weather (a road-weather station's note and credit belong to an
	// answer that states its readings).
	warned, err := s.consultedIn(ctx, provenance.DatasetWarnings, weather)
	if err != nil {
		return Conditions{}, err
	}
	consulted = append(consulted, warned...)
	// A Land's answer credits the families that placed its situations in
	// the Land, as the platform's Land form credits BKG.
	consulted = append(consulted, p.Placed...)
	slices.Sort(consulted)
	consulted = slices.Compact(consulted)
	attributed, err := s.repos.Register.Provenance(ctx, consulted, s.lang(ctx))
	if err != nil {
		return Conditions{}, err
	}
	acc := roads.AccountOf(p.Messages, warningLevels(p), sights(p, fams, partial, weatherRead, now))
	return Conditions{
		Answer: Answer[roads.Situation]{Items: p.Items, HasMore: p.HasMore, Families: attributed, Partial: partial, At: now,
			ChangesAt: p.ChangesAt(now)},
		Lamp: acc.Lamp, Reason: acc.Reason, Reasons: acc.Reasons, Sight: acc.Sight, Read: fams, Covering: p.Covering,
	}, nil
}

// roadFamilies are the families a roads answer reads for key's gate: the
// roads families it may read (only those of planned works, ahead) and
// whether some were left out (partial), and the weather families its lamp
// reads. The weather warnings are public: the lamp reads every weather family
// the key may read, whether or not its plan holds the weather scope, as the
// platform's road status reads the DWD warnings for every asker. A key that
// may read no weather family reads no warning: the weather group is then
// missing, never an error of the roads question. An answer over a window
// ahead reads none.
func roadFamilies(reg []access.Terms, gate access.Gate, ahead bool, planned []string) (fams roads.Families, partial, weatherRead bool, err error) {
	permitted, partial, err := gate.Answer(access.Answering(reg, "roads"))
	if err != nil {
		return roads.Families{}, false, false, err
	}
	if ahead {
		permitted = slices.DeleteFunc(permitted, func(f string) bool { return !slices.Contains(planned, f) })
	}
	w, wpartial, werr := gate.Answer(access.Answering(reg, "weather"))
	weather, weatherRead := w, werr == nil && !wpartial
	if ahead {
		weather, weatherRead = nil, false
	}
	return roads.Families{Roads: permitted, Weather: weather}, partial, weatherRead, nil
}

// warningLevels are the levels of the page's weather warnings.
func warningLevels(p roads.Page) []int {
	levels := make([]int, len(p.Warnings))
	for i, w := range p.Warnings {
		levels[i] = w.Level
	}
	return levels
}

// MotorwayConditions answers "what is happening on this road" for key
// (FR-ROAD-003, FR-ROAD-004). The road is resolved first. A motorway the
// register does not hold is unknown (roads.ErrUnknownRoad), as the
// platform's roadworks-ahead answer refuses it (G6), unless q.AnyMotorway
// (a watch) reads it as any other unheld road. Any other road it does
// not hold (a federal, Land or city road) is answered as that answer answers
// it: from the situations whose reference names it, with the families that
// hold any situation on a road of its class (Covering), and crediting only
// the families that contributed one, so an empty list credits none.
func (s *Service) MotorwayConditions(ctx context.Context, key access.Record, q roads.CorridorQuery) (Conditions, error) {
	road, err := s.repos.Roads.Road(ctx, q.Road)
	if errors.Is(err, roads.ErrUnknownRoad) && (q.AnyMotorway || roads.ClassOf(q.Road) != roads.ClassMotorway) {
		road, err = roads.Road{Ref: q.Road, Class: roads.ClassOf(q.Road), States: []string{}}, nil
		q.Unregistered, q.Class = true, road.Class
	}
	if err != nil {
		return Conditions{}, err
	}
	q.Road = road.Ref
	c, err := s.conditions(ctx, key, q.Ahead, func(fams roads.Families, now time.Time) (roads.Page, error) {
		return s.repos.Roads.SituationsOnRoad(ctx, q, fams, now)
	})
	if err != nil {
		return Conditions{}, err
	}
	c.Road = &road
	if q.Unregistered {
		if c.Covering == nil {
			c.Covering = []string{}
		}
		if len(c.Items) == 0 {
			c.Families = nil
		}
	}
	return c, nil
}

// RoadConditions answers "what is happening on the roads near here" for key
// (FR-ROAD-006), with the lamp over every situation in the scope and every
// weather warning in force there, as a Land's answer (q.State) has it.
func (s *Service) RoadConditions(ctx context.Context, key access.Record, q roads.AreaQuery) (Conditions, error) {
	return s.conditions(ctx, key, q.Ahead, func(fams roads.Families, now time.Time) (roads.Page, error) {
		return s.repos.Roads.SituationsNear(ctx, q, fams, now)
	})
}

// RoadSituation answers "what is this situation" for key: one situation of
// a permitted roads family, active or ended less than
// Settings.EndedRetention ago; one ended longer ago is not found.
func (s *Service) RoadSituation(ctx context.Context, key access.Record, id string) (Answer[roads.Situation], error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return Answer[roads.Situation]{}, err
	}
	// One situation is either answered or not found: a family the key may
	// not read makes it unknown, never a partial answer.
	permitted, _, err := gate.Answer(access.Answering(reg, "roads"))
	if err != nil {
		return Answer[roads.Situation]{}, err
	}
	now := s.now()
	sit, err := s.repos.Roads.Situation(ctx, id, permitted, now.Add(-s.set.EndedRetention))
	if err != nil {
		return Answer[roads.Situation]{}, err
	}
	if err := gate.Check([]string{sit.Family}); err != nil {
		return Answer[roads.Situation]{}, err
	}
	fams, err := s.repos.Register.Provenance(ctx, []string{sit.Family}, s.lang(ctx))
	if err != nil {
		return Answer[roads.Situation]{}, err
	}
	out := Answer[roads.Situation]{Items: []roads.Situation{sit}, Families: fams, At: now}
	// An ended situation is answered until it leaves the retention.
	if sit.Until != nil {
		gone := sit.Until.Add(s.set.EndedRetention)
		out.ChangesAt = &gone
	}
	return out, nil
}
