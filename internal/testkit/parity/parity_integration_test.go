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
	// Generating and loading the questions' fixture rows is setup too: it
	// took about 1.3 s inside the first test that asked for them, which
	// pushed that test past its bound.
	if err := seedQuestions(); err != nil {
		db.Stop()
		fmt.Fprintln(os.Stderr, "seeding the parity fixture:", err)
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

// seedQuestions loads the fixture rows the questions name, before any test.
func seedQuestions() error {
	q, err := parity.LoadQuestions(os.DirFS(dir))
	if err != nil {
		return err
	}
	_, err = load(q.Fixture)
	return err
}

// seed returns the checksum of the fixture rows the bundle names, loading
// them unless they are loaded already.
func seed(t *testing.T, fx parity.Fixture) string {
	t.Helper()
	sum, err := load(fx)
	if err != nil {
		t.Fatal(err)
	}
	return sum
}

// load loads the fixture rows of fx, once, and returns their checksum.
func load(fx parity.Fixture) (string, error) {
	fx.SHA256 = ""
	if sum, ok := seeded[fx]; ok {
		return sum, nil
	}
	var buf bytes.Buffer
	if _, err := fixtures.Generate(&buf, fx.Profile, fx.Seed, contract.Version()); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := db.Owner(ctx)
	if err != nil {
		return "", err
	}
	defer func() { _ = conn.Close(ctx) }()
	if err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, buf.String())
		return err
	}); err != nil {
		return "", err
	}
	seeded[fx] = parity.Checksum(buf.Bytes())
	return seeded[fx], nil
}

// asker answers a case through the real handler, store and use cases.
type asker struct {
	cfg    *config.Config
	st     *store.Store
	owners parity.Owners
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
	return asker{cfg: cfg, st: st, owners: noteOwners(t)}
}

// noteOwners reads which families' condition notes carry each text, from the
// fixture rows: the API states its notes as texts, and rules/4 states a
// condition note by its family. A kind the service does not know is a
// condition (provenance.MapNoteKind), so every kind but purpose is read.
func noteOwners(t *testing.T) parity.Owners {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testBound)
	defer cancel()
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	rows, _ := conn.Query(ctx, `select body, source_ref from contract.source_note where note_kind <> 'purpose' order by body, source_ref`)
	pairs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[struct{ Body, Family string }])
	if err != nil {
		t.Fatal(err)
	}
	out := parity.Owners{}
	for _, p := range pairs {
		out[p.Body] = append(out[p.Body], p.Family)
	}
	return out
}

// compare asks every case of the bundle pinned under name and compares the
// facts; it returns the bundle and the asker it used, whose store and
// connections a caller reuses rather than opening a second one.
func compare(t *testing.T, name string) (parity.Bundle, asker) {
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
	facts, differ := 0, 0
	for _, c := range b.Cases {
		got, err := parity.Project(a.ask(t, c), parity.Default, c.Request, a.owners)
		if err != nil {
			t.Fatal(err)
		}
		d := parity.Diff(c.Facts, got)
		if len(d) > 0 {
			t.Errorf("case %s (%s, MCP %q): %d of %d facts differ:\n  %s", c.ID, c.Operation, c.Counterpart, len(d), len(c.Facts), strings.Join(d, "\n  "))
		}
		facts += len(c.Facts)
		differ += len(d)
		compared[c.Operation] = append(compared[c.Operation], c.ID)
	}
	for op, ids := range compared {
		t.Logf("compared %s: %d case(s): %s", op, len(ids), strings.Join(ids, ", "))
	}
	t.Logf("bundle %s: %d case(s), %d fact(s) compared, %d difference(s)", name, len(b.Cases), facts, differ)
	return b, a
}

// TestParity asks every case of the bundle pinned for the platform version
// and compares the facts. Only a bundle the MCP side produced is parity
// evidence.
func TestParity(t *testing.T) {
	req.Covers(t, "TEST-009", "FR-GEN-002", "CON-007")
	b, a := compare(t, platform())
	switch b.Producer {
	case parity.ProducerAPI:
		t.Logf("bundle %s was recorded by the API itself: it is a regression lock, not parity evidence; parity needs a bundle the MCP side produced", platform())
	case parity.ProducerMCP:
		t.Logf("bundle %s was produced by the MCP side at platform commit %s: parity evidence", platform(), b.Commit)
	}

	// A planted change in one projection rule must fail and name the fact.
	planted := parity.Default
	planted.PositionDecimals = 4
	answer := a.ask(t, b.Cases[0])
	got, err := parity.Project(answer, planted, b.Cases[0].Request, a.owners)
	if err != nil {
		t.Fatal(err)
	}
	want, err := parity.Project(answer, parity.Default, b.Cases[0].Request, a.owners)
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
	if b, _ := compare(t, buildinfo.Unsynced); b.Producer != parity.ProducerAPI {
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
		facts, err := parity.Project(a.ask(t, c), parity.Default, c.Request, a.owners)
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
	// Every other line is kept where it stands, its comments too: only the
	// regression lock's own line is rewritten.
	lock := ""
	old, _ := os.ReadFile(filepath.Join(dir, parity.LockFile))
	for _, line := range strings.Split(string(old), "\n") {
		if line != "" && !strings.HasPrefix(line, name+"\t") {
			lock += line + "\n"
		}
	}
	if lock == "" {
		lock = "# Parity bundles pinned per platform version: platform, bundle, sha256.\n" +
			"# Pinned by the version sync; see docs/parity.md.\n"
	}
	lock += name + "\t" + file + "\t" + parity.Checksum(raw) + "\n"
	if err := os.WriteFile(filepath.Join(dir, parity.LockFile), []byte(lock), 0o600); err != nil {
		t.Fatal(err)
	}
}
