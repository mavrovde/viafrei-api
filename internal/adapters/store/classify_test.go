package store

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestClassify(t *testing.T) {
	req.Covers(t, "NFR-PERF-004")
	done, cancel := context.WithCancel(context.Background())
	cancel()
	cases := []struct {
		name string
		ctx  context.Context
		err  error
		want error
	}{
		{"context ended", done, errors.New("x"), ErrDeadlineExhausted},
		{"deadline error", context.Background(), context.DeadlineExceeded, ErrDeadlineExhausted},
		{"statement timeout", context.Background(), &pgconn.PgError{Code: "57014"}, ErrDeadlineExhausted},
		{"other server error", context.Background(), &pgconn.PgError{Code: "42501"}, ErrUnavailable},
		{"connection error", context.Background(), errors.New("refused"), ErrUnavailable},
	}
	for _, c := range cases {
		if got := classify(c.ctx, c.err); !errors.Is(got, c.want) || !errors.Is(got, c.err) {
			t.Errorf("%s: classify = %v, want %v wrapping the cause", c.name, got, c.want)
		}
	}
}
