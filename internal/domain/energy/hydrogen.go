package energy

import (
	"encoding/json"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// HydrogenStation is a hydrogen filling station as its source publishes it.
type HydrogenStation struct {
	ID        string
	Name      *string
	Operator  *string
	Brand     *string
	Location  geo.Point
	DistanceM int
	// Address, Refill and Opening are the published objects, verbatim.
	Address           json.RawMessage
	Refill            json.RawMessage
	Opening           json.RawMessage
	ProviderUpdatedAt *time.Time
	LastSeenAt        *time.Time
	Family            string
}

// HydrogenQuery asks for the hydrogen stations within RadiusM of At, at most
// Limit, nearest first; Vehicle (car or truck, empty: any) keeps the stations
// with a refill solution for it.
type HydrogenQuery struct {
	At      geo.Point
	RadiusM int
	Vehicle string
	Limit   int
}
