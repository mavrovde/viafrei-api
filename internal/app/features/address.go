package features

import (
	"context"

	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
)

// Addresses is the address port.
type Addresses interface {
	// Addresses returns up to q.Limit addresses of the families that q
	// names, those in its postcode first.
	Addresses(ctx context.Context, q places.AddressQuery, families []string) ([]places.Address, error)
}

// AddressAnswer is the answer to an address search.
type AddressAnswer struct {
	Addresses []places.Address
	Meta      Answer[struct{}]
}

// Addresses answers "where is Street 12, Town" for key as the platform's
// address search answers it.
func (s *Service) Addresses(ctx context.Context, key access.Record, q places.AddressQuery) (AddressAnswer, error) {
	v, meta, err := listed(ctx, s, key, "places", "", func(permitted []string) ([]places.Address, []string, error) {
		v, err := s.repos.Addresses.Addresses(ctx, q, permitted)
		var contributed []string
		for _, a := range v {
			contributed = append(contributed, a.Family)
		}
		return v, contributed, err
	})
	return AddressAnswer{Addresses: v, Meta: meta}, err
}
