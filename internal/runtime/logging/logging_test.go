package logging_test

import (
	"bytes"
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
