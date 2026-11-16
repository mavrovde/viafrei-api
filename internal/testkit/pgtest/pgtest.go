// Package pgtest starts a throwaway PostGIS container for integration tests
// (testcontainers-go, a random host port, removed when the tests end),
// migrates it with the viafrei-database release database.pin names and
// gives the service's two roles a password.
//
// Everything in that database is the pinned image's: `version` must name
// the pinned release and head, `migrate` applies every migration it carries
// (the platform's tables, the read contract of schema contract with its
// role api_reader, the access store of schema access with its role
// api_access) and the role settings it gives each role in this database,
// `partitions ensure` makes the daily partitions from PartitionsFrom on,
// and `head` must print READY <head>. This package runs no DDL: tests seed
// the platform's own tables, which the contract's views read.
//
// It makes no copy of a database (CREATE DATABASE … TEMPLATE): a copy would
// hold none of the settings the image sets per role IN DATABASE. Each
// package's container is migrated by the image itself.
//
// It never reaches a shared or long-lived database: the only address it
// knows is the one the container runtime hands back.
package pgtest

import (
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/mavrovde/viafrei-api/internal/dbcontract"
)

// Image is the PostGIS image every test and development database runs on,
// the platform's own (postgis/postgis:16-3.4), pinned by digest like the
// Dockerfile's devdb stage (SSC-005; TestTestImagesArePinned holds the two
// equal). It is published for linux/amd64 only; an arm64 host runs it
// emulated.
const Image = "postgis/postgis:16-3.4@sha256:44126d872ac91993766c341e369c539e8196614321765d36a6f1bab0419a5fa5"

// PartitionsFrom is the first day the daily partitions are made for: the day
// before the synthetic fixtures' first day (fixtures.ReferenceInstant), so a
// tick of any fixture day, in UTC or Berlin time, has its partition.
const PartitionsFrom = "2026-03-27"

// maxLocks is the lock table the container gets: making the partitions from
// PartitionsFrom to today is one transaction over every tick table and day.
const maxLocks = "max_locks_per_transaction=4096"

// DB is one running test database.
type DB struct {
	// OwnerURL connects as the database owner (a superuser in the container).
	OwnerURL string
	// ReaderURL connects as the read-only role the service uses.
	ReaderURL string
	// AccessURL connects as the API's own access-store role.
	AccessURL string
	// Pin is the viafrei-database release the database was migrated by.
	Pin       Pin
	container testcontainers.Container
}

// Start reads database.pin, runs the container, migrates it with the
// pinned image and gives the reader and access roles a password. On any
// failure the container is removed.
func Start(ctx context.Context) (*DB, error) {
	wd, _ := os.Getwd() // an empty directory finds no module, and ReadPin says so
	pin, err := ReadPin(wd)
	if err != nil {
		return nil, fmt.Errorf("pgtest: %w", err)
	}
	c, err := postgres.Run(ctx, Image,
		postgres.WithDatabase("contract"),
		postgres.WithUsername("owner"),
		postgres.WithPassword(rand.Text()),
		postgres.BasicWaitStrategies(),
		testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				ImagePlatform: "linux/amd64",
				Cmd:           []string{"-c", "fsync=off", "-c", maxLocks},
			},
		}),
	)
	db := &DB{container: c, Pin: pin}
	if err == nil {
		err = db.migrate(ctx, c.ContainerIP, c.ConnectionString)
	}
	if err == nil {
		err = db.finish(ctx, c.ConnectionString)
	}
	if err != nil {
		db.Stop()
		return nil, fmt.Errorf("pgtest: start %s: %w", Image, err)
	}
	return db, nil
}

// migrate runs the pinned image's steps against the container, over the
// container network, as the owner.
func (db *DB) migrate(ctx context.Context, containerIP func(context.Context) (string, error), connString func(context.Context, ...string) (string, error)) error {
	ip, err := containerIP(ctx)
	if err != nil {
		return fmt.Errorf("container address: %w", err)
	}
	owner, err := connString(ctx, "sslmode=disable")
	if err != nil {
		return fmt.Errorf("connection string: %w", err)
	}
	u, err := url.Parse(owner)
	if err != nil {
		return fmt.Errorf("owner URL: %w", err)
	}
	u.Host = net.JoinHostPort(ip, "5432")
	return MigrateWith(ctx, db.Pin, u.String(), RunImage)
}

// Runner runs ref with env and args to its end and returns what it wrote
// and its exit status.
type Runner func(ctx context.Context, ref string, env map[string]string, args ...string) (string, int, error)

// MigrateWith migrates the database at dbURL with the pinned release, run
// by run: the image must say it is the pinned release, its migrate and
// partitions steps must succeed, and its head must print READY <head>.
func MigrateWith(ctx context.Context, pin Pin, dbURL string, run Runner) error {
	out, code, err := run(ctx, pin.Ref, nil, "version")
	if err := stepError("version", out, code, err); err != nil {
		return err
	}
	if got := lastLine(out); got != pin.VersionLine() {
		return fmt.Errorf("the pinned image says %q, %s says %q", got, PinFile, pin.VersionLine())
	}
	env := map[string]string{"DATABASE_URL": dbURL}
	for _, step := range [][]string{{"migrate"}, {"partitions", "ensure", "--days", "7", "--from", PartitionsFrom}, {"head"}} {
		out, code, err = run(ctx, pin.Ref, env, step...)
		if err := stepError(strings.Join(step, " "), out, code, err); err != nil {
			return err
		}
	}
	if got := lastLine(out); got != pin.Ready() {
		return fmt.Errorf("head printed %q, not %q (%s)", got, pin.Ready(), PinFile)
	}
	return nil
}

func stepError(step, out string, code int, err error) error {
	switch {
	case err != nil:
		return fmt.Errorf("the pinned image's %s: %w", step, err)
	case code != 0:
		return fmt.Errorf("the pinned image's %s exited %d: %s", step, code, lastLine(out))
	}
	return nil
}

// lastLine is the last line that is not blank: the answer of a step whose
// progress lines come before it.
func lastLine(out string) string {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

// RunImage is the Runner of the container runtime: ref runs on the default
// network to its exit, and its combined output is returned.
func RunImage(ctx context.Context, ref string, env map[string]string, args ...string) (string, int, error) {
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:      ref,
			Env:        env,
			Cmd:        args,
			WaitingFor: wait.ForExit(), // bounded by ctx, the caller's bound on setup
		},
		Started: true,
	})
	defer func() { _ = testcontainers.TerminateContainer(c) }()
	if err != nil {
		return "", 0, err
	}
	return readExit(ctx, c)
}

// exited is what RunImage reads of a container that has run to its end.
type exited interface {
	State(context.Context) (*container.State, error)
	Logs(context.Context) (io.ReadCloser, error)
}

func readExit(ctx context.Context, c exited) (string, int, error) {
	state, err := c.State(ctx)
	if err != nil {
		return "", 0, err
	}
	logs, err := c.Logs(ctx)
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = logs.Close() }()
	out, err := io.ReadAll(logs)
	return string(out), state.ExitCode, err
}

// finish reads the owner URL from the running container, gives the two
// roles the image created a password and derives their URLs.
func (db *DB) finish(ctx context.Context, connString func(context.Context, ...string) (string, error)) error {
	owner, err := connString(ctx, "sslmode=disable")
	if err != nil {
		return fmt.Errorf("connection string: %w", err)
	}
	u, err := url.Parse(owner)
	if err != nil {
		return fmt.Errorf("owner URL: %w", err)
	}
	// rand.Text is base32: safe inside a quoted SQL literal and a URL.
	readerPassword, accessPassword := rand.Text(), rand.Text()
	if err := passwords(ctx, owner, readerPassword, accessPassword); err != nil {
		return err
	}
	db.OwnerURL = owner
	u.User = url.UserPassword(dbcontract.ReaderRole, readerPassword)
	db.ReaderURL = u.String()
	u.User = url.UserPassword(dbcontract.AccessRole, accessPassword)
	db.AccessURL = u.String()
	return nil
}

// passwords sets the password of each role, as the image's roles step does
// in production from its secret files.
func passwords(ctx context.Context, ownerURL, readerPassword, accessPassword string) error {
	conn, err := pgx.Connect(ctx, ownerURL)
	if err != nil {
		return fmt.Errorf("connect as owner: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	for role, pw := range map[string]string{dbcontract.ReaderRole: readerPassword, dbcontract.AccessRole: accessPassword} {
		if _, err := conn.Exec(ctx, fmt.Sprintf("alter role %s password '%s'", role, pw)); err != nil {
			return fmt.Errorf("set the password of %s: %w", role, err)
		}
	}
	return nil
}

// Owner opens a connection as the owner, for assertions the reader role must
// not be able to make.
func (db *DB) Owner(ctx context.Context) (*pgx.Conn, error) {
	return pgx.Connect(ctx, db.OwnerURL)
}

// Stop removes the container; it does nothing when none was started.
func (db *DB) Stop() {
	_ = testcontainers.TerminateContainer(db.container)
}
