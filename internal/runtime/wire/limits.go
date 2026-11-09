package wire

import (
	"time"

	"github.com/mavrovde/viafrei-api/internal/app/admission"
	"github.com/mavrovde/viafrei-api/internal/app/metering"
	"github.com/mavrovde/viafrei-api/internal/app/ratelimit"
	"github.com/mavrovde/viafrei-api/internal/domain/limits"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

// AdmissionSettings reads the tiers.* and admission.* settings. The permits
// are db.pool_max, so nothing ever waits inside the pool (RATE-008); the
// critical and standard tiers may hold every permit.
func AdmissionSettings(cfg *config.Config) admission.Settings {
	t := cfg.Tiers
	permits := cfg.DB.PoolMax
	return admission.Settings{
		Permits: permits,
		Tiers: [3]admission.TierSettings{
			admission.Critical: {Reserved: t.Critical.Reserved, QueueLen: t.Critical.QueueLen, MaxWait: t.Critical.QueueMaxWait, MaxInflight: permits},
			admission.Standard: {Reserved: t.Standard.Reserved, QueueLen: t.Standard.QueueLen, MaxWait: t.Standard.QueueMaxWait, MaxInflight: permits},
			admission.Bulk:     {Reserved: t.Bulk.Reserved, QueueLen: t.Bulk.QueueLen, MaxWait: t.Bulk.QueueMaxWait, MaxInflight: t.Bulk.MaxInflight},
		},
		PerKey:     t.PerKeyInflight,
		AgingAfter: cfg.Admission.AgingAfter,
		Alpha:      cfg.Admission.EWMAAlpha,
		Reserve:    cfg.Deadline.Reserve,
	}
}

// AddressSettings reads the per-address ratelimit.* settings.
func AddressSettings(cfg *config.Config) ratelimit.IPSettings {
	r := cfg.RateLimit
	return ratelimit.IPSettings{Rate: limits.Rate{Per: r.AnonRPS, Burst: r.AnonBurst}, TTL: r.IPStateTTL, Size: r.IPTrackSize, IPv6Prefix: r.IPv6Prefix}
}

// QuotaZone reads quota.reset_tz; the declaration has validated it.
func QuotaZone(cfg *config.Config) *time.Location {
	loc, err := time.LoadLocation(cfg.Quota.ResetTZ)
	if err != nil {
		return time.UTC
	}
	return loc
}

// MeteringSettings reads the metering.* settings.
func MeteringSettings(cfg *config.Config) metering.Settings {
	m := cfg.Metering
	return metering.Settings{FlushInterval: m.FlushInterval, WriteTimeout: m.WriteTimeout, MaxCounters: m.MaxKeysBuffered}
}
