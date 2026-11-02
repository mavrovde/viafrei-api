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
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/mavrovde/viafrei-api/db/contract"
)

// Image is the PostGIS image the tests run against, the same major versions
// as the compose stack, pinned by digest like the Dockerfile's devdb stage
// (SSC-005; TestTestImagesArePinned holds the two equal).
const Image = "postgis/postgis:18-3.6@sha256:4a43e3c3e5d55b9208240fc71db543b79c638766280c625a425bd0a2390989c4"

// DB is one running test database.
type DB struct {
	// OwnerURL connects as the database owner (a superuser in the container).
	OwnerURL string
	// ReaderURL connects as the read-only role the service uses.
	ReaderURL string
	container testcontainers.Container
}

// Start runs the container, builds the contract stub, creates the reader role
// and publishes the contract version this build expects. On any failure the
// container is removed.
func Start(ctx context.Context) (*DB, error) {
	c, err := postgres.Run(ctx, Image,
		postgres.WithDatabase("contract"),
		postgres.WithUsername("owner"),
		postgres.WithPassword(rand.Text()),
		postgres.BasicWaitStrategies(),
		// The upstream PostGIS image is published for linux/amd64 only, the
		// platform compose.yaml names too; an arm64 host runs it emulated.
		testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{ImagePlatform: "linux/amd64"},
		}),
	)
	db := &DB{container: c}
	if err == nil {
		err = db.finish(ctx, c.ConnectionString)
	}
	if err != nil {
		db.Stop()
		return nil, fmt.Errorf("pgtest: start %s: %w", Image, err)
	}
	return db, nil
}

// finish reads the owner URL from the running container, builds the stub and
// derives the reader URL.
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
	readerPassword := rand.Text()
	if err := prepare(ctx, owner, readerPassword); err != nil {
		return err
	}
	db.OwnerURL = owner
	u.User = url.UserPassword(contract.ReaderRole, readerPassword)
	db.ReaderURL = u.String()
	return nil
}

type step struct {
	sql  string
	args []any
}

func prepare(ctx context.Context, ownerURL, readerPassword string) error {
	conn, err := pgx.Connect(ctx, ownerURL)
	if err != nil {
		return fmt.Errorf("connect as owner: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	steps := []step{
		{sql: contract.SchemaSQL()},
		{sql: contract.ReaderRoleSQL()},
		{sql: fmt.Sprintf("alter role %s password '%s'", contract.ReaderRole, readerPassword)},
		{sql: "insert into contract.contract_info (contract_version, published_at) values ($1, $2)",
			args: []any{contract.Version(), time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}},
	}
	for _, st := range steps {
		if _, err := conn.Exec(ctx, st.sql, st.args...); err != nil {
			return fmt.Errorf("build contract stub: %w", err)
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
