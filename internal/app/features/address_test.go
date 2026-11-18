package features_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

type addressFake struct {
	*fake
	v   []places.Address
	err error
}

func (f *addressFake) Addresses(context.Context, places.AddressQuery, []string) ([]places.Address, error) {
	return f.v, f.err
}

func addressService(f *addressFake) *features.Service {
	if f.fake == nil {
		f.fake = &fake{}
	}
	f.reg = registerOf("places")
	return features.New(features.Repositories{Register: f, Addresses: f}, features.Settings{Language: "en"}, func() time.Time { return now })
}

func TestAddresses(t *testing.T) {
	req.Covers(t, "SEC-030")
	full := key("commercial", []access.Scope{"places"}, "f1")
	a, err := addressService(&addressFake{v: []places.Address{{ID: "osm:node1", Family: "f1"}}}).Addresses(context.Background(), full, places.AddressQuery{Limit: 3})
	if err != nil || len(a.Addresses) != 1 || !a.Meta.At.Equal(now) {
		t.Errorf("answer %+v %v", a, err)
	}
	for name, c := range map[string]struct {
		f    *addressFake
		want error
	}{
		"read":       {&addressFake{err: errStore}, errStore},
		"post-check": {&addressFake{v: []places.Address{{Family: "f2"}}}, features.ErrGateViolation},
	} {
		if _, err := addressService(c.f).Addresses(context.Background(), full, places.AddressQuery{Limit: 3}); !errors.Is(err, c.want) {
			t.Errorf("%s: %v, want %v", name, err, c.want)
		}
	}
}
