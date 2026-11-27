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
	"github.com/mavrovde/viafrei-api/internal/domain/roads"
	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// TestRoadStatusPlannedPayloadOnce: the plan sources' read derives its
// answer rows' texts from each entry's payload decompressed once, never
// from the stored column in each path (a plan entry's payload is up to 4.5 MB
// as text; eight paths decompressed it eight times, #290: A3 median 360 ms on prod).
// The decompressed value is a column of its own (the lateral kept apart by
// offset 0), never an expression the planner copies into each path.
func TestRoadStatusPlannedPayloadOnce(t *testing.T) {
	req.Covers(t, "NFR-PERF-001")
	var sql string
	for _, s := range store.NamedReads() {
		if s.Name == "api_road_status_planned" {
			sql = s.SQL
		}
	}
	if sql == "" {
		t.Fatal("no api_road_status_planned")
	}
	ctx := bounded(t)
	conn, err := db.Owner(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(ctx) }()
	arr := func(l []string) string { return `'{` + strings.Join(l, ",") + `}'` }
	var raw []byte
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "prepare rp(text[], timestamptz, timestamptz, text[], int, float8, float8, float8, text[], text[], text[], text[], text[]) as "+sql); err != nil {
			return err
		}
		// EXECUTE takes no bound parameters: the arguments are literals.
		return tx.QueryRow(ctx, fmt.Sprintf("explain (verbose, format json) execute rp('{A3}', now(), now() + interval '3 days', %s, 10, null, null, null, '{A3}', %s, %s, %s, %s)",
			arr(roads.RoadStatusFamilies), arr(roads.RoadStatusClosesTextFamilies), arr(roads.RoadStatusMessageFamilies),
			arr(roads.RoadStatusPlanFamilies), arr(roads.RoadStatusProgrammeFamilies))).Scan(&raw)
	})
	if err != nil {
		t.Fatal(err)
	}
	var plan any
	if err := json.Unmarshal(raw, &plan); err != nil {
		t.Fatal(err)
	}
	stored := regexp.MustCompile(`x\.payload(?: #> '\{\}')?`)
	for _, out := range planOutputs(plan) {
		for _, m := range stored.FindAllString(out, -1) {
			if m == "x.payload" && out != "x.payload" {
				t.Errorf("an answer column reads the stored payload: %.160s", out)
			}
		}
	}
	if !payloadOnce(plan) {
		t.Error("no node of its own decompresses the payload")
	}
}

// payloadOnce tells whether a node of the plan computes the decompressed
// payload alone, so its parents read the value it computed.
func payloadOnce(n any) bool {
	switch v := n.(type) {
	case []any:
		for _, e := range v {
			if payloadOnce(e) {
				return true
			}
		}
	case map[string]any:
		if o, ok := v["Output"].([]any); ok && v["Node Type"] == "Result" && len(o) == 1 && o[0] == "(x.payload #> '{}'::text[])" {
			return true
		}
		for _, e := range v {
			if payloadOnce(e) {
				return true
			}
		}
	}
	return false
}

// planOutputs are the Output entries of every node of a JSON plan.
func planOutputs(n any) []string {
	var out []string
	switch v := n.(type) {
	case []any:
		for _, e := range v {
			out = append(out, planOutputs(e)...)
		}
	case map[string]any:
		if o, ok := v["Output"].([]any); ok {
			for _, e := range o {
				if s, ok := e.(string); ok {
					out = append(out, s)
				}
			}
		}
		for k, e := range v {
			if k != "Output" {
				out = append(out, planOutputs(e)...)
			}
		}
	}
	return out
}
