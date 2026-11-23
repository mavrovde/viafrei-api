package accessstore

import (
	"context"
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
// The connection LISTENs on SettledChannel, whose payload is the settled
// request's id. An ask registers its request's id and is poked on each
// notification naming it, and every ask is poked once the connection
// listens (after a start or a reconnect), so a settle the connection did
// not hear is read then. While the connection is down the asks are answered
// at their deadlines; it is opened again after a pause.
type settles struct {
	connect func(context.Context) (listenConn, error)
	pause   time.Duration

	once   sync.Once
	run    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	// start runs the listener's loop: a goroutine; a test may hold the
	// starting ask until the loop has run.
	start func(loop func())

	mu      sync.Mutex
	waiters map[string]map[chan struct{}]bool // request id → its asks
}

// listenConn is what the listener needs of its connection (a *pgx.Conn).
type listenConn interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	WaitForNotification(ctx context.Context) (*pgconn.Notification, error)
	Close(ctx context.Context) error
}

func newSettles(connect func(context.Context) (listenConn, error), pause time.Duration) *settles {
	run, cancel := context.WithCancel(context.Background())
	return &settles{connect: connect, pause: pause, run: run, cancel: cancel, done: make(chan struct{}), start: func(loop func()) { go loop() },
		waiters: map[string]map[chan struct{}]bool{}}
}

// add registers an ask for the request id; the returned poke is signalled
// when the ask should read its request again.
//
// The ask is registered before the listener starts, so the poke every ask
// gets once the connection listens never runs ahead of the ask that started
// it.
func (h *settles) add(id string) chan struct{} {
	poke := make(chan struct{}, 1)
	h.mu.Lock()
	if h.waiters[id] == nil {
		h.waiters[id] = map[chan struct{}]bool{}
	}
	h.waiters[id][poke] = true
	h.mu.Unlock()
	h.once.Do(func() { h.start(h.loop) })
	return poke
}

// remove deregisters the ask.
func (h *settles) remove(id string, poke chan struct{}) {
	h.mu.Lock()
	delete(h.waiters[id], poke)
	if len(h.waiters[id]) == 0 {
		delete(h.waiters, id)
	}
	h.mu.Unlock()
}

// poke signals the asks of the ids; every ask when all is set.
func (h *settles) poke(all bool, ids ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if all {
		ids = ids[:0]
		for id := range h.waiters {
			ids = append(ids, id)
		}
	}
	for _, id := range ids {
		for p := range h.waiters[id] {
			select {
			case p <- struct{}{}:
			default:
			}
		}
	}
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

// loop keeps the connection open and listening until close.
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
	if _, err := conn.Exec(h.run, "listen "+pgx.Identifier{SettledChannel}.Sanitize()); err != nil { //nolint:misspell // the pgx method's name
		return err
	}
	h.poke(true)
	for {
		note, err := conn.WaitForNotification(h.run)
		if err != nil {
			return err
		}
		if note.Channel == SettledChannel {
			h.poke(false, note.Payload)
		}
	}
}
