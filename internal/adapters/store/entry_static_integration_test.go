//go:build integration

package store_test

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
)

// strategySQL is the strategy read's statement text.
func strategySQL(t *testing.T) string {
	t.Helper()
	for _, s := range store.NamedReads() {
		if s.Name == "api_road_status_strategies" {
			return s.SQL
		}
	}
	t.Fatal("no strategy read")
	return ""
}

// The strategy read's entry facts take the clock-free half the database
// stores at write time (road_events.entry_static, kept by its trigger from
// road_event_entry_static) and compute it only where it is null: the rows
// are the same either way, and a planted value is what the read answers.
func TestStrategyEntryFactsReadTheStoredHalf(t *testing.T) {
	_, pdb := private(t, append(roadStatusSeed,
		`insert into public.road_events (feed_key, source_id, publication_id, version, payload, event_type, road_number, overall_start, overall_end, source_feed, first_seen_at, updated_at, last_seen_at)
		 values ('roadworks_bast', 's', 'b2', 1, '{"situationRecord": [{"groupOfLocations": {"alertCMethod4PrimaryPointLocation": {"alertCLocation": {"specificLocation": {"#text": "12345"}}}, "alertCDirectionCoded": {"#text": "positive"}}}]}',
		         'MaintenanceWorks', 'A99', now() - interval '1 day', now() + interval '1 day', 'roadworks_bast', now(), now(), now() - interval '1 hour')`)...)
	ctx := bounded(t)
	conn, err := pgx.Connect(ctx, pdb.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })
	var same bool
	if err := conn.QueryRow(ctx, `select bool_and(entry_static is not null and entry_static = road_event_entry_static(payload)) from road_events where publication_id in ('b1', 'b2')`).Scan(&same); err != nil || !same {
		t.Fatalf("the trigger stored the half: %v %v", same, err)
	}
	now := time.Now()
	read := func() map[string]string {
		rows, _ := conn.Query(ctx, `select q.publication_id, q.entry_facts::text from (`+strategySQL(t)+`) q`,
			[]string{"roadworks_bast", "routing_bab"}, []string{"A99"}, now, now.Add(roads.StrategyPlanDays*24*time.Hour), roads.StrategyPlanMax, roads.StrategyFamilies)
		out := map[string]string{}
		var id string
		var facts *string
		_, err := pgx.ForEachRow(rows, []any{&id, &facts}, func() error {
			if facts != nil {
				out[id] = *facts
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	stored := read()
	if !strings.Contains(stored["b2"], `"pri": "12345"`) || stored["b1"] == "" {
		t.Fatalf("entry facts %v", stored)
	}
	if _, err := conn.Exec(ctx, `update road_events set entry_static = null where publication_id in ('b1', 'b2')`); err != nil {
		t.Fatal(err)
	}
	if computed := read(); len(computed) != len(stored) || computed["b1"] != stored["b1"] || computed["b2"] != stored["b2"] {
		t.Errorf("computed %v, stored %v", computed, stored)
	}
	if _, err := conn.Exec(ctx, `update road_events set entry_static = '{"pri": "planted"}' where publication_id = 'b2'`); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read()["b2"], `"pri": "planted"`) {
		t.Error("the stored half was not read")
	}
}

// Drift guard: the stored value is the read's own only while the
// database's road_event_entry_static says what the statement's clock-free
// half says, character for character up to whitespace.
func TestEntryStaticIsTheStatementsClockFreeHalf(t *testing.T) {
	_, pdb := private(t)
	ctx := bounded(t)
	conn, err := pgx.Connect(ctx, pdb.OwnerURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close(ctx) })
	var def string
	if err := conn.QueryRow(ctx, `select pg_get_functiondef('public.road_event_entry_static(jsonb)'::regprocedure)`).Scan(&def); err != nil {
		t.Fatal(err)
	}
	body := def[strings.Index(def, "$function$")+len("$function$") : strings.LastIndex(def, "$function$")]
	sql := strategySQL(t)
	head := "coalesce(r.entry_static, jsonb_build_object("
	from := strings.Index(sql, head)
	to := strings.Index(sql[from:], ")) || coalesce((")
	if from < 0 || to < 0 {
		t.Fatal("the statement reads no stored half")
	}
	half := "(" + strings.TrimPrefix(sql[from:from+to+1], "coalesce(r.entry_static, ")
	half = strings.ReplaceAll(half, "dp.p", "p_payload")
	ws := regexp.MustCompile(`\s+`)
	norm := func(s string) string { return strings.TrimSpace(ws.ReplaceAllString(s, " ")) }
	if norm(body) != norm("select "+half+")") {
		t.Errorf("the function and the statement differ:\n function  %s\n statement %s", norm(body), norm("select "+half+")"))
	}
}
