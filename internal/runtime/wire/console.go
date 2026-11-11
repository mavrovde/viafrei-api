package wire

import (
	"net/url"

	consoleweb "github.com/mavrovde/viafrei-api/internal/adapters/console"
	"github.com/mavrovde/viafrei-api/internal/app/console"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

// ConsoleListener reads whether the console runs, and the address it listens
// on.
func ConsoleListener(cfg *config.Config) (addr string, enabled bool) {
	return cfg.Admin.ListenAddr, cfg.Admin.Enabled
}

// ConsoleGuard reads the admin.* settings of the console's guard.
func ConsoleGuard(cfg *config.Config) console.GuardSettings {
	a := cfg.Admin
	return console.GuardSettings{
		SessionTTL: a.SessionTTL, ChallengeTTL: a.ChallengeTTL, ConfirmTTL: a.ConfirmTTL,
		SignInLimit: a.SignInLimit, SignInWindow: a.SignInWindow,
		MaxPending: a.MaxPending, MaxSessions: a.MaxSessions,
	}
}

// ConsoleOwners reads the settings of the owners' passkey lifecycle: the
// current pepper MACs enrolment codes.
func ConsoleOwners(cfg *config.Config) console.OwnerSettings {
	return console.OwnerSettings{Pepper: []byte(cfg.Auth.Pepper.Reveal()), EnrolmentTTL: cfg.Admin.EnrolmentTTL}
}

// ConsoleWeb reads the console adapter's settings. The origin is held to
// scheme://host[:port], the form a browser sends in its Origin header.
func ConsoleWeb(cfg *config.Config) consoleweb.Settings {
	a := cfg.Admin
	origin := a.Origin
	if u, err := url.Parse(a.Origin); err == nil {
		origin = u.Scheme + "://" + u.Host
	}
	return consoleweb.Settings{
		Origin: origin, RPID: a.RPID, RPName: a.RPName,
		ChallengeTTL: a.ChallengeTTL, SessionTTL: a.SessionTTL, AuditPage: a.AuditPage,
	}
}
