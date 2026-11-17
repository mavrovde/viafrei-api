package roads

import (
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/geo"
)

// The platform's lorry reach (analysis/lorry-routes): a restriction within
// LorryRadiusM of a place is "here", a long-lorry network road within
// LongLorryRadiusM is "near".
const (
	LorryRadiusM     = 5000
	LongLorryRadiusM = 25000
)

// LorryQuery asks where a lorry (lorry) or a long lorry (long_lorry) may go:
// on a road (the long-lorry network rows of that road), or around a
// position (the network roads, or the restrictions and priority routes).
type LorryQuery struct {
	Vehicle string
	Road    string
	At      *geo.Point
	RadiusM int
	Limit   int
}

// LorryRule is one published lorry row.
type LorryRule struct {
	ID, Layer                                     string
	Kind, Sign, Unit, PublishedText, Road, Street *string
	Municipality, Direction, Times, Days          *string
	Category, NetworkState                        *string
	Value                                         *float64
	Exceptions                                    []string
	VersionDate                                   *time.Time
	DistanceM                                     *int
	Family                                        string
}
