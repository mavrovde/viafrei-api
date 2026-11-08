package logging_test

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/runtime/logging"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestLevels(t *testing.T) {
	req.Covers(t, "CFG-006")
	want := map[string]slog.Level{"debug": slog.LevelDebug, "info": slog.LevelInfo, "warn": slog.LevelWarn, "error": slog.LevelError}
	for s, l := range want {
		if got := logging.Level(s); got != l {
			t.Errorf("Level(%q) = %v, want %v", s, got, l)
		}
	}
}

func TestFormats(t *testing.T) {
	req.Covers(t, "CFG-006")
	for format, prefix := range map[string]string{"json": "{", "text": "time="} {
		var buf bytes.Buffer
		logging.New(config.Log{Level: "warn", Format: format}, &buf).Warn("x")
		if !strings.HasPrefix(buf.String(), prefix) {
			t.Errorf("%s: %q", format, buf.String())
		}
		buf.Reset()
		logging.New(config.Log{Level: "warn", Format: format}, &buf).Info("dropped")
		if buf.Len() != 0 {
			t.Errorf("%s: an info line passed level warn: %q", format, buf.String())
		}
	}
}

// plantedKey is shaped like a key; it is generated here, never a real one.
const plantedKey = "vf_live_AAAAAAAAAA_" + "0123456789012345678901234567890123456789012" + "ABCDEF"

func TestKeysNeverReachTheLog(t *testing.T) {
	req.Covers(t, "SEC-010")
	for _, format := range []string{"json", "text"} {
		var buf bytes.Buffer
		log := logging.New(config.Log{Level: "debug", Format: format}, &buf)
		log.Info("request with "+plantedKey,
			"authorization", "Bearer something",
			"X-API-Key", "anything",
			"path", "/v1/x?key="+plantedKey,
			"error", errors.New("failed for "+plantedKey),
			"panic", []string{plantedKey},
			"count", 3,
			"plain", fmt.Errorf("no key here"),
		)
		out := buf.String()
		if strings.Contains(out, "AAAAAAAAAA") || strings.Contains(out, "something") || strings.Contains(out, "anything") {
			t.Fatalf("%s: a credential reached the log: %s", format, out)
		}
		if strings.Count(out, logging.Masked) != 6 || !strings.Contains(out, "no key here") {
			t.Fatalf("%s: redaction miscounted: %s", format, out)
		}
	}
	if logging.Redact("plain") != "plain" {
		t.Fatal("plain text altered")
	}
}
