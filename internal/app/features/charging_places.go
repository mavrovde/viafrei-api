package features

import (
	"context"
	"slices"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// ChargingPlaces is the port to the place-grouped charging read: the
// charging source register, the platform's site read and run ledger, and the
// address reads of the register's rows.
type ChargingPlaces interface {
	// ChargingRegister reads the charging source register of the families.
	ChargingRegister(ctx context.Context, families []string) (energy.SourceRegister, error)
	// ChargingPlaceRecords reads the site records under the register.
	ChargingPlaceRecords(ctx context.Context, q energy.PlaceRead, reg energy.SourceRegister, now time.Time) ([]energy.PlaceRecord, error)
	// ChargingLiveness reads the run ledger of the register's status sources.
	ChargingLiveness(ctx context.Context, reg energy.SourceRegister, now time.Time) ([]energy.SourceLiveness, error)
	// ChargingAtTownPoint tells which records lie at their town's point.
	ChargingAtTownPoint(ctx context.Context, recs []energy.PlaceRecord) ([]int, error)
	// ChargingAddresses reads the addresses of a record's address.
	ChargingAddresses(ctx context.Context, rec energy.PlaceRecord) ([]energy.AddressHit, error)
	// ChargingRegisterElsewhere reads which register records were filed
	// under an address elsewhere.
	ChargingRegisterElsewhere(ctx context.Context, recs []energy.PlaceRecord, reg energy.SourceRegister, now time.Time) (map[string]energy.Elsewhere, error)
}

// PlaceQuery is a place-grouped charging question.
type PlaceQuery struct {
	At            geo.Point
	RadiusM       int
	Limit         int
	MinKw         float64
	Connector     string
	OnlyAvailable bool
}

// connectorSpellings are the stored spellings of each asked plug
// (CONNECTOR_FEED_VALUES).
var connectorSpellings = map[string][]string{
	"ccs2":    {"iec62196T2COMBO", "iec62196T2Combo", "iec62196t2combo", "combo", "ccs"},
	"type2":   {"iec62196T2", "iec62196t2", "type2"},
	"chademo": {"chademo", "chaDemo", "CHAdeMO"},
	"mcs":     {"mcs"},
}

// PlacesAnswer is a place-grouped charging answer.
type PlacesAnswer struct {
	Answer[energy.Place]
	// Register is the charging source register the answer was read under.
	Register energy.SourceRegister
	// Lang is the language of the answer's labels and plug names.
	Lang string
	// RegisterReleasedOn is the register release of the register rows
	// shown, when one is.
	RegisterReleasedOn *string
}

// ChargingPlaces answers "where can I charge near here" one line a place,
// as the platform's charging search (find_charging_station).
func (s *Service) ChargingPlaces(ctx context.Context, key access.Record, q PlaceQuery) (PlacesAnswer, error) {
	reg, gate, err := s.gate(ctx, key)
	if err != nil {
		return PlacesAnswer{}, err
	}
	permitted, partial, err := gate.Answer(access.Answering(reg, "energy"))
	if err != nil {
		return PlacesAnswer{}, err
	}
	a, err := s.chargingPlaces(ctx, permitted, q)
	if err != nil {
		return PlacesAnswer{}, err
	}
	a.Partial = partial
	contributed := placeFamilies(a.Items)
	if err := gate.Check(contributed); err != nil {
		return PlacesAnswer{}, err
	}
	if a.Families, err = s.creditsOf(ctx, contributed); err != nil {
		return PlacesAnswer{}, err
	}
	if len(a.Items) == 0 {
		n, err := s.repos.Charging.ChargingInRadius(ctx, energy.Query{At: q.At, RadiusM: q.RadiusM}, permitted)
		if err != nil {
			return PlacesAnswer{}, err
		}
		a.InRadius = &n
	}
	return a, nil
}

// chargingPlaces reads and folds the places of the families.
func (s *Service) chargingPlaces(ctx context.Context, families []string, q PlaceQuery) (PlacesAnswer, error) {
	repo, now, lang := s.repos.ChargingPlaces, s.now(), s.lang(ctx)
	fr, err := repo.ChargingRegister(ctx, families)
	if err != nil {
		return PlacesAnswer{}, err
	}
	scan := min(energy.MaxScanSites, q.Limit*energy.ScanPerLine)
	if q.OnlyAvailable {
		scan = energy.MaxScanSites
	}
	read := energy.PlaceRead{At: q.At, RadiusM: float64(q.RadiusM), Scan: scan + energy.MovedHeadroom, Connectors: connectorSpellings[q.Connector]}
	if q.MinKw > 0 {
		read.MinKw = &q.MinKw
	}
	recs, err := repo.ChargingPlaceRecords(ctx, read, fr, now)
	if err != nil {
		return PlacesAnswer{}, err
	}
	live, err := repo.ChargingLiveness(ctx, fr, now)
	if err != nil {
		return PlacesAnswer{}, err
	}
	moved, err := s.relocated(ctx, recs, fr)
	if err != nil {
		return PlacesAnswer{}, err
	}
	judged := energy.Judge(energy.PlaceAtAddresses(recs, moved, q.At, float64(q.RadiusM), scan, fr), fr, live, now, lang)
	a := PlacesAnswer{Answer: Answer[energy.Place]{Items: []energy.Place{}, At: now}, Register: fr, Lang: lang}
	candidates := judged
	if q.OnlyAvailable {
		candidates = nil
		d := energy.Dropped{}
		for _, j := range judged {
			switch energy.FreeNowOf(j.States) {
			case energy.Free:
				candidates = append(candidates, j)
			case energy.Stale:
				d.Stale++
			case energy.NoStatus:
				d.NoStatus++
			default:
				d.NotFree++
			}
		}
		a.Dropped = &d
	}
	lines := energy.RoughLines(candidates, q.Limit, fr, lang)
	elsewhere, err := repo.ChargingRegisterElsewhere(ctx, lines.RoughRecords(), fr, now)
	if err != nil {
		return PlacesAnswer{}, err
	}
	a.Items = lines.Places(elsewhere, q.Limit)
	a.HasMore = lines.More
	var shown []energy.PlaceRecord
	for _, p := range a.Items {
		for _, m := range p.Members {
			shown = append(shown, m.Record)
		}
	}
	a.RegisterReleasedOn = energy.RegisterReleaseOf(shown, fr)
	return a, nil
}

// relocated places the register records filed at their town's point at
// their address, where it lies elsewhere.
func (s *Service) relocated(ctx context.Context, recs []energy.PlaceRecord, reg energy.SourceRegister) (map[string]geo.Point, error) {
	repo := s.repos.ChargingPlaces
	moved := map[string]geo.Point{}
	var located []energy.PlaceRecord
	for _, r := range recs {
		if energy.Relocatable(r, reg) {
			located = append(located, r)
		}
	}
	if len(located) == 0 {
		return moved, nil
	}
	at, err := repo.ChargingAtTownPoint(ctx, located)
	if err != nil {
		return nil, err
	}
	for _, i := range at {
		hits, err := repo.ChargingAddresses(ctx, located[i])
		if err != nil {
			return nil, err
		}
		if p, ok := energy.RelocationOf(located[i], hits); ok {
			moved[located[i].Key()] = p
		}
	}
	return moved, nil
}

// placeFamilies are the families of the places' records and of the status
// sources that decided a point.
func placeFamilies(ps []energy.Place) []string {
	var out []string
	for _, p := range ps {
		for _, m := range p.Members {
			if !slices.Contains(out, m.Record.Source) {
				out = append(out, m.Record.Source)
			}
			for _, f := range m.StatusSources {
				if !slices.Contains(out, f) {
					out = append(out, f)
				}
			}
		}
	}
	return out
}
