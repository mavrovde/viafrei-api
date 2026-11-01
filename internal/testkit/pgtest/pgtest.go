// Package pgtest starts a throwaway PostGIS container for integration tests
// (testcontainers-go, a random host port, removed when the tests end), builds
// the read-contract stub in it and creates the service's read-only role.
//
// It never reaches a shared or long-lived database: the only address it
// knows is the one the container runtime hands back.
package pgtest

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/mavrovde/viafrei-api/db/contract"
)

// Image is the PostGIS image the tests run against, the same major versions
// as the compose stack.
const Image = "postgis/postgis:16-3.4"

// DB is one running test database.
type DB struct {
	// OwnerURL connects as the database owner (a superuser in the container).
	OwnerURL string
	// ReaderURL connects as the read-only role the service uses.
	ReaderURL string
	container testcontainers.Container
}

// Start runs the container, builds the contract stub, creates the reader role
// and publishes the contract version this build expects.
func Start(ctx context.Context) (*DB, error) {
	ownerPassword, err := randomHex()
	if err != nil {
		return nil, err
	}
	c, err := postgres.Run(ctx, Image,
		postgres.WithDatabase("contract"),
		postgres.WithUsername("owner"),
		postgres.WithPassword(ownerPassword),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		if c != nil {
			_ = testcontainers.TerminateContainer(c)
		}
		return nil, fmt.Errorf("pgtest: start %s: %w", Image, err)
	}
	db := &DB{container: c}
	db.OwnerURL, err = c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		db.Stop()
		return nil, fmt.Errorf("pgtest: connection string: %w", err)
	}
	readerPassword, err := randomHex()
	if err != nil {
		db.Stop()
		return nil, err
	}
	if err := db.prepare(ctx, readerPassword); err != nil {
		db.Stop()
		return nil, err
	}
	u, err := url.Parse(db.OwnerURL)
	if err != nil {
		db.Stop()
		return nil, fmt.Errorf("pgtest: parse owner URL: %w", err)
	}
	u.User = url.UserPassword(contract.ReaderRole, readerPassword)
	db.ReaderURL = u.String()
	return db, nil
}

func (db *DB) prepare(ctx context.Context, readerPassword string) error {
	conn, err := pgx.Connect(ctx, db.OwnerURL)
	if err != nil {
		return fmt.Errorf("pgtest: connect as owner: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	steps := []string{
		contract.SchemaSQL(),
		contract.ReaderRoleSQL(),
		// The password is random hex, so quoting it inline is safe.
		fmt.Sprintf("alter role %s password '%s'", contract.ReaderRole, readerPassword),
	}
	for _, sql := range steps {
		if _, err := conn.Exec(ctx, sql); err != nil {
			return fmt.Errorf("pgtest: build contract stub: %w", err)
		}
	}
	if _, err := conn.Exec(ctx, "insert into contract.contract_info (contract_version, published_at) values ($1, $2)",
		contract.Version(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)); err != nil {
		return fmt.Errorf("pgtest: publish contract version: %w", err)
	}
	return nil
}

// Owner opens a connection as the owner, for assertions the reader role must
// not be able to make.
func (db *DB) Owner(ctx context.Context) (*pgx.Conn, error) {
	return pgx.Connect(ctx, db.OwnerURL)
}

// Stop removes the container.
func (db *DB) Stop() {
	if db.container != nil {
		_ = testcontainers.TerminateContainer(db.container)
	}
}

func randomHex() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("pgtest: random: %w", err)
	}
	return hex.EncodeToString(b), nil
}
