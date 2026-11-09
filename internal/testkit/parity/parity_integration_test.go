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

// record rewrites the API's own recording of the questions, the unsynced
// bundle (the regression lock); see docs/parity.md. A platform version's
// bundle is produced on the MCP side.
var record = flag.Bool("parity.record", false, "record the API's answers to test/parity/questions.json as the unsynced bundle")

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

// compare asks every case of the bundle pinned under name and compares the
// facts; it returns the bundle.
func compare(t *testing.T, name string) parity.Bundle {
	t.Helper()
	b, err := parity.Load(os.DirFS(dir), name)
	if err != nil {
		t.Fatal(err)
	}
	q, err := parity.LoadQuestions(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	if d := parity.Answers(b, q); len(d) > 0 {
		t.Fatalf("bundle %s does not answer %s:\n  %s", name, parity.QuestionsFile, strings.Join(d, "\n  "))
	}
	if got := seed(t, b.Fixture); got != b.Fixture.SHA256 {
		t.Fatalf("the fixture rows are %s, the bundle was recorded on %s: re-record the bundle", got, b.Fixture.SHA256)
	}
	a := open(t)
	compared := map[string][]string{}
	var seen []parity.Difference
	for _, c := range b.Cases {
		got, err := parity.Project(a.ask(t, c), parity.Default, c.Request)
		if err != nil {
			t.Fatal(err)
		}
		d, named := parity.Compare(c.Facts, got, parity.Default)
		if len(d) > 0 {
			t.Errorf("case %s (%s, MCP %q): %d of %d facts differ:\n  %s", c.ID, c.Operation, c.Counterpart, len(d), len(c.Facts), strings.Join(d, "\n  "))
		}
		for _, n := range named {
			t.Logf("case %s: named difference %s (%s vs %s): %s", c.ID, n.Pattern, n.Bundle, n.API, n.Reason)
		}
		seen = append(seen, named...)
		compared[c.Operation] = append(compared[c.Operation], c.ID)
	}
	for op, ids := range compared {
		t.Logf("compared %s: %d case(s): %s", op, len(ids), strings.Join(ids, ", "))
	}
	// A named difference stands for a fact the MCP side states otherwise; if
	// no case shows it any more, one side changed and the rule must say so.
	if b.Producer == parity.ProducerMCP {
		for _, d := range parity.Default.Unseen(seen) {
			t.Errorf("named difference %s (%s vs %s) is shown by no case: either side changed, or no question reaches it", d.Pattern, d.Bundle, d.API)
		}
	}
	return b
}

// TestParity asks every case of the bundle pinned for the platform version
// and compares the facts. Only a bundle the MCP side produced is parity
// evidence.
func TestParity(t *testing.T) {
	req.Covers(t, "TEST-009", "FR-GEN-002", "CON-007")
	b := compare(t, platform())
	switch b.Producer {
	case parity.ProducerAPI:
		t.Logf("bundle %s was recorded by the API itself: it is a regression lock, not parity evidence; parity needs a bundle the MCP side produced", platform())
	case parity.ProducerMCP:
		t.Logf("bundle %s was produced by the MCP side at platform commit %s: parity evidence", platform(), b.Commit)
	}

	// A planted change in one projection rule must fail and name the fact.
	a := open(t)
	planted := parity.Default
	planted.PositionDecimals = 4
	got, err := parity.Project(a.ask(t, b.Cases[0]), planted, b.Cases[0].Request)
	if err != nil {
		t.Fatal(err)
	}
	want, err := parity.Project(a.ask(t, b.Cases[0]), parity.Default, b.Cases[0].Request)
	if err != nil {
		t.Fatal(err)
	}
	d := parity.Diff(want, got)
	if len(d) == 0 {
		t.Error("a position rounded to four decimals compared equal")
	}
	for _, line := range d {
		if !strings.HasPrefix(line, "changed data[") || !strings.Contains(line, "].location.la") && !strings.Contains(line, "].location.lo") {
			t.Errorf("a position rounded to four decimals named another fact: %s", line)
		}
	}
}

// TestRegressionLock compares the API's own recording (the unsynced bundle,
// -parity.record): it locks the API's answers against regressions and is no
// evidence of parity.
func TestRegressionLock(t *testing.T) {
	req.Covers(t, "TEST-009")
	if *record {
		recordBundle(t)
	}
	if b := compare(t, buildinfo.Unsynced); b.Producer != parity.ProducerAPI {
		t.Errorf("the regression lock %s is produced by %q; it is the API's own recording", buildinfo.Unsynced, b.Producer)
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
	findings, err := parity.Inventory(doc, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) > 0 {
		t.Errorf("the bundle does not cover the document:\n  %s", strings.Join(findings, "\n  "))
	}
}

// recordBundle records the API's own answers to the questions as the
// unsynced bundle, the regression lock. It never writes the bundle of a
// platform version: that one is the MCP side's.
func recordBundle(t *testing.T) {
	t.Helper()
	q, err := parity.LoadQuestions(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	name := buildinfo.Unsynced
	b := parity.Bundle{Format: parity.Format, Platform: name, Producer: parity.ProducerAPI, Rules: parity.Default.ID, Fixture: q.Fixture}
	if got := seed(t, b.Fixture); got != q.Fixture.SHA256 {
		t.Fatalf("the fixture rows are %s, %s names %s", got, parity.QuestionsFile, q.Fixture.SHA256)
	}
	a := open(t)
	for _, qq := range q.Cases {
		c := parity.Case{ID: qq.ID, Operation: qq.Operation, Counterpart: qq.Counterpart, Request: qq.Request, Key: qq.Key, Now: qq.Now}
		facts, err := parity.Project(a.ask(t, c), parity.Default, c.Request)
		if err != nil {
			t.Fatal(err)
		}
		c.Facts = facts
		b.Cases = append(b.Cases, c)
	}
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	file := filepath.Join("bundles", name+".json")
	if err := os.WriteFile(filepath.Join(dir, file), raw, 0o600); err != nil {
		t.Fatal(err)
	}
	lock := "# Parity bundles pinned per platform version: platform, bundle, sha256.\n" +
		"# Pinned by the version sync; see docs/parity.md.\n"
	old, _ := os.ReadFile(filepath.Join(dir, parity.LockFile))
	for _, line := range strings.Split(string(old), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") && !strings.HasPrefix(line, name+"\t") {
			lock += line + "\n"
		}
	}
	lock += name + "\t" + file + "\t" + parity.Checksum(raw) + "\n"
	if err := os.WriteFile(filepath.Join(dir, parity.LockFile), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
}
