package wire_test

import (
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/admission"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/runtime/wire"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestAdmissionSettings(t *testing.T) {
	req.Covers(t, "RATE-008")
	cfg := config.Defaults()
	s := wire.AdmissionSettings(cfg)
	// The permits are the pool; the two upper tiers may use every one.
	if s.Permits != cfg.DB.PoolMax || s.Tiers[admission.Critical].MaxInflight != cfg.DB.PoolMax || s.Tiers[admission.Standard].MaxInflight != cfg.DB.PoolMax {
		t.Fatalf("permits: %+v", s)
	}
	if b := s.Tiers[admission.Bulk]; b.MaxInflight != cfg.Tiers.Bulk.MaxInflight || b.QueueLen != cfg.Tiers.Bulk.QueueLen || b.MaxWait != cfg.Tiers.Bulk.QueueMaxWait {
		t.Fatalf("bulk: %+v", b)
	}
	if c := s.Tiers[admission.Critical]; c.Reserved != cfg.Tiers.Critical.Reserved || c.MaxWait != cfg.Tiers.Critical.QueueMaxWait {
		t.Fatalf("critical: %+v", c)
	}
	if s.Reserve != cfg.Deadline.Reserve || s.PerKey != cfg.Tiers.PerKeyInflight || s.Alpha != cfg.Admission.EWMAAlpha || s.AgingAfter != cfg.Admission.AgingAfter {
		t.Fatalf("controller: %+v", s)
	}
}

func TestAddressAndMeteringSettings(t *testing.T) {
	req.Covers(t, "RATE-001", "RATE-014")
	cfg := config.Defaults()
	a := wire.AddressSettings(cfg)
	if a.Rate.Per != cfg.RateLimit.AnonRPS || a.Rate.Burst != cfg.RateLimit.AnonBurst || a.TTL != cfg.RateLimit.IPStateTTL || a.Size != cfg.RateLimit.IPTrackSize {
		t.Fatalf("address: %+v", a)
	}
	m := wire.MeteringSettings(cfg)
	if m.FlushInterval != cfg.Metering.FlushInterval || m.WriteTimeout != cfg.Metering.WriteTimeout || m.MaxCounters != cfg.Metering.MaxKeysBuffered {
		t.Fatalf("metering: %+v", m)
	}
	if got := wire.AuthSettings(cfg).UsageRetentionDays; got != 400 {
		t.Fatalf("usage retention %v", got)
	}
}

func TestQuotaZone(t *testing.T) {
	req.Covers(t, "RATE-003")
	cfg := config.Defaults()
	if got := wire.QuotaZone(cfg).String(); got != "Europe/Berlin" {
		t.Fatalf("zone %s", got)
	}
	// The declaration refuses an unknown zone; a configuration built past
	// it falls back to UTC rather than to the local zone of the host.
	cfg.Quota.ResetTZ = "Nowhere/Never"
	if got := wire.QuotaZone(cfg); got != time.UTC {
		t.Fatalf("fallback %s", got)
	}
}
