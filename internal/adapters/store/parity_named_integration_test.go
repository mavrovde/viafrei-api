//go:build integration

package store_test

import (
	"context"
	"flag"
	"fmt"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/domain/geo"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/domain/transit"
)

// namedCases are the arguments a parity test runs a named statement with,
// read from the seeded database by each statement's own case builder.
var namedCases = map[string]func(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any{
	"api_places_page": placesCases,
	"api_kreis_names": kreisCases,
}

// column reads one text column of the seeded database.
func column(t *testing.T, ctx context.Context, c *pgx.Conn, sql string) []string {
	t.Helper()
	rows, _ := c.Query(ctx, sql)
	out, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func placesCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	families := column(t, ctx, c, `select distinct source_id from public.gazetteer_places`)
	stops := column(t, ctx, c, `select distinct source_feed from public.feeds`)
	roads := []string{"bast_lcl", "autobahn"}
	texts := column(t, ctx, c, `select left(contract_search_fold(name), 3) from public.gazetteer_places where geom is not null
		union select left(contract_search_fold(name_en), 2) from public.gazetteer_places where name_en is not null
		union select left(contract_search_fold(name), 4) from public.stops where name is not null`)
	motorways := column(t, ctx, c, `select road from public.autobahn_roads order by road limit 3`)
	berlin, munich := []float64{52.52, 13.405}, []float64{48.137, 11.575}
	var out [][]any
	for _, q := range append(texts, "zzzz-nothing") {
		out = append(out,
			[]any{families, q, "", nil, nil, 0, 26, stops, roads, ""},
			[]any{families, q, "municipality", nil, nil, 0, 26, stops, roads, ""},
			[]any{families, q, "", berlin[0], berlin[1], 0, 6, stops, roads, ""},
			[]any{families, q, "", munich[0], munich[1], 50000, 26, stops, roads, ""},
			[]any{families[:1], q, "", nil, nil, 0, 26, []string{}, []string{"bast_lcl"}, ""})
	}
	for _, m := range motorways {
		out = append(out,
			[]any{families, strings.ToLower(m), "", nil, nil, 0, 26, stops, roads, m},
			[]any{families, strings.ToLower(m), "motorway", berlin[0], berlin[1], 0, 26, stops, []string{"bast_lcl"}, m})
	}
	return out
}

func kreisCases(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any {
	// The fixture's districts are regions: plant an open district row of the
	// platform's Kreis family for a fixture Gemeinde, and a closed one.
	o, err := pgx.Connect(ctx, db.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = o.Close(context.WithoutCancel(ctx)) }()
	if _, err := o.Exec(ctx, `insert into vf_seed.place (place_id, place_kind, name, location, region_code, official_key, prominence, source_ref, origin_key,
    recorded_at, changed_at, withdrawn_at, source_refs, display_class, audience)
  select k.id, 'district', k.name, p.location, p.region_code, left(p.official_key, 5), 0, $1, 'o-' || k.id,
    p.recorded_at, p.changed_at, k.gone, array[$1::text], 'open', 'public'
  from (select p.* from contract.place p where p.place_kind = 'municipality' and p.official_key ~ '^[0-9]{8}$'
        order by p.place_id limit 1) p,
       (values ('990000011', 'Paritätskreis', null::timestamptz), ('990000012', 'Altkreis', now())) k(id, name, gone)`, places.KreisFamily); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		o, err := pgx.Connect(context.WithoutCancel(ctx), db.OwnerURL)
		if err == nil {
			_, err = o.Exec(context.WithoutCancel(ctx), `delete from contract.place where place_id in ('990000011', '990000012')`)
			_ = o.Close(context.WithoutCancel(ctx))
		}
		if err != nil {
			t.Error(err)
		}
	})
	keys := column(t, ctx, c, `select distinct admin_code from public.gazetteer_places where kind = 'district' and admin_code is not null`)
	families := column(t, ctx, c, `select distinct source_id from public.gazetteer_places`)
	return [][]any{{keys, families}, {keys, []string{}}, {[]string{"00000"}, families}}
}

// collect reads every row of sql run with args as text.
func collect(t *testing.T, ctx context.Context, tx pgx.Tx, sql string, args []any) []string {
	t.Helper()
	rows, err := tx.Query(ctx, sql, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		v, err := rows.Values()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, store.RowText(v))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%v (%v)", err, args)
	}
	return out
}

// TestNamedStatementsAnswerAsTheViews: each named statement answers every
// case with the rows of the view statement it replaced, in order, for a
// public key and an own key; under the plan the server caches after its
// custom plans (the generic plan) it answers the same rows, and with the
// plan cache left to choose it settles on that generic plan.
func TestNamedStatementsAnswerAsTheViews(t *testing.T) {
	byName := map[string]store.NamedRead{}
	for _, r := range store.NamedReads() {
		byName[r.Name] = r
	}
	// One pool for every leaf: its connections are made once, and it holds
	// as many reads side by side as the reader's statement timeout bears.
	cfg, err := pgxpool.ParseConfig(db.ReaderURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = poolSize
	ctx, cancel := context.WithTimeout(context.Background(), statementBound)
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	cancel()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	// The statements that plant rows one after the other (their cases are
	// read while their rows are there), the rest side by side; within a
	// statement, its settling and each case are leaves of their own, side by
	// side.
	t.Run("statements", func(t *testing.T) {
		for name, cases := range namedCases {
			t.Run(name, func(t *testing.T) {
				if !plants(name) {
					t.Parallel()
				}
				namedParity(t, pool, byName, name, cases)
			})
		}
	})
}

// poolSize is a connection for each leaf the run holds side by side
// (-parallel, GOMAXPROCS by default), and two to spare: a leaf never waits
// for a connection another leaf holds.
var poolSize = int32(runtime.GOMAXPROCS(0) + 2)

// statementBound is the ceiling on one leaf of the harness: a statement's
// cases read, its settling, or one case over both keys and both plans.
const statementBound = time.Minute

// namedParity is one statement's parity: its cases read, then a leaf that
// checks it settles on a generic plan and a leaf a case, each over both
// keys and both plans; when they are done, whether any case answered a row.
func namedParity(t *testing.T, pool *pgxpool.Pool, byName map[string]store.NamedRead, name string,
	cases func(t *testing.T, ctx context.Context, c *pgx.Conn) [][]any) {
	r, ok := byName[name]
	if !ok {
		t.Fatalf("no named statement %s", name)
	}
	legacy := store.LegacyRead(name)
	if legacy == "" {
		t.Fatalf("no frozen view statement for %s", name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), statementBound)
	c, err := pool.Acquire(ctx)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	argSets := cases(t, ctx, c.Conn())
	c.Release()
	cancel()
	var matched atomic.Int64
	// Registered before the leaves start: runs when they have all ended.
	t.Cleanup(func() {
		if !t.Failed() && matched.Load() == 0 {
			t.Error("no case answered a row: the parity proves nothing")
		}
	})
	if len(argSets) > 0 {
		t.Run("settles", func(t *testing.T) {
			t.Parallel()
			inTx(t, pool, r, "off", func(ctx context.Context, tx pgx.Tx) {
				// A name of its own, prepared now: no run under a forced
				// plan has counted a plan of it.
				n := "settle_" + r.Name
				if _, err := tx.Conn().Prepare(ctx, n, r.SQL); err != nil {
					t.Fatal(err)
				}
				if err := settlesGeneric(ctx, tx, n, argSets[0]); err != nil {
					t.Error(err)
				}
			})
		})
	}
	// A leaf a case, key and plan: the view statement, then the named one
	// under that plan, in one snapshot.
	for i, args := range argSets {
		for _, own := range []string{"off", "on"} {
			for _, plan := range []string{"force_custom_plan", "force_generic_plan"} {
				t.Run(fmt.Sprintf("%d/own_%s/%s", i, own, plan), func(t *testing.T) {
					t.Parallel()
					inTx(t, pool, r, own, func(ctx context.Context, tx pgx.Tx) {
						want := collect(t, ctx, tx, legacy, args)
						if _, err := tx.Exec(ctx, "set local plan_cache_mode = "+plan); err != nil {
							t.Fatal(err)
						}
						if got := collect(t, ctx, tx, r.Name, args); !reflect.DeepEqual(got, want) {
							t.Errorf("own %s %v, %s:\n got %q\nwant %q", own, args, plan, got, want)
						}
						matched.Add(int64(len(want)))
						store.NoteParity(r.Name, len(want))
					})
				})
			}
		}
	}
}

// inTx runs fn in a read-only transaction of a connection of pool, under
// the key's own-audience setting own, with r prepared on the connection.
func inTx(t *testing.T, pool *pgxpool.Pool, r store.NamedRead, own string, fn func(context.Context, pgx.Tx)) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), statementBound)
	defer cancel()
	c, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Release()
	tx, err := c.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, "select set_config('viafrei_api.own_key', $1, true)", own); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Conn().Prepare(ctx, r.Name, r.SQL); err != nil {
		t.Fatal(err)
	}
	fn(ctx, tx)
}

// plants reports whether the cases of the statement name plant rows of
// their own, which their test removes when it ends (kreisCases,
// plantNeedPlaces): those run one after the other, before the rest.
func plants(name string) bool {
	return name == "api_kreis_names" || strings.HasPrefix(name, "api_nearby_need_")
}

// settlingRuns are the runs after which the plan cache, left to choose,
// holds a generic plan: five custom plans, then the generic one if it costs
// no more than they did on average.
const settlingRuns = 6

// settlesGeneric runs the prepared statement name with args settlingRuns
// times under tx's plan cache mode and fails unless the server then planned
// it generically at least once: the server caches the plan, not re-planning
// each call.
func settlesGeneric(ctx context.Context, tx pgx.Tx, name string, args []any) error {
	for range settlingRuns {
		rows, err := tx.Query(ctx, name, args...)
		if err != nil {
			return err
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	var generic int64
	if err := tx.QueryRow(ctx, "select generic_plans from pg_prepared_statements where name = $1", name).Scan(&generic); err != nil {
		return err
	}
	if generic == 0 {
		return fmt.Errorf("%s: never planned generically after %d runs under the plan cache's choice", name, settlingRuns)
	}
	return nil
}

// TestSettlingCheckFailsWithoutAGenericPlan: the check passes a statement
// the plan cache settles on a generic plan for, and fails one it never plans
// generically (custom plans forced).
func TestSettlingCheckFailsWithoutAGenericPlan(t *testing.T) {
	ctx := bounded(t)
	c, err := pgx.Connect(ctx, db.ReaderURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(context.WithoutCancel(ctx)) }()
	for _, mode := range []string{"auto", "force_custom_plan"} {
		tx, err := c.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.Exec(ctx, "set local plan_cache_mode = "+mode); err != nil {
			t.Fatal(err)
		}
		name := "settle_" + mode
		if _, err := tx.Conn().Prepare(ctx, name, "select $1::int + 1"); err != nil {
			t.Fatal(err)
		}
		err = settlesGeneric(ctx, tx, name, []any{1})
		if (err == nil) != (mode == "auto") {
			t.Errorf("%s: %v", mode, err)
		}
		_ = tx.Rollback(ctx)
	}
}

// noRowsAccepted are the named statements a full run may compare with 0
// rows in every run, each with its reason.
var noRowsAccepted = map[string]string{}

// parityHeld reports whether every named statement the tests ran answered
// the rows of its view statement, under both plans, and whether a full run
// ran each named statement at least once.
func parityHeld() bool {
	// The named statements the tests ran, compared now, outside their budget.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	if err := store.ReplayParity(ctx, db.ReaderURL); err != nil {
		fmt.Fprintln(os.Stderr, "parity: the replay failed:", err)
		return false
	}
	misses, runs, rows := store.ParityResults()
	for name, n := range runs {
		fmt.Fprintf(os.Stderr, "parity: %s ran %d times beside its view statement, %d rows compared\n", name, n, rows[name])
	}
	for _, m := range misses {
		fmt.Fprintf(os.Stderr, "parity: %s (%s) %v:\n got %q\nwant %q\n", m.Name, m.Plan, m.Args, m.Got, m.Want)
	}
	for name, n := range store.ReplayedAfter() {
		fmt.Fprintf(os.Stderr, "parity: %s: %d checks read outside a test's context, compared after the tests\n", name, n)
	}
	unrun := 0
	if flag.Lookup("test.run").Value.String() == "" {
		for _, r := range store.NamedReads() {
			switch {
			case runs[r.Name] == 0:
				fmt.Fprintf(os.Stderr, "parity: %s never ran beside its view statement\n", r.Name)
				unrun++
			case rows[r.Name] == 0 && noRowsAccepted[r.Name] == "":
				fmt.Fprintf(os.Stderr, "parity: %s compared 0 rows in every run: the parity proves nothing\n", r.Name)
				unrun++
			}
		}
	}
	return len(misses) == 0 && unrun == 0
}

// TestFacilitiesAnswerAsTheirView: each fixture station's facilities, read
// beside the view statement (the parity hook), with a fresh and a stale
// clock, and some station answers some.
func TestFacilitiesAnswerAsTheirView(t *testing.T) {
	st := seeded(t)
	var ids []string
	owner(t, `select coalesce(array_agg(distinct stop_place_id), '{}') from contract.station_facility where stop_place_id is not null`, &ids)
	if len(ids) == 0 {
		t.Fatal("the fixture holds no station facility")
	}
	var fams []string
	owner(t, `select coalesce(array_agg(distinct source_ref), '{}') from contract.station_facility`, &fams)
	n := 0
	for _, id := range append(ids, "1:de:00000:0", "x") {
		for _, at := range []time.Time{time.Now(), time.Now().Add(240 * time.Hour)} {
			fs, err := st.Facilities(bounded(t), id, fams, at)
			if err != nil {
				t.Fatal(err)
			}
			n += len(fs)
		}
	}
	if n == 0 {
		t.Error("no station answered a facility")
	}
}

// TestEveryStationShapeAnswersAsTheView: a station search of each shape,
// from fixture stations, read beside the view statement (the parity hook).
func TestEveryStationShapeAnswersAsTheView(t *testing.T) {
	st := seeded(t)
	var fams []string
	var name, code, area string
	var lat, lon float64
	owner(t, `select coalesce(array_agg(distinct source_ref), '{}') from contract.stop_place`, &fams)
	owner(t, `select left(contract_search_fold(name), 4), coalesce(eva_no, national_stop_code, ''), coalesce(left(region_code, 2), ''),
  st_y(location), st_x(location) from contract.stop_place where location is not null order by eva_no is null, stop_place_id limit 1`, &name, &code, &area, &lat, &lon)
	for m := range 1 << 5 {
		q := transit.Query{Limit: 20, RadiusM: everywhere}
		if m&1 != 0 {
			q.At = &geo.Point{Lat: lat, Lon: lon}
		}
		if m&2 != 0 {
			q.Text = name
		}
		if m&4 != 0 {
			q.Code = code
		}
		q.Railway = m&8 != 0
		if m&16 != 0 {
			q.Area = area
		}
		if _, _, err := st.Stations(bounded(t), q, fams); err != nil {
			t.Fatalf("shape %02d: %v", m, err)
		}
	}
}

// TestEveryBoardAnswersAsTheView: the board of each fixture station that
// departs, over the days its calls depart on, read beside the view
// statement (the parity hook); some board answers departures.
func TestEveryBoardAnswersAsTheView(t *testing.T) {
	st := seeded(t)
	var ids, fams []string
	var from, to time.Time
	owner(t, `select coalesce(array_agg(distinct stop_place_id), '{}'), coalesce(array_agg(distinct source_ref), '{}'),
  min(planned_departure_at), max(planned_departure_at) from contract.departure where planned_departure_at is not null`, &ids, &fams, &from, &to)
	if len(ids) == 0 {
		t.Fatal("the fixture holds no departure")
	}
	n := 0
	for _, id := range ids {
		for _, w := range []struct {
			at  time.Time
			win time.Duration
		}{{from, to.Sub(from) + time.Minute}, {from.Add(to.Sub(from) / 2), 3 * time.Hour}} {
			ds, err := st.Departures(bounded(t), transit.DepartureQuery{StationID: id, From: w.at, Window: w.win, Limit: 500}, fams)
			if err != nil {
				t.Fatal(err)
			}
			n += len(ds)
		}
	}
	if n == 0 {
		t.Error("no board answered a departure")
	}
}
