package admission

import (
	"context"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// TestAPermitGrantedAsTheWaitEndsIsKept: a permit handed to a waiter whose
// request ends at the same moment belongs to that waiter, and is released
// through it; it is never lost. Both endings are made ready at once, so the
// waiter's select takes either; each round must keep the permit count.
func TestAPermitGrantedAsTheWaitEndsIsKept(t *testing.T) {
	req.Covers(t, "RATE-008")
	ts := TierSettings{QueueLen: 1, MaxWait: time.Second, MaxInflight: 1}
	for range 64 {
		c := New(Settings{Permits: 1, Tiers: [tierCount]TierSettings{ts, ts, ts}, PerKey: 1, AgingAfter: time.Hour, Alpha: 1}, nil)
		// The first permit is taken back by hand below, not through its release.
		_, err := c.Acquire(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(WithCaller(context.Background(), Caller{Tier: Standard, Key: "k"}))
		done := make(chan func(), 1)
		go func() {
			release, err := c.Acquire(ctx)
			if err != nil {
				t.Error("a granted permit was refused:", err)
			}
			done <- release
		}()
		deadline := time.Now().Add(time.Second)
		for c.Snapshot().Waiting[Standard] == 0 {
			if time.Now().After(deadline) {
				t.Fatal("the waiter never queued")
			}
			time.Sleep(time.Millisecond)
		}
		// Grant and cancel under the lock, so the waiter sees both together.
		c.mu.Lock()
		c.free++
		c.inflight[Critical]--
		delete(c.perKey, "")
		c.dispatch(c.now())
		cancel()
		c.mu.Unlock()
		release := <-done
		if release == nil {
			t.Fatal("no release for a granted permit")
		}
		release()
		if s := c.Snapshot(); s.Free != 1 || s.Waiting[Standard] != 0 {
			t.Fatalf("the permit leaked: %+v", s)
		}
	}
}
