package baseseed_test

import (
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/testkit/baseseed"
)

// TestScriptFramesTheRows: the triggers first, then the guard naming the
// seed (quoted), the rows, and the read-back last.
func TestScriptFramesTheRows(t *testing.T) {
	s := baseseed.Script("it's", "insert into vf_seed.place (place_id) values ('1');")
	begin := strings.Index(s, "select vf_test.contract_seed_begin('it''s');")
	rows := strings.Index(s, "insert into vf_seed.place")
	end := strings.LastIndex(s, "select vf_test.contract_seed_end();")
	triggers := strings.Index(s, "CREATE TABLE IF NOT EXISTS vf_seed.place ")
	if triggers < 0 || begin < triggers || rows < begin || end < rows {
		t.Fatalf("out of order: triggers %d, begin %d, rows %d, end %d", triggers, begin, rows, end)
	}
}

// TestTriggersIsTheScriptsHead: the install alone is the script's start.
func TestTriggersIsTheScriptsHead(t *testing.T) {
	if !strings.HasPrefix(baseseed.Script("x", ""), baseseed.Triggers()) || !strings.Contains(baseseed.Triggers(), "AFTER INSERT ON vf_seed.") {
		t.Fatal("the install is not the script's head")
	}
}

// TestScriptSendsTheFixtureSetToTheStagingTables: a fixture statement in the
// contract's names lands in the staging table; a mention inside a value does
// not move.
func TestScriptSendsTheFixtureSetToTheStagingTables(t *testing.T) {
	s := baseseed.Script("x", "insert into contract.place (name) values\n  ('insert into contract.x');\n")
	if !strings.Contains(s, "insert into vf_seed.place (name) values\n  ('insert into contract.x');") {
		t.Fatalf("not moved: %s", s[strings.Index(s, "contract_seed_begin"):])
	}
}
