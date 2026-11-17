package fuelrefresh_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/adapters/fuelrefresh"
	"github.com/mavrovde/viafrei-api/internal/domain/energy"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// upstream is a refresher answering status and body, after delay; it
// records the last ask it read.
func upstream(t *testing.T, status int, body string, delay time.Duration) (*httptest.Server, *map[string]any) {
	t.Helper()
	got := map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		got = map[string]any{"method": r.Method, "type": r.Header.Get("Content-Type"), "accept": r.Header.Get("Accept")}
		var ask map[string]any
		_ = json.Unmarshal(raw, &ask)
		for k, v := range ask {
			got[k] = v
		}
		if delay > 0 {
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
				return
			}
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func ask(t *testing.T, url string, a energy.RefreshAsk) (energy.RefreshAnswer, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return fuelrefresh.New(url, 200*time.Millisecond, nil).Refresh(ctx, a)
}

// TestTheAskIsPostedAsTheSiteSendsIt (G8): one POST of {at, snap|anchor},
// JSON both ways.
func TestTheAskIsPostedAsTheSiteSendsIt(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	srv, got := upstream(t, http.StatusOK, `{"state":"none_due","settledAt":null,"reused":true}`, 0)
	a, err := ask(t, srv.URL, energy.RefreshAsk{At: "pt:48.1,11.5", Snap: true})
	if err != nil || a.State != energy.RefreshNoneDue || !a.Reused || a.SettledAt != nil || a.Queue != nil {
		t.Fatalf("%+v, %v", a, err)
	}
	g := *got
	if g["method"] != http.MethodPost || g["type"] != "application/json" || g["accept"] != "application/json" ||
		g["at"] != "pt:48.1,11.5" || g["snap"] != true || g["anchor"] != nil {
		t.Errorf("asked %v", g)
	}
	if _, err := ask(t, srv.URL, energy.RefreshAsk{At: "pt:48.1,11.5", Anchor: true}); err != nil || (*got)["anchor"] != true || (*got)["snap"] != nil {
		t.Errorf("anchor: asked %v, %v", *got, err)
	}
}

// TestTheAnswerIsRelayed (G8): every state of the platform's answer, with
// its settling instant, its queue place on a pending ask and its reason.
func TestTheAnswerIsRelayed(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	settled := time.Date(2026, 11, 17, 15, 0, 0, 0, time.UTC)
	srv, _ := upstream(t, http.StatusOK, `{"state":"refreshed","settledAt":`+jsonInt(settled.UnixMilli())+`,"reused":false}`, 0)
	if a, err := ask(t, srv.URL, energy.RefreshAsk{At: "ags:09162000"}); err != nil || a.State != energy.RefreshRefreshed || a.SettledAt == nil || !a.SettledAt.Equal(settled) {
		t.Errorf("refreshed: %+v, %v", a, err)
	}
	srv, _ = upstream(t, http.StatusOK, `{"state":"pending","settledAt":null,"reused":false,"queue":{"position":3,"etaMs":4500}}`, 0)
	if a, err := ask(t, srv.URL, energy.RefreshAsk{At: "ags:09162000"}); err != nil || a.Queue == nil || a.Queue.Position != 3 || a.Queue.ETA != 4500*time.Millisecond {
		t.Errorf("pending: %+v, %v", a, err)
	}
	srv, _ = upstream(t, http.StatusOK, `{"state":"refused","settledAt":null,"reused":false,"queue":{"position":1,"etaMs":1},"reason":"cap"}`, 0)
	if a, err := ask(t, srv.URL, energy.RefreshAsk{At: "ags:09162000"}); err != nil || a.State != energy.RefreshRefused || a.Queue != nil || a.Reason != "cap" {
		t.Errorf("refused: %+v, %v", a, err)
	}
}

func jsonInt(v int64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestTheRefreshersFaultsAreNamed (G8): a 400 is an area the refresher does
// not know; an ask it has not answered by the deadline is pending; any
// other status, an unreadable body, an unknown state, a transport fault or
// the caller giving up is ErrUpstream.
func TestTheRefreshersFaultsAreNamed(t *testing.T) {
	req.Covers(t, "FR-ENERGY-001")
	a := energy.RefreshAsk{At: "ags:09162000"}
	srv, _ := upstream(t, http.StatusBadRequest, `{"error":"no"}`, 0)
	if _, err := ask(t, srv.URL, a); !errors.Is(err, energy.ErrRefreshArea) {
		t.Errorf("400: %v", err)
	}
	srv, _ = upstream(t, http.StatusOK, `{"state":"refreshed"}`, time.Second)
	if got, err := ask(t, srv.URL, a); err != nil || got.State != energy.RefreshPending || got.Queue != nil {
		t.Errorf("slow: %+v, %v", got, err)
	}
	for name, body := range map[string]string{"status": "", "body": "{", "state": `{"state":"done"}`} {
		status := http.StatusOK
		if name == "status" {
			status = http.StatusServiceUnavailable
		}
		srv, _ := upstream(t, status, body, 0)
		if _, err := ask(t, srv.URL, a); !errors.Is(err, fuelrefresh.ErrUpstream) {
			t.Errorf("%s: %v", name, err)
		}
	}
	closed, _ := upstream(t, http.StatusOK, "", 0)
	closed.Close()
	if _, err := ask(t, closed.URL, a); !errors.Is(err, fuelrefresh.ErrUpstream) {
		t.Errorf("closed: %v", err)
	}
	if _, err := ask(t, "http://\x7f", a); !errors.Is(err, fuelrefresh.ErrUpstream) {
		t.Errorf("bad url: %v", err)
	}
	slow, _ := upstream(t, http.StatusOK, `{"state":"refreshed"}`, time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := fuelrefresh.New(slow.URL, time.Second, slow.Client()).Refresh(ctx, a); !errors.Is(err, fuelrefresh.ErrUpstream) {
		t.Errorf("caller gave up: %v", err)
	}
}
