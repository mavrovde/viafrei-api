//go:build integration

package parity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/db/contract"
	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/buildinfo"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/fixtures"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/runtime/wire"
	"github.com/mavrovde/viafrei-api/internal/testkit/parity"
	"github.com/mavrovde/viafrei-api/internal/testkit/pgtest"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// record rewrites the pinned bundle from the questions below. It is how a
// bundle is made until the MCP side produces one; see docs/parity.md.
var record = flag.Bool("parity.record", false, "record the parity bundle of this platform version from the questions in the harness")

// dir is the parity directory, relative to this package.
const dir = "../../../test/parity"

const testBound = 2 * time.Second

var db *pgtest.DB

func TestMain(m *testing.M) {
	flag.Parse()
	// Starting the container is setup, outside every test body's bound.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	var err error
	db, err = pgtest.Start(ctx)
	cancel()
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration tests need a container runtime:", err)
		os.Exit(1)
	}
	code := m.Run()
	db.Stop()
	os.Exit(code)
}

// platform is the lock file's name for the pinned platform version.
func platform() string {
	if p := buildinfo.Get().Platform; p != "" {
		return p
	}
	return buildinfo.Unsynced
}

var seeded = map[parity.Fixture]string{}

// seed loads the fixture rows the bundle names, once, and returns their
// checksum.
func seed(t *testing.T, fx parity.Fixture) string {
	t.Helper()
	fx.SHA256 = ""
	if sum, ok := seeded[fx]; ok {
		return sum
	}
	var buf bytes.Buffer
	if _, err := fixtures.Generate(&buf, fx.Profile, fx.Seed, contract.Version()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, buf.String())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	seeded[fx] = parity.Checksum(buf.Bytes())
	return seeded[fx]
}

// asker answers a case through the real handler, store and use cases.
type asker struct {
	cfg *config.Config
	st  *store.Store
}

type keyAuth struct{ p auth.Principal }

func (k keyAuth) Authenticate(context.Context, string, string) (auth.Principal, error) {
	return k.p, nil
}

func principal(c parity.Case) auth.Principal {
	k := access.Record{
		ID: "parityharn", Environment: access.Live, ExpiresAt: c.Now.Add(time.Hour),
		Plan: access.Plan{Name: "parity", UseClass: access.UseClass(c.Key.UseClass), Tier: "standard", Sources: c.Key.Sources},
	}
	for _, s := range c.Key.Scopes {
		k.Plan.Scopes = append(k.Plan.Scopes, access.Scope(s))
	}
	for _, a := range c.Key.Accepted {
		k.Accepted = append(k.Accepted, access.Acceptance{Source: a.Source, Version: a.Version})
	}
	return auth.Principal{Key: k}
}

func (a asker) ask(t *testing.T, c parity.Case) []byte {
	t.Helper()
	now := func() time.Time { return c.Now }
	h, err := httpapi.NewHandler(httpapi.Deps{
		Config: a.cfg, Build: buildinfo.Info{Version: "parity", Commit: "parity"}, ContractVersion: contract.Version(), Now: now,
		Auth: keyAuth{principal(c)}, Sources: a.st,
		Features: features.New(features.Repositories{Register: a.st, Charging: a.st, Parking: a.st, Places: a.st}, wire.FeatureSettings(a.cfg), now),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testBound)
	defer cancel()
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, c.Request, nil)
	r.Header.Set("Authorization", "Bearer parity")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body, _ := io.ReadAll(w.Body)
	if w.Code != http.StatusOK {
		t.Fatalf("%s: %s answered %d: %s", c.ID, c.Request, w.Code, body)
	}
	return body
}

func open(t *testing.T) asker {
	t.Helper()
	cfg, err := config.Load(config.Options{Environ: []string{"VFAPI_DB_URL=" + db.ReaderURL}, RequireSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testBound)
	defer cancel()
	st, err := store.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return asker{cfg: cfg, st: st}
}

// TestParity asks every case of the pinned bundle and compares the facts.
func TestParity(t *testing.T) {
	req.Covers(t, "TEST-009", "FR-GEN-002", "CON-007")
	if *record {
		recordBundle(t)
	}
	b, err := parity.Load(os.DirFS(dir), platform())
	if err != nil {
		t.Fatal(err)
	}
	if got := seed(t, b.Fixture); got != b.Fixture.SHA256 {
		t.Fatalf("the fixture rows are %s, the bundle was recorded on %s: re-record the bundle", got, b.Fixture.SHA256)
	}
	a := open(t)
	compared := map[string][]string{}
	for _, c := range b.Cases {
		got, err := parity.Project(a.ask(t, c), parity.Default)
		if err != nil {
			t.Fatal(err)
		}
		if d := parity.Diff(c.Facts, got); len(d) > 0 {
			t.Errorf("case %s (%s, MCP %q): %d of %d facts differ:\n  %s", c.ID, c.Operation, c.Counterpart, len(d), len(c.Facts), strings.Join(d, "\n  "))
		}
		compared[c.Operation] = append(compared[c.Operation], c.ID)
	}
	for op, ids := range compared {
		t.Logf("compared %s: %d case(s): %s", op, len(ids), strings.Join(ids, ", "))
	}

	// A planted change in one projection rule must fail and name the fact.
	planted := parity.Default
	planted.PositionDecimals = 4
	got, err := parity.Project(a.ask(t, b.Cases[0]), planted)
	if err != nil {
		t.Fatal(err)
	}
	d := parity.Diff(b.Cases[0].Facts, got)
	if len(d) == 0 {
		t.Error("a position rounded to four decimals compared equal")
	}
	for _, line := range d {
		if !strings.HasPrefix(line, "changed data[") || !strings.Contains(line, "].location.la") && !strings.Contains(line, "].location.lo") {
			t.Errorf("a position rounded to four decimals named another fact: %s", line)
		}
	}
}

// TestInventory: every operation with an MCP counterpart has a case.
func TestInventory(t *testing.T) {
	req.Covers(t, "TEST-009")
	b, err := parity.Load(os.DirFS(dir), platform())
	if err != nil {
		t.Fatal(err)
	}
	doc, err := os.ReadFile("../../../api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	missing, err := parity.Inventory(doc, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) > 0 {
		t.Errorf("operations with an MCP counterpart and no parity case: %v", missing)
	}
}

// questions are the parity questions; record mode answers them.
var questions = []parity.Case{
	{
		ID: "charging-public-commercial", Operation: "listChargingOptions", Counterpart: "charging station search",
		Request: "/v1/charging-options?at=50.82,6.0&radius_m=20000",
		Key:     parity.Key{UseClass: "commercial", Scopes: []string{"energy"}, Sources: []string{"src-alpha-energy", "src-beta-energy", "src-gamma-energy"}},
	},
	{
		ID: "charging-all-consumer-fast", Operation: "listChargingOptions", Counterpart: "charging station search",
		Request: "/v1/charging-options?at=48.41,7.68&radius_m=30000&access=all&min_kw=50&limit=3",
		Key:     parity.Key{UseClass: "consumer", Scopes: []string{"energy"}, Sources: []string{"src-alpha-energy", "src-beta-energy", "src-gamma-energy"}},
	},
	{
		ID: "parking-terms-pending", Operation: "listParkingOptions", Counterpart: "parking search",
		Request: "/v1/parking-options?at=50.39,10.23&radius_m=50000",
		Key:     parity.Key{UseClass: "commercial", Scopes: []string{"parking"}, Sources: []string{"src-alpha-parking", "src-beta-parking", "src-delta-parking"}},
	},
	{
		ID: "parking-terms-accepted", Operation: "listParkingOptions", Counterpart: "parking search",
		Request: "/v1/parking-options?at=50.39,10.23&radius_m=50000",
		Key: parity.Key{UseClass: "commercial", Scopes: []string{"parking"}, Sources: []string{"src-alpha-parking", "src-beta-parking", "src-delta-parking"},
			Accepted: []parity.Acceptance{{Source: "src-delta-parking", Version: "2026-03-01"}}},
	},
}

func recordBundle(t *testing.T) {
	t.Helper()
	b := parity.Bundle{Format: parity.Format, Platform: platform(), Rules: parity.Default.ID, Fixture: parity.Fixture{Profile: "tiny", Seed: 1}}
	b.Fixture.SHA256 = seed(t, b.Fixture)
	a := open(t)
	for _, q := range questions {
		q.Now = fixtures.ReferenceInstant.AddDate(0, 0, 2)
		facts, err := parity.Project(a.ask(t, q), parity.Default)
		if err != nil {
			t.Fatal(err)
		}
		q.Facts = facts
		b.Cases = append(b.Cases, q)
	}
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	name := filepath.Join("bundles", platform()+".json")
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	lock := "# Parity bundles pinned per platform version: platform, bundle, sha256.\n" +
		"# Pinned by the version sync; see docs/parity.md.\n"
	old, _ := os.ReadFile(filepath.Join(dir, parity.LockFile))
	for _, line := range strings.Split(string(old), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, platform()+"\t") {
			lock += line + "\n"
		}
	}
	lock += platform() + "\t" + name + "\t" + parity.Checksum(raw) + "\n"
	if err := os.WriteFile(filepath.Join(dir, parity.LockFile), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
}
