package accessstore

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mavrovde/viafrei-api/internal/domain/refresh"
)

// The on-demand refreshes (G8) live in the store's live schema, in the shape
// of the live refresher's request protocol: the API records a request
// (live.refresh_requests) and links its asker (live.refresh_askers); the
// live refresher claims it (claimed_at), refreshes and settles it, and the
// database notifies live_refresh_settled with the request's id. One open
// request per source and subject key is held by a unique index, so
// concurrent askers share one row. The API's role inserts and reads; it
// never claims or settles, and deletes only through the retention function.

// SettledChannel is the channel a settle notifies, with the request's id.
const SettledChannel = "live_refresh_settled"

// requestColumns are the columns scanRequest reads, with the queue position
// of an open request: 0 while claimed, else 1 + the unclaimed open requests
// made before it.
const requestColumns = `r.id::text, r.source, r.state, r.created_at, r.settled_at, r.reason, r.reused,
  case when r.settled_at is not null then null
       when r.claimed_at is not null then 0
       else 1 + (select count(*) from live.refresh_requests p
                 where p.settled_at is null and p.claimed_at is null and (p.created_at, p.id) < (r.created_at, r.id))
  end`

func scanRequest(row pgx.Row) (refresh.Request, error) {
	var (
		r        refresh.Request
		state    string
		reason   *string
		position *int64
	)
	if err := row.Scan(&r.ID, &r.Source, &state, &r.CreatedAt, &r.SettledAt, &reason, &r.Reused, &position); err != nil {
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
	id, reused, err := join(ctx, s.pool, n, freshSince)
	if err != nil {
		return refresh.Request{}, err
	}
	if _, err := s.pool.Exec(ctx, `insert into live.refresh_askers (request_id, key_id, asked_at) values ($1::bigint, $2, $3) on conflict do nothing`,
		id, n.KeyID, n.At); err != nil {
		return refresh.Request{}, err
	}
	r, err := s.waitSettled(ctx, id, wait)
	r.Reused = r.Reused || reused
	return r, err
}

// join returns the id answering n, and whether it is a request asked
// before: the newest refresh settled refreshed or none_due since
// freshSince, else the open one, else n recorded.
//
// An open request seen by the insert's conflict can settle before it is
// read; the next round then finds it fresh or records a new one, so the
// loop ends but for a race of settles faster than a round trip.
func join(ctx context.Context, conn *pgxpool.Pool, n refresh.New, freshSince time.Time) (string, bool, error) {
	for {
		var id string
		if !freshSince.IsZero() {
			err := conn.QueryRow(ctx, `select id::text from live.refresh_requests
				where source = $1 and subject_key = $2 and state in ('refreshed', 'none_due') and settled_at >= $3
				order by settled_at desc, id desc limit 1`, n.Source, n.SubjectKey, freshSince).Scan(&id)
			if err == nil || !errors.Is(err, pgx.ErrNoRows) {
				return id, true, err
			}
		}
		err := conn.QueryRow(ctx, `insert into live.refresh_requests (source, subject, subject_key, created_at, requested_by)
			values ($1, $2, $3, $4, $5) on conflict do nothing returning id::text`,
			n.Source, subject(n.Subject), n.SubjectKey, n.At, n.RequestedBy).Scan(&id)
		if err == nil || !errors.Is(err, pgx.ErrNoRows) {
			return id, false, err
		}
		err = conn.QueryRow(ctx, `select id::text from live.refresh_requests
			where source = $1 and subject_key = $2 and settled_at is null`, n.Source, n.SubjectKey).Scan(&id)
		if err == nil || !errors.Is(err, pgx.ErrNoRows) {
			return id, true, err
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

// read reads the request as it stands.
func read(ctx context.Context, q interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}, id string) (refresh.Request, error) {
	return scanRequest(q.QueryRow(ctx, `select `+requestColumns+` from live.refresh_requests r where r.id = $1::bigint`, id))
}

// waitSettled reads the request, and while it is open waits up to wait for
// its settle (the store's one settle listener pokes it) and reads it again.
// The wait is registered before the first read, so a settle between the
// read and the wait is not missed.
func (s *Store) waitSettled(ctx context.Context, id string, wait time.Duration) (refresh.Request, error) {
	if wait <= 0 {
		return read(ctx, s.pool, id)
	}
	poke := s.settles.add(id)
	defer s.settles.remove(id, poke)
	r, err := read(ctx, s.pool, id)
	if err != nil || r.State.Settled() {
		return r, err
	}
	wctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
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

// requestID is the shape of a request's id: a positive bigint.
var requestID = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)

// Refresh implements features.RefreshStore.
func (s *Store) Refresh(ctx context.Context, id, keyID string) (refresh.Request, error) {
	if !requestID.MatchString(id) {
		return refresh.Request{}, refresh.ErrNotFound
	}
	r, err := scanRequest(s.pool.QueryRow(ctx, `select `+requestColumns+` from live.refresh_requests r
		join live.refresh_askers a on a.request_id = r.id where r.id = $1::bigint and a.key_id = $2`, id, keyID))
	if errors.Is(err, pgx.ErrNoRows) {
		return refresh.Request{}, refresh.ErrNotFound
	}
	return r, err
}

// RefreshRetention is how long a refresh request is kept after it was made,
// in any state: the store refuses to prune a younger one.
const RefreshRetention = 30 * 24 * time.Hour

// PruneRefreshes removes the refresh requests made before before, in any
// state, with their askers, and returns how many.
func (s *Store) PruneRefreshes(ctx context.Context, before time.Time) (int64, error) {
	var n int64
	if err := s.pool.QueryRow(ctx, `select live.prune_refresh_requests($1)`, before).Scan(&n); err != nil {
		return 0, fmt.Errorf("accessstore: prune refreshes: %w", err)
	}
	return n, nil
}

// RunRefreshRetention prunes the refresh requests older than
// RefreshRetention at once and then every interval until ctx ends,
// reporting each round's count or error. A round cuts at the database's own
// clock, the one its prune function measures the 30 days by, so a service
// clock running ahead of it never has a round refused.
func (s *Store) RunRefreshRetention(ctx context.Context, every time.Duration, report func(int64, error)) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		var n int64
		err := s.pool.QueryRow(ctx, `select live.prune_refresh_requests(now() - make_interval(secs => $1))`, RefreshRetention.Seconds()).Scan(&n)
		if err != nil {
			err = fmt.Errorf("accessstore: prune refreshes: %w", err)
		}
		report(n, err)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
