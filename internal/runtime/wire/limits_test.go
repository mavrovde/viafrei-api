package wire_test

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/admission"
	"github.com/mavrovde/viafrei-api/internal/app/breaker"
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

func TestBreakerSettings(t *testing.T) {
	req.Covers(t, "NFR-RES-002")
	cfg := config.Defaults()
	b := wire.BreakerSettings(cfg)
	if b.FailureThreshold != 5 || b.Window != 30*time.Second || b.OpenFor != 15*time.Second || b.HalfOpenProbes != 2 {
		t.Fatalf("breaker: %+v, want the specified defaults 5 / 30s / 15s / 2", b)
	}
}

func TestBreakerLogsEachChangeOfState(t *testing.T) {
	req.Covers(t, "NFR-RES-002")
	cfg := config.Defaults()
	cfg.Breaker.FailureThreshold = 1
	cfg.Breaker.OpenFor = time.Second
	var out bytes.Buffer
	at := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	b := wire.BreakerAt(cfg, slog.New(slog.NewJSONHandler(&out, nil)), func() time.Time { return at })
	tk, err := b.Allow()
	if err != nil {
		t.Fatal(err)
	}
	tk.Done(breaker.Failure)
	if !strings.Contains(out.String(), `"level":"WARN","msg":"storage circuit breaker changed state","from":"closed","to":"open","generation":1`) {
		t.Fatalf("opening not logged as a warning: %s", out.String())
	}
	at = at.Add(cfg.Breaker.OpenFor)
	if b.State() != breaker.HalfOpen || !strings.Contains(out.String(), `"level":"INFO","msg":"storage circuit breaker changed state","from":"open","to":"half-open","generation":2`) {
		t.Fatalf("half-open not logged as information: %s", out.String())
	}
	if wire.Breaker(cfg, slog.New(slog.NewJSONHandler(&out, nil))).State() != breaker.Closed {
		t.Fatal("the wall-clock breaker does not start closed")
	}
}
