package features_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
)

func (f *fuFake) FuelStation(_ context.Context, id string, fams []string, _ time.Time, _ time.Duration) (energy.FuelStationPage, error) {
	f.gotFams = fams
	for _, s := range f.stations {
		if s.ID == id {
			return energy.FuelStationPage{FuelStation: s}, f.readErr
		}
	}
	if f.readErr != nil {
		return energy.FuelStationPage{}, f.readErr
	}
	return energy.FuelStationPage{}, energy.ErrNotFound
}

func TestFuelStationByIDIsGatedAndAttributed(t *testing.T) {
	f := &fuFake{stations: []energy.FuelStation{{ID: "a", Family: "f1"}, {ID: "x", Family: "zz"}}}
	s := fuService(f)
	k := key("commercial", []access.Scope{"energy"}, "f1", "f2")
	st, a, err := s.FuelStation(context.Background(), k, "a", time.Hour)
	if err != nil || st.ID != "a" || !slices.Equal(names(a.Families), []string{"f1"}) || !a.At.Equal(now) {
		t.Fatalf("station: %+v %+v %v", st, a, err)
	}
	if !slices.Equal(f.gotFams, []string{"f1"}) {
		t.Errorf("a consumer-only family is read for a commercial key: %v", f.gotFams)
	}
	if _, _, err := s.FuelStation(context.Background(), k, "nope", time.Hour); !errors.Is(err, energy.ErrNotFound) {
		t.Errorf("unknown id: %v", err)
	}
	if _, _, err := s.FuelStation(context.Background(), k, "x", time.Hour); !errors.Is(err, features.ErrGateViolation) {
		t.Errorf("a family outside the permitted set must abort: %v", err)
	}
	if _, _, err := s.FuelStation(context.Background(), key("commercial", []access.Scope{"energy"}, "nothing"), "a", time.Hour); err == nil {
		t.Error("a key with no energy family must be refused")
	}
	boom := errors.New("boom")
	f.readErr = boom
	if _, _, err := s.FuelStation(context.Background(), k, "a", time.Hour); !errors.Is(err, boom) {
		t.Errorf("read error: %v", err)
	}
	f.readErr = nil
	f.provErr = boom
	if _, _, err := s.FuelStation(context.Background(), k, "a", time.Hour); !errors.Is(err, boom) {
		t.Errorf("provenance error: %v", err)
	}
	f.provErr, f.regErr = nil, boom
	if _, _, err := s.FuelStation(context.Background(), k, "a", time.Hour); !errors.Is(err, boom) {
		t.Errorf("register error: %v", err)
	}
}

func TestChargingSiteByIDReadsOneSite(t *testing.T) {
	f := &fake{sites: []energy.Site{{ID: "s1", Family: "e1"}}}
	s := service(f, nil)
	k := key("commercial", allScopes, everything...)
	a, err := s.ChargingSite(context.Background(), k, "s1", time.Hour)
	if err != nil || len(a.Items) != 1 || a.HasMore || a.Partial {
		t.Fatalf("site: %+v %v", a, err)
	}
	f.sites = nil
	if _, err := s.ChargingSite(context.Background(), k, "s1", time.Hour); !errors.Is(err, energy.ErrNotFound) {
		t.Errorf("no site: %v", err)
	}
	f.regErr = errors.New("boom")
	if _, err := s.ChargingSite(context.Background(), k, "s1", time.Hour); err == nil {
		t.Error("register error must surface")
	}
}
