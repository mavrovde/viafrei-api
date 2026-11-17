//go:build integration

// Package examplerun_test runs every documented example (api/examples, the
// examples the OpenAPI document carries) against the service on the synthetic
// fixture set, and fails when an answer drifts from its example (DX-003).
// Record mode rewrites the examples' answers; docs/examples.md says when.
package examplerun_test

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
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mavrovde/viafrei-api/api"
	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/app/health"
	"github.com/mavrovde/viafrei-api/internal/buildinfo"
	"github.com/mavrovde/viafrei-api/internal/dbcontract"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/fixtures"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/runtime/wire"
	"github.com/mavrovde/viafrei-api/internal/testkit/oascheck"
	"github.com/mavrovde/viafrei-api/internal/testkit/pgtest"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

var record = flag.Bool("examples.record", false, "rewrite the answers of api/examples from the service on the fixtures")

// root is the repository root, relative to this package.
const root = "../../.."

const testBound = 2 * time.Second

// The fixed inputs every example is answered under, so that an answer is a
// function of the fixtures and the code only.
const (
	profile   = "tiny"
	seed      = 1
	requestID = "example-request-0001"
	keyID     = "examplekey"
)

var (
	now   = fixtures.ReferenceInstant.AddDate(0, 0, 2)
	build = buildinfo.Info{Version: "1.0.0-example", Commit: "0123456789ab"}
)

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
	// Seeding the fixture set is setup too: done in the first test body
	// it counted against that test's bound, and grew with every example.
	if err := seedFixtures(); err != nil {
		fmt.Fprintln(os.Stderr, "seeding the fixture set:", err)
		db.Stop()
		os.Exit(1)
	}
	code := m.Run()
	db.Stop()
	os.Exit(code)
}

// load opens the service's store on the fixture set TestMain seeded.
func load(t *testing.T) (*config.Config, *store.Store) {
	t.Helper()
	cfg, err := config.Load(config.Options{Environ: []string{"VFAPI_DB_URL=" + db.ReaderURL}, RequireSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	octx, ocancel := context.WithTimeout(context.Background(), testBound)
	defer ocancel()
	st, err := store.Open(octx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return cfg, st
}

// seedFixtures seeds the fixture set and the rows the examples are answered
// on (fixtures.ExamplePlants) through the pinned image's test seed, in one
// seed: the read contract's relations are views, written only by the seed.
func seedFixtures() error {
	var buf bytes.Buffer
	if _, err := fixtures.Generate(&buf, profile, seed, dbcontract.ContractVersion); err != nil {
		return err
	}
	if err := fixtures.ExamplePlants(&buf, profile, seed, dbcontract.ContractVersion, now, freshReports); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return db.Seed(ctx, buf.String())
}

// freshReports are, on the charging sites the examples document, reports
// made minutes before the examples' clock (now, the end of the fixture
// window): the fixtures' own reports are spread over whole days, so at that
// clock every one of them is older than the 30-minute status limit and the
// examples would show only stale points. With these, each documented site
// shows a live point beside its stale ones and a non-null availableNow.
var freshReports = []fixtures.Report{
	{Point: "DE*SYN*E000010", Age: 5 * time.Minute, State: "available"},
	{Point: "DE*SYN*E000011", Age: 12 * time.Minute, State: "occupied"},
	{Point: "DE*SYN*E000020", Age: 8 * time.Minute, State: "available"},
}

// keyAuth admits every request as the example's key.
type keyAuth struct{ p auth.Principal }

func (k keyAuth) Authenticate(context.Context, string, string) (auth.Principal, error) {
	return k.p, nil
}

func principal(k *api.Key) auth.Principal {
	rec := access.Record{ID: keyID, Environment: access.Live, ExpiresAt: now.AddDate(0, 0, 90),
		Plan: access.Plan{Name: "example", Tier: "standard"}}
	if k != nil {
		rec.Plan.UseClass, rec.Plan.Sources = access.UseClass(k.UseClass), k.Sources
		rec.Audience = access.Audience(k.Audience)
		for _, s := range k.Scopes {
			rec.Plan.Scopes = append(rec.Plan.Scopes, access.Scope(s))
		}
	}
	return auth.Principal{Key: rec}
}

// ask answers one example through the real handler, use cases and store.
func ask(t *testing.T, cfg *config.Config, st *store.Store, e api.Example) (int, []byte) {
	t.Helper()
	clock := func() time.Time { return now }
	expected, err := health.ExpectedVersion(cfg.Contract.Expected, dbcontract.ContractVersion)
	if err != nil {
		t.Fatal(err)
	}
	h, err := httpapi.NewHandler(httpapi.Deps{
		Config: cfg, Build: build, ContractVersion: dbcontract.ContractVersion, Now: clock,
		Readiness: health.NewChecker(st, expected, cfg.Health.ReadyTimeout),
		Auth:      keyAuth{principal(e.Key)}, Sources: st,
		Features: features.New(features.Repositories{Register: st, Charging: st, Parking: st, Places: st, Roads: st, Weather: st, Rules: st, Transit: st, Fuel: st, Hydrogen: st}, wire.FeatureSettings(cfg), clock),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), testBound)
	defer cancel()
	r := httptest.NewRequestWithContext(ctx, http.MethodGet, e.Request, nil)
	r.Header.Set(httpapi.RequestIDHeader, requestID)
	if e.Key != nil {
		r.Header.Set("Authorization", "Bearer example")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	body, _ := io.ReadAll(w.Body)
	if err := contractDoc(t).Check(http.MethodGet, e.Request, w.Code, w.Header(), body); err != nil {
		t.Errorf("the answer to %s breaks the document: %v", e.Request, err)
	}
	return w.Code, body
}

// readDoc reads and parses api/openapi.json once: every answer is checked
// against it, and the document does not change during the run.
var readDoc = sync.OnceValues(func() (*oascheck.Document, error) {
	raw, err := os.ReadFile(filepath.Join(root, "api", "openapi.json"))
	if err != nil {
		return nil, err
	}
	return oascheck.Load(raw)
})

func contractDoc(t *testing.T) *oascheck.Document {
	t.Helper()
	d, err := readDoc()
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func equalJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}

// TestExamplesRunAgainstFixtures: every example's request is answered with
// its documented status and exactly its documented body, and every operation
// of the document has an example that was run (DX-003).
func TestExamplesRunAgainstFixtures(t *testing.T) {
	req.Covers(t, "DX-003", "TEST-003")
	examples, err := api.ReadExamples(os.DirFS(filepath.Join(root, "api")))
	if err != nil {
		t.Fatal(err)
	}
	cfg, st := load(t)
	ran := 0
	for _, id := range slices.Sorted(maps.Keys(examples)) {
		e := examples[id]
		status, body := ask(t, cfg, st, e)
		ran++
		if *record {
			var pretty bytes.Buffer
			_ = json.Indent(&pretty, body, "", "  ")
			e.Status, e.Response = status, pretty.Bytes()
			out, err := e.Marshal()
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, "api", "examples", id+".json"), out, 0o600); err != nil {
				t.Fatal(err)
			}
			t.Logf("recorded %s: %s answered %d", id, e.Request, status)
			continue
		}
		if status != e.Status {
			t.Errorf("%s: %s answered %d, the example documents %d", id, e.Request, status, e.Status)
		}
		if !equalJSON(body, e.Response) {
			t.Errorf("%s: the answer to %s drifted from api/examples/%s.json; re-record (docs/examples.md):\n%s", id, e.Request, id, body)
		}
	}
	if ran == 0 {
		t.Fatal("no example was run")
	}
	var ops []string
	for _, op := range contractDoc(t).Operations() {
		ops = append(ops, op.ID)
		if _, ok := examples[op.ID]; !ok {
			t.Errorf("operation %s has no example in api/examples", op.ID)
		}
	}
	t.Logf("ran %d example(s) against fixture set %s/%d for %d operation(s): %s", ran, profile, seed, len(ops), strings.Join(ops, ", "))
}

// TestADriftedExampleFails: an example whose body differs from the service's
// answer by one value is caught, so the comparison above can say no.
func TestADriftedExampleFails(t *testing.T) {
	req.Covers(t, "DX-003")
	examples, err := api.ReadExamples(os.DirFS(filepath.Join(root, "api")))
	if err != nil {
		t.Fatal(err)
	}
	e, ok := examples["getMeta"]
	if !ok {
		t.Fatal("no getMeta example")
	}
	cfg, st := load(t)
	_, body := ask(t, cfg, st, e)
	if !equalJSON(body, e.Response) {
		t.Fatalf("the committed getMeta example already differs: %s", body)
	}
	if equalJSON(body, []byte(`{"status":"degraded"}`)) || equalJSON(body, []byte(`not json`)) {
		t.Fatal("a changed body compared equal")
	}
}
