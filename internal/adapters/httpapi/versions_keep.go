package httpapi

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
)

// KeptVersions reads the family versions and the register fingerprint in
// the background (Keep), so no request waits on that read: on prod
// (2026-11-24) a burst of requests that arrived as the last read aged past
// cache.versions_ttl all waited on the one read in flight, and five
// fuel-option pages ended together at 1951 to 1967 ms, 503, behind a read
// the slow log held at 0.6 to 1.5 s. It is a VersionReader: a request whose
// kept read is older than the TTL (the keeper behind or failing) reads the
// versions itself, as before, so an answer is never checked against
// versions older than the TTL.
type KeptVersions struct {
	reader VersionReader
	ttl    time.Duration
	now    func() time.Time
	log    *slog.Logger

	mu   sync.Mutex
	vs   provenance.Versions
	at   time.Time // when the kept read started
	read bool      // vs holds a read
}

// NewKeptVersions keeps the versions reader reads, younger than ttl
// (cache.versions_ttl), logging a failed background read on log.
func NewKeptVersions(reader VersionReader, ttl time.Duration, log *slog.Logger) *KeptVersions {
	return &KeptVersions{reader: reader, ttl: ttl, now: time.Now, log: log}
}

// FreshnessVersions reads the versions on the caller's context: the read of
// a request whose kept read was too old.
func (k *KeptVersions) FreshnessVersions(ctx context.Context) (provenance.Versions, error) {
	return k.reader.FreshnessVersions(ctx)
}

// kept returns the kept read and when it started, and whether there is one.
func (k *KeptVersions) kept() (provenance.Versions, time.Time, bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.vs, k.at, k.read
}

// Keep reads the versions at once and again until ctx ends, each read
// starting late enough to spare the database and early enough that it
// lands before the last one is as old as the TTL: the next start is the
// TTL less the last read's duration and a margin of a tenth of the TTL
// after the last start (next), so with the default 1 s TTL and reads of
// 70 to 120 ms about 75 reads a minute, where a read every half TTL made
// 120. A failed read is logged at warn and tried again at the next start;
// requests read the versions themselves once the kept read is as old as
// the TTL.
func (k *KeptVersions) Keep(ctx context.Context) {
	if k.ttl <= 0 {
		return
	}
	for {
		started := k.now()
		k.step(ctx)
		took := k.now().Sub(started)
		wait := time.NewTimer(k.next(took) - took)
		select {
		case <-ctx.Done():
			wait.Stop()
			return
		case <-wait.C:
		}
	}
}

// next is the time from one read's start to the next one's, given the
// read took took: the TTL less took and a tenth of the TTL, so the next
// read, if it takes as long, lands before the last is as old as the TTL;
// and at least a quarter of the TTL after the last read ended, so a slow
// database is not read back to back (requests then read the versions
// themselves while the kept read is as old as the TTL).
func (k *KeptVersions) next(took time.Duration) time.Duration {
	return max(k.ttl-took-k.ttl/10, took+k.ttl/4)
}

// step is one background read, bounded by the TTL.
func (k *KeptVersions) step(ctx context.Context) {
	at := k.now()
	rctx, cancel := context.WithTimeout(ctx, k.ttl)
	vs, err := k.reader.FreshnessVersions(rctx)
	cancel()
	if err != nil {
		if ctx.Err() == nil {
			k.log.Warn("httpapi: the background versions read failed; requests read the versions themselves once the last read is cache.versions_ttl old", "err", err)
		}
		return
	}
	k.mu.Lock()
	k.vs, k.at, k.read = vs, at, true
	k.mu.Unlock()
}
