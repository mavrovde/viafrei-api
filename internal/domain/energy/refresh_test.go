package energy_test

import (
	"errors"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// TestARefreshAskIsReadAsThePlatformReadsIt (G8): an area key is a
// Gemeinde's eight-digit AGS or a point of at most six decimals inside
// Germany's box, of at most 40 characters; snap and anchor exclude each
// other.
func TestARefreshAskIsReadAsThePlatformReadsIt(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	for _, a := range []energy.RefreshAsk{
		{At: "ags:09162000"}, {At: "pt:48.137,11.575", Snap: true}, {At: "pt:52.5,13.4", Anchor: true},
		{At: "pt:47.2,5.8"}, {At: "pt:55.1,15.1"},
	} {
		if err := a.Validate(); err != nil {
			t.Errorf("%+v: %v", a, err)
		}
	}
	for _, at := range []string{"", "ags:0916200", "ags:091620001", "pt:48.1234567,11.5", "pt:48,", "pt:40.0,11.5", "pt:48.0,16.0",
		"pt:56.0,11.0", "pt:48.0,5.0", "AGS:09162000", "pt:48.137,11.575 ", "pt:-48.1,11.5"} {
		if err := (energy.RefreshAsk{At: at}).Validate(); !errors.Is(err, energy.ErrRefreshAt) {
			t.Errorf("%q: %v", at, err)
		}
	}
	if err := (energy.RefreshAsk{At: "ags:09162000", Snap: true, Anchor: true}).Validate(); !errors.Is(err, energy.ErrRefreshMode) {
		t.Errorf("snap and anchor: %v", err)
	}
	for _, s := range energy.RefreshStates {
		if !energy.KnownRefreshState(string(s)) {
			t.Errorf("%s is not known", s)
		}
	}
	if energy.KnownRefreshState("done") {
		t.Error("an unknown state is known")
	}
}
