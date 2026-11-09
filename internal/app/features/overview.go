package features

import (
	"context"
	"errors"
	"slices"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/parking"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
)

// Part is one part of a composed answer.
type Part string

// The parts of the place overview, in the order they run.
const (
	PartCharging Part = "charging"
	PartParking  Part = "parking"
)

// Parts lists every part in its running order.
var Parts = []Part{PartCharging, PartParking}

// scopeOf is the scope each part needs.
var scopeOf = map[Part]access.Scope{PartCharging: "energy", PartParking: "parking"}

// Status is the outcome of one part (meta.parts).
type Status string

// The part outcomes.
const (
	StatusOK           Status = "ok"
	StatusTimeout      Status = "timeout"
	StatusNotPermitted Status = "not_permitted"
	StatusUnavailable  Status = "unavailable"
	StatusNotRequested Status = "not_requested"
)

// PartResult is the outcome of one part, and the error that caused it when
// it is not ok.
type PartResult struct {
	Part   Part
	Status Status
	Err    error
}

// Overview is the composed answer about a place.
type Overview struct {
	Place    places.Place
	Charging *Answer[energy.Site]
	Parking  *Answer[parking.Site]
	Parts    []PartResult
	// Families are the families of the place and of every part that is ok.
	Families []provenance.Family
	Partial  bool
}

// ErrNoPart means no part of a composed answer is ok; Overview.Parts says
// why each one is not.
var ErrNoPart = errors.New("features: no part of the answer is available")

// ErrScopeDenied is the reason of a part the key's plan has no scope for.
var ErrScopeDenied = errors.New("features: the key's plan does not include the part's scope")

// OverviewQuery asks what is around a place.
type OverviewQuery struct {
	PlaceID string
	// Needs are the parts asked for; empty asks for every part.
	Needs        []Part
	RadiusM      int
	PerPartLimit int
}

// Overview answers "what is around this place" for key (FR-BRIEF-008): the
// place is read from the places store, then each asked part from its own
// store, one after the other in the order of Parts, each under the licence
// gate on its own and within its own sub-deadline. The answer stands when at
// least one part is ok (FR-BRIEF-005).
func (s *Service) Overview(ctx context.Context, key access.Record, q OverviewQuery) (Overview, error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return Overview{}, err
	}
	permitted, _, err := gate.Answer(access.Answering(reg, "places"))
	if err != nil {
		return Overview{}, err
	}
	place, err := s.repos.Places.Place(ctx, q.PlaceID, permitted)
	if err != nil {
		return Overview{}, err
	}
	if err := gate.Check([]string{place.Family}); err != nil {
		return Overview{}, err
	}
	out := Overview{Place: place}
	families := []string{place.Family}
	ok := false
	for _, part := range Parts {
		res := PartResult{Part: part, Status: StatusNotRequested}
		if len(q.Needs) == 0 || slices.Contains(q.Needs, part) {
			fams, partial, err := s.runPart(ctx, key, part, place, q, &out)
			res.Status, res.Err = s.status(err), err
			if errors.Is(err, ErrGateViolation) {
				return Overview{}, err
			}
			if err == nil {
				ok = true
				out.Partial = out.Partial || partial
				for _, f := range fams {
					families = append(families, f.Source.Family)
				}
			}
		}
		out.Parts = append(out.Parts, res)
	}
	if !ok {
		return out, ErrNoPart
	}
	slices.Sort(families)
	out.Families, err = s.repos.Register.Provenance(ctx, slices.Compact(families), s.set.Language)
	return out, err
}

// runPart runs one part within its sub-deadline and stores its answer in out.
func (s *Service) runPart(ctx context.Context, key access.Record, part Part, place places.Place, q OverviewQuery, out *Overview) ([]provenance.Family, bool, error) {
	if !key.Plan.Has(scopeOf[part]) {
		return nil, false, ErrScopeDenied
	}
	pctx, cancel := context.WithTimeout(ctx, s.set.PartDeadline)
	defer cancel()
	if part == PartCharging {
		a, err := s.Charging(pctx, key, energy.Query{At: place.Location, RadiusM: q.RadiusM, Access: energy.FilterPublic, Limit: q.PerPartLimit})
		if err != nil {
			return nil, false, err
		}
		out.Charging = &a
		return a.Families, a.Partial, nil
	}
	a, err := s.Parking(pctx, key, parking.Query{At: place.Location, RadiusM: q.RadiusM, Limit: q.PerPartLimit})
	if err != nil {
		return nil, false, err
	}
	out.Parking = &a
	return a.Families, a.Partial, nil
}

// status is the outcome a part's error stands for.
func (s *Service) status(err error) Status {
	switch {
	case err == nil:
		return StatusOK
	case errors.Is(err, ErrScopeDenied), errors.Is(err, access.ErrNonePermitted), errors.Is(err, access.ErrTermsNotAccepted):
		return StatusNotPermitted
	case errors.Is(err, context.DeadlineExceeded), s.set.IsTimeout != nil && s.set.IsTimeout(err):
		return StatusTimeout
	default:
		return StatusUnavailable
	}
}
