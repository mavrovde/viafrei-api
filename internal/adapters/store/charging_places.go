package store

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/domain/energy"
)

// chargingFeedRegister is the charging feed register (charging_feed_register)
// of the families $1 the key reads: each site feed's rank, twin radius and
// retirement, each status feed's age limits and the site feed it speaks for.
var chargingFeedRegister = named("api_charging_feed_register", `
select r.feed_key, r.role, r.site_rank, r.twin_radius_m, r.stale_after_s, r.statement_max_age_s, r.frozen_after_s,
       r.points_from, r.answers_until
from charging_feed_register r
where r.feed_key = any((`+ruledFamilies("(select $1::text[])")+`)::text[])
order by r.role, r.feed_key`, "text[]")

// ChargingRegister reads the charging feed register of the families.
func (s *Store) ChargingRegister(ctx context.Context, families []string) (energy.SourceRegister, error) {
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (energy.SourceRegister, error) {
		rows, _ := run(ctx, tx, chargingFeedRegister, families)
		var (
			reg                                  energy.SourceRegister
			key, role                            string
			rank, twin, stale, statement, frozen *int
			pointsFrom                           *string
			until                                *time.Time
		)
		_, err := pgx.ForEachRow(rows, []any{&key, &role, &rank, &twin, &stale, &statement, &frozen, &pointsFrom, &until}, func() error {
			if role == "site" {
				f := energy.SiteSource{Key: key, Rank: *rank, AnswersUntil: until}
				if twin != nil {
					v := *twin
					f.TwinRadiusM = &v
				}
				reg.Sites = append(reg.Sites, f)
				return nil
			}
			f := energy.StatusSource{Key: key, StaleAfter: seconds(*stale), StatementMaxAge: seconds(*statement), PointsFrom: pointsFrom}
			if frozen != nil {
				d := seconds(*frozen)
				f.FrozenAfter = &d
			}
			reg.Status = append(reg.Status, f)
			return nil
		})
		return reg, err
	})
}

func seconds(n int) time.Duration { return time.Duration(n) * time.Second }

// placePoint is a point of the site read's points, as the statement
// builds it.
type placePoint struct {
	PointKey         string     `json:"pointKey"`
	SiteID           *string    `json:"siteId"`
	EvseID           *string    `json:"evseId"`
	ConnectorTypes   []string   `json:"connectorTypes"`
	MaxPowerKw       *float64   `json:"maxPowerKw"`
	PricePerKwh      *float64   `json:"pricePerKwh"`
	PriceTaxIncluded *bool      `json:"priceTaxIncluded"`
	PriceTaxRate     *float64   `json:"priceTaxRate"`
	PriceCurrency    *string    `json:"priceCurrency"`
	StatusSource     *string    `json:"statusFeed"`
	Status           *string    `json:"status"`
	OperationStatus  *string    `json:"operationStatus"`
	OpeningStatus    *string    `json:"openingStatus"`
	ObservedAt       *time.Time `json:"observedAt"`
	SourceTs         *time.Time `json:"feedTs"`
	LastUpdated      *time.Time `json:"lastUpdated"`
	ExchangeProtocol *string    `json:"exchangeProtocol"`
}

// ChargingPlaceRecords reads the records of the platform's site read
// (chargingPlacesNear) under the register at now.
func (s *Store) ChargingPlaceRecords(ctx context.Context, q energy.PlaceRead, reg energy.SourceRegister, now time.Time) ([]energy.PlaceRecord, error) {
	status, _ := reg.StatusKeys()
	window, _ := reg.WidestWindow()
	rankKeys, ranks := reg.Ranks()
	rank32 := make([]int32, len(ranks))
	for i, r := range ranks {
		rank32[i] = int32(r) //nolint:gosec // a rank is a small register number
	}
	pairStatus, pairMaster := reg.Pairs()
	twins, radius := reg.Twins()
	var connectors []string
	if len(q.Connectors) > 0 {
		connectors = q.Connectors
	}
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]energy.PlaceRecord, error) {
		rows, _ := run(ctx, tx, chargingPlacesNear, q.At.Lon, q.At.Lat, q.RadiusM, int64(q.Scan), nonNil(reg.Masters(now)), q.MinKw,
			connectors, status, int(window/time.Second), now, now.Add(-energy.DeltaTrustHorizon), rankKeys, rank32,
			pairStatus, pairMaster, twins, radius)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (energy.PlaceRecord, error) {
			var (
				v    energy.PlaceRecord
				slug *string
				ps   []placePoint
			)
			err := r.Scan(&v.Source, &v.SiteID, &v.Name, &v.Operator, &v.Street, &v.HouseNumber, &v.Postcode, &v.City,
				&v.SourceVersion, &v.Location.Lat, &v.Location.Lon, &v.DistanceM, &slug, &ps, &v.SiteIDs)
			for _, p := range ps {
				v.Points = append(v.Points, energy.PlacePoint(p))
			}
			return v, err
		})
	})
}

// nonNil is xs, or an empty list for none: an empty array, not a null one.
func nonNil(xs []string) []string {
	if xs == nil {
		return []string{}
	}
	return xs
}

// ChargingLiveness reads what the run ledger says about each status feed
// of the register at now.
func (s *Store) ChargingLiveness(ctx context.Context, reg energy.SourceRegister, now time.Time) ([]energy.SourceLiveness, error) {
	keys, from := reg.StatusKeys()
	_, statement := reg.WidestWindow()
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]energy.SourceLiveness, error) {
		rows, _ := run(ctx, tx, chargingStatusLiveness, keys, energy.CycleCheckedStatuses, now, energy.LedgerDays,
			int(statement/time.Second), from)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (energy.SourceLiveness, error) {
			var v energy.SourceLiveness
			err := r.Scan(&v.Source, &v.MovedAt, &v.ReceivedAt, &v.RestatedAt, &v.RestatedReceivedAt, &v.GapAt,
				&v.LastConclusiveAt, &v.Validator, &v.ValidatorSince)
			return v, err
		})
	})
}

// ChargingAtTownPoint tells which of the records (by their index) lie at
// their town's point.
func (s *Store) ChargingAtTownPoint(ctx context.Context, recs []energy.PlaceRecord) ([]int, error) {
	lons, lats, towns := make([]float64, len(recs)), make([]float64, len(recs)), make([]string, len(recs))
	for i, r := range recs {
		lons[i], lats[i], towns[i] = r.Location.Lon, r.Location.Lat, energy.TownFold(r)
	}
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]int, error) {
		rows, _ := run(ctx, tx, chargingAtTownPoint, lons, lats, towns, float64(energy.TownPointM))
		at, err := pgx.CollectRows(rows, pgx.RowTo[int])
		for i := range at {
			at[i]--
		}
		return at, err
	})
}

// ChargingAddresses reads the OpenStreetMap addresses of a record's
// address.
func (s *Store) ChargingAddresses(ctx context.Context, rec energy.PlaceRecord) ([]energy.AddressHit, error) {
	a := energy.AddressQueryOf(rec)
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) ([]energy.AddressHit, error) {
		rows, _ := run(ctx, tx, chargingSiteAddresses, a[0], a[1], a[2], a[3], a[4], a[5], energy.AddressHitsPerSite)
		return pgx.CollectRows(rows, func(r pgx.CollectableRow) (energy.AddressHit, error) {
			var (
				v       energy.AddressHit
				i       int
				id      string
				extract *string
				land    *string
			)
			err := r.Scan(&i, &id, &v.Street, &v.HouseNumber, &v.Postcode, &v.Place, &land, &v.Location.Lat, &v.Location.Lon,
				&extract, &v.LiteralScore, &v.ByPostcode)
			return v, err
		})
	})
}

// ChargingRegisterElsewhere reads which register records were filed under
// an address that is not where they lie, and the town each lies in.
func (s *Store) ChargingRegisterElsewhere(ctx context.Context, recs []energy.PlaceRecord, reg energy.SourceRegister, now time.Time) (map[string]energy.Elsewhere, error) {
	var feeds, ids, towns []string
	for _, r := range recs {
		if reg.IsRegister(r.Source) {
			feeds, ids, towns = append(feeds, r.Source), append(ids, r.SiteID), append(towns, energy.TownFold(r))
		}
	}
	out := map[string]energy.Elsewhere{}
	if len(feeds) == 0 {
		return out, nil
	}
	return ReadValue(ctx, s, func(ctx context.Context, tx pgx.Tx) (map[string]energy.Elsewhere, error) {
		rows, _ := run(ctx, tx, chargingRegisterAddress, feeds, ids, towns, float64(energy.RegisterAddressFarM), nonNil(reg.Masters(now)))
		var (
			feed, id    string
			seen, known *bool
			nearest     *float64
			near        *string
		)
		_, err := pgx.ForEachRow(rows, []any{&feed, &id, &seen, &known, &nearest, &near}, func() error {
			if energy.RegisterAddressElsewhere(known, nearest, seen) {
				out[energy.PlaceRecord{Source: feed, SiteID: id}.Key()] = energy.Elsewhere{LocatedNear: near}
			}
			return nil
		})
		return out, err
	})
}
