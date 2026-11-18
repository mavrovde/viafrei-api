//go:build integration

package pgtest

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
)

var errCut = errors.New("injected: the connection is lost")

// cutConn loses the connection at the first message it writes that holds
// marker, or, with next, once it has sent the message after it (the
// statement's execution, once its text is parsed): the statement runs, and
// its rows are lost on the way back.
type cutConn struct {
	net.Conn
	marker []byte
	next   bool
	armed  bool
}

func (c *cutConn) Write(b []byte) (int, error) {
	if c.armed {
		n, err := c.Conn.Write(b)
		_ = c.Close()
		return n, err
	}
	if bytes.Contains(b, c.marker) {
		if !c.next {
			_ = c.Close()
			return 0, errCut
		}
		c.armed = true
	}
	return c.Conn.Write(b)
}

// swapConnect makes connect f until the test ends.
func swapConnect(t *testing.T, f func(context.Context, string) (*pgx.Conn, error)) {
	t.Helper()
	t.Cleanup(func() { connect = pgx.Connect })
	connect = f
}

// cutAt makes every session the package opens lose its connection at
// marker (see cutConn).
func cutAt(t *testing.T, marker string, next bool) {
	swapConnect(t, func(ctx context.Context, u string) (*pgx.Conn, error) {
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
			return &cutConn{Conn: c, marker: []byte(marker), next: next}, nil
		}
		return pgx.ConnectConfig(ctx, cfg)
	})
}

// refuseCall makes the n-th session the package opens, counted from 1,
// refused.
func refuseCall(t *testing.T, n int32) {
	var calls atomic.Int32
	swapConnect(t, func(ctx context.Context, u string) (*pgx.Conn, error) {
		if calls.Add(1) == n {
			return nil, errCut
		}
		return pgx.Connect(ctx, u)
	})
}

// swapImage makes the pinned image run as run until the test ends.
func swapImage(t *testing.T, run Runner) {
	t.Helper()
	t.Cleanup(func() { runImage = RunImage })
	runImage = run
}

// pinnedRun answers every step of the pinned image as the pinned release
// does.
func pinnedRun(t *testing.T) Runner {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	pin, err := ReadPin(wd)
	if err != nil {
		t.Fatal(err)
	}
	f := healthyRun()
	f.answers["version"] = answer{out: pin.VersionLine() + "\n"}
	f.answers["head"] = answer{out: pin.Ready() + "\n"}
	return f.run
}

// onHarness is a shared container that is the harness's own: its database
// is not yet a template, so a set-up on it migrates.
type onHarness struct{ csErr error }

func (h onHarness) ConnectionString(context.Context, ...string) (string, error) {
	return harness.OwnerURL, h.csErr
}

func (onHarness) ContainerIP(context.Context) (string, error) { return "127.0.0.1", nil }

// swapShared makes the shared container c until the test ends.
func swapShared(t *testing.T, c sharedContainer, err error) {
	t.Helper()
	t.Cleanup(func() {
		runShared = func(ctx context.Context, password string, extra ...testcontainers.ContainerCustomizer) (sharedContainer, error) {
			return runContainer(ctx, password, extra...)
		}
	})
	runShared = func(context.Context, string, ...testcontainers.ContainerCustomizer) (sharedContainer, error) {
		return c, err
	}
}

func setUp(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), SetupBound)
	t.Cleanup(cancel)
	return ctx
}

// Each fault of the shared container's set-up is Shared's error.
func TestSharedBaseReportsEachFault(t *testing.T) {
	for name, arrange := range map[string]func(t *testing.T){
		"no container":         func(t *testing.T) { swapShared(t, nil, errCut) },
		"no connection string": func(t *testing.T) { swapShared(t, onHarness{csErr: errCut}, nil) },
		"no maintenance session": func(t *testing.T) {
			swapShared(t, onHarness{}, nil)
			refuseCall(t, 1)
		},
		"the template read": func(t *testing.T) {
			swapShared(t, onHarness{}, nil)
			cutAt(t, "select datistemplate", false)
		},
		"the migration": func(t *testing.T) {
			swapShared(t, onHarness{}, nil)
			swapImage(t, (&fakeRun{}).run)
		},
		"the passwords": func(t *testing.T) {
			swapShared(t, onHarness{}, nil)
			swapImage(t, pinnedRun(t))
			refuseCall(t, 2)
		},
	} {
		t.Run(name, func(t *testing.T) {
			arrange(t)
			if db, err := Shared(setUp(t)); err == nil {
				db.Stop()
				t.Fatal("the set-up succeeded")
			}
		})
	}
}

// Each fault of a copy of the shared database, or of the seed of its
// template, is the copy's error.
func TestSharedCopyReportsEachFault(t *testing.T) {
	for name, c := range map[string]struct {
		rows    string
		arrange func(t *testing.T)
	}{
		"no maintenance session": {"", func(t *testing.T) { refuseCall(t, 2) }},
		"the seed": {"-- pgtest seam: the seed\n", func(t *testing.T) {
			swapImage(t, (&fakeRun{answers: map[string]answer{"test-fixtures": {code: 1}}}).run)
		}},
		"the copy":          {"", func(t *testing.T) { cutAt(t, `create database "test_c_`, false) }},
		"the settings read": {"", func(t *testing.T) { cutAt(t, "pg_db_role_setting", true) }},
		"a setting's copy":  {"", func(t *testing.T) { cutAt(t, `in database "test_c_`, false) }},
		"the statistics session": {"-- pgtest seam: the statistics session\n", func(t *testing.T) {
			swapImage(t, pinnedRun(t))
			refuseCall(t, 3)
		}},
		"the statistics": {"-- pgtest seam: the statistics\n", func(t *testing.T) {
			swapImage(t, pinnedRun(t))
			cutAt(t, "analyze", false) //nolint:misspell // the SQL keyword
		}},
		"the template's name": {"-- pgtest seam: the template's name\n", func(t *testing.T) {
			swapImage(t, pinnedRun(t))
			cutAt(t, "rename to", false)
		}},
	} {
		t.Run(name, func(t *testing.T) {
			ctx := setUp(t)
			if _, err := Shared(ctx); err != nil { // the shared base, unfaulted
				t.Fatal(err)
			}
			c.arrange(t)
			var db *DB
			var err error
			if c.rows == "" {
				db, err = Shared(ctx)
			} else {
				db, err = Seeded(ctx, c.rows)
			}
			if err == nil {
				db.Stop()
				t.Fatal("the copy succeeded")
			}
		})
	}
}

// A template whose sessions cannot be closed is not marked; a Rollback that
// cannot begin fails its test; a seed runs the pinned image's seed.
func TestTheHarnessStepsReportTheirFaults(t *testing.T) {
	ctx := setUp(t)
	owner, err := pgx.Connect(ctx, harness.urlOf(harness.OwnerURL, "postgres"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = owner.Close(context.Background()) }()
	if _, err := owner.Exec(ctx, "create database pgtest_mark"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = owner.Exec(context.Background(), "alter database pgtest_mark with is_template false allow_connections true")
		_, _ = owner.Exec(context.Background(), "drop database if exists pgtest_mark")
	})
	cutAt(t, "pg_terminate_backend", false)
	maint, err := connect(ctx, harness.urlOf(harness.OwnerURL, "postgres"))
	if err != nil {
		t.Fatal(err)
	}
	if err := markTemplate(ctx, maint, "pgtest_mark"); err == nil {
		t.Error("the sessions of a template were closed on a lost connection")
	}

	cutAt(t, "begin", false)
	ft := &fatalT{}
	func() {
		defer func() { _ = recover() }()
		harness.Rollback(ctx, ft)
	}()
	if !ft.failed {
		t.Error("a Rollback that could not begin passed")
	}

	f := healthyRun()
	swapImage(t, f.run)
	if err := harness.Seed(ctx, "-- no rows\n"); err != nil || len(f.args) != 1 || f.args[0][0] != "test-fixtures" {
		t.Errorf("the seed ran %v: %v", f.args, err)
	}
}
