package main

import (
	"bytes"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// freeAddr returns a loopback address nothing listens on.
func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

// dbURL points at a closed port: serve opens its pool without connecting.
func dbURL(t *testing.T) string {
	return "VFAPI_DB_URL=postgres://reader:pw@" + freeAddr(t) + "/contract?sslmode=disable"
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func runCmd(args, environ []string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, environ, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestCommands(t *testing.T) {
	req.Covers(t, "CFG-003", "CFG-005")
	ok := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer ok.Close()
	healthy := "VFAPI_SERVER_LISTEN_ADDR=" + strings.TrimPrefix(ok.URL, "http://")
	meta := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"platformVersion":"2.5.21","contractVersion":"0.1"}}`))
	}))
	defer meta.Close()
	synced := "VFAPI_SERVER_LISTEN_ADDR=" + strings.TrimPrefix(meta.URL, "http://")
	cases := []struct {
		name    string
		args    []string
		environ []string
		code    int
		out     string
	}{
		{"unknown", []string{"nope"}, nil, 2, ""},
		{"version", []string{"version"}, nil, 0, `"version"`},
		{"config without a command", []string{"config"}, nil, 2, ""},
		{"config unknown", []string{"config", "nope"}, nil, 2, ""},
		{"config docs", []string{"config", "docs"}, nil, 0, "# Configuration reference"},
		{"config check", []string{"config", "check"}, []string{dbURL(t)}, 0, "configuration valid"},
		{"config check refused", []string{"config", "check"}, nil, config.ExitCode, ""},
		{"config show", []string{"config", "show"}, nil, 0, "db.url = <unset>"},
		{"config show refused", []string{"config", "show"}, []string{"VFAPI_NOPE=1"}, config.ExitCode, ""},
		{"openapi", []string{"openapi"}, nil, 0, `"openapi": "3.1.0"`},
		{"openapi refused", []string{"openapi"}, []string{"VFAPI_NOPE=1"}, config.ExitCode, ""},
		{"openapi bad switch", []string{"openapi"}, []string{"VFAPI_ENDPOINTS_DISABLED=noSuchOperation"}, 1, ""},
		{"healthcheck", []string{"healthcheck"}, []string{healthy}, 0, ""},
		{"healthcheck no answer", []string{"healthcheck"}, []string{"VFAPI_SERVER_LISTEN_ADDR=" + freeAddr(t)}, 1, ""},
		{"meta-check", []string{"meta-check", "-platform", "2.5.21", "-contract", "0.1"}, []string{synced}, 0, ""},
		{"meta-check mismatch", []string{"meta-check", "-platform", "2.5.22"}, []string{synced}, 1, ""},
		{"meta-check no answer", []string{"meta-check", "-contract", "0.1"}, []string{"VFAPI_SERVER_LISTEN_ADDR=" + freeAddr(t)}, 2, ""},
		{"meta-check of nothing", []string{"meta-check"}, []string{synced}, 2, ""},
		{"meta-check bad flag", []string{"meta-check", "-nope"}, nil, 2, ""},
		{"meta-check refused", []string{"meta-check", "-contract", "0.1"}, []string{"VFAPI_NOPE=1"}, config.ExitCode, ""},
		{"healthcheck refused", []string{"healthcheck"}, []string{"VFAPI_NOPE=1"}, config.ExitCode, ""},
		{"serve refused", nil, nil, config.ExitCode, ""},
		{"serve bad contract", []string{"serve"}, []string{dbURL(t), "VFAPI_CONTRACT_EXPECTED=99999999999999999999.1"}, config.ExitCode, ""},
		{"serve bad URL", []string{"serve"}, []string{"VFAPI_DB_URL=postgres://u:p@h:notaport/x"}, config.ExitCode, ""},
		{"serve bad switch", []string{"serve"}, []string{dbURL(t), "VFAPI_ENDPOINTS_DISABLED=noSuchOperation"}, config.ExitCode, ""},
	}
	for _, c := range cases {
		code, stdout, stderr := runCmd(c.args, c.environ)
		if code != c.code || !strings.Contains(stdout, c.out) {
			t.Errorf("%s: exit %d (want %d), stdout %.80q, stderr %.200q", c.name, code, c.code, stdout, stderr)
		}
	}
	if code := run([]string{"config", "docs"}, nil, failingWriter{}, &bytes.Buffer{}); code != 1 {
		t.Errorf("config docs into a closed writer: exit %d, want 1", code)
	}
}

func TestServeAddressInUse(t *testing.T) {
	req.Covers(t, "FR-SVC-004")
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	if code, _, _ := runCmd([]string{"serve"}, []string{dbURL(t), "VFAPI_SERVER_LISTEN_ADDR=" + l.Addr().String()}); code != 1 {
		t.Fatalf("serve on a taken address: exit %d, want 1", code)
	}
}

// serveUntilTerm starts serve, waits for /healthz, runs during, then sends
// SIGTERM and returns serve's exit code.
func serveUntilTerm(t *testing.T, environ []string, during func(addr string)) int {
	t.Helper()
	addr := freeAddr(t)
	done := make(chan int, 1)
	go func() {
		code, _, _ := runCmd([]string{"serve"}, append(environ, dbURL(t), "VFAPI_SERVER_LISTEN_ADDR="+addr))
		done <- code
	}()
	cfg := config.Defaults()
	cfg.Server.ListenAddr = addr
	for i := 0; ; i++ {
		if httpapi.Probe(t.Context(), cfg) == nil {
			break
		}
		select {
		case code := <-done:
			t.Fatalf("serve exited with %d before answering", code)
		case <-time.After(20 * time.Millisecond):
		}
		if i > 50 {
			t.Fatal("serve did not answer /healthz within 1 s")
		}
	}
	during(addr)
	// serve's signal context is installed before it listens, so the signal
	// reaches it and not the default handler.
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case code := <-done:
		return code
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("serve did not exit within 1.5 s of SIGTERM")
		return -1
	}
}

func TestServeShutsDownOnSIGTERM(t *testing.T) {
	req.Covers(t, "FR-SVC-004")
	if code := serveUntilTerm(t, nil, func(string) {}); code != 0 {
		t.Fatalf("graceful shutdown: exit %d, want 0", code)
	}
}

func TestServeReportsAnIncompleteShutdown(t *testing.T) {
	req.Covers(t, "CFG-006")
	var conn net.Conn
	code := serveUntilTerm(t, []string{"VFAPI_SERVER_SHUTDOWN_GRACE=1s"}, func(addr string) {
		// A connection that never sends a request stays "new" for 5 s, longer
		// than the grace, so Shutdown gives up.
		var err error
		conn, err = net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
	})
	_ = conn.Close()
	if code != 1 {
		t.Fatalf("shutdown past the grace: exit %d, want 1", code)
	}
}
