package store

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

// deadlineCut names a read cut only when its deadline cut it: the statement
// timeout (57014) or the context's deadline. A read whose request the client
// left (a canceled context) is no cut, even when the server answers that
// cancel with 57014 and the read returns through its own result.
func TestDeadlineCutIsTheDeadlineAlone(t *testing.T) {
	timeout := fmt.Errorf("read: %w", &pgconn.PgError{Code: "57014"})
	live := context.Background()
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, c := range []struct {
		name string
		ctx  context.Context
		err  error
		want bool
	}{
		{"statement timeout", live, timeout, true},
		{"context deadline", live, fmt.Errorf("read: %w", context.DeadlineExceeded), true},
		{"another server error", live, &pgconn.PgError{Code: "40001"}, false},
		{"no error", live, nil, false},
		{"another error", live, errors.New("broken"), false},
		{"a client's cancel answered with 57014", canceled, timeout, false},
		{"a client's cancel", canceled, context.Canceled, false},
	} {
		if got := deadlineCut(c.ctx, c.err); got != c.want {
			t.Errorf("%s: deadlineCut %v, want %v", c.name, got, c.want)
		}
	}
}
