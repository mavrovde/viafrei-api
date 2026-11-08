//go:build integration

package accessstore_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mavrovde/viafrei-api/db/contract"
	"github.com/mavrovde/viafrei-api/internal/adapters/accessstore"
	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/fixtures"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/runtime/logging"
	"github.com/mavrovde/viafrei-api/internal/runtime/wire"
	"github.com/mavrovde/viafrei-api/internal/testkit/pgtest"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// testBound is the ceiling on every test body (TST-017).
const testBound = 2 * time.Second

var db *pgtest.DB

func TestMain(m *testing.M) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	var err error
	db, err = pgtest.Start(ctx)
	if err == nil {
		err = seed(ctx)
	}
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration tests need a container runtime:", err)
		if db != nil {
			db.Stop()
		}
		os.Exit(1)
	}
	code := m.Run()
	db.Stop()
	os.Exit(code)
}

func seed(ctx context.Context) error {
	var buf bytes.Buffer
	if _, err := fixtures.Generate(&buf, "tiny", 1, contract.Version()); err != nil {
		return err
	}
	conn, err := db.Owner(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	return pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, buf.String())
		return err
	})
}

func bounded(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testBound)
	t.Cleanup(cancel)
	return ctx
}

// testPepper is generated per run; no pepper is committed.
var testPepper = strings.Repeat(fmt.Sprintf("%x", time.Now().UnixNano()), 4)

func cfg(t *testing.T, extra ...string) *config.Config {
	t.Helper()
	c, err := config.Load(config.Options{Environ: append([]string{
		"VFAPI_DB_URL=" + db.ReaderURL, "VFAPI_ACCESS_URL=" + db.AccessURL, "VFAPI_AUTH_PEPPER=" + testPepper[:40],
	}, extra...), RequireSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func open(t *testing.T) *accessstore.Store {
	t.Helper()
	s, err := accessstore.Open(context.Background(), cfg(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s
}

func owner(t *testing.T) *pgx.Conn {
	t.Helper()
	conn, err := db.Owner(bounded(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(context.Background()) })
	return conn
}

var planSeq int

// newPlan creates a plan of the use class with every fixture family of the
// given names and every scope.
func newPlan(t *testing.T, k *auth.Keys, use access.UseClass, families ...string) string {
	t.Helper()
	planSeq++
	name := fmt.Sprintf("plan-%d", planSeq)
	if err := k.PutPlan(bounded(t), access.Plan{Name: name, UseClass: use, Tier: "standard", Scopes: access.Scopes, Sources: families}, "tester"); err != nil {
		t.Fatal(err)
	}
	return name
}

func TestSchemaVersion(t *testing.T) {
	s := open(t)
	if err := s.CheckSchema(bounded(t)); err != nil {
		t.Fatal(err)
	}
	o := owner(t)
	if _, err := o.Exec(bounded(t), `insert into access.schema_version (version) values (99)`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = o.Exec(context.Background(), `delete from access.schema_version where version = 99`) }()
	if err := s.CheckSchema(bounded(t)); !errors.Is(err, accessstore.ErrSchema) {
		t.Fatalf("a newer schema: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.CheckSchema(ctx); err == nil {
		t.Fatal("an ended context checked the schema")
	}
}

func TestLifecycleAgainstTheStore(t *testing.T) {
	req.Covers(t, "SEC-010", "SEC-040", "SEC-041", "SEC-042", "SEC-044")
	c := cfg(t)
	s := open(t)
	k := auth.NewKeys(wire.AuthSettings(c), s, nil, nil)
	ctx := bounded(t)
	plan := newPlan(t, k, "consumer", "src-alpha-roads")
	key, err := k.Issue(ctx, plan, "label", "ops@example.test", 0, "tester")
	if err != nil {
		t.Fatal(err)
	}
	rec, err := s.Lookup(ctx, key.ID)
	if err != nil || rec.Plan.Name != plan || rec.Plan.UseClass != "consumer" || len(rec.Plan.Scopes) != len(access.Scopes) {
		t.Fatalf("lookup %+v %v", rec, err)
	}
	if !bytes.Equal(rec.MAC, access.MAC([]byte(testPepper[:40]), key.ID, key.Secret)) {
		t.Fatal("the stored value is not the MAC")
	}
	rotated, err := k.Rotate(ctx, key.ID, "tester")
	if err != nil {
		t.Fatal(err)
	}
	rec, _ = s.Lookup(ctx, key.ID)
	if rec.PreviousMAC == nil || !rec.PreviousUntil.After(time.Now().Add(6*24*time.Hour)) {
		t.Fatalf("rotation did not keep the old secret for the overlap: %+v", rec)
	}
	if _, err := access.Verify(rec, key.Secret, access.Peppers{Current: []byte(testPepper[:40])}, time.Now()); err != nil {
		t.Fatalf("old secret in the overlap: %v", err)
	}
	if _, err := access.Verify(rec, rotated.Secret, access.Peppers{Current: []byte(testPepper[:40])}, time.Now()); err != nil {
		t.Fatalf("new secret: %v", err)
	}
	if err := s.Remac(ctx, key.ID, true, access.MAC([]byte("x"), key.ID, key.Secret)); err != nil {
		t.Fatal(err)
	}
	if err := s.Remac(ctx, key.ID, false, access.MAC([]byte("x"), key.ID, rotated.Secret)); err != nil {
		t.Fatal(err)
	}
	since := time.Now().Add(-time.Second)
	if err := k.Accept(ctx, key.ID, "src-delta-parking", "2026-03-01", "tester"); err != nil {
		t.Fatal(err)
	}
	other := newPlan(t, k, "commercial")
	if err := k.Move(ctx, key.ID, other, "tester"); err != nil {
		t.Fatal(err)
	}
	if err := k.Revoke(ctx, key.ID, "leaked", "tester"); err != nil {
		t.Fatal(err)
	}
	rec, _ = s.Lookup(ctx, key.ID)
	if rec.RevokedAt == nil || rec.Plan.Name != other || len(rec.Accepted) != 1 || rec.Accepted[0].Version != "2026-03-01" {
		t.Fatalf("after accept, move and revoke: %+v", rec)
	}
	ids, mark, err := s.ChangedSince(ctx, since)
	if err != nil || !slices.Contains(ids, key.ID) || mark.Before(since) {
		t.Fatalf("changed since: %v %v %v", ids, mark, err)
	}
	if _, err := k.Rotate(ctx, key.ID, "tester"); !errors.Is(err, auth.ErrInput) {
		t.Fatalf("rotating a revoked key: %v", err)
	}
	if err := k.Revoke(ctx, key.ID, "again", "tester"); !errors.Is(err, accessstore.ErrNotFound) {
		t.Fatalf("revoking twice: %v", err)
	}

	// Every action was audited, and no audit row holds the secret or the MAC.
	o := owner(t)
	rows, _ := o.Query(ctx, `select action, coalesce(plan_before, ''), coalesce(plan_after, ''), actor, coalesce(reason, ''), row_to_json(a)::text
		from access.audit a where key_id = $1 order by audit_id`, key.ID)
	type row struct{ action, before, after, actor, reason, all string }
	got, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (row, error) {
		var v row
		err := r.Scan(&v.action, &v.before, &v.after, &v.actor, &v.reason, &v.all)
		return v, err
	})
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, r := range got {
		actions = append(actions, r.action)
		if strings.Contains(r.all, key.Secret) || strings.Contains(r.all, rotated.Secret) || strings.Contains(r.all, "\\x") {
			t.Fatalf("an audit row holds secret material: %s", r.all)
		}
	}
	if want := []string{"issue", "rotate", "accept_terms", "plan_move", "revoke"}; !slices.Equal(actions, want) {
		t.Fatalf("audit %v, want %v", actions, want)
	}
	if got[3].before != plan || got[3].after != other || got[4].reason != "leaked" || got[0].actor != "tester" {
		t.Fatalf("audit details %+v", got)
	}
}

func TestStoreRefusals(t *testing.T) {
	s := open(t)
	k := auth.NewKeys(wire.AuthSettings(cfg(t)), s, nil, nil)
	ctx := bounded(t)
	bad := "\xff" // not valid UTF-8: the database refuses it as text
	if _, err := s.Lookup(ctx, "nosuchkey0"); !errors.Is(err, auth.ErrUnknown) {
		t.Errorf("unknown: %v", err)
	}
	if _, err := s.Lookup(ctx, bad); err == nil || errors.Is(err, auth.ErrUnknown) {
		t.Errorf("bad id: %v", err)
	}
	if err := k.Move(ctx, "nosuchkey0", "x", "tester"); !errors.Is(err, accessstore.ErrNotFound) {
		t.Errorf("move unknown: %v", err)
	}
	if err := k.Move(ctx, bad, "x", "tester"); err == nil {
		t.Error("move bad id")
	}
	plan := newPlan(t, k, "consumer")
	key, _ := k.Issue(ctx, plan, "l", "c", 0, "tester")
	if err := k.Move(ctx, key.ID, "no-such-plan", "tester"); err == nil {
		t.Error("move to a missing plan")
	}
	if _, err := k.Issue(ctx, "no-such-plan", "l", "c", 0, "tester"); err == nil {
		t.Error("issue on a missing plan")
	}
	now := time.Now()
	for name, err := range map[string]error{
		"revoke bad reason":  s.Revoke(ctx, auth.Change{Action: "revoke", KeyID: key.ID, Actor: "t", Reason: bad, At: now}),
		"rotate bad mac":     s.Rotate(ctx, auth.Change{Action: "rotate", KeyID: key.ID, Actor: "t", At: now}, []byte("short"), now),
		"accept bad id":      s.Accept(ctx, auth.Change{Action: "accept_terms", KeyID: bad, Actor: "t", At: now}, access.Acceptance{Source: "x", Version: "1"}),
		"accept unknown":     s.Accept(ctx, auth.Change{Action: "accept_terms", KeyID: "nosuchkey0", Actor: "t", At: now}, access.Acceptance{Source: "x", Version: "1"}),
		"accept bad source":  s.Accept(ctx, auth.Change{Action: "accept_terms", KeyID: key.ID, Actor: "t", At: now}, access.Acceptance{Source: bad, Version: "1"}),
		"audit bad action":   s.Revoke(ctx, auth.Change{Action: "bogus", KeyID: key.ID, Actor: "t", Reason: "r", At: now}),
		"rotate unknown":     s.Rotate(ctx, auth.Change{Action: "rotate", KeyID: "nosuchkey0", Actor: "t", At: now}, make([]byte, 32), now),
		"plan bad use class": s.PutPlan(ctx, auth.Change{Action: "plan_put", Actor: "t", At: now}, access.Plan{Name: "zz", UseClass: "x", Tier: "bulk"}),
		"changed bad context": func() error {
			c, cancel := context.WithCancel(ctx)
			cancel()
			_, _, err := s.ChangedSince(c, now)
			return err
		}(),
	} {
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if _, err := k.Prune(ctx, "tester"); err != nil {
		t.Fatalf("prune at the configured retention: %v", err)
	}
	if _, err := s.PruneAudit(ctx, auth.Change{Action: "audit_pruned", Actor: "t", At: now}, now); err == nil {
		t.Error("audit records younger than 30 days were pruned")
	}
}

func TestAccessRoleKeepsTheAuditAppendOnly(t *testing.T) {
	req.Covers(t, "SEC-045")
	pool, err := pgxpool.New(bounded(t), db.AccessURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	ctx := bounded(t)
	for _, q := range []string{
		`update access.audit set actor = 'x'`,
		`delete from access.audit`,
		`truncate access.audit`,
		`delete from access.api_key`,
		`select count(*) from contract.source`,
		`create table access.extra (x int)`,
	} {
		if _, err := pool.Exec(ctx, q); err == nil {
			t.Errorf("the access role may run: %s", q)
		}
	}
	if _, err := pool.Exec(ctx, `insert into access.audit (at, action, actor) values (now(), 'plan_put', 'tester')`); err != nil {
		t.Fatalf("the access role cannot append: %v", err)
	}
}

// --------------------------------------------------- the gate, end to end

type stack struct {
	h    http.Handler
	keys *auth.Keys
	log  *bytes.Buffer
}

func newStack(t *testing.T, extra ...string) stack {
	t.Helper()
	c := cfg(t, extra...)
	as := open(t)
	st, err := store.Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	var log bytes.Buffer
	h, err := httpapi.NewHandler(httpapi.Deps{
		Config: c, Sources: st, Auth: auth.New(wire.AuthSettings(c), as, nil),
		Logger: logging.New(c.Log, &log), ContractVersion: contract.Version(),
	})
	if err != nil {
		t.Fatal(err)
	}
	return stack{h: h, keys: auth.NewKeys(wire.AuthSettings(c), as, nil, nil), log: &log}
}

func (s stack) get(t *testing.T, key string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequestWithContext(bounded(t), http.MethodGet, "/v1/key", nil)
	r.Header.Set("Authorization", "Bearer "+key)
	w := httptest.NewRecorder()
	s.h.ServeHTTP(w, r)
	return w
}

func (s stack) families(t *testing.T, key string) []string {
	t.Helper()
	w := s.get(t, key)
	if w.Code != http.StatusOK {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	var body struct {
		Data struct {
			Sources []struct{ Family string } `json:"sources"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, s := range body.Data.Sources {
		out = append(out, s.Family)
	}
	return out
}

func (s stack) issue(t *testing.T, use access.UseClass, families ...string) access.Key {
	t.Helper()
	key, err := s.keys.Issue(bounded(t), newPlan(t, s.keys, use, families...), "l", "c", 0, "tester")
	if err != nil {
		t.Fatal(err)
	}
	return key
}

var allFamilies = []string{"src-alpha-energy", "src-beta-energy", "src-gamma-energy", "src-delta-parking", "src-epsilon-roads", "src-zeta-places", "src-eta-weather"}

func TestGateRestrictedPurposeFamilyEndToEnd(t *testing.T) {
	req.Covers(t, "SEC-030", "SEC-031")
	s := newStack(t)
	for use, served := range map[access.UseClass]bool{"consumer": true, "non_commercial": false, "commercial": false} {
		got := s.families(t, s.issue(t, use, allFamilies...).Full)
		if slices.Contains(got, "src-gamma-energy") != served {
			t.Errorf("%s: %v", use, got)
		}
	}
}

func TestGateTermsFamilyEndToEnd(t *testing.T) {
	req.Covers(t, "SEC-030", "SEC-034")
	s := newStack(t)
	key := s.issue(t, "commercial", allFamilies...)
	if slices.Contains(s.families(t, key.Full), "src-delta-parking") {
		t.Fatal("served before acceptance")
	}
	if err := s.keys.Accept(bounded(t), key.ID, "src-delta-parking", "2025-12-31", "tester"); err != nil {
		t.Fatal(err)
	}
	fresh := newStack(t) // a new instance, no cached copy of the key
	if slices.Contains(fresh.families(t, key.Full), "src-delta-parking") {
		t.Fatal("served after accepting an older terms version")
	}
	if err := s.keys.Accept(bounded(t), key.ID, "src-delta-parking", "2026-03-01", "tester"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(newStack(t).families(t, key.Full), "src-delta-parking") {
		t.Fatal("withheld after accepting the current terms")
	}
}

func TestGateUnreviewedFamilyEndToEnd(t *testing.T) {
	req.Covers(t, "SEC-030")
	s := newStack(t)
	for _, use := range access.UseClasses {
		if slices.Contains(s.families(t, s.issue(t, use, allFamilies...).Full), "src-epsilon-roads") {
			t.Fatalf("unreviewed family served to %s", use)
		}
	}
}

func TestGateDisabledFamilyEndToEnd(t *testing.T) {
	req.Covers(t, "SEC-030")
	s := newStack(t, "VFAPI_SOURCES_DISABLED=src-zeta-places")
	if got := s.families(t, s.issue(t, "consumer", allFamilies...).Full); slices.Contains(got, "src-zeta-places") || !slices.Contains(got, "src-alpha-energy") {
		t.Fatalf("disabled family: %v", got)
	}
}

func TestGateFamilyOutsideThePlanEndToEnd(t *testing.T) {
	req.Covers(t, "SEC-030")
	s := newStack(t)
	if got := s.families(t, s.issue(t, "consumer", "src-alpha-energy").Full); !slices.Equal(got, []string{"src-alpha-energy"}) {
		t.Fatalf("plan with one family read %v", got)
	}
}

func TestKeyRefusalsEndToEnd(t *testing.T) {
	req.Covers(t, "SEC-001", "SEC-004", "SEC-010", "SEC-042")
	s := newStack(t)
	key := s.issue(t, "consumer", "src-alpha-energy")
	if w := s.get(t, key.Full); w.Code != http.StatusOK {
		t.Fatalf("valid key: %d %s", w.Code, w.Body)
	}
	if err := s.keys.Revoke(bounded(t), key.ID, "test", "tester"); err != nil {
		t.Fatal(err)
	}
	if w := newStack(t).get(t, key.Full); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "key-revoked") {
		t.Fatalf("revoked: %d %s", w.Code, w.Body)
	}
	sandbox := newStack(t, "VFAPI_AUTH_ENVIRONMENT=test")
	live := s.issue(t, "consumer", "src-alpha-energy")
	if w := sandbox.get(t, live.Full); w.Code != http.StatusUnauthorized || !strings.Contains(w.Body.String(), "invalid-key") {
		t.Fatalf("a live key on the sandbox: %d %s", w.Code, w.Body)
	}
	if strings.Contains(s.log.String(), key.Secret) || strings.Contains(sandbox.log.String(), live.Secret) {
		t.Fatal("a secret reached the log")
	}
}
