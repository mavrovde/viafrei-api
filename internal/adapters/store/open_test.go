package store_test

import (
	"context"
	"errors"
	"net"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/adapters/store/contractdb"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// closedPortURL is a connection URL nothing listens on: a refused connection
// answers at once, so no test waits for a timeout.
func closedPortURL(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return "postgres://reader:pw@" + addr + "/contract?sslmode=disable"
}

func TestOpenRefusesABadURL(t *testing.T) {
	req.Covers(t, "CFG-007")
	cfg, err := config.Load(config.Options{Environ: []string{"VFAPI_DB_URL=postgres://u:p@h:notaport/x"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(context.Background(), cfg); err == nil {
		t.Fatal("a bad URL opened a pool")
	}
}

func TestUnreachableStorageIsUnavailable(t *testing.T) {
	req.Covers(t, "FR-SVC-004")
	cfg, err := config.Load(config.Options{Environ: []string{"VFAPI_DB_URL=" + closedPortURL(t)}})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	// No deadline on the context: Read applies deadline.request itself.
	err = st.Read(context.Background(), func(context.Context, pgx.Tx) error { return nil })
	if !errors.Is(err, store.ErrUnavailable) {
		t.Fatalf("Read = %v, want ErrUnavailable", err)
	}
}

func TestGeneratedQueriesBindATransaction(t *testing.T) {
	if contractdb.New(nil).WithTx(nil) == nil {
		t.Fatal("WithTx returned nil")
	}
}
