package store

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mavrovde/viafrei-api/internal/app/admission"
	"github.com/mavrovde/viafrei-api/internal/app/breaker"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

func TestVerdictOfARead(t *testing.T) {
	req.Covers(t, "NFR-RES-002")
	gone, cancel := context.WithCancel(context.Background())
	cancel()
	live := context.Background()
	unavailable := func(err error) error { return fmt.Errorf("%w: %w", ErrUnavailable, err) }
	sqlstate := func(code string) error { return unavailable(&pgconn.PgError{Code: code}) }
	reset := unavailable(&net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset by peer")})
	late := fmt.Errorf("%w: %w", ErrDeadlineExhausted, context.DeadlineExceeded)
	cases := []struct {
		name    string
		ctx     context.Context
		reached stage
		err     error
		want    breaker.Outcome
	}{
		{"refused before storage", live, refused, ErrDeadlineExhausted, breaker.Ignored},
		{"the caller went away while the transaction was opened", gone, begun, late, breaker.Ignored},
		{"storage could not be reached", live, begun, reset, breaker.Failure},
		{"storage did not answer the begin in time", live, begun, late, breaker.Failure},
		{"the connection was reset inside the transaction", live, opened, reset, breaker.Failure},
		{"the stream ended inside the transaction", live, opened, unavailable(io.ErrUnexpectedEOF), breaker.Failure},
		{"the connection was closed inside the transaction", live, opened, unavailable(net.ErrClosed), breaker.Failure},
		{"connection failure (08006)", live, opened, sqlstate("08006"), breaker.Failure},
		{"too many connections (53300)", live, opened, sqlstate("53300"), breaker.Failure},
		{"system error (58030)", live, opened, sqlstate("58030"), breaker.Failure},
		{"internal error (XX000)", live, opened, sqlstate("XX000"), breaker.Failure},
		{"administrator shutdown (57P01)", live, opened, sqlstate("57P01"), breaker.Failure},
		{"crash shutdown (57P02)", live, opened, sqlstate("57P02"), breaker.Failure},
		{"cannot connect now (57P03)", live, opened, sqlstate("57P03"), breaker.Failure},
		{"one statement named a missing relation (42P01)", live, opened, sqlstate("42P01"), breaker.Success},
		{"one statement divided by zero (22012)", live, opened, sqlstate("22012"), breaker.Success},
		{"a statement was cancelled by its timeout (57014)", live, opened, sqlstate("57014"), breaker.Success},
		{"a malformed SQLSTATE", live, opened, sqlstate("08"), breaker.Success},
		{"a row the code could not decode", live, opened, unavailable(errors.New("can't scan into dest[0]")), breaker.Success},
		{"a read shed by the shared budget", live, opened, admission.ErrOverloaded, breaker.Success},
		{"a statement ran out of time", live, opened, late, breaker.Success},
		{"a cancelled read", live, opened, unavailable(context.Canceled), breaker.Success},
		{"the caller's deadline passed as the connection read failed", gone, opened, reset, breaker.Success},
		{"the read succeeded", live, opened, nil, breaker.Success},
	}
	for _, c := range cases {
		if got := verdict(c.ctx, c.reached, c.err); got != c.want {
			t.Errorf("%s: verdict %d, want %d", c.name, got, c.want)
		}
	}
}
