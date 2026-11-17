package pgtest

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"

	"github.com/mavrovde/viafrei-api/internal/dbcontract"
)

// The shared database: one container per test run (every package of one
// `go test`, which share testcontainers' session), migrated once. Each
// fixture set is seeded once into a template database, and every caller
// gets its own copy (CREATE DATABASE … TEMPLATE), so no package migrates or
// seeds what another already has. Under the arm64 emulation of the amd64
// PostGIS image a migration and a seed cost minutes each; a copy costs
// seconds.

// sessionSecret derives a value of this run from testcontainers' session id
// and what: the shared container's name and passwords are the same in every
// package process of one run, and differ between runs.
func sessionSecret(what string) string {
	h := sha256.Sum256([]byte("viafrei-api pgtest " + testcontainers.SessionID() + " " + what))
	return hex.EncodeToString(h[:16])
}

// sharedName is the shared container's name, of this run and this pin.
func sharedName(pin Pin) string {
	return "viafrei-api-pgtest-" + sessionSecret(pin.Ref)[:16]
}

// SetupBound bounds a package's database set-up: joining the shared
// container may wait on another package's migration and seed, which the
// emulated image takes minutes for.
const SetupBound = 15 * time.Minute

// sharedContainer is what the shared set-up reads of its container.
type sharedContainer interface {
	ConnectionString(ctx context.Context, args ...string) (string, error)
	ContainerIP(ctx context.Context) (string, error)
}

// The seams of the shared set-up: how it runs its container, opens a
// session and runs the pinned image. A test of the package swaps one for a
// fake that fails where it is asked to.
var (
	runShared = func(ctx context.Context, password string, extra ...testcontainers.ContainerCustomizer) (sharedContainer, error) {
		return runContainer(ctx, password, extra...)
	}
	connect         = pgx.Connect
	runImage Runner = RunImage
)

// lockKey serialises the shared container's set-up steps across the run's
// package processes (pg_advisory_lock on the maintenance database).
const lockKey = 7_040_001

// Shared returns a copy of the migrated, empty database of the run's shared
// container. Stop drops the copy; the container ends with the run.
func Shared(ctx context.Context) (*DB, error) {
	return sharedCopy(ctx, "")
}

// Seeded returns a copy of the database seeded with rows (as Seed writes
// them): the run seeds each distinct set once, into a template the copies
// are made from.
func Seeded(ctx context.Context, rows string) (*DB, error) {
	return sharedCopy(ctx, rows)
}

func sharedCopy(ctx context.Context, rows string) (*DB, error) {
	base, err := sharedBase(ctx)
	if err != nil {
		return nil, err
	}
	maint, err := connect(ctx, base.urlOf(base.OwnerURL, "postgres"))
	if err != nil {
		return nil, fmt.Errorf("pgtest: connect to the maintenance database: %w", err)
	}
	defer func() { _ = maint.Close(context.WithoutCancel(ctx)) }()
	template := Database
	if rows != "" {
		h := sha256.Sum256([]byte(rows))
		template = "test_tpl_" + hex.EncodeToString(h[:8])
		if err := locked(ctx, maint, func() error { return base.seedTemplate(ctx, maint, template, rows) }); err != nil {
			return nil, err
		}
	}
	name := "test_c_" + strings.ToLower(rand.Text())[:16]
	if _, err := maint.Exec(ctx, "create database "+ident(name)+" template "+ident(template)); err != nil {
		return nil, fmt.Errorf("pgtest: copy %s: %w", template, err)
	}
	if err := copySettings(ctx, maint, name); err != nil {
		return nil, err
	}
	return base.copyNamed(name), nil
}

// copySettings gives each role in the copy name the settings the migrated
// database gives it (ALTER ROLE … IN DATABASE): CREATE DATABASE … TEMPLATE
// copies none of them, and the reader's (its statement bound, its search
// path) are part of what the image migrates.
func copySettings(ctx context.Context, maint *pgx.Conn, name string) error {
	// A failed query is the rows' error, which CollectRows returns.
	rows, _ := maint.Query(ctx, `select r.rolname, split_part(c, '=', 1), substr(c, strpos(c, '=') + 1)
  from pg_db_role_setting s
  join pg_database d on d.oid = s.setdatabase and d.datname = $1
  join pg_roles r on r.oid = s.setrole, unnest(s.setconfig) c`, Database)
	stmts, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (string, error) {
		var role, key, value string
		err := r.Scan(&role, &key, &value)
		return "alter role " + ident(role) + " in database " + ident(name) +
			" set " + ident(key) + " = " + quoteLiteral(value), err
	})
	if err != nil {
		return fmt.Errorf("pgtest: read the role settings: %w", err)
	}
	for _, q := range stmts {
		if _, err := maint.Exec(ctx, q); err != nil {
			return fmt.Errorf("pgtest: copy a role setting to %s: %w", name, err)
		}
	}
	return nil
}

// ident is name quoted as an SQL identifier.
func ident(name string) string {
	return pgx.Identifier{name}.Sanitize() //nolint:misspell // the pgx method's name
}

// quoteLiteral is v as an SQL string literal.
func quoteLiteral(v string) string {
	return "'" + strings.ReplaceAll(v, "'", "''") + "'"
}

// sharedBase runs, or joins, the run's shared container, and migrates its
// database once, marking it the template of every copy.
func sharedBase(ctx context.Context) (*DB, error) {
	wd, _ := os.Getwd() // an empty directory finds no module, and ReadPin says so
	pin, err := ReadPin(wd)
	if err != nil {
		return nil, fmt.Errorf("pgtest: %w", err)
	}
	c, err := runShared(ctx, sessionSecret("owner"), testcontainers.WithReuseByName(sharedName(pin)))
	if err != nil {
		return nil, fmt.Errorf("pgtest: shared %s: %w", Image, err)
	}
	db := &DB{Pin: pin, shared: true}
	if err := db.urls(ctx, c); err != nil {
		return nil, err
	}
	maint, err := connect(ctx, db.urlOf(db.OwnerURL, "postgres"))
	if err != nil {
		return nil, fmt.Errorf("pgtest: connect to the maintenance database: %w", err)
	}
	defer func() { _ = maint.Close(context.WithoutCancel(ctx)) }()
	err = locked(ctx, maint, func() error {
		var done bool
		if err := maint.QueryRow(ctx, "select datistemplate from pg_database where datname = $1", Database).Scan(&done); err != nil {
			return fmt.Errorf("pgtest: read the shared database: %w", err)
		}
		if done {
			return nil
		}
		if err := MigrateWith(ctx, pin, db.imageURL, runImage); err != nil {
			return fmt.Errorf("pgtest: migrate the shared database: %w", err)
		}
		if err := passwords(ctx, db.OwnerURL, sessionSecret(dbcontract.ReaderRole), sessionSecret(dbcontract.AccessRole)); err != nil {
			return err
		}
		return markTemplate(ctx, maint, Database)
	})
	if err != nil {
		return nil, err
	}
	return db, nil
}

// seedTemplate seeds rows into the template name, unless the run has: into
// a copy of the migrated database, marked a template once seeded.
func (db *DB) seedTemplate(ctx context.Context, maint *pgx.Conn, name, rows string) error {
	var n int
	if err := maint.QueryRow(ctx, "select count(*) from pg_database where datname = $1 and datistemplate", name).Scan(&n); err != nil {
		return fmt.Errorf("pgtest: read template %s: %w", name, err)
	}
	if n == 1 {
		return nil
	}
	build := name + "_b"
	if _, err := maint.Exec(ctx, "drop database if exists "+ident(build)); err != nil {
		return fmt.Errorf("pgtest: drop %s: %w", build, err)
	}
	if _, err := maint.Exec(ctx, "create database "+ident(build)+" template "+ident(Database)); err != nil {
		return fmt.Errorf("pgtest: create %s: %w", build, err)
	}
	if err := SeedWith(ctx, db.Pin, db.urlOf(db.imageURL, build), rows, runImage); err != nil {
		return err
	}
	// A copy carries the template's statistics: gathered here once, a copy's
	// first reads are planned as a database autovacuum has read would be.
	if err := analyseStats(ctx, db.urlOf(db.OwnerURL, build)); err != nil {
		return err
	}
	if _, err := maint.Exec(ctx, "alter database "+ident(build)+" rename to "+ident(name)); err != nil {
		return fmt.Errorf("pgtest: name template %s: %w", name, err)
	}
	return markTemplate(ctx, maint, name)
}

// analyseStats gathers the statistics of the database at dbURL.
func analyseStats(ctx context.Context, dbURL string) error {
	conn, err := connect(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("pgtest: connect to analyse: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if _, err := conn.Exec(ctx, "analyze"); err != nil { //nolint:misspell // the SQL keyword
		return fmt.Errorf("pgtest: analyse: %w", err)
	}
	return nil
}

// markTemplate makes name a template no one connects to, so a copy is never
// refused for a session left on it.
func markTemplate(ctx context.Context, maint *pgx.Conn, name string) error {
	if _, err := maint.Exec(ctx, "alter database "+ident(name)+" with is_template true allow_connections false"); err != nil {
		return fmt.Errorf("pgtest: mark %s a template: %w", name, err)
	}
	if _, err := maint.Exec(ctx, "select pg_terminate_backend(pid) from pg_stat_activity where datname = $1 and pid <> pg_backend_pid()", name); err != nil {
		return fmt.Errorf("pgtest: close the sessions on %s: %w", name, err)
	}
	return nil
}

// locked runs f holding the run's set-up lock.
func locked(ctx context.Context, maint *pgx.Conn, f func() error) error {
	if _, err := maint.Exec(ctx, "select pg_advisory_lock($1)", lockKey); err != nil {
		return fmt.Errorf("pgtest: take the set-up lock: %w", err)
	}
	defer func() { _, _ = maint.Exec(context.WithoutCancel(ctx), "select pg_advisory_unlock($1)", lockKey) }()
	return f()
}

// urls sets the owner, reader, access and image URLs of the shared
// container's database; the roles' passwords are the run's.
func (db *DB) urls(ctx context.Context, c sharedContainer) error {
	owner, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return fmt.Errorf("pgtest: connection string: %w", err)
	}
	db.OwnerURL = owner
	db.ReaderURL = withUser(owner, dbcontract.ReaderRole, sessionSecret(dbcontract.ReaderRole))
	db.AccessURL = withUser(owner, dbcontract.AccessRole, sessionSecret(dbcontract.AccessRole))
	return db.imageAddress(ctx, c.ContainerIP, owner)
}

// copyNamed is db pointed at its copy name.
func (db *DB) copyNamed(name string) *DB {
	return &DB{Pin: db.Pin, shared: true, copyOf: db.OwnerURL, copyName: name,
		OwnerURL: db.urlOf(db.OwnerURL, name), ReaderURL: db.urlOf(db.ReaderURL, name),
		AccessURL: db.urlOf(db.AccessURL, name), imageURL: db.urlOf(db.imageURL, name)}
}

// urlOf is u naming the database name.
func (db *DB) urlOf(u, name string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	p.Path = "/" + name
	return p.String()
}

// withUser is u with the user and password.
func withUser(u, user, password string) string {
	p, err := url.Parse(u)
	if err != nil {
		return u
	}
	p.User = url.UserPassword(user, password)
	return p.String()
}

// dropCopy drops a copy, ending the sessions still on it.
func (db *DB) dropCopy(ctx context.Context) {
	maint, err := connect(ctx, db.urlOf(db.copyOf, "postgres"))
	if err != nil {
		return
	}
	defer func() { _ = maint.Close(ctx) }()
	_, _ = maint.Exec(ctx, "drop database if exists "+ident(db.copyName)+" with (force)")
}

// Rollback opens a transaction as the owner that t's cleanup rolls back:
// a test that writes to arrange its case shares the package's copy with the
// read-only tests and leaves nothing behind.
func (db *DB) Rollback(ctx context.Context, t interface {
	Helper()
	Fatal(...any)
	Cleanup(func())
}) pgx.Tx {
	t.Helper()
	conn, err := connect(ctx, db.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		_ = conn.Close(ctx)
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		_ = conn.Close(context.WithoutCancel(ctx))
	})
	return tx
}
