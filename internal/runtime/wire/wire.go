// Package wire translates the configuration into the settings of the use
// cases, which know no configuration package.
package wire

import (
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/features"

	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

// AuthSettings reads the auth.* and audit.* settings.
func AuthSettings(cfg *config.Config) auth.Settings {
	a := cfg.Auth
	s := auth.Settings{
		Environment:           access.Environment(a.Environment),
		Prefixes:              access.Prefixes{Live: a.KeyPrefixLive, Test: a.KeyPrefixTest},
		Peppers:               access.Peppers{Current: []byte(a.Pepper.Reveal())},
		KeyCacheTTL:           a.KeyCacheTTL,
		NegativeCacheTTL:      a.NegativeCacheTTL,
		KeyCacheSize:          a.KeyCacheSize,
		ExpiryWarning:         time.Duration(a.ExpiryWarningDays) * config.Day,
		FailureLimit:          a.FailureLimit,
		FailureWindow:         a.FailureWindow,
		FailureTrackSize:      a.FailureTrackSize,
		RotationOverlap:       a.RotationOverlap,
		DefaultExpiry:         a.DefaultExpiry,
		RevocationPropagation: a.RevocationPropagation,
		MinExpiry:             config.Day,
		MaxExpiry:             config.MaxKeyLifetime,
		AuditRetention:        time.Duration(cfg.Audit.RetentionDays) * config.Day,
		UsageRetentionDays:    cfg.Metering.RetentionDays,
	}
	if a.PepperPrevious.IsSet() {
		s.Peppers.Previous = []byte(a.PepperPrevious.Reveal())
	}
	return s
}

// FeatureSettings reads the settings of the feature use cases.
func FeatureSettings(cfg *config.Config) features.Settings {
	return features.Settings{
		Language:       Language,
		PartDeadline:   cfg.Compose.PartDeadline,
		EndedRetention: time.Duration(cfg.Roads.EndedRetentionH) * time.Hour,
		Disabled:       cfg.Sources.Disabled,
		IsTimeout:      store.IsDeadline,
	}
}

// Language is the language of every answer's texts until language
// negotiation arrives.
const Language = "en"
