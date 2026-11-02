// Package logging builds the service's structured logger (DEV-046): JSON or
// text lines on standard output, at the configured level. Secrets cannot be
// written through it, because config.Secret logs as "<redacted>".
package logging

import (
	"io"
	"log/slog"

	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

// New returns a logger writing to w as log.format at log.level.
func New(c config.Log, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: Level(c.Level)}
	if c.Format == "text" {
		return slog.New(slog.NewTextHandler(w, opts))
	}
	return slog.New(slog.NewJSONHandler(w, opts))
}

// Level maps a validated log.level to a slog level.
func Level(s string) slog.Level {
	switch s {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
