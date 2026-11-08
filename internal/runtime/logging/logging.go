// Package logging builds the service's structured logger (DEV-046): JSON or
// text lines on standard output, at the configured level. Secrets cannot be
// written through it: config.Secret logs as "<redacted>", and every line is
// passed through a redactor that masks anything shaped like an API key and
// the value of any attribute named for a credential header.
package logging

import (
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"strings"

	"github.com/mavrovde/viafrei-api/internal/runtime/config"
)

// Masked replaces a key or a credential header value in a log line.
const Masked = "<key redacted>"

// keyShape matches a key of any valid prefix (auth.key_prefix_live and
// auth.key_prefix_test both have this form): prefix, 10-character id,
// underscore, 43-character secret and 6-character checksum.
var keyShape = regexp.MustCompile(`[a-z0-9]{1,5}_[a-z0-9]{1,5}_[0-9A-Za-z]{10}_[0-9A-Za-z]{49}`)

// credentialAttrs are attribute names whose value is always masked.
var credentialAttrs = []string{"authorization", "x-api-key", "apikey", "api_key"}

// Redact masks every key-shaped substring of s.
func Redact(s string) string { return keyShape.ReplaceAllString(s, Masked) }

func redactAttr(_ []string, a slog.Attr) slog.Attr {
	for _, n := range credentialAttrs {
		if strings.EqualFold(a.Key, n) {
			return slog.String(a.Key, Masked)
		}
	}
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, Redact(a.Value.String()))
	case slog.KindAny:
		s := fmt.Sprint(a.Value.Any())
		if r := Redact(s); r != s {
			return slog.String(a.Key, r)
		}
	}
	return a
}

// New returns a logger writing to w as log.format at log.level.
func New(c config.Log, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{Level: Level(c.Level), ReplaceAttr: redactAttr}
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
