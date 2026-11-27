//go:build integration

package store_test

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/mavrovde/viafrei-api/internal/adapters/store"
	"github.com/mavrovde/viafrei-api/internal/domain/places"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// TestPlaceCandidatesPlan: the place resolver's candidate read, in the
// generic plan the plan cache settles on, reads the gazetteer only through
// an index condition (the suffix rule and the prefix-qualified namesakes
// probe the text_pattern_ops btree, never a read of a whole index or of the
// table joined to the patterns), reads each fold's rows by its name (never
// every fold beside every row: 214 000 rows filtered for "bamberg") and
// filters no joined row, and reads nothing for the expanded spelling when it
// is the folded one (#291: "bamberg" 455 ms custom, 689 ms generic on
// prod, the trigram candidates of one spelling rechecked twice).
func TestPlaceCandidatesPlan(t *testing.T) {
	req.Covers(t, "NFR-PERF-001")
	var sql string
	for _, s := range store.NamedReads() {
		if s.Name == "api_place_candidates" {
			sql = s.SQL
		}
	}
	if sql == "" {
		t.Fatal("no api_place_candidates")
	}
	// A Gemeinde's fold, so every part of the statement has rows to read.
	var fold string
	owner(t, `select name_norm from gazetteer_places
where closed_at is null and name_norm ~ '^[a-z]+$'
order by name_norm limit 1`, &fold)
	ctx := bounded(t)
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	pats := places.PrefixQualifiedPatterns(fold)
	var raw []byte
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		// A small fixture table is cheaper to scan than to probe: priced out,
		// a scan in the plan means the statement left no other path. The
		// plan is the generic one, as the server keeps after five runs.
		for _, set := range []string{"set local enable_seqscan = off", "set local plan_cache_mode = force_generic_plan",
			"prepare cand(text, text, double precision, integer, boolean, text, text, text[], text[], text[]) as " + sql} {
			if _, err := tx.Exec(ctx, set); err != nil {
				return err
			}
		}
		// EXECUTE takes no bound parameters: the arguments are literals.
		arr := `'{"` + strings.Join(pats, `","`) + `"}'`
		explain := "explain (analyze, format json) " //nolint:misspell // the SQL keyword
		return tx.QueryRow(ctx, fmt.Sprintf(explain+"execute cand('%s', '%s', 0.2, 30, true, null, null, %s, %s, '{bkg_gvisys,db_stada,delfi_zhv,geonames}')",
			fold, fold, arr, arr)).Scan(&raw)
	})
	if err != nil {
		t.Fatal(err)
	}
	var plan any
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	for _, p := range candidatePlanFaults(plan) {
		t.Error(p)
	}
}

// trigramFilter is a filter of one trigram match of the folded or the
// English name against a spelling, and nothing else.
var trigramFilter = regexp.MustCompile(`^\((?:\(name_en IS NOT NULL\) AND \()?(?:name_norm|lower\(name_en\)) % \$[12]\)\)?$`)

// candidatePlanFaults are the nodes of an analysed plan that break
// TestPlaceCandidatesPlan's rules.
func candidatePlanFaults(plan any) []string {
	var out []string
	byFold := false
	// trigramArm is set for the children of a bitmap heap scan whose filter
	// is one trigram match and nothing else: prod's statistics serve that arm
	// from its trigram index, while the fixture's generic plan, priced on a
	// few rows, may serve the filter from a whole partial btree. Only that
	// bitmap source is not judged; every other read is.
	var walk func(n any, trigramArm bool)
	walk = func(n any, trigramArm bool) {
		switch v := n.(type) {
		case []any:
			for _, e := range v {
				walk(e, trigramArm)
			}
		case map[string]any:
			ran, _ := v["Actual Loops"].(float64)
			kind, _ := v["Node Type"].(string)
			filter, _ := v["Filter"].(string)
			// Every index the statement reads is the gazetteer's: an index
			// read with no condition is a read of all of it.
			c, cond := v["Index Cond"].(string)
			byFold = byFold || ran > 0 && strings.Contains(c, "= f.name_norm")
			if ran > 0 && (kind == "Seq Scan" && v["Relation Name"] == "gazetteer_places" ||
				(kind == "Index Scan" || kind == "Index Only Scan" || kind == "Bitmap Index Scan" && !trigramArm) && !cond) {
				out = append(out, fmt.Sprintf("%s of %v with no index condition", kind, v["Index Name"]))
			}
			if removed, _ := v["Rows Removed by Join Filter"].(float64); removed > 0 {
				out = append(out, fmt.Sprintf("%s filtered %v joined rows (%v)", kind, removed, v["Join Filter"]))
			}
			for _, key := range []string{"Index Cond", "Recheck Cond", "Filter"} {
				if c, _ := v[key].(string); ran > 0 && (strings.Contains(c, "% $2") || strings.Contains(c, "$2 ||")) {
					out = append(out, fmt.Sprintf("%s read the expanded spelling, which is the folded one: %s", kind, c))
				}
			}
			arm := kind == "Bitmap Heap Scan" && trigramFilter.MatchString(filter)
			for _, e := range v {
				walk(e, arm)
			}
		}
	}
	walk(plan, false)
	if !byFold {
		out = append(out, "no fold's rows were read by its name: the folds were joined to the rows read")
	}
	return out
}
