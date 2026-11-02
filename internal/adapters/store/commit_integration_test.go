//go:build integration

package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// TestAbortedReadIsNotCommitted: a read whose statement failed inside fn,
// with the failure swallowed, ends in a refused commit and never in success.
func TestAbortedReadIsNotCommitted(t *testing.T) {
	req.Covers(t, "NFR-SEC-004")
	ctx := bounded(t)
	st := openStore(t, ctx)
	err := st.Read(ctx, func(ctx context.Context, tx pgx.Tx) error {
		_, _ = tx.Exec(ctx, "delete from contract.contract_info")
		return nil
	})
	if !errors.Is(err, store.ErrUnavailable) || !errors.Is(err, pgx.ErrTxCommitRollback) {
		t.Fatalf("Read = %v, want a refused commit", err)
	}
}
