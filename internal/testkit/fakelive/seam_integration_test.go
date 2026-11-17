//go:build integration

package fakelive

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"

	"github.com/jackc/pgx/v5"
)

// cutConn loses the connection at the first message it writes that holds
// marker.
type cutConn struct {
	net.Conn
	marker []byte
}

func (c *cutConn) Write(b []byte) (int, error) {
	if bytes.Contains(b, c.marker) {
		_ = c.Conn.Close()
		return 0, errors.New("injected: the connection is lost")
	}
	return c.Conn.Write(b)
}

// cutAt makes every session the fake opens lose its connection at the first
// statement holding marker, until the test ends.
func cutAt(t *testing.T, marker string) {
	t.Helper()
	t.Cleanup(func() { connect = pgx.Connect })
	connect = func(ctx context.Context, u string) (*pgx.Conn, error) {
		cfg, err := pgx.ParseConfig(u)
		if err != nil {
			return nil, err
		}
		var d net.Dialer
		cfg.DialFunc = func(ctx context.Context, network, addr string) (net.Conn, error) {
			c, err := d.DialContext(ctx, network, addr)
			if err != nil {
				return nil, err
			}
			return &cutConn{Conn: c, marker: []byte(marker)}, nil
		}
		return pgx.ConnectConfig(ctx, cfg)
	}
}

// A session that cannot listen does not start the fake.
func TestASessionThatCannotListenDoesNotStart(t *testing.T) {
	db, _ := fresh(t)
	cutAt(t, "listen live_refresh_requested")
	ctx, cancel := context.WithTimeout(context.Background(), bound)
	defer cancel()
	if f, err := Start(ctx, db.OwnerURL, Script{}); err == nil {
		_ = f.Stop()
		t.Fatal("the fake started without listening")
	}
}

// A request the ticks cannot be told of is the stop's error.
func TestASettleThatCannotSetTheRequestFails(t *testing.T) {
	db, owner := fresh(t)
	ask(t, owner, "09162000")
	cutAt(t, "set_config('fakelive.subject_key'")
	f := start(t, db.OwnerURL, `{"sources":{"fuel-prices-de":{"ticks":"select 1","default":{"state":"refreshed"}}}}`)
	stopWith(t, f, "injected")
}
