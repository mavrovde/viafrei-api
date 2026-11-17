package accessstore

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// settles is the one connection a store listens on for refresh settles,
// whatever the number of asks waiting: a wait never holds a connection of
// the access pool, so it never holds back a key check, and the store's role
// holds one connection more, not one per waiting ask.
//
// An ask registers its request's id and is poked once the connection
// listens on the request's channel (it then reads the request again, so a
// settle between its first read and the LISTEN is not missed) and on each
// notification of the channel. While the connection is down the asks are
// answered at their deadlines; it is opened again after a pause.
type settles struct {
	connect func(context.Context) (listenConn, error)
	pause   time.Duration

	once   sync.Once
	run    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu      sync.Mutex
	waiters map[string]map[chan struct{}]bool // channel → its asks
	wake    chan struct{}
}

// listenConn is what the listener needs of its connection (a *pgx.Conn).
type listenConn interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	WaitForNotification(ctx context.Context) (*pgconn.Notification, error)
	Close(ctx context.Context) error
}

func newSettles(connect func(context.Context) (listenConn, error), pause time.Duration) *settles {
	run, cancel := context.WithCancel(context.Background())
	return &settles{connect: connect, pause: pause, run: run, cancel: cancel, done: make(chan struct{}),
		waiters: map[string]map[chan struct{}]bool{}, wake: make(chan struct{}, 1)}
}

// add registers an ask for channel ch; the returned poke is signalled when
// the ask should read its request again.
func (h *settles) add(ch string) chan struct{} {
	h.once.Do(func() { go h.loop() })
	poke := make(chan struct{}, 1)
	h.mu.Lock()
	if h.waiters[ch] == nil {
		h.waiters[ch] = map[chan struct{}]bool{}
	}
	h.waiters[ch][poke] = true
	h.mu.Unlock()
	h.nudge()
	return poke
}

// remove deregisters the ask.
func (h *settles) remove(ch string, poke chan struct{}) {
	h.mu.Lock()
	delete(h.waiters[ch], poke)
	if len(h.waiters[ch]) == 0 {
		delete(h.waiters, ch)
	}
	h.mu.Unlock()
	h.nudge()
}

func (h *settles) nudge() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

// poke signals every ask of the channels.
func (h *settles) poke(chs ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, ch := range chs {
		for p := range h.waiters[ch] {
			select {
			case p <- struct{}{}:
			default:
			}
		}
	}
}

func (h *settles) wanted() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Sorted(maps.Keys(h.waiters))
}

// close stops the listener, if it runs.
func (h *settles) close() {
	h.cancel()
	started := true
	h.once.Do(func() { started = false })
	if started {
		<-h.done
	}
}

// loop keeps the connection open and listening on the channels of the
// waiting asks until close.
func (h *settles) loop() {
	defer close(h.done)
	for h.run.Err() == nil {
		conn, err := h.connect(h.run)
		if err == nil {
			_ = h.listen(conn) // a lost connection is opened again
			_ = conn.Close(context.WithoutCancel(h.run))
		}
		select {
		case <-h.run.Done():
		case <-time.After(h.pause):
		}
	}
}

// listen serves one connection until it fails or the store closes.
func (h *settles) listen(conn listenConn) error {
	on := map[string]bool{}
	for {
		want := h.wanted()
		var added []string
		for _, ch := range want {
			if !on[ch] {
				if _, err := conn.Exec(h.run, "listen "+pgx.Identifier{ch}.Sanitize()); err != nil {
					return err
				}
				on[ch], added = true, append(added, ch)
			}
		}
		for ch := range on {
			if !slices.Contains(want, ch) {
				if _, err := conn.Exec(h.run, "unlisten "+pgx.Identifier{ch}.Sanitize()); err != nil {
					return err
				}
				delete(on, ch)
			}
		}
		h.poke(added...)

		wctx, stop := context.WithCancel(h.run)
		go func() {
			select {
			case <-h.wake:
				stop()
			case <-wctx.Done():
			}
		}()
		note, err := conn.WaitForNotification(wctx)
		woken := wctx.Err() != nil && h.run.Err() == nil
		stop()
		switch {
		case err == nil:
			if strings.HasPrefix(note.Channel, "refresh_") {
				h.poke(note.Channel)
			}
		case woken:
			// the set of asks changed: listen again
		default:
			return err
		}
	}
}
