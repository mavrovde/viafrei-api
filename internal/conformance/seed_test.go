package conformance_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/req"
)

// seedFindings lists what the development seed script does wrong: the
// fixture load and the contract-version row must run in one psql session
// with --single-transaction, and the version row must be the last statement,
// so a database never carries a contract version without its fixtures.
func seedFindings(src string) []string {
	var out []string
	if n := strings.Count(src, "\npsql "); n != 1 {
		out = append(out, "the seed runs psql more than once or not at all")
	}
	if !strings.Contains(src, "--single-transaction") {
		out = append(out, "the seed is not one transaction")
	}
	load := strings.Index(src, `\i /seed/seed.sql`)
	insert := strings.Index(src, "insert into contract.contract_info")
	end := strings.LastIndex(src, "\nSQL\n")
	switch {
	case load < 0 || insert < 0 || end < 0:
		out = append(out, "the seed does not load the fixtures and publish the version in its heredoc")
	case insert < load || strings.TrimSpace(src[insert:end]) != strings.TrimSpace(src[insert:insert+strings.Index(src[insert:], "\n")]):
		out = append(out, "the contract-version row is not the last statement of the seed")
	}
	return out
}

func TestSeedPublishesVersionLast(t *testing.T) {
	req.Covers(t, "FR-SVC-004")
	src, err := os.ReadFile(filepath.Join(root, "deploy", "compose", "30_seed.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range seedFindings(string(src)) {
		t.Error(f)
	}
}

func TestSeedRulesCanSayNo(t *testing.T) {
	req.Covers(t, "FR-SVC-004")
	good, err := os.ReadFile(filepath.Join(root, "deploy", "compose", "30_seed.sh"))
	if err != nil {
		t.Fatal(err)
	}
	insert := "insert into contract.contract_info (contract_version, published_at) values (:'contract_version', now());\n"
	for want, edit := range map[string][2]string{
		"not one transaction":     {"--single-transaction ", ""},
		"not the last statement":  {`\i /seed/seed.sql` + "\n" + insert, insert + `\i /seed/seed.sql` + "\n"},
		"more than once":          {"\nSQL\n", "\nSQL\n\npsql -f /seed/seed.sql\n"},
		"does not load the fixtu": {`\i /seed/seed.sql`, ""},
	} {
		bad := strings.Replace(string(good), edit[0], edit[1], 1)
		if bad == string(good) {
			t.Fatalf("plant for %q did not change the file", want)
		}
		if got := strings.Join(seedFindings(bad), "; "); !strings.Contains(got, want) {
			t.Errorf("planted %q, the rules said %q", want, got)
		}
	}
}
