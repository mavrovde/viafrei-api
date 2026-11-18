//go:build integration

package store_test

import (
	"bytes"
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/dbcontract"
	"github.com/mavrovde/viafrei-api/internal/domain/access"
	"github.com/mavrovde/viafrei-api/internal/domain/provenance"
	"github.com/mavrovde/viafrei-api/internal/fixtures"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// seed loads the tiny fixture set and its planted rows into the shared test
// database. TestMain runs it once, as setup: generating and loading the
// fixtures is outside every test body's bound, which it would otherwise spend
// in whichever test happened to ask first.
// seedRows are the store tests' rows: the tiny set and its planted rows.
func seedRows() (string, error) {
	var buf bytes.Buffer
	if _, err := fixtures.Generate(&buf, "tiny", 1, dbcontract.ContractVersion); err != nil {
		return "", err
	}
	err := fixtures.Planted(&buf, "tiny", 1, dbcontract.ContractVersion)
	return buf.String(), err
}

// seeded opens a store on the shared test database, which TestMain seeded.
func seeded(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(context.Background(), readerConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(st.Close)
	return st
}

func TestRegisterReadsEveryFamilysTerms(t *testing.T) {
	req.Covers(t, "SEC-030")
	reg, err := seeded(t).Register(bounded(t))
	if err != nil {
		t.Fatal(err)
	}
	byFamily := map[string]access.Terms{}
	for _, r := range reg {
		byFamily[r.Family] = r
	}
	// Expected from docs/test-data.md, not from the code under test.
	if g := byFamily["chargepool_gamma"]; !slices.Equal(g.UseClasses, []access.UseClass{"consumer"}) || g.AcceptanceRequired {
		t.Errorf("restricted-purpose family: %+v", g)
	}
	// A family that needs accepting is versioned by its terms gate.
	if d := byFamily["truckpark_sid_static"]; !d.AcceptanceRequired || d.Version != "sid-terms-1" {
		t.Errorf("terms family: %+v", d)
	}
	if e := byFamily["roads_epsilon"]; e.Family == "" || len(e.UseClasses) != 0 {
		t.Errorf("unreviewed family: %+v", e)
	}
	if len(reg) < 20 {
		t.Errorf("only %d families read", len(reg))
	}
}

func TestSourcesReadsTheAllowedFamiliesOnly(t *testing.T) {
	req.Covers(t, "SEC-031")
	st := seeded(t)
	got, err := st.Sources(bounded(t), []string{"roads_alpha", "afir_beta", "no-such-family"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Family != "afir_beta" || got[1].Family != "roads_alpha" {
		t.Fatalf("read %+v", got)
	}
	if b := got[0]; !b.ShareAlike || b.ShareAlikeOffer == nil || b.Publisher == "" || b.TermsURL == nil {
		t.Errorf("share-alike attribution: %+v", b)
	}
	none, err := st.Sources(bounded(t), nil)
	if err != nil || len(none) != 0 {
		t.Fatalf("an empty allowed set read %d families: %v", len(none), err)
	}
}

func TestRegisterAndSourcesReportStorageFaults(t *testing.T) {
	st := seeded(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := st.Register(ctx); err == nil {
		t.Error("Register on an ended context succeeded")
	}
	if _, err := st.Sources(ctx, []string{"x"}); err == nil {
		t.Error("Sources on an ended context succeeded")
	}
}

func TestTheRegisterFingerprintMovesWithEveryLicenceInput(t *testing.T) {
	req.Covers(t, "NFR-PERF-005", "SEC-030")
	st := seeded(t)
	seedChanges(t, "source.register_version", "source.use_classes", "source.attribution_text", "source_note.body")
	ctx := bounded(t)
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	base, err := st.FreshnessVersions(ctx)
	if err != nil || base.Register == "" || len(base.Families) == 0 {
		t.Fatalf("versions %+v, %v", base, err)
	}
	for _, change := range []struct{ name, do, undo string }{
		{"register_version", `update contract.source set register_version = register_version + 1 where source_ref = 'truckpark_sid_static'`,
			`update contract.source set register_version = register_version - 1 where source_ref = 'truckpark_sid_static'`},
		{"use_classes", `update contract.source set use_classes = array_remove(use_classes, 'commercial') where source_ref = 'afir_alpha'`,
			`update contract.source set use_classes = use_classes || '{commercial}' where source_ref = 'afir_alpha'`},
		{"a note", `update contract.source_note set body = body || ' (probe)' where source_ref = 'chargepool_gamma'`,
			`update contract.source_note set body = left(body, -8) where source_ref = 'chargepool_gamma'`},
		{"an attribution", `update contract.source set attribution_text = attribution_text || ' (probe)' where source_ref = 'afir_beta'`,
			`update contract.source set attribution_text = left(attribution_text, -8) where source_ref = 'afir_beta'`},
	} {
		if _, err := conn.Exec(ctx, change.do); err != nil {
			t.Fatal(err)
		}
		got, err := st.FreshnessVersions(ctx)
		if _, uerr := conn.Exec(ctx, change.undo); uerr != nil {
			t.Fatal(uerr)
		}
		if err != nil || got.Register == base.Register {
			t.Errorf("%s changed and the fingerprint did not: %v", change.name, err)
		}
		if back, err := st.FreshnessVersions(ctx); err != nil || back.Register != base.Register {
			t.Errorf("after undoing %s the fingerprint is %q, want %q (%v)", change.name, back.Register, base.Register, err)
		}
	}
}

// TestAFamilyVersionMovesWithItsDataAsOf is the clock-skew case of the load
// tests: the source's data_as_of moves while its freshness version stays the
// same. Every answer renders its age, staleness and Last-Modified from
// data_as_of, so the version the HTTP cache compares, and the one an answer
// is tagged with, must move with it, and the two must stay equal.
func TestAFamilyVersionMovesWithItsDataAsOf(t *testing.T) {
	req.Covers(t, "NFR-PERF-005")
	st := seeded(t)
	seedChanges(t, "source_freshness.data_as_of")
	ctx := bounded(t)
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	const family = "chargepool_gamma"
	tagged := func() string {
		t.Helper()
		fs, err := st.Provenance(ctx, []string{family}, "en")
		if err != nil || len(fs) != 1 {
			t.Fatalf("provenance %+v, %v", fs, err)
		}
		return fs[0].Version
	}
	base, err := st.FreshnessVersions(ctx)
	if err != nil || base.Families[family] == "" {
		t.Fatalf("versions %+v, %v", base, err)
	}
	if got := tagged(); got != base.Families[family] {
		t.Fatalf("the answer is tagged %q, the cache compares %q", got, base.Families[family])
	}
	for _, c := range []struct{ shift, back string }{
		{"+ interval '10 minutes'", "- interval '10 minutes'"},
		{"- interval '30 days'", "+ interval '30 days'"},
	} {
		shift := c.shift
		if _, err := conn.Exec(ctx, `update contract.source_freshness set data_as_of = data_as_of `+shift+` where source_ref = $1`, family); err != nil {
			t.Fatal(err)
		}
		moved, err := st.FreshnessVersions(ctx)
		answer := tagged()
		if _, uerr := conn.Exec(ctx, `update contract.source_freshness set data_as_of = data_as_of `+c.back+` where source_ref = $1`, family); uerr != nil {
			t.Fatal(uerr)
		}
		if err != nil || moved.Families[family] == base.Families[family] {
			t.Errorf("data_as_of moved %s and the version did not: %q (%v)", shift, moved.Families[family], err)
		}
		if answer != moved.Families[family] {
			t.Errorf("after data_as_of moved %s the answer is tagged %q, the cache compares %q", shift, answer, moved.Families[family])
		}
		if back, err := st.FreshnessVersions(ctx); err != nil || back.Families[family] != base.Families[family] {
			t.Errorf("data_as_of moved back and the version is %q, want %q (%v)", back.Families[family], base.Families[family], err)
		}
	}
}

// TestTheLampsVersionsMoveWithWhatItJudges: the road lamp judges a family by
// its newest confirmation and reads the coverage of traffic messages and the
// junctions' states, so a confirmation moves that family's confirmation
// version and no data version, and a coverage row or a junction's state moves
// the lamp's reference and not the register.
func TestTheLampsVersionsMoveWithWhatItJudges(t *testing.T) {
	req.Covers(t, "NFR-PERF-005", "FR-ROAD-004")
	st := seeded(t)
	seedChanges(t, "source_freshness.confirmed_at", "source_freshness.confirm_interval_s", "source_freshness.updates_paused", "message_coverage.current_only", "junction.state_code")
	ctx := bounded(t)
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	const family = "chargepool_gamma"
	read := func() provenance.Versions {
		t.Helper()
		vs, err := st.FreshnessVersions(ctx)
		if err != nil || vs.LampReference == "" || vs.Confirmations[family] == "" {
			t.Fatalf("versions %+v, %v", vs, err)
		}
		return vs
	}
	exec := func(sql string) {
		t.Helper()
		if _, err := conn.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	base := read()
	var junction string
	var state *string
	owner(t, `select junction_id, state_code from contract.junction order by junction_id limit 1`, &junction, &state)
	var held struct {
		at       *string
		interval *int64
		paused   bool
	}
	owner(t, `select confirmed_at::text, confirm_interval_s::bigint, updates_paused from contract.source_freshness where source_ref = '`+family+`'`, &held.at, &held.interval, &held.paused)
	t.Cleanup(func() {
		ctx := context.WithoutCancel(ctx)
		c, err := db.Owner(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = c.Close(ctx) }()
		for _, sql := range []string{
			`update contract.source_freshness set confirmed_at = ` + literal(held.at) + `, confirm_interval_s = ` + literal(held.interval) + `, updates_paused = ` + fmt.Sprint(held.paused) + ` where source_ref = '` + family + `'`,
			`delete from contract.message_coverage where state_code = '96'`,
			`update contract.junction set state_code = ` + literal(state) + ` where junction_id = '` + junction + `'`,
		} {
			if _, err := c.Exec(ctx, sql); err != nil {
				t.Error(err)
			}
		}
	})
	seen := map[string]bool{base.Confirmations[family]: true}
	for _, change := range []string{
		`update contract.source_freshness set confirmed_at = '2102-01-01T00:30:00Z' where source_ref = '` + family + `'`,
		`update contract.source_freshness set confirm_interval_s = 301 where source_ref = '` + family + `'`,
		`update contract.source_freshness set updates_paused = true where source_ref = '` + family + `'`, // paused by its budget
	} {
		before := read()
		exec(change)
		moved := read()
		if seen[moved.Confirmations[family]] {
			t.Errorf("%s did not move the confirmation version %q", change, moved.Confirmations[family])
		}
		seen[moved.Confirmations[family]] = true
		// The confirmation interval is the family's freshness target too
		// (the platform keeps one interval), so it moves the register.
		registerMoves := strings.Contains(change, "confirm_interval_s")
		if moved.Families[family] != base.Families[family] || (moved.Register != before.Register) != registerMoves || moved.LampReference != base.LampReference {
			t.Errorf("%s moved more than the confirmation version: %+v", change, moved)
		}
	}
	refs := map[string]bool{base.LampReference: true}
	for _, change := range []string{
		`insert into contract.message_coverage (state_code, source_ref, coverage_role, current_only) values ('96', '` + family + `', 'own', true)`,
		`update contract.message_coverage set current_only = false where state_code = '96'`,
		`update contract.junction set state_code = '96' where junction_id = '` + junction + `'`,
	} {
		before := read().Register
		exec(change)
		moved := read()
		if refs[moved.LampReference] {
			t.Errorf("%s did not move the lamp's reference %q", change, moved.LampReference)
		}
		refs[moved.LampReference] = true
		if moved.Register != before {
			t.Errorf("%s moved the register", change)
		}
	}
}

// TestTheRulesReferenceMovesWithTheRuleRows: the curated rules carry no
// freshness version of their own, so an edited, added or removed rule or
// topic moves the rules reference, and nothing else moves with it.
func TestTheRulesReferenceMovesWithTheRuleRows(t *testing.T) {
	req.Covers(t, "NFR-PERF-005", "FR-RULES-001")
	st := seeded(t)
	seedChanges(t, "rule_topic.reviewed_on", "rule_statement.body")
	ctx := bounded(t)
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	read := func() provenance.Versions {
		t.Helper()
		vs, err := st.FreshnessVersions(ctx)
		if err != nil || vs.RulesReference == "" {
			t.Fatalf("versions %+v, %v", vs, err)
		}
		return vs
	}
	base := read()
	t.Cleanup(func() {
		ctx := context.WithoutCancel(ctx)
		c, err := db.Owner(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = c.Close(ctx) }()
		for _, sql := range []string{
			`delete from contract.rule_statement where topic_code = 'rt_reference'`,
			`delete from contract.rule_topic where topic_code = 'rt_reference'`,
		} {
			if _, err := c.Exec(ctx, sql); err != nil {
				t.Error(err)
			}
		}
	})
	refs := map[string]bool{base.RulesReference: true}
	for _, change := range []string{
		`insert into contract.rule_topic (topic_code, title, title_en, reviewed_on) values ('rt_reference', 'Bezug', 'Reference', '2026-01-01')`,
		`insert into contract.rule_statement (statement_id, topic_code, position, body, body_en, legal_basis, legal_basis_url, reviewed_on, headline)
values ('rt_reference.one', 'rt_reference', 1, 'Eins', 'One', '§ 0 synthetic', 'https://law.example/rt_reference', '2026-01-01', false)`,
		`update contract.rule_statement set body = 'Eins, geändert' where statement_id = 'rt_reference.one'`,
		`update contract.rule_topic set reviewed_on = '2026-02-01' where topic_code = 'rt_reference'`,
		`delete from contract.rule_statement where statement_id = 'rt_reference.one'`,
	} {
		if _, err := conn.Exec(ctx, change); err != nil {
			t.Fatal(err)
		}
		moved := read()
		if refs[moved.RulesReference] {
			t.Errorf("%s did not move the rules reference %q", change, moved.RulesReference)
		}
		refs[moved.RulesReference] = true
		if moved.Register != base.Register || moved.LampReference != base.LampReference || !maps.Equal(moved.Families, base.Families) {
			t.Errorf("%s moved more than the rules reference: %+v", change, moved)
		}
	}
}

// literal writes a nullable value as SQL.
func literal[T any](v *T) string {
	if v == nil {
		return "null"
	}
	return "'" + fmt.Sprint(*v) + "'"
}

// deref is a nullable value, or nil.
func deref[T any](v *T) any {
	if v == nil {
		return nil
	}
	return *v
}

// TestRegisterSnapshotTTL: with db.register_snapshot_ttl 0s every Register
// reads storage, so a closed pool fails it at once; with the default, a read
// within the snapshot's lifetime answers from the snapshot without storage.
func TestRegisterSnapshotTTL(t *testing.T) {
	ctx := bounded(t)
	want, err := seeded(t).Register(ctx)
	if err != nil || len(want) == 0 {
		t.Fatalf("the register: %d families, %v", len(want), err)
	}
	for _, c := range []struct {
		name   string
		extra  []string
		closed bool // Register still answers once the pool is closed
	}{
		{"0s reads every request", []string{"VFAPI_DB_REGISTER_SNAPSHOT_TTL=0s"}, false},
		{"the default answers from the snapshot", nil, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			st, err := store.Open(ctx, readerConfig(t, c.extra...))
			if err != nil {
				t.Fatal(err)
			}
			got, err := st.Register(ctx)
			if err != nil || len(got) != len(want) {
				t.Fatalf("first read: %d families, want %d, %v", len(got), len(want), err)
			}
			st.Close()
			got, err = st.Register(ctx)
			if c.closed != (err == nil) {
				t.Fatalf("after the pool closed: %d families, %v", len(got), err)
			}
		})
	}
}
