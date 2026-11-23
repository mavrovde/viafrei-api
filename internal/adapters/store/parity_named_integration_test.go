//go:build integration

package store_test

import (
	"context"
	"flag"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

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
		out = append(out, fmt.Sprint(v...))
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
	// Every case runs three times over two keys: bounded as a whole suite.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c, err := pgx.Connect(ctx, db.ReaderURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close(context.WithoutCancel(ctx)) }()
	byName := map[string]store.NamedRead{}
	for _, r := range store.NamedReads() {
		byName[r.Name] = r
	}
	for name, cases := range namedCases {
		t.Run(name, func(t *testing.T) {
			r, ok := byName[name]
			if !ok {
				t.Fatalf("no named statement %s", name)
			}
			legacy := store.LegacyRead(name)
			if legacy == "" {
				t.Fatalf("no frozen view statement for %s", name)
			}
			argSets := cases(t, ctx, c)
			matched := 0
			for _, own := range []string{"off", "on"} {
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
				for _, args := range argSets {
					want := collect(t, ctx, tx, legacy, args)
					got := collect(t, ctx, tx, r.Name, args)
					if !reflect.DeepEqual(got, want) {
						t.Errorf("own %s %v:\n got %q\nwant %q", own, args, got, want)
					}
					matched += len(want)
					store.NoteParity(r.Name, len(want))
					if _, err := tx.Exec(ctx, "set local plan_cache_mode = force_generic_plan"); err != nil {
						t.Fatal(err)
					}
					if generic := collect(t, ctx, tx, r.Name, args); !reflect.DeepEqual(generic, want) {
						t.Errorf("own %s %v, generic plan:\n got %q\nwant %q", own, args, generic, want)
					}
					if _, err := tx.Exec(ctx, "set local plan_cache_mode = auto"); err != nil {
						t.Fatal(err)
					}
				}
				var generic int64
				if err := tx.QueryRow(ctx, "select generic_plans from pg_prepared_statements where name = $1", r.Name).Scan(&generic); err != nil {
					t.Fatal(err)
				}
				if generic == 0 {
					t.Errorf("own %s: never planned generically", own)
				}
				_ = tx.Rollback(ctx)
			}
			if matched == 0 {
				t.Error("no case answered a row: the parity proves nothing")
			}
		})
	}
}

// parityHeld reports whether every named statement the tests ran answered
// the rows of its view statement, under both plans, and whether a full run
// ran each named statement at least once.
func parityHeld() bool {
	misses, runs, rows := store.ParityResults()
	for name, n := range runs {
		fmt.Fprintf(os.Stderr, "parity: %s ran %d times beside its view statement, %d rows compared\n", name, n, rows[name])
	}
	for _, m := range misses {
		fmt.Fprintf(os.Stderr, "parity: %s (%s) %v:\n got %q\nwant %q\n", m.Name, m.Plan, m.Args, m.Got, m.Want)
	}
	unrun := 0
	if flag.Lookup("test.run").Value.String() == "" {
		for _, r := range store.NamedReads() {
			if runs[r.Name] == 0 {
				fmt.Fprintf(os.Stderr, "parity: %s never ran beside its view statement\n", r.Name)
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
