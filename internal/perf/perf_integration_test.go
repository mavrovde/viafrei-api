//go:build integration

package perf_test

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mavrovde/viafrei-api/internal/adapters/accessstore"
	"github.com/mavrovde/viafrei-api/internal/adapters/httpapi"
	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/app/auth"
	"github.com/mavrovde/viafrei-api/internal/app/features"
	"github.com/mavrovde/viafrei-api/internal/app/health"
	"github.com/mavrovde/viafrei-api/internal/buildinfo"
	"github.com/mavrovde/viafrei-api/internal/dbcontract"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/fixtures"
	"github.com/mavrovde/viafrei-api/internal/perf"
	"github.com/mavrovde/viafrei-api/internal/runtime/config"
	"github.com/mavrovde/viafrei-api/internal/runtime/wire"
	"github.com/mavrovde/viafrei-api/internal/testkit/pgtest"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// The benchmark leg runs this file with -perf.assert, a larger sample and,
// for a release, -perf.profile=benchmark (docs/performance.md). The
// integration job runs it as a smoke: every plan and cache assertion, the
// latencies reported but not asserted (a race-instrumented run is no
// latency measurement).
var (
	profile = flag.String("perf.profile", "ci", "fixture profile to load: tiny, ci or benchmark")
	samples = flag.Int("perf.samples", 30, "timed requests per operation, after the warm-up")
	assert  = flag.Bool("perf.assert", false, "fail when an operation misses its p95 or p99 budget")
)

const (
	warmup      = 10
	concurrency = 4
	// testBound bounds one request; the loops around them are bounded by the
	// sample count.
	testBound = 2 * time.Second
)

var (
	db      *pgtest.DB
	loadErr error
)

// TestMain starts PostGIS, adds day partitions beside the default ones and
// loads the fixtures: all setup, outside every test body.
func TestMain(m *testing.M) {
	flag.Parse()
	ctx, cancel := context.WithTimeout(context.Background(), pgtest.SetupBound)
	loadErr = loadFixtures(ctx)
	if db == nil {
		cancel()
		fmt.Fprintln(os.Stderr, "integration tests need a container runtime:", loadErr)
		os.Exit(1)
	}
	cancel()
	code := m.Run()
	db.Stop()
	os.Exit(code)
}

// loadFixtures seeds the fixtures through the pinned image's test seed
// (which adds the day partitions of the platform's time series the rows fall
// in) and analyses them, as a database with a long history is.
func loadFixtures(ctx context.Context) error {
	var buf bytes.Buffer
	if _, err := fixtures.Generate(&buf, *profile, 1, dbcontract.ContractVersion); err != nil {
		return err
	}
	var err error
	if db, err = pgtest.Seeded(ctx, buf.String()); err != nil {
		return err
	}
	conn, err := db.Owner(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	_, err = conn.Exec(ctx, "analyze") //nolint:misspell // the SQL keyword
	return err
}

// partitions names every partition of a partitioned relation the contract's
// views read: the platform's time series, outside the system schemas.
func partitions(ctx context.Context, conn *pgx.Conn) (map[string]bool, error) {
	rows, _ := conn.Query(ctx, `
select c.relname from pg_inherits i
join pg_class c on c.oid = i.inhrelid
join pg_class p on p.oid = i.inhparent
join pg_namespace n on n.oid = p.relnamespace
where n.nspname not in ('pg_catalog', 'information_schema') and p.relkind = 'p'`)
	names, err := pgx.CollectRows(rows, pgx.RowTo[string])
	out := map[string]bool{}
	for _, n := range names {
		out[n] = true
	}
	return out, err
}

// statement is one SQL statement the service sent, with its arguments; a
// named statement's is the text it was prepared with, under its name.
type statement struct {
	name string
	sql  string
	args []any
}

// capture records every read statement the store sends: each statement
// sent as text, and each named statement with the text it was prepared with
// (seen, which a plan test explains), and a count of each, of a named
// statement under its name.
type capture struct {
	mu       sync.Mutex
	seen     map[string]statement
	count    map[string]*atomic.Int64
	prepared map[string]string
}

// TracePrepareStart keeps the text of each named statement the store prepares.
func (c *capture) TracePrepareStart(ctx context.Context, _ *pgx.Conn, d pgx.TracePrepareStartData) context.Context {
	if strings.HasPrefix(d.Name, "api_") {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.prepared == nil {
			c.prepared = map[string]string{}
		}
		c.prepared[d.Name] = d.SQL
	}
	return ctx
}

func (c *capture) TracePrepareEnd(context.Context, *pgx.Conn, pgx.TracePrepareEndData) {}

func (c *capture) TraceQueryStart(ctx context.Context, _ *pgx.Conn, d pgx.TraceQueryStartData) context.Context {
	sql := strings.TrimSpace(d.SQL)
	lower := strings.ToLower(sql)
	text := strings.HasPrefix(lower, "select") || strings.HasPrefix(lower, "with")
	if !text && !strings.HasPrefix(sql, "api_") {
		return ctx
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.seen[sql]; !ok {
		body, named := c.prepared[sql]
		switch {
		case text:
			c.seen[sql] = statement{sql: sql, args: slices.Clone(d.Args)}
		case named:
			c.seen[sql] = statement{name: sql, sql: body, args: slices.Clone(d.Args)}
		}
	}
	if c.count[sql] == nil {
		c.count[sql] = &atomic.Int64{}
	}
	c.count[sql].Add(1)
	return ctx
}

func (c *capture) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// reads counts the statements sent that mention relation, or are named it.
func (c *capture) reads(relation string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	var n int64
	for sql, k := range c.count {
		if strings.Contains(sql, relation) {
			n += k.Load()
		}
	}
	return n
}

// keyAuth answers every bearer as the benchmark key, but ownBearer as the
// own key a refresh is asked with (only ViaFrei's own audience may).
type keyAuth struct{ p, own auth.Principal }

func (k keyAuth) Authenticate(_ context.Context, presented, _ string) (auth.Principal, error) {
	if presented == ownBearer {
		return k.own, nil
	}
	return k.p, nil
}

// ownBearer is the bearer of the own key; refreshSubjects are the subjects
// the refresh benchmarks ask about: districts, so no lookup runs.
const ownBearer = "own"

var refreshSubjects = []string{"ags:09162000", "ags:05315000", "ags:11000000"}

// rig is the service over the loaded database: the real store, use cases
// and handler, a key allowed every source, and the SQL it sends captured.
type rig struct {
	cfg     *config.Config
	st      *store.Store
	sql     *capture
	key     auth.Principal
	own     auth.Principal
	refresh features.Refreshes
	now     time.Time
	targets map[string][]string
}

func open(t *testing.T) *rig {
	t.Helper()
	if loadErr != nil {
		t.Fatalf("loading the fixtures: %v", loadErr)
	}
	cfg, err := config.Load(config.Options{Environ: []string{"VFAPI_DB_URL=" + db.ReaderURL, "VFAPI_ACCESS_URL=" + db.AccessURL,
		"VFAPI_AUTH_PEPPER=perf-run-pepper-not-a-secret-0123456789", "VFAPI_REFRESH_SOURCES_FILE=" + filepath.Join("..", "..", "test", "live", "registry.json")}, RequireSecrets: true})
	if err != nil {
		t.Fatal(err)
	}
	pc, err := store.PoolConfig(cfg)
	if err != nil {
		t.Fatal(err)
	}
	c := &capture{seen: map[string]statement{}, count: map[string]*atomic.Int64{}}
	pc.ConnConfig.Tracer = c
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, pc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	r := &rig{cfg: cfg, st: store.New(pool, cfg), sql: c, now: fixtures.ReferenceInstant.AddDate(0, 0, 2), targets: map[string][]string{}}
	// The service reads the road shapes and junction Länder in the
	// background from its start (KeepRoads); the rig reads them once.
	if err := r.st.WarmRoads(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	sources, err := pgx.CollectRows(must(conn.Query(ctx, "select source_ref from contract.source order by 1")), pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	r.key = auth.Principal{Key: access.Record{
		ID: "benchmarkk", Environment: access.Live, ExpiresAt: r.now.Add(24 * time.Hour),
		Plan: access.Plan{Name: "benchmark", UseClass: "commercial", Tier: "standard", Sources: sources,
			Scopes: []access.Scope{"places", "energy", "parking", "roads", "weather", "rules", "transit", "accessibility"}},
	}}
	// The own key (ViaFrei's own audience) asks what only an own key may:
	// a refresh, and a fuel station's page (the fuel family is own-only).
	r.own = auth.Principal{Key: access.Record{
		ID: "benchown00", Environment: access.Live, ExpiresAt: r.now.Add(24 * time.Hour), Audience: access.AudienceOwn,
		Plan: access.Plan{Name: "dev", UseClass: "consumer", Tier: "standard", Sources: sources, Scopes: r.key.Key.Plan.Scopes},
	}}
	near := func(table, path string) {
		pts, err := pgx.CollectRows(must(conn.Query(ctx, fmt.Sprintf(
			"select round(st_y(location)::numeric, 3)::text || ',' || round(st_x(location)::numeric, 3)::text from contract.%s where withdrawn_at is null order by 1 limit 40", table))), pgx.RowTo[string])
		if err != nil || len(pts) == 0 {
			t.Fatalf("no %s to ask near: %v", table, err)
		}
		for _, p := range pts {
			r.targets[path] = append(r.targets[path], path+"?at="+p+"&radius_m=5000")
		}
	}
	near("charging_site", "/v1/charging-options")
	near("parking_facility", "/v1/parking-options")
	near("fuel_station", "/v1/fuel-options")
	near("hydrogen_station", "/v1/hydrogen-options")
	near("stop_place", "/v1/nearby")
	near("stop_place", "/v1/stations")
	ids, err := pgx.CollectRows(must(conn.Query(ctx,
		"select place_id from contract.place where withdrawn_at is null order by prominence desc, place_id limit 40")), pgx.RowTo[string])
	if err != nil || len(ids) == 0 {
		t.Fatalf("no place to ask about: %v", err)
	}
	for _, id := range ids {
		r.targets["overview"] = append(r.targets["overview"], "/v1/places/"+id+"/overview")
	}
	// The road reads: each motorway's conditions, the conditions near each
	// junction, and each situation in force at the benchmark's instant.
	list := func(key, query, prefix, suffix string, args ...any) {
		vals, err := pgx.CollectRows(must(conn.Query(ctx, query, args...)), pgx.RowTo[string])
		if err != nil || len(vals) == 0 {
			t.Fatalf("nothing to ask for %s: %v", key, err)
		}
		for _, v := range vals {
			r.targets[key] = append(r.targets[key], prefix+v+suffix)
		}
	}
	list("motorway", "select replace(road_ref, ' ', '') from contract.road where road_ref like 'A%' order by 1 limit 40",
		"/v1/motorways/", "/conditions")
	list("road-conditions", "select round(st_y(location)::numeric, 3)::text || ',' || round(st_x(location)::numeric, 3)::text from contract.junction order by 1 limit 40",
		"/v1/road-conditions?at=", "&radius_m=50000")
	// The weather and rules reads: the warnings at a place of a region (the
	// platform matches a warning by its region, and its contract views state
	// neither a region's boundary nor a warning's area), and each rule topic.
	list("weather", "select round(st_y(p)::numeric, 3)::text || ',' || round(st_x(p)::numeric, 3)::text "+
		"from (select location as p from contract.place where region_code is not null and withdrawn_at is null) d order by 1 limit 40",
		"/v1/weather-risk?at=", "")
	list("rules", "select topic_code from contract.rule_topic order by 1 limit 40", "/v1/driving-rules?topic=", "")
	// The searches and lookups by name, code or position: each asks rows the
	// fixture holds, the read the operation makes for them.
	at := "select round(st_y(location)::numeric, 3)::text || ',' || round(st_x(location)::numeric, 3)::text from contract.%s where location is not null order by 1 limit 40"
	list("speed", fmt.Sprintf(at, "speed_camera"), "/v1/speed-cameras?at=", "&radius_m=10000")
	list("sharing", fmt.Sprintf(at, "sharing_station"), "/v1/sharing-options?at=", "&radius_m=5000")
	list("location", fmt.Sprintf(at, "place"), "/v1/location?at=", "")
	list("lorry", fmt.Sprintf(at, "junction"), "/v1/lorry-rules?vehicle=long_lorry&at=", "")
	// named asks each pair of the query (a, b) as prefix + a + middle + b, each escaped for a query string.
	named := func(key, query, prefix, middle string) {
		rows, err := conn.Query(ctx, query)
		if err != nil {
			t.Fatalf("nothing to ask for %s: %v", key, err)
		}
		pairs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[struct{ A, B string }])
		if err != nil || len(pairs) == 0 {
			t.Fatalf("nothing to ask for %s: %v", key, err)
		}
		for _, p := range pairs {
			v := prefix + url.QueryEscape(p.A)
			if middle != "" {
				v += middle + url.QueryEscape(p.B)
			}
			r.targets[key] = append(r.targets[key], v)
		}
	}
	named("places", "select distinct name, '' from contract.place where withdrawn_at is null and length(name) >= 2 order by 1 limit 40", "/v1/places?q=", "")
	named("addresses", "select distinct street || ' ' || house_number || ', ' || postcode, '' from contract.address "+
		"where street is not null and house_number is not null and postcode is not null order by 1 limit 40", "/v1/addresses?q=", "")
	named("pois", "select distinct category_value, place_name from contract.poi where category_value is not null and place_name is not null order by 1, 2 limit 40",
		"/v1/pois?name=", "&in=")
	list("departures", "select stop_place_id from contract.stop_place where withdrawn_at is null order by 1 limit 40",
		"/v1/stations/", "/departures?from="+r.now.Add(-2*time.Hour).UTC().Format(time.RFC3339)+"&duration_min=240")
	list("facilities", "select distinct stop_place_id from contract.station_facility order by 1 limit 40", "/v1/stations/", "/facilities")
	list("disruptions", "select region_code from contract.admin_region order by 1 limit 40", "/v1/transit-disruptions?area=", "")
	list("states", "select distinct region_code from contract.admin_region where region_level = 'state' order by 1 limit 40",
		"/v1/states/", "/road-conditions?road_class=motorway")
	list("motorway-parking", "select replace(road_ref, ' ', '') from contract.road where road_ref like 'A%' order by 1 limit 40",
		"/v1/motorways/", "/parking-options")
	list("motorways", "select v from (values ('?limit=20'), ('?state=01'), ('?road=A100')) t(v)", "/v1/motorways", "")
	list("sources", "select v from (values (''), ('?domain=places'), ('?domain=roads')) t(v)", "/v1/sources", "")
	list("watch", "select replace(road_ref, ' ', '') from contract.road where road_ref like 'A%' order by 1 limit 40",
		"/v1/watch?kind=road&target=", "&hours=3")
	// A situation is answered only from a family the key may read: the
	// licence gate decides, as it does for the request.
	reg, err := r.st.Register(ctx)
	if err != nil {
		t.Fatal(err)
	}
	permitted, _, err := access.NewGate(reg, r.key.Key.Plan, r.key.Key.Audience, nil, cfg.Sources.Disabled).Answer(access.Answering(reg, "roads"))
	if err != nil {
		t.Fatal(err)
	}
	list("situation", "select event_id from contract.traffic_event where source_ref = any($1) and withdrawn_at is null "+
		"and (valid_to is null or valid_to > $2) order by 1 limit 40", "/v1/road-situations/", "", permitted, r.now)
	// The keyed pages, summaries and boards, each built only where the
	// catalogue serves its operation (an older schema holds no table for it).
	serves := func(op string) bool { return slices.Contains(httpapi.OperationIDs(), op) }
	if serves("getFuelStation") {
		energy, _, err := access.NewGate(reg, r.own.Key.Plan, r.own.Key.Audience, nil, cfg.Sources.Disabled).Answer(access.Answering(reg, "energy"))
		if err != nil {
			t.Fatal(err)
		}
		list("fuel-station", "select station_id from contract.fuel_station where withdrawn_at is null and source_ref = any($1) "+
			"and display_class <> 'withhold' order by 1 limit 40", "/v1/fuel-options/", "", energy)
	}
	if serves("getChargingSite") {
		list("charging-site", "select site_id from contract.charging_site where withdrawn_at is null order by 1 limit 40", "/v1/charging-options/", "")
	}
	if serves("getTrainBoard") {
		list("train-board", "select distinct station_eva from contract.train_board order by 1 limit 40", "/v1/train-boards/", "?lang=en")
	}
	if serves("listParkingBays") {
		list("parking-bays", fmt.Sprintf(at, "junction"), "/v1/parking-bays?at=", "&radius_m=2000&limit=20")
	}
	if serves("listStateServiceAlerts") {
		list("state-alerts", "select distinct region_code from contract.admin_region where region_level = 'state' order by 1 limit 40", "/v1/states/", "/service-alerts")
		list("state-weather", "select distinct region_code from contract.admin_region where region_level = 'state' order by 1 limit 40", "/v1/states/", "/weather")
	}
	r.targets["summaries"] = []string{"/v1/germany", "/v1/germany?lang=en"}
	r.targets["coverage"] = []string{"/v1/places/coverage", "/v1/places/coverage?lang=en"}
	r.targets["pressure"] = []string{"/v1/pressure"}
	r.targets["affected"] = []string{"/v1/affected-stations?limit=5", "/v1/affected-stations?limit=20"}
	r.refreshes(ctx, t, conn)
	return r
}

// refreshes wires the refresh store and the own key, and asks each subject
// once and settles it none_due, as the live refresher would: an ask
// measured afterwards is answered by that fresh refresh (201) and a read
// finds it, so neither waits on a refresher the benchmark does not run.
func (r *rig) refreshes(ctx context.Context, t *testing.T, conn *pgx.Conn) {
	t.Helper()
	rs, err := accessstore.Open(ctx, r.cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(rs.Close)
	if r.refresh, err = wire.Refreshes(r.cfg, rs, r.st.Lookup); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `insert into access.plan (plan_name, use_class, tier, scopes, sources, changed_at, rate_per_second, burst, quota_day, quota_month)
		values ('dev', 'consumer', 'standard', '{}', '{}', $1, 1, 1, 0, 0) on conflict do nothing`, r.now); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `insert into access.api_key (key_id, environment, plan_name, label, contact, mac, issued_at, expires_at, changed_at, audience)
		values ('benchown00', 'live', 'dev', 'perf', 'perf@example.test', decode(repeat('00', 32), 'hex'), $1, $1::timestamptz + interval '1 day', $1, 'own')
		on conflict do nothing`, r.now); err != nil {
		t.Fatal(err)
	}
	h := r.handler(t, false) //nolint:contextcheck // building the handler; each request carries its own context
	for _, subject := range refreshSubjects {
		target := refreshAsk(subject)
		code, location := send(ctx, h, target)
		if code != http.StatusCreated && code != http.StatusAccepted {
			t.Fatalf("%s answered %d", target, code)
		}
		id := location[strings.LastIndex(location, "/")+1:]
		if _, err := conn.Exec(ctx, `update live.refresh_requests set state = 'none_due', settled_at = $2
			where id = $1::bigint and settled_at is null`, id, r.now); err != nil {
			t.Fatal(err)
		}
		r.targets["create-refresh"] = append(r.targets["create-refresh"], target)
		self, err := url.Parse(location)
		if err != nil {
			t.Fatal(err)
		}
		r.targets["get-refresh"] = append(r.targets["get-refresh"], self.Path)
	}
}

// refreshAsk is the target of an ask (createRefresh) of the fuel prices of
// subject, sent with the own key's bearer.
func refreshAsk(subject string) string {
	return http.MethodPost + " /v1/refreshes " + `{"source":"fuel-prices-de","subject":{"at":"` + subject + `"}}`
}

func must(rows pgx.Rows, _ error) pgx.Rows { return rows }

// handler builds the handler, with or without the HTTP cache.
func (r *rig) handler(t *testing.T, cached bool) http.Handler {
	t.Helper()
	cfg := *r.cfg
	cfg.Cache.Enabled = cached
	now := func() time.Time { return r.now }
	expected, err := health.ExpectedVersion("", dbcontract.ContractVersion)
	if err != nil {
		t.Fatal(err)
	}
	deps := httpapi.Deps{
		Config: &cfg, Build: buildinfo.Info{Version: "bench", Commit: "bench"}, ContractVersion: dbcontract.ContractVersion, Now: now,
		Readiness: health.NewChecker(r.st, expected, cfg.Health.ReadyTimeout),
		Auth:      keyAuth{r.key, r.own}, Sources: r.st, Versions: r.st,
		Features: features.New(features.Repositories{Register: r.st, Charging: r.st, Parking: r.st, Places: r.st, Roads: r.st, Weather: r.st, Rules: r.st, Transit: r.st, Fuel: r.st, Hydrogen: r.st, Speed: r.st, Sharing: r.st, Addresses: r.st, Pois: r.st, Locations: r.st, Disruptions: r.st, Nearby: r.st, Lorry: r.st, Catalogue: r.st, National: r.st, Affected: r.st, Coverage: r.st, States: r.st, TrainBoards: r.st, Bays: r.st}, wire.FeatureSettings(&cfg), now).WithRefreshes(r.refresh),
	}
	h, err := httpapi.NewHandler(deps)
	if err != nil {
		t.Fatal(err)
	}
	// Liveness and readiness are the health listener's (#92); every other
	// operation is the public one's, measured without the allow-list.
	hh, err := httpapi.NewHealthHandler(deps)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("/healthz", hh)
	mux.Handle("/readyz", hh)
	mux.Handle("/", h)
	return mux
}

// targetsOf are the requests one operation is measured with.
func (r *rig) targetsOf(op string) []string {
	switch op {
	case "getHealth":
		return []string{"/healthz"}
	case "getReadiness":
		return []string{"/readyz"}
	case "getMeta":
		return []string{"/v1/meta"}
	case "getKey":
		return []string{"/v1/key"}
	case "listChargingOptions":
		return r.targets["/v1/charging-options"]
	case "listParkingOptions":
		return r.targets["/v1/parking-options"]
	case "getPlaceOverview":
		return r.targets["overview"]
	case "getMotorwayConditions":
		return r.targets["motorway"]
	case "listRoadConditions":
		return r.targets["road-conditions"]
	case "getRoadSituation":
		return r.targets["situation"]
	case "getWeatherRisk":
		return r.targets["weather"]
	case "getDrivingRules":
		return r.targets["rules"]
	case "listFuelOptions":
		return r.targets["/v1/fuel-options"]
	case "listHydrogenOptions":
		return r.targets["/v1/hydrogen-options"]
	case "listNearby":
		return r.targets["/v1/nearby"]
	case "listStations":
		return r.targets["/v1/stations"]
	case "listSpeedCameras":
		return r.targets["speed"]
	case "listSharingOptions":
		return r.targets["sharing"]
	case "describeLocation":
		return r.targets["location"]
	case "listLorryRules":
		return r.targets["lorry"]
	case "listPlaces":
		return r.targets["places"]
	case "listAddresses":
		return r.targets["addresses"]
	case "listPois":
		return r.targets["pois"]
	case "listStationDepartures":
		return r.targets["departures"]
	case "listStationFacilities":
		return r.targets["facilities"]
	case "listTransitDisruptions":
		return r.targets["disruptions"]
	case "listStateRoadConditions":
		return r.targets["states"]
	case "listMotorwayParking":
		return r.targets["motorway-parking"]
	case "listMotorways":
		return r.targets["motorways"]
	case "listSources":
		return r.targets["sources"]
	case "getWatch":
		return r.targets["watch"]
	case "getFuelStation":
		return r.targets["fuel-station"]
	case "getChargingSite":
		return r.targets["charging-site"]
	case "getTrainBoard":
		return r.targets["train-board"]
	case "listParkingBays":
		return r.targets["parking-bays"]
	case "listStateServiceAlerts":
		return r.targets["state-alerts"]
	case "listStateWeatherWarnings":
		return r.targets["state-weather"]
	case "getNationalSummary":
		return r.targets["summaries"]
	case "getPlacesCoverage":
		return r.targets["coverage"]
	case "getPressure":
		return r.targets["pressure"]
	case "listAffectedStations":
		return r.targets["affected"]
	case "createRefresh":
		return r.targets["create-refresh"]
	case "getRefresh":
		return r.targets["get-refresh"]
	}
	return nil
}

// call sends one request and returns its status, ETag and the time from the
// first byte read to the last byte written, as the service boundary sees it.
func call(h http.Handler, target, ifNoneMatch string) (int, string, time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), testBound)
	defer cancel()
	q := request(ctx, target)
	q.Header.Set("Accept-Encoding", "br, gzip")
	if ifNoneMatch != "" {
		q.Header.Set("If-None-Match", ifNoneMatch)
	}
	w := httptest.NewRecorder()
	start := time.Now()
	h.ServeHTTP(w, q)
	return w.Code, w.Header().Get("ETag"), time.Since(start)
}

// request is the request of target: a GET of the path, or an ask written
// "POST <path> <body>". A refresh is asked and read, and a fuel station's
// page read, with the own key; everything else with the benchmark key.
func request(ctx context.Context, target string) *http.Request {
	if rest, ok := strings.CutPrefix(target, http.MethodPost+" "); ok {
		path, body, _ := strings.Cut(rest, " ")
		q := httptest.NewRequestWithContext(ctx, http.MethodPost, path, strings.NewReader(body))
		q.Header.Set("Authorization", "Bearer "+ownBearer)
		q.Header.Set("Content-Type", "application/json")
		return q
	}
	q := httptest.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	q.Header.Set("Authorization", "Bearer benchmark")
	if strings.HasPrefix(target, "/v1/refreshes/") || strings.HasPrefix(target, "/v1/fuel-options/") {
		q.Header.Set("Authorization", "Bearer "+ownBearer)
	}
	return q
}

// send sends one request and returns its status and Location.
func send(ctx context.Context, h http.Handler, target string) (int, string) {
	ctx, cancel := context.WithTimeout(ctx, testBound)
	defer cancel()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request(ctx, target))
	return w.Code, w.Header().Get("Location")
}

// wantOf is the status a measured request of op answers: an ask answered
// by a refresh already settled is 201, every other read 200.
func wantOf(op string) int {
	if op == "createRefresh" {
		return http.StatusCreated
	}
	return http.StatusOK
}

// timed sends n requests over the targets from concurrency workers and
// returns their latencies; any answer but want fails.
func timed(t *testing.T, h http.Handler, targets []string, n int, want int, inm func(string) string) []time.Duration {
	t.Helper()
	var (
		mu  sync.Mutex
		out []time.Duration
		bad atomic.Value
		wg  sync.WaitGroup
		at  atomic.Int64
	)
	for range concurrency {
		wg.Go(func() {
			for {
				i := at.Add(1) - 1
				if i >= int64(n) {
					return
				}
				target := targets[int(i)%len(targets)]
				code, _, d := call(h, target, inm(target))
				if code != want {
					bad.Store(fmt.Sprintf("%s answered %d, want %d", target, code, want))
					continue
				}
				mu.Lock()
				out = append(out, d)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	if b := bad.Load(); b != nil {
		t.Fatal(b)
	}
	return out
}

func noValidator(string) string { return "" }

// TestEveryOperationMeetsItsBudget measures every operation through the real
// handler and store on the fixtures, the result cache off so each request
// reads storage, and the 304 of a data operation with the cache on.
func TestEveryOperationMeetsItsBudget(t *testing.T) {
	if *assert {
		req.Covers(t, "NFR-PERF-003")
	}
	r := open(t)
	budgets := loadBudgets(t, r.cfg.Deadline.Request)
	h := r.handler(t, false)
	var findings []string
	// One subtest per operation: each is its own leaf under the per-test
	// bound (TST-012), so the catalogue can grow without one test carrying
	// every operation's samples.
	for _, op := range httpapi.OperationIDs() {
		t.Run(op, func(t *testing.T) {
			targets := r.targetsOf(op)
			if len(targets) == 0 {
				t.Errorf("%s has no benchmark request; add one to targetsOf", op)
				return
			}
			class, _ := budgets.Of(op)
			timed(t, h, targets, warmup, wantOf(op), noValidator)
			res := perf.Measure(op, timed(t, h, targets, *samples, wantOf(op), noValidator))
			t.Logf("%s  budget %s p95=%s p99=%s", res, class.Name, class.P95, class.P99)
			findings = append(findings, res.Check(class)...)
		})
	}
	t.Run("conditional", func(t *testing.T) {
		cached := r.handler(t, true)
		// An answer that lists nothing credits no family and so carries no
		// ETag (the read of a site whose family the key may not read, say):
		// the 304 is measured over the answers that carry one.
		var targets []string
		etags := map[string]string{}
		for _, target := range r.targetsOf("listChargingOptions") {
			code, etag, _ := call(cached, target, "")
			if code != http.StatusOK {
				t.Fatalf("%s answered %d, want 200", target, code)
			}
			if etag != "" {
				targets = append(targets, target)
				etags[target] = etag
			}
		}
		if len(targets) < 10 {
			t.Fatalf("%d charging answers carry an ETag, want at least 10 to measure the 304 over", len(targets))
		}
		res := perf.Measure("304 (listChargingOptions)", timed(t, cached, targets, *samples, http.StatusNotModified, func(s string) string { return etags[s] }))
		class := budgets.Classes[perf.Conditional]
		t.Logf("%s  budget %s p95=%s p99=%s", res, class.Name, class.P95, class.P99)
		findings = append(findings, res.Check(class)...)
	})
	if *assert {
		for _, f := range findings {
			t.Error(f)
		}
	} else if len(findings) > 0 {
		t.Logf("not asserted in this run (-perf.assert is off): %s", strings.Join(findings, "; "))
	}
}

func loadBudgets(t *testing.T, deadline time.Duration) perf.Budgets {
	t.Helper()
	c, err := os.Open("../../test/perf/classes.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	o, err := os.Open("../../test/perf/operations.tsv")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = o.Close() }()
	b, err := perf.Load(c, o, deadline)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestRepeatedRequestsAreNotModified is the repeated-request scenario: a client repeating
// one request with the last ETag is answered 304 at least 95 % of the time.
func TestRepeatedRequestsAreNotModified(t *testing.T) {
	req.Covers(t, "NFR-PERF-005")
	r := open(t)
	h := r.handler(t, true)
	// A charging answer withholding a family the key may not read is
	// partial and carries no ETag; the scenario repeats one that carries one.
	var target, etag string
	for _, c := range r.targetsOf("listChargingOptions") {
		if _, e, _ := call(h, c, ""); e != "" {
			target, etag = c, e
			break
		}
	}
	if etag == "" {
		t.Fatal("no charging answer carries an ETag")
	}
	notModified := 0
	const repeats = 100
	for range repeats {
		code, next, _ := call(h, target, etag)
		switch code {
		case http.StatusNotModified:
			notModified++
		case http.StatusOK:
			etag = next
		default:
			t.Fatalf("a repeat answered %d", code)
		}
	}
	if notModified*100 < repeats*95 {
		t.Fatalf("%d of %d repeats were 304; at least 95 %% must be", notModified, repeats)
	}
}

// TestIdenticalConcurrentRequestsReadStorageOnce is the single-flight
// scenario: 50 identical concurrent requests on a cold cache cause exactly
// one storage read of the operation.
func TestIdenticalConcurrentRequestsReadStorageOnce(t *testing.T) {
	req.Covers(t, "NFR-PERF-006")
	r := open(t)
	h := r.handler(t, true)
	target := r.targetsOf("listChargingOptions")[1]
	// The charging list's read: the named statement over the charging sites.
	before := r.sql.reads("api_charging_near")
	start := make(chan struct{})
	var wg sync.WaitGroup
	codes := make([]int, 50)
	for i := range codes {
		wg.Go(func() {
			<-start
			codes[i], _, _ = call(h, target, "")
		})
	}
	close(start)
	wg.Wait()
	for _, c := range codes {
		if c != http.StatusOK {
			t.Fatalf("answers %v", codes)
		}
	}
	if n := r.sql.reads("api_charging_near") - before; n != 1 {
		t.Fatalf("%d storage reads for 50 identical concurrent requests, want 1", n)
	}
}

// TestNoSequentialScanOfTimeSeries runs every operation once, then explains
// every read statement the service sent, as a custom plan with its real
// arguments and as a generic plan, with sequential scans priced out, and
// fails on a sequential scan of any time partition (NFR-PERF-004).
//
// With enable_seqscan off the planner takes a sequential scan only when no
// other path exists, so the test proves that an index path exists for every
// read of a time series. It does not prove the planner picks that path with
// sequential scans priced normally: on the small fixture tables a
// sequential scan is often cheaper, and the planner rightly takes it.
func TestNoSequentialScanOfTimeSeries(t *testing.T) {
	req.Covers(t, "NFR-PERF-004")
	r := open(t)
	h := r.handler(t, false)
	for _, op := range httpapi.OperationIDs() {
		// An operation with no benchmark request yet is named by
		// TestEveryOperationMeetsItsBudget; it is not read here.
		for _, target := range r.targetsOf(op)[:min(1, len(r.targetsOf(op)))] {
			if code, _, _ := call(h, target, ""); code != wantOf(op) {
				t.Fatalf("%s answered %d", target, code)
			}
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	parts, err := partitions(ctx, conn)
	if err != nil || len(parts) == 0 {
		t.Fatalf("no time partition to guard: %v", err)
	}
	r.sql.mu.Lock()
	stmts := make([]statement, 0, len(r.sql.seen))
	for _, s := range r.sql.seen {
		stmts = append(stmts, s)
	}
	r.sql.mu.Unlock()
	timeSeries, named, namedSeries := 0, 0, 0
	for _, s := range stmts {
		if s.name != "" {
			named++
		}
		for _, mode := range []string{"custom", "generic"} {
			plan, err := explain(ctx, conn, s, mode)
			if err != nil {
				t.Errorf("explain (%s) failed: %v\n%s %s", mode, err, s.name, s.sql)
				continue
			}
			scanned, seq := relations(plan, parts)
			if len(scanned) > 0 && mode == "custom" {
				timeSeries++
				if s.name != "" {
					namedSeries++
				}
			}
			for _, rel := range seq {
				t.Errorf("a sequential scan of the time partition %s (%s plan):\n%s %s", rel, mode, s.name, s.sql)
			}
		}
	}
	t.Logf("explained %d read statements (%d named), %d of them on time series (%d named), in custom and generic plans",
		len(stmts), named, timeSeries, namedSeries)
	if timeSeries == 0 || namedSeries == 0 {
		t.Fatalf("%d statements, %d of them named, read a time series: the test guarded no named read", timeSeries, namedSeries)
	}
}

// explain returns the JSON plan of s with sequential scans priced out
// (enable_seqscan = off): a sequential scan in it means no other path exists.
func explain(ctx context.Context, conn *pgx.Conn, s statement, mode string) (any, error) {
	var raw []byte
	err := pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "set local enable_seqscan = off"); err != nil {
			return err
		}
		if mode == "generic" {
			// The raw simple protocol: the driver would otherwise bind (or
			// substitute) the $n placeholders a generic plan keeps.
			res, err := tx.Conn().PgConn().Exec(ctx, "explain (generic_plan, format json) "+s.sql).ReadAll()
			if err != nil {
				return err
			}
			if len(res) != 1 || len(res[0].Rows) != 1 {
				return fmt.Errorf("explain answered %d results", len(res))
			}
			raw = res[0].Rows[0][0]
			return nil
		}
		return tx.QueryRow(ctx, "explain (format json) "+s.sql, s.args...).Scan(&raw)
	})
	if err != nil {
		return nil, err
	}
	var plan any
	return plan, json.Unmarshal(raw, &plan)
}

// relations walks a JSON plan and returns the time partitions it reads and
// those it reads by a sequential scan.
func relations(plan any, parts map[string]bool) (scanned, seq []string) {
	var walk func(any)
	walk = func(n any) {
		switch v := n.(type) {
		case []any:
			for _, e := range v {
				walk(e)
			}
		case map[string]any:
			if rel, ok := v["Relation Name"].(string); ok && parts[rel] {
				scanned = append(scanned, rel)
				if v["Node Type"] == "Seq Scan" {
					seq = append(seq, rel)
				}
			}
			for _, e := range v {
				walk(e)
			}
		}
	}
	walk(plan)
	return scanned, seq
}
