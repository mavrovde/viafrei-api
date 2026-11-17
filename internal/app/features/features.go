// Package features holds the feature use cases: the questions a client asks
// (where to charge near here, where to park, what is around a place), each
// answered under the licence gate from the repositories behind its ports.
package features

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
)

// Register is the port to the source register.
type Register interface {
	// Register returns every family's licence terms.
	Register(ctx context.Context) ([]access.Terms, error)
	// Provenance returns the attribution, notes in lang and freshness of
	// the given families only.
	Provenance(ctx context.Context, families []string, lang string) ([]provenance.Family, error)
	// TermsTexts returns the texts of the given terms gates, every
	// language.
	TermsTexts(ctx context.Context, gates []string) ([]access.TermsText, error)
}

// Charging is the port to the charging store.
type Charging interface {
	// ChargingNear returns up to q.Limit sites of the given families, by
	// distance then id, whether there are more, and the earliest
	// availability report of their points dated after now, if any.
	// Availability reports are read from the given families only and never
	// after now.
	ChargingNear(ctx context.Context, q energy.Query, families []string, now time.Time) ([]energy.Site, bool, *time.Time, error)
}

// Parking is the port to the parking store.
type Parking interface {
	// ParkingNear returns up to q.Limit sites of the listed families, by
	// distance then id, whether there are more, and the earliest occupancy
	// report dated after now of any listed site within the radius, before
	// the free-space filter, if any. Occupancy is read from the occupancy
	// families only and never after now.
	ParkingNear(ctx context.Context, q parking.Query, listed, occupancy []string, now time.Time) ([]parking.Site, bool, *time.Time, error)
}

// Places is the port to the places store.
type Places interface {
	// Place returns the place of one of the given families, or
	// places.ErrNotFound.
	Place(ctx context.Context, id string, families []string) (places.Place, error)
	// Places returns the places of the given families that answer q, best
	// match first, and whether more answer.
	Places(ctx context.Context, q places.Query, families []string) ([]places.Place, bool, error)
}

// Repositories are the ports a Service reads through.
type Repositories struct {
	Register    Register
	Charging    Charging
	Parking     Parking
	Places      Places
	Roads       Roads
	Weather     Weather
	Rules       Rules
	Transit     Transit
	Fuel        Fuel
	Hydrogen    Hydrogen
	Speed       Speed
	Sharing     Sharing
	Addresses   Addresses
	Pois        Pois
	Locations   Locations
	Disruptions Disruptions
	Nearby      Nearby
	Lorry       Lorry
}

// Settings are the tunables of the use cases.
type Settings struct {
	// Language is the language of the texts of every answer.
	Language string
	// PartDeadline bounds each part of a composed answer.
	PartDeadline time.Duration
	// EndedRetention is how long an ended road situation still answers.
	EndedRetention time.Duration
	// MaxWarnings is the most warnings a weather answer lists.
	MaxWarnings int
	// Disabled are the families the licence gate always denies.
	Disabled []string
	// IsTimeout reports whether a repository error means the time ran out.
	IsTimeout func(error) bool
}

// Service answers the feature questions.
type Service struct {
	repos Repositories
	set   Settings
	now   func() time.Time
}

// New returns the service.
func New(repos Repositories, set Settings, now func() time.Time) *Service {
	return &Service{repos: repos, set: set, now: now}
}

// Answer is a gated list answer.
type Answer[T any] struct {
	Items   []T
	HasMore bool
	// Families are the families that contributed, or the families that were
	// consulted when nothing was found (ENV-003).
	Families []provenance.Family
	// Partial is set when a family that could answer was withheld.
	Partial bool
	// Withheld are the families listed with their gated data withheld (a
	// parking family whose terms are not accepted). Their conditions are
	// withheld with that data: Families carries none of them, and a composed
	// answer that reads the families again must drop them too.
	Withheld []string
	// Terms are the texts of the terms the withheld families need accepted,
	// verbatim, for the caller to show its user before it accepts them.
	Terms []access.TermsText
	// At is the instant the answer is computed at.
	At time.Time
	// NextReport is the earliest report the read considered that is dated
	// after At, if any: the answer may change when it takes effect.
	NextReport *time.Time
	// ChangesAt is the earliest instant after At at which the answer changes
	// with the clock alone besides its families turning stale: a situation
	// starting or ending, a confirmation lapsing, an ended situation leaving
	// the retention. Nil when nothing else does.
	ChangesAt *time.Time
	// Dropped counts the sites an only-available charging answer left out.
	Dropped *energy.Dropped
}

// ErrGateViolation wraps access.ErrForbiddenItem: storage returned an item
// of a family outside the allowed set and the answer must be aborted.
var ErrGateViolation = access.ErrForbiddenItem

func (s *Service) gate(ctx context.Context, key access.Record) ([]access.Terms, access.Gate, error) {
	reg, err := s.repos.Register.Register(ctx)
	if err != nil {
		return nil, access.Gate{}, err
	}
	return reg, access.NewGate(reg, key.Plan, key.Audience, access.AcceptedFrom(ctx), s.set.Disabled), nil
}

// checkAll is the post-check over a set: every family must be in allowed.
func checkAll(families, allowed []string) error {
	for _, f := range families {
		if !slices.Contains(allowed, f) {
			return ErrGateViolation
		}
	}
	return nil
}

// provenanceOf reads the families that contributed, or the consulted ones
// when none did.
func (s *Service) provenanceOf(ctx context.Context, contributed, consulted []string) ([]provenance.Family, error) {
	fams := contributed
	if len(fams) == 0 {
		fams = consulted
	}
	fams = slices.Clone(fams)
	slices.Sort(fams)
	return s.repos.Register.Provenance(ctx, slices.Compact(fams), s.set.Language)
}

// sight reads every permitted family's provenance once: for a lamp, whether
// one is missing (no provenance row) or stale, which limits the sight, and
// the earliest instant after now at which one that is fresh turns stale (nil
// when none is fresh); for the attribution, those that contributed, or all of
// them when none did. The gate's post-check keeps contributed inside
// permitted.
func (s *Service) sight(ctx context.Context, permitted, contributed []string, now time.Time) ([]provenance.Family, bool, *time.Time, error) {
	consulted := slices.Clone(permitted)
	slices.Sort(consulted)
	consulted = slices.Compact(consulted)
	all, err := s.repos.Register.Provenance(ctx, consulted, s.set.Language)
	if err != nil {
		return nil, false, nil, err
	}
	stale, fams := len(all) < len(consulted), []provenance.Family{}
	var turns *time.Time
	for _, f := range all {
		if f.Assess(now).Stale {
			stale = true
		} else if t, ok := f.TurnsStale(); ok && (turns == nil || t.Before(*turns)) {
			turns = &t
		}
		if len(contributed) == 0 || slices.Contains(contributed, f.Source.Family) {
			fams = append(fams, f)
		}
	}
	return fams, stale, turns, nil
}

// withholdConditions drops the condition notes of the withheld families. A
// condition note is stated only for a family whose data the answer shows
// under it: a pending family's occupancy is withheld, so its conditions are
// too, as the MCP capability does (docs/parity.md).
func withholdConditions(fams []provenance.Family, withheld []string) []provenance.Family {
	for i, f := range fams {
		if slices.Contains(withheld, f.Source.Family) {
			fams[i] = f.Without(provenance.Condition)
		}
	}
	return fams
}

// Charging answers "where can I charge near here" for key (FR-ENERGY).
func (s *Service) Charging(ctx context.Context, key access.Record, q energy.Query) (Answer[energy.Site], error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return Answer[energy.Site]{}, err
	}
	return s.charging(ctx, reg, gate, q)
}

// charging answers Charging under a register and gate already read.
func (s *Service) charging(ctx context.Context, reg []access.Terms, gate access.Gate, q energy.Query) (Answer[energy.Site], error) {
	permitted, partial, err := gate.Answer(access.Answering(reg, "energy"))
	if err != nil {
		return Answer[energy.Site]{}, err
	}
	now := s.now()
	read := q
	if q.Filter.OnlyAvailable {
		// The platform examines its nearest sites and keeps the free ones.
		read.Limit = energy.OnlyAvailableScan
	}
	sites, more, next, err := s.repos.Charging.ChargingNear(ctx, read, permitted, now)
	if err != nil {
		return Answer[energy.Site]{}, err
	}
	contributed := siteFamilies(sites)
	if err := gate.Check(contributed); err != nil {
		return Answer[energy.Site]{}, err
	}
	fams, err := s.provenanceOf(ctx, contributed, permitted)
	if err != nil {
		return Answer[energy.Site]{}, err
	}
	out := Answer[energy.Site]{Items: sites, HasMore: more, Families: fams, Partial: partial, At: now, NextReport: next}
	if q.Filter.OnlyAvailable {
		onlyAvailable(&out, q)
	}
	return out, nil
}

func siteFamilies(sites []energy.Site) []string {
	var out []string
	for _, site := range sites {
		out = append(out, site.Families()...)
	}
	return out
}

// StatusLimits is how old each family's charge point report may be and still
// count: the status age limit, or the family's own threshold when shorter
// (energy.StatusLimit); a family with no fixed interval has the limit alone.
func StatusLimits(maxAge time.Duration, fams []provenance.Family) func(family string) time.Duration {
	thresholds := map[string]time.Duration{}
	for _, f := range fams {
		thresholds[f.Source.Family] = f.StaleAfter
		if f.NeverStale {
			thresholds[f.Source.Family] = maxAge
		}
	}
	return func(family string) time.Duration { return energy.StatusLimit(maxAge, thresholds[family]) }
}

// onlyAvailable keeps the examined sites with a point reported free now,
// at most q.Limit, counts the others by reason, and credits the families of
// the kept sites only.
func onlyAvailable(a *Answer[energy.Site], q energy.Query) {
	limit := StatusLimits(q.Filter.StatusMaxAge, a.Families)
	kept := []energy.Site{}
	d := energy.Dropped{}
	for _, site := range a.Items {
		switch site.FreeNow(a.At, limit) {
		case energy.Free:
			kept = append(kept, site)
		case energy.NotFree:
			d.NotFree++
		case energy.Stale:
			d.Stale++
		default:
			d.NoStatus++
		}
	}
	a.HasMore = len(kept) > q.Limit
	a.Items = kept[:min(len(kept), q.Limit)]
	a.Dropped = &d
	if len(a.Items) > 0 {
		credited := siteFamilies(a.Items)
		a.Families = slices.DeleteFunc(a.Families, func(f provenance.Family) bool { return !slices.Contains(credited, f.Source.Family) })
	}
}

// Parking answers "where can I park near here" for key (FR-PARK). A family
// refused only because its terms are not accepted is listed without its
// occupancy and marked; the answer is then partial (SEC-030), also when every
// family that could answer is such a family.
func (s *Service) Parking(ctx context.Context, key access.Record, q parking.Query) (Answer[parking.Site], error) {
	reg, gate, err := s.gate(ctx, key)
	if err == nil {
		err = s.acceptance(ctx, reg)
	}
	if err != nil {
		return Answer[parking.Site]{}, err
	}
	return s.parking(ctx, reg, gate, q)
}

// TermsRefusal refuses a request that accepted terms which are not the
// current terms of a family that needs accepting; it carries the current
// texts (access.ErrTermsVersion).
type TermsRefusal struct {
	Texts []access.TermsText
}

func (e *TermsRefusal) Error() string { return access.ErrTermsVersion.Error() }

// Unwrap makes errors.Is(err, access.ErrTermsVersion) hold.
func (e *TermsRefusal) Unwrap() error { return access.ErrTermsVersion }

// acceptance checks the terms the request accepted (accept_terms) against
// the current terms of the register, in an operation whose data they gate:
// a wrong or old version is refused with the current texts.
func (s *Service) acceptance(ctx context.Context, reg []access.Terms) error {
	err := access.CheckAcceptance(reg, access.AcceptedFrom(ctx))
	var tv *access.TermsVersionError
	if !errors.As(err, &tv) {
		return nil
	}
	texts, terr := s.repos.Register.TermsTexts(ctx, tv.Current)
	if terr != nil {
		return terr
	}
	return &TermsRefusal{Texts: texts}
}

// termsOf returns the texts of the terms the pending families need.
func (s *Service) termsOf(ctx context.Context, reg []access.Terms, pending []string) ([]access.TermsText, error) {
	var gates []string
	for _, t := range reg {
		if slices.Contains(pending, t.Family) && !slices.Contains(gates, t.Version) {
			gates = append(gates, t.Version)
		}
	}
	if len(gates) == 0 {
		return nil, nil
	}
	slices.Sort(gates)
	return s.repos.Register.TermsTexts(ctx, gates)
}

// parking answers Parking under a register and gate already read.
func (s *Service) parking(ctx context.Context, reg []access.Terms, gate access.Gate, q parking.Query) (Answer[parking.Site], error) {
	answering := access.Answering(reg, "parking")
	pending := gate.Pending(answering)
	permitted, partial, err := gate.Answer(answering)
	if err != nil {
		if len(pending) == 0 {
			return Answer[parking.Site]{}, err
		}
		// Nothing is permitted, but the pending families are listed
		// without their occupancy.
		permitted, partial = nil, true
	}
	listed := slices.Sorted(slices.Values(append(slices.Clone(permitted), pending...)))
	now := s.now()
	sites, more, next, err := s.repos.Parking.ParkingNear(ctx, q, listed, permitted, now)
	if err != nil {
		return Answer[parking.Site]{}, err
	}
	var contributed, occupancy []string
	for i, site := range sites {
		contributed = append(contributed, site.Family)
		if slices.Contains(pending, site.Family) {
			sites[i] = site.Withheld()
			continue
		}
		if site.Occupancy != nil {
			// The report may come from another family than the site's: it is
			// checked and attributed as its own family.
			occupancy = append(occupancy, site.Occupancy.Family)
			contributed = append(contributed, site.Occupancy.Family)
		}
	}
	if err := errors.Join(checkAll(contributed, listed), gate.Check(occupancy)); err != nil {
		return Answer[parking.Site]{}, ErrGateViolation
	}
	fams, err := s.provenanceOf(ctx, contributed, listed)
	if err != nil {
		return Answer[parking.Site]{}, err
	}
	terms, err := s.termsOf(ctx, reg, pending)
	if err != nil {
		return Answer[parking.Site]{}, err
	}
	return Answer[parking.Site]{Items: sites, HasMore: more, Families: withholdConditions(fams, pending), Partial: partial, Withheld: pending, Terms: terms, At: now, NextReport: next}, nil
}
