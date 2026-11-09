package wire_test

import (
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/runtime/wire"
)

func TestAuthSettings(t *testing.T) {
	cfg := config.Defaults()
	s := wire.AuthSettings(cfg)
	if s.Peppers.Previous != nil || s.ExpiryWarning != 14*24*time.Hour || s.AuditRetention != 395*24*time.Hour || s.MaxExpiry != 1095*24*time.Hour {
		t.Fatalf("defaults: %+v", s)
	}
	cfg.Auth.PepperPrevious = config.NewSecret(strings.Repeat("o", 32))
	if got := wire.AuthSettings(cfg).Peppers.Previous; string(got) != strings.Repeat("o", 32) {
		t.Fatal("pepper_previous not carried")
	}
}

func TestFeatureSettings(t *testing.T) {
	cfg := config.Defaults()
	cfg.Sources.Disabled = []string{"src-x"}
	s := wire.FeatureSettings(cfg)
	if s.Language != wire.Language || s.PartDeadline != 800*time.Millisecond || len(s.Disabled) != 1 || s.Disabled[0] != "src-x" {
		t.Fatalf("settings %+v", s)
	}
	if !s.IsTimeout(store.ErrDeadlineExhausted) || s.IsTimeout(store.ErrUnavailable) {
		t.Fatal("IsTimeout is not the store's deadline test")
	}
}
