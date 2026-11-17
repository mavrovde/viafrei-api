// Package fuelrefresh forwards a person's fuel refresh ask to the service
// that holds the provider key, its guards and its write path (G8), over
// HTTP: one POST of the ask, as the platform's site sends it, answered by
// the platform's refresh answer. The API never calls the provider itself.
package fuelrefresh

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/energy"
)

// ErrUpstream is an answer the refresher could not give: a transport
// fault, a status other than 200 and 400, or a body it cannot read.
var ErrUpstream = errors.New("fuelrefresh: the refresher did not answer")

// maxBody bounds the answer read: the platform's answer is a few dozen
// bytes.
const maxBody = 4 << 10

// Client forwards refresh asks to one refresher.
type Client struct {
	url      string
	deadline time.Duration
	http     *http.Client
}

// New returns a client of the refresher at url, each ask bounded by
// deadline; hc nil is http.DefaultClient.
func New(url string, deadline time.Duration, hc *http.Client) *Client {
	if hc == nil {
		hc = http.DefaultClient
	}
	return &Client{url: url, deadline: deadline, http: hc}
}

// ask is the body the platform's site sends.
type ask struct {
	At     string `json:"at"`
	Snap   bool   `json:"snap,omitempty"`
	Anchor bool   `json:"anchor,omitempty"`
}

// answer is the platform's refresh answer; settledAt is epoch milliseconds.
type answer struct {
	State     string  `json:"state"`
	SettledAt *int64  `json:"settledAt"`
	Reused    bool    `json:"reused"`
	Queue     *queue  `json:"queue"`
	Reason    *string `json:"reason"`
}

type queue struct {
	Position int   `json:"position"`
	EtaMs    int64 `json:"etaMs"`
}

// Refresh posts the ask and relays the answer. An ask the refresher does
// not answer within the deadline is pending, as the site reads it today:
// the refresh runs on there and stores its rows. A 400 is
// energy.ErrRefreshArea.
func (c *Client) Refresh(ctx context.Context, a energy.RefreshAsk) (energy.RefreshAnswer, error) {
	body, _ := json.Marshal(ask{At: a.At, Snap: a.Snap, Anchor: a.Anchor})
	rctx, cancel := context.WithTimeout(ctx, c.deadline)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return energy.RefreshAnswer{}, fmt.Errorf("%w: %w", ErrUpstream, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		// The refresher's own deadline passed while the caller still waits:
		// the ask is pending there.
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			return energy.RefreshAnswer{State: energy.RefreshPending}, nil
		}
		return energy.RefreshAnswer{}, fmt.Errorf("%w: %w", ErrUpstream, err)
	}
	defer func() { _ = res.Body.Close() }()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusBadRequest:
		return energy.RefreshAnswer{}, energy.ErrRefreshArea
	default:
		return energy.RefreshAnswer{}, fmt.Errorf("%w: status %d", ErrUpstream, res.StatusCode)
	}
	var v answer
	if err := json.NewDecoder(io.LimitReader(res.Body, maxBody)).Decode(&v); err != nil {
		return energy.RefreshAnswer{}, fmt.Errorf("%w: %w", ErrUpstream, err)
	}
	if !energy.KnownRefreshState(v.State) {
		return energy.RefreshAnswer{}, fmt.Errorf("%w: state %q", ErrUpstream, v.State)
	}
	out := energy.RefreshAnswer{State: energy.RefreshState(v.State), Reused: v.Reused}
	if v.SettledAt != nil {
		t := time.UnixMilli(*v.SettledAt).UTC()
		out.SettledAt = &t
	}
	if v.Queue != nil && v.State == string(energy.RefreshPending) {
		out.Queue = &energy.RefreshQueue{Position: v.Queue.Position, ETA: time.Duration(v.Queue.EtaMs) * time.Millisecond}
	}
	if v.Reason != nil {
		out.Reason = *v.Reason
	}
	return out, nil
}
