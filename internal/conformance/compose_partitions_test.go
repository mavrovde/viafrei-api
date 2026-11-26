package conformance_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/pgtest"
)

// composePartitionFindings: compose.yaml's db-migrate makes the daily
// partitions as pgtest does, the coming pgtest.PartitionDays days and no day
// before today. A partition no tick is in is one every statement over its
// table plans for: made from a fixed first day, their number grows by one a
// day, and so does the time a fresh connection plans its statements
// (viafrei-api#248).
func composePartitionFindings(compose, days string) []string {
	ensure := regexp.MustCompile(`(?m)^\s+node /app/dist/cli\.js partitions ensure(.*)$`).FindAllStringSubmatch(compose, -1)
	if len(ensure) != 1 {
		return []string{"compose.yaml does not run partitions ensure exactly once"}
	}
	var out []string
	if args := strings.Fields(ensure[0][1]); len(args) != 2 || args[0] != "--days" || args[1] != days {
		out = append(out, "compose.yaml's partitions ensure is not --days "+days+" alone, as pgtest's: "+strings.TrimSpace(ensure[0][1]))
	}
	return out
}

func TestComposeMakesThePartitionsPgtestMakes(t *testing.T) {
	compose := readText(t, "compose.yaml")
	for _, f := range composePartitionFindings(compose, pgtest.PartitionDays) {
		t.Error(f)
	}
	line := "node /app/dist/cli.js partitions ensure --days " + pgtest.PartitionDays + "\n"
	for want, plant := range map[string]string{
		"is not --days " + pgtest.PartitionDays + " alone": strings.Replace(compose, line, strings.TrimSuffix(line, "\n")+" --from 2026-03-27\n", 1),
		"exactly once": strings.Replace(compose, line, "", 1),
		// A longer window is the same failure as a fixed first day.
		"alone, as pgtest's: --days 30": strings.Replace(compose, line, "node /app/dist/cli.js partitions ensure --days 30\n", 1),
	} {
		if plant == compose {
			t.Fatalf("plant for %q changed nothing", want)
		}
		if got := strings.Join(composePartitionFindings(plant, pgtest.PartitionDays), "; "); !strings.Contains(got, want) {
			t.Errorf("planted %q, the rules said %q", want, got)
		}
	}
}
