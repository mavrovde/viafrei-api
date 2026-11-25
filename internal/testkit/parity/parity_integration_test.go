//go:build integration

package parity_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/adapters/accessstore"
	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/buildinfo"
	"github.com/mavrovde/viafrei-api/internal/dbcontract"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/domain/rules"
	"github.com/mavrovde/viafrei-api/internal/fixtures"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/runtime/wire"
	"github.com/mavrovde/viafrei-api/internal/testkit/fakelive"
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
	// Generating and loading the questions' fixture rows is setup too: it
	// took about 1.3 s inside the first test that asked for them, which
	// pushed that test past its bound.
	if err := seedQuestions(); err != nil {
		fmt.Fprintln(os.Stderr, "seeding the parity fixture:", err)
		os.Exit(1)
	}
	fake, err := startFake()
	if err != nil {
		fmt.Fprintln(os.Stderr, "starting the fake live refresher:", err)
		db.Stop()
		os.Exit(1)
	}
	code := m.Run()
	if err := fake.Stop(); err != nil {
		fmt.Fprintln(os.Stderr, "the fake live refresher:", err)
		code = 1
	}
	if refreshStore != nil {
		refreshStore.Close()
	}
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
	if _, err := fixtures.Generate(&buf, fx.Profile, fx.Seed, dbcontract.ContractVersion); err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), pgtest.SetupBound)
	defer cancel()
	// The first set is the run's shared copy of it; a later one is seeded
	// into that copy.
	if db == nil {
		var err error
		if db, err = pgtest.Seeded(ctx, buf.String()); err != nil {
			return "", err
		}
	} else if err := db.Seed(ctx, buf.String()); err != nil {
		return "", err
	}
	if err := derive(ctx); err != nil {
		return "", err
	}
	seeded[fx] = parity.Checksum(buf.Bytes())
	return seeded[fx], nil
}

// derived are the platform's views its workers refresh after every load
// (the stops' modes the stop boards pick by, the routes' districts, the
// untimed trips, the street tiles): the fixture is the platform's state
// only once they are refreshed, as the platform's side of the kit
// refreshes them after its load.
var derived = []string{"gtfs_route_kreise", "gtfs_untimed_trips", "transit_stop_modes", "osm_street_tiles", "osm_street_tiles_lo"}

// derive refreshes the derived views over the questions' database.
func derive(ctx context.Context) error {
	conn, err := pgx.Connect(ctx, db.OwnerURL)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(ctx) }()
	for _, v := range derived {
		if _, err := conn.Exec(ctx, "refresh materialized view public."+v); err != nil { //nolint:misspell // the SQL keyword
			return fmt.Errorf("refresh %s: %w", v, err)
		}
	}
	return nil
}

// The repository's refresh registry and the fake live refresher's script
// (test/live): a question's refresh is settled by the fake, never a provider.
var (
	liveSources = filepath.Join("..", "..", "..", "test", "live", "registry.json")
	liveScript  = filepath.Join("..", "..", "..", "test", "live", "fakelive.json")
)

// parityPepper is the harness's key pepper: the harness authenticates no
// key by it; the configuration requires one where refreshes are recorded.
const parityPepper = "parity-run-pepper-not-a-secret-0123456789"

// script is the fake's script, read once: a question's refresh is undone
// after its read by the tables the script names for its source.
var script fakelive.Script

// refreshStore is the access store a question's refresh is recorded in.
var refreshStore *accessstore.Store

// startFake records the harness's key (a refresh names its asker) and runs
// the fake live refresher over the questions' database.
func startFake() (*fakelive.Fake, error) {
	ctx, cancel := context.WithTimeout(context.Background(), pgtest.SetupBound)
	defer cancel()
	conn, err := pgx.Connect(ctx, db.OwnerURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = conn.Close(ctx) }()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := conn.Exec(ctx, `insert into access.plan (plan_name, use_class, tier, scopes, sources, changed_at, rate_per_second, burst, quota_day, quota_month)
		values ('parity', 'commercial', 'standard', '{}', '{}', $1, 1, 1, 0, 0)`, at); err != nil {
		return nil, err
	}
	if _, err := conn.Exec(ctx, `insert into access.api_key (key_id, environment, plan_name, label, contact, mac, issued_at, expires_at, changed_at, audience)
		values ($2, 'live', 'parity', 'parity', 'parity@example.test', decode(repeat('00', 32), 'hex'), $1, $1::timestamptz + interval '3650 days', $1, 'own')`, at, harnessKey); err != nil {
		return nil, err
	}
	if script, err = fakelive.Load(liveSources, liveScript); err != nil {
		return nil, err
	}
	return fakelive.Start(ctx, db.OwnerURL, script)
}

// asker answers a case through the real handler, store and use cases.
type asker struct {
	cfg       *config.Config
	st        *store.Store
	owners    parity.Owners
	refreshes features.Refreshes
}

type keyAuth struct{ p auth.Principal }

func (k keyAuth) Authenticate(context.Context, string, string) (auth.Principal, error) {
	return k.p, nil
}

// harnessKey is the key every question is asked as; its plan is the case's.
const harnessKey = "parityharn"

func principal(c parity.Case) auth.Principal {
	k := access.Record{
		ID: harnessKey, Environment: access.Live, ExpiresAt: c.Now.Add(time.Hour),
		Plan:     access.Plan{Name: "parity", UseClass: access.UseClass(c.Key.UseClass), Tier: "standard", Sources: c.Key.Sources},
		Audience: access.Audience(c.Key.Audience),
	}
	for _, s := range c.Key.Scopes {
		k.Plan.Scopes = append(k.Plan.Scopes, access.Scope(s))
	}
	return auth.Principal{Key: k}
}

func (a asker) ask(t *testing.T, c parity.Case) []byte {
	t.Helper()
	now := func() time.Time { return c.Now }
	svc := features.New(features.Repositories{Register: a.st, Charging: a.st, Parking: a.st, Places: a.st, Resolver: a.st, Roads: a.st, Weather: a.st, Rules: a.st, Transit: a.st, Fuel: a.st, Hydrogen: a.st, Speed: a.st, Sharing: a.st, Addresses: a.st, Pois: a.st, Locations: a.st, Disruptions: a.st, Nearby: a.st, Lorry: a.st, Catalogue: a.st, National: a.st, Affected: a.st, Coverage: a.st, States: a.st, TrainBoards: a.st, Bays: a.st}, wire.FeatureSettings(a.cfg), now)
	svc.WithRefreshes(a.refreshes)
	h, err := httpapi.NewHandler(httpapi.Deps{
		Config: a.cfg, Build: buildinfo.Info{Version: "parity", Commit: "parity"}, ContractVersion: dbcontract.ContractVersion, Now: now,
		Auth: keyAuth{principal(c)}, Sources: a.st,
		Features: svc,
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.Refresh != nil {
		undo := refresh(t, h, c)
		defer undo()
	}
	ctx, cancel := context.WithTimeout(context.Background(), testBound)
	defer cancel()
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, c.Request, nil)
	r.Header.Set("Authorization", "Bearer parity")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body, _ := io.ReadAll(w.Body)
	// A question the platform refuses as unknown (a tool's not_found) is
	// answered 404 and compared as a refusal; any other status is a fault.
	if w.Code != http.StatusOK && w.Code != http.StatusNotFound {
		t.Fatalf("%s: %s answered %d: %s", c.ID, c.Request, w.Code, body)
	}
	return body
}

// refresh asks the case's refresh (createRefresh) as the case's key and
// requires it settled refreshed: a question's read is compared after a
// refresh that brought data, never beside one still pending or refused. It
// returns the undo the caller runs after the read: the rows the fake wrote
// at the case's clock and the recorded request are removed, so every pass
// over the questions (TestParity, then TestRegressionLock) reads the same
// rows, and a later pass's refresh is asked anew, not joined.
func refresh(t *testing.T, h http.Handler, c parity.Case) func() {
	t.Helper()
	tables := script.Sources[c.Refresh.Source].Partitions
	if view, ok := tickViews[c.Refresh.Source]; ok {
		tables = append(slices.Clone(tables), ownerText(t, baseTable, view))
	}
	for _, tb := range tables {
		if n := ownerCount(t, `select count(*) from `+pgx.Identifier(strings.Split(tb, ".")).Sanitize()+` where observed_at = $1`, c.Now); n != 0 { //nolint:misspell // the pgx method's name
			t.Fatalf("%s: %s holds %d fixture row(s) at %s; a refresh question is asked at a clock no fixture row is dated", c.ID, tb, n, c.Now.Format(time.RFC3339))
		}
	}
	body, err := json.Marshal(c.Refresh)
	if err != nil {
		t.Fatal(err)
	}
	// The refresh waits on the fake's settle (refresh.wait), outside the
	// read's bound.
	ctx, cancel := context.WithTimeout(context.Background(), pgtest.SetupBound)
	defer cancel()
	r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/v1/refreshes", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Authorization", "Bearer parity")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var got struct {
		Data struct {
			State string `json:"state"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || w.Code != http.StatusCreated || got.Data.State != "refreshed" {
		t.Fatalf("%s: the refresh %s answered %d: %s", c.ID, body, w.Code, w.Body.Bytes())
	}
	return func() {
		type stmt struct {
			sql  string
			args []any
		}
		stmts := []stmt{
			{`delete from live.refresh_askers where request_id in (select id from live.refresh_requests where source = $1 and created_at = $2)`, []any{c.Refresh.Source, c.Now}},
			{`delete from live.refresh_requests where source = $1 and created_at = $2`, []any{c.Refresh.Source, c.Now}},
		}
		for _, tb := range tables {
			stmts = append(stmts, stmt{`delete from ` + pgx.Identifier(strings.Split(tb, ".")).Sanitize() + ` where observed_at = $1`, []any{c.Now}}) //nolint:misspell // the pgx method's name
		}
		if latest, ok := restatedLatest[c.Refresh.Source]; ok {
			ticks := pgx.Identifier(strings.Split(tables[len(tables)-1], ".")).Sanitize() //nolint:misspell // the pgx method's name
			stmts = append(stmts, stmt{`update ` + latest + ` l set observed_at = t.at
  from (select station_id, product, max(observed_at) at from ` + ticks + ` group by 1, 2) t
  where l.observed_at = $1 and t.station_id = l.station_id and t.product = l.product`, []any{c.Now}})
		}
		ctx, cancel := context.WithTimeout(context.Background(), testBound)
		defer cancel()
		conn, err := db.Owner(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close(ctx) }()
		for _, q := range stmts {
			if _, err := conn.Exec(ctx, q.sql, q.args...); err != nil {
				t.Fatalf("%s: undoing the refresh: %v", c.ID, err)
			}
		}
	}
}

// tickViews are the contract views over the tables a source's ticks write
// when the script finds its table itself rather than naming it in
// partitions (no platform table is named in the tree): the undo removes the
// rows of the partitioned table under the view.
var tickViews = map[string]string{"fuel-prices-de": "contract.fuel_price_observation"}

// restatedLatest are the newest-price tables a source's refresh restates
// (a confirmed price's observed time set to the refresh's, as the
// platform's price poll does, with no new tick): the undo sets each row it
// restated back to its newest remaining tick, the time the seed gave it.
var restatedLatest = map[string]string{"fuel-prices-de": "public.fuel_price_latest"}

// baseTable is the partitioned table under the view $1, as schema.table,
// found from the view's own dependencies.
const baseTable = `select n.nspname || '.' || c.relname from pg_rewrite r
	join pg_depend d on d.objid = r.oid and d.classid = 'pg_rewrite'::regclass
	join pg_class c on c.oid = d.refobjid and c.relkind = 'p'
	join pg_namespace n on n.oid = c.relnamespace
	where r.ev_class = $1::regclass order by 1 limit 1`

// ownerText answers a text as the database owner.
func ownerText(t *testing.T, q string, args ...any) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testBound)
	defer cancel()
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var v string
	if err := conn.QueryRow(ctx, q, args...).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// ownerCount answers a count as the database owner.
func ownerCount(t *testing.T, q string, args ...any) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testBound)
	defer cancel()
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	var n int
	if err := conn.QueryRow(ctx, q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func open(t *testing.T) asker {
	t.Helper()
	cfg, err := config.Load(config.Options{Environ: []string{"VFAPI_DB_URL=" + db.ReaderURL, "VFAPI_ACCESS_URL=" + db.AccessURL, "VFAPI_AUTH_PEPPER=" + parityPepper,
		"VFAPI_REFRESH_SOURCES_FILE=" + liveSources}, RequireSecrets: true})
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
	if refreshStore == nil {
		if refreshStore, err = accessstore.Open(context.Background(), cfg); err != nil {
			t.Fatal(err)
		}
	}
	refreshes, err := wire.Refreshes(cfg, refreshStore, st.Lookup)
	if err != nil {
		t.Fatal(err)
	}
	return asker{cfg: cfg, st: st, owners: noteOwners(t), refreshes: refreshes}
}

// noteOwners reads which families' notes carry each text, from the fixture
// rows: the API states its notes as texts, and rules/5 states a condition or
// purpose note by its family. A kind the service does not know is a
// condition (provenance.MapNoteKind), so every kind but purpose is read as a
// condition. One map serves both kinds, so a text that is a purpose of one
// row and a condition of another would be owned ambiguously: it is refused.
func noteOwners(t *testing.T) parity.Owners {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testBound)
	defer cancel()
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	rows, _ := conn.Query(ctx, `select body, source_ref, note_kind = 'purpose' from contract.source_note order by body, source_ref`)
	pairs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[struct {
		Body, Family string
		Purpose      bool
	}])
	if err != nil {
		t.Fatal(err)
	}
	purpose := map[string]bool{}
	for _, p := range pairs {
		if was, ok := purpose[p.Body]; ok && was != p.Purpose {
			t.Fatalf("a note text of %d character(s) is both a purpose and a condition", len(p.Body))
		}
		purpose[p.Body] = p.Purpose
	}
	out := parity.Owners{}
	for _, p := range pairs {
		out[p.Body] = append(out[p.Body], p.Family)
	}
	// The speed cameras' § 23 Abs. 1c StVO notice is the platform's own
	// wording, no family's row: it is stated unowned, as the MCP side states it.
	if n := roads.SpeedNotice(wire.Language); out[n] == nil {
		out[n] = []string{parity.Unowned}
	} else {
		t.Fatal("a fixture family owns the speed cameras' notice")
	}
	// So is the caveat of a city the zone list holds no entry for.
	q, err := parity.LoadQuestions(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range q.Cases {
		if u, err := url.Parse(c.Request); err == nil && u.Query().Get("city") != "" {
			out[rules.NoBoundaryCaveat(u.Query().Get("city"), wire.Language)] = []string{parity.Unowned}
		}
	}
	return out
}

// compare asks every case of the bundle pinned under name and compares the
// facts; it returns the bundle and the asker it used, whose store and
// connections a caller reuses rather than opening a second one.
func compare(t *testing.T, name string, deferred []parity.Deferral) (parity.Bundle, asker) {
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
	// The report of #245: the deferred differences per class and per tool,
	// and the deferrals nothing in the bundle needs any more.
	perClass := map[string]map[string]int{}
	used := make([]bool, len(deferred))
	// Each case is a subtest of its own: a case is a read (and a refresh it
	// waits on), and the bundle's cases together outgrow one leaf's bound
	// (TST-012).
	for _, c := range b.Cases {
		t.Run(c.ID, func(t *testing.T) {
			got, err := parity.Project(a.ask(t, c), parity.Default, c.Operation, c.Request, a.owners)
			if err != nil {
				t.Fatal(err)
			}
			var d []string
			for _, line := range parity.Diff(c.Facts, got) {
				i := parity.Defers(deferred, c.Operation, line)
				if i < 0 {
					d = append(d, line)
					continue
				}
				used[i] = true
				cl := deferred[i].Class
				if deferred[i].BlindSpot() {
					cl += " (blind spot)"
				}
				if perClass[cl] == nil {
					perClass[cl] = map[string]int{}
				}
				perClass[cl][c.Operation]++
			}
			if len(d) > 0 {
				t.Errorf("case %s (%s, MCP %q): %d of %d facts differ:\n  %s", c.ID, c.Operation, c.Counterpart, len(d), len(c.Facts), strings.Join(d, "\n  "))
			}
			facts += len(c.Facts)
			differ += len(d)
			compared[c.Operation] = append(compared[c.Operation], c.ID)
		})
	}
	for op, ids := range compared {
		t.Logf("compared %s: %d case(s): %s", op, len(ids), strings.Join(ids, ", "))
	}
	t.Logf("bundle %s: %d case(s), %d fact(s) compared, %d difference(s) open", name, len(b.Cases), facts, differ)
	for _, cl := range slices.Concat(parity.DeferClasses, []string{"C (blind spot)"}) {
		ops := slices.Sorted(maps.Keys(perClass[cl]))
		n, by := 0, make([]string, 0, len(ops))
		for _, op := range ops {
			n += perClass[cl][op]
			by = append(by, fmt.Sprintf("%s %d", op, perClass[cl][op]))
		}
		t.Logf("deferred class %s: %d difference(s): %s", cl, n, strings.Join(by, ", "))
	}
	for i, u := range used {
		if !u {
			t.Errorf("%s line %+v defers a difference the bundle %s no longer has: remove it", parity.DeferredFile, deferred[i], name)
		}
	}
	return b, a
}

// TestParity asks every case of the bundle pinned for the platform version
// and compares the facts. Only a bundle the MCP side produced is parity
// evidence.
func TestParity(t *testing.T) {
	req.Covers(t, "TEST-009", "FR-GEN-002", "CON-007")
	deferred, err := parity.LoadDeferred(os.DirFS(dir))
	if err != nil {
		t.Fatal(err)
	}
	b, a := compare(t, platform(), deferred)
	switch b.Producer {
	case parity.ProducerAPI:
		t.Logf("bundle %s was recorded by the API itself: it is a regression lock, not parity evidence; parity needs a bundle the MCP side produced", platform())
	case parity.ProducerMCP:
		t.Logf("bundle %s was produced by the MCP side at platform commit %s: parity evidence", platform(), b.Commit)
	}

	// A planted change in one projection rule must fail and name the fact.
	t.Run("a planted rule change names the fact", func(t *testing.T) { plantedRule(t, b, a) })
}

// plantedRule: a position rounded to four decimals differs from the
// default projection of the first case, and only in its positions.
func plantedRule(t *testing.T, b parity.Bundle, a asker) {
	t.Helper()
	planted := parity.Default
	planted.PositionDecimals = 4
	answer := a.ask(t, b.Cases[0])
	got, err := parity.Project(answer, planted, b.Cases[0].Operation, b.Cases[0].Request, a.owners)
	if err != nil {
		t.Fatal(err)
	}
	want, err := parity.Project(answer, parity.Default, b.Cases[0].Operation, b.Cases[0].Request, a.owners)
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
	if b, _ := compare(t, buildinfo.Unsynced, nil); b.Producer != parity.ProducerAPI {
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
		c := parity.Case{ID: qq.ID, Operation: qq.Operation, Counterpart: qq.Counterpart, Request: qq.Request, Key: qq.Key, Now: qq.Now, Refresh: qq.Refresh, Ruling: qq.Ruling, APIOnly: qq.APIOnly}
		facts, err := parity.Project(a.ask(t, c), parity.Default, c.Operation, c.Request, a.owners)
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
