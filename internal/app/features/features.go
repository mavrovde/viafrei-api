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
}

// Charging is the port to the charging store.
type Charging interface {
	// ChargingNear returns up to q.Limit sites of the given families, by
	// distance then id, and whether there are more. Availability reports are
	// read from the given families only and never after now.
	ChargingNear(ctx context.Context, q energy.Query, families []string, now time.Time) ([]energy.Site, bool, error)
}

// Parking is the port to the parking store.
type Parking interface {
	// ParkingNear returns up to q.Limit sites of the listed families, by
	// distance then id, and whether there are more. Occupancy is read from
	// the occupancy families only and never after now.
	ParkingNear(ctx context.Context, q parking.Query, listed, occupancy []string, now time.Time) ([]parking.Site, bool, error)
}

// Places is the port to the places store.
type Places interface {
	// Place returns the place of one of the given families, or
	// places.ErrNotFound.
	Place(ctx context.Context, id string, families []string) (places.Place, error)
}

// Repositories are the ports a Service reads through.
type Repositories struct {
	Register Register
	Charging Charging
	Parking  Parking
	Places   Places
	Roads    Roads
	Weather  Weather
	Rules    Rules
}

// Settings are the tunables of the use cases.
type Settings struct {
	// Language is the language of the texts of every answer.
	Language string
	// PartDeadline bounds each part of a composed answer.
	PartDeadline time.Duration
	// EndedRetention is how long an ended road situation still answers.
	EndedRetention time.Duration
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
	// At is the instant the answer is computed at.
	At time.Time
}

// ErrGateViolation wraps access.ErrForbiddenItem: storage returned an item
// of a family outside the allowed set and the answer must be aborted.
var ErrGateViolation = access.ErrForbiddenItem

func (s *Service) gate(ctx context.Context, key access.Record) ([]access.Terms, access.Gate, error) {
	reg, err := s.repos.Register.Register(ctx)
	if err != nil {
		return nil, access.Gate{}, err
	}
	return reg, access.NewGate(reg, key.Plan, key.Accepted, s.set.Disabled), nil
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
	sites, more, err := s.repos.Charging.ChargingNear(ctx, q, permitted, now)
	if err != nil {
		return Answer[energy.Site]{}, err
	}
	var contributed []string
	for _, site := range sites {
		contributed = append(contributed, site.Families()...)
	}
	if err := gate.Check(contributed); err != nil {
		return Answer[energy.Site]{}, err
	}
	fams, err := s.provenanceOf(ctx, contributed, permitted)
	if err != nil {
		return Answer[energy.Site]{}, err
	}
	return Answer[energy.Site]{Items: sites, HasMore: more, Families: fams, Partial: partial, At: now}, nil
}

// Parking answers "where can I park near here" for key (FR-PARK). A family
// refused only because its terms are not accepted is listed without its
// occupancy and marked; the answer is then partial (SEC-030), also when every
// family that could answer is such a family.
func (s *Service) Parking(ctx context.Context, key access.Record, q parking.Query) (Answer[parking.Site], error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return Answer[parking.Site]{}, err
	}
	return s.parking(ctx, reg, gate, q)
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
	sites, more, err := s.repos.Parking.ParkingNear(ctx, q, listed, permitted, now)
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
	return Answer[parking.Site]{Items: sites, HasMore: more, Families: fams, Partial: partial, At: now}, nil
}
