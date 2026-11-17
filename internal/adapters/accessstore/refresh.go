package accessstore

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mavrovde/viafrei-api/internal/domain/refresh"
)

// The on-demand refreshes (G8) live in the store's live schema: the API
// records a request, the live refresher claims and settles it, and the
// database notifies the row's channel, refresh_<id as 32 hex>, when it
// settles. One open request per source and subject key is held by a unique
// index, so concurrent askers share one row.

// requestColumns are the columns refreshRow scans, with the queue position
// of an open request: 0 while claimed, else 1 + the pending requests created
// before it.
const requestColumns = `r.request_id::text, r.source, r.state, r.created_at, r.settled_at, r.reason,
  case r.state when 'claimed' then 0
               when 'pending' then 1 + (select count(*) from live.refresh_request p
                                        where p.state = 'pending' and (p.created_at, p.request_id) < (r.created_at, r.request_id))
  end`

func scanRequest(row pgx.Row) (refresh.Request, error) {
	var (
		r        refresh.Request
		state    string
		reason   *string
		position *int64
	)
	if err := row.Scan(&r.ID, &r.Source, &state, &r.CreatedAt, &r.SettledAt, &reason, &position); err != nil {
		return refresh.Request{}, err
	}
	r.State = refresh.State(state)
	r.CreatedAt = r.CreatedAt.UTC()
	if r.SettledAt != nil {
		t := r.SettledAt.UTC()
		r.SettledAt = &t
	}
	if reason != nil {
		r.Reason = *reason
	}
	if position != nil {
		p := int(*position)
		r.Position = &p
	}
	return r, nil
}

// Request implements features.RefreshStore.
func (s *Store) Request(ctx context.Context, n refresh.New, freshSince time.Time, wait time.Duration) (refresh.Request, error) {
	id, err := join(ctx, s.pool, n, freshSince)
	if err != nil {
		return refresh.Request{}, err
	}
	if _, err := s.pool.Exec(ctx, `insert into live.refresh_asker (request_id, key_id, asked_at) values ($1, $2, $3) on conflict do nothing`,
		id, n.KeyID, n.At); err != nil {
		return refresh.Request{}, err
	}
	return s.waitSettled(ctx, id, wait)
}

// join returns the id answering n: the newest refresh settled refreshed or
// none_due since freshSince, else the open one, else n recorded.
//
// An open request seen by the insert's conflict can settle before it is
// read; the next round then finds it fresh or records a new one, so the
// loop ends but for a race of settles faster than a round trip.
func join(ctx context.Context, conn *pgxpool.Pool, n refresh.New, freshSince time.Time) (string, error) {
	for {
		var id string
		if !freshSince.IsZero() {
			err := conn.QueryRow(ctx, `select request_id::text from live.refresh_request
				where source = $1 and subject_key = $2 and state in ('refreshed', 'none_due') and settled_at >= $3
				order by settled_at desc limit 1`, n.Source, n.SubjectKey, freshSince).Scan(&id)
			if err == nil || !errors.Is(err, pgx.ErrNoRows) {
				return id, err
			}
		}
		err := conn.QueryRow(ctx, `insert into live.refresh_request (request_id, source, subject, subject_key, created_at)
			values ($1, $2, $3, $4, $5) on conflict do nothing returning request_id::text`,
			n.ID, n.Source, subject(n.Subject), n.SubjectKey, n.At).Scan(&id)
		if err == nil || !errors.Is(err, pgx.ErrNoRows) {
			return id, err
		}
		err = conn.QueryRow(ctx, `select request_id::text from live.refresh_request
			where source = $1 and subject_key = $2 and state in ('pending', 'claimed')`, n.Source, n.SubjectKey).Scan(&id)
		if err == nil || !errors.Is(err, pgx.ErrNoRows) {
			return id, err
		}
	}
}

// subject is the subject as stored: an object, never null.
func subject(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

// channel is the request's settle channel.
func channel(id string) string { return "refresh_" + strings.ReplaceAll(id, "-", "") }

// read reads the request as it stands.
func read(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string) (refresh.Request, error) {
	return scanRequest(q.QueryRow(ctx, `select `+requestColumns+` from live.refresh_request r where r.request_id = $1`, id))
}

// waitSettled reads the request, and while it is open waits up to wait for
// its settle (the store's one settle listener pokes it) and reads it again.
func (s *Store) waitSettled(ctx context.Context, id string, wait time.Duration) (refresh.Request, error) {
	r, err := read(ctx, s.pool, id)
	if err != nil || r.State.Settled() || wait <= 0 {
		return r, err
	}
	wctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	ch := channel(id)
	poke := s.settles.add(ch)
	defer s.settles.remove(ch, poke)
	for {
		select {
		case <-poke:
			if r, err := read(ctx, s.pool, id); err != nil || r.State.Settled() {
				return r, err
			}
		case <-wctx.Done():
			if ctx.Err() != nil {
				return refresh.Request{}, ctx.Err()
			}
			return read(ctx, s.pool, id) // the wait is over: answer it as it stands
		}
	}
}

// Refresh implements features.RefreshStore.
func (s *Store) Refresh(ctx context.Context, id, keyID string) (refresh.Request, error) {
	if _, err := uuid.Parse(id); err != nil {
		return refresh.Request{}, refresh.ErrNotFound
	}
	r, err := scanRequest(s.pool.QueryRow(ctx, `select `+requestColumns+` from live.refresh_request r
		join live.refresh_asker a using (request_id) where r.request_id = $1 and a.key_id = $2`, id, keyID))
	if errors.Is(err, pgx.ErrNoRows) {
		return refresh.Request{}, refresh.ErrNotFound
	}
	return r, err
}

// RefreshRetention is how long a settled refresh is kept: the store
// refuses to prune a younger one.
const RefreshRetention = 30 * 24 * time.Hour

// PruneRefreshes removes the refreshes settled before before, with their
// askers, and returns how many.
func (s *Store) PruneRefreshes(ctx context.Context, before time.Time) (int64, error) {
	var n int64
	if err := s.pool.QueryRow(ctx, `select live.prune_refresh_requests($1)`, before).Scan(&n); err != nil {
		return 0, fmt.Errorf("accessstore: prune refreshes: %w", err)
	}
	return n, nil
}
