package store

import (
	"errors"
	"regexp"
	"strings"
	"testing"
)

// refusedReads are what no statement of the store may read: every column of a
// relation (select *, t.*), the read contract's views (the store reads the
// platform's tables), and the columns the platform keeps from the API
// (raw provider payloads, contact data and operational internals).
var refusedReads = map[string]*regexp.Regexp{
	"every column":        regexp.MustCompile(`(?i)\bselect\s+(distinct\s+)?\*|\b[a-z_][a-z0-9_]*\.\*`),
	"a contract view":     regexp.MustCompile(`(?i)\bcontract\.`),
	"a raw payload":       regexp.MustCompile(`(?i)\b(raw|raw_hash|payload)\b`),
	"a contact column":    regexp.MustCompile(`(?i)\b(operator_tokens|service_phone)\b`),
	"an internal setting": regexp.MustCompile(`(?i)\b(subscription_var|runtime_config|source_schedule|control\.(refusals|budgets|feed_state))\b`),
}

// refusals names what of refusedReads the text sql reads.
func refusals(sql string) []string {
	var out []string
	for kind, re := range refusedReads {
		if re.MatchString(sql) {
			out = append(out, kind)
		}
	}
	return out
}

// TestNamedStatementsReadNoRefusedColumn: no named statement reads a whole
// row, a contract view or a column the platform keeps from the API.
//
// The traffic events relation alone reads the Autobahn payload, to derive
// an event's fields from it as the platform does; it never answers it.
func TestNamedStatementsReadNoRefusedColumn(t *testing.T) {
	for _, s := range namedList() {
		if r := refusals(strings.ReplaceAll(s.SQL, relTrafficEvent, "")); len(r) > 0 {
			t.Errorf("%s reads %s", s.Name, strings.Join(r, ", "))
		}
		if !strings.HasPrefix(s.Name, "api_") {
			t.Errorf("%s: a statement's name starts with api_", s.Name)
		}
	}
}

// TestRefusalsCatchEachKind: each refused kind is caught, and a plain read
// passes.
func TestRefusalsCatchEachKind(t *testing.T) {
	for sql, want := range map[string]string{
		"select * from t":                   "every column",
		"select distinct * from t":          "every column",
		"select t.* from t":                 "every column",
		"select a from contract.place":      "a contract view",
		"select e.raw from autobahn_events": "a raw payload",
		"select payload from road_events":   "a raw payload",
		"select operator_tokens from s":     "a contact column",
		"select service_phone from s":       "a contact column",
		"select subscription_var from c":    "an internal setting",
		"select k from control.budgets":     "an internal setting",
	} {
		if got := refusals(sql); len(got) != 1 || got[0] != want {
			t.Errorf("%q: %v, want %s", sql, got, want)
		}
	}
	if got := refusals("select count(*), p.name from gazetteer_places p"); len(got) != 0 {
		t.Errorf("a plain read refused: %v", got)
	}
}

// TestNamedRefusesAMalformedStatement: a taken name, a parameter without a
// type, an unused type and a use without its cast each panic.
func TestNamedRefusesAMalformedStatement(t *testing.T) {
	named("api_test_once", "select $1::int", "int")
	for what, f := range map[string]func(){
		"taken":   func() { named("api_test_once", "select $1::int", "int") },
		"untyped": func() { named("api_test_untyped", "select $2::int", "int") },
		"unused":  func() { named("api_test_unused", "select $1::int", "int", "text") },
		"uncast":  func() { named("api_test_uncast", "select $1", "int") },
		"miscast": func() { named("api_test_miscast", "select $1::text", "int") },
		"zero":    func() { named("api_test_zero", "select $0::int", "int") },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", what)
				}
			}()
			f()
		}()
	}
	namedMu.Lock()
	delete(namedStmts, "api_test_once")
	namedMu.Unlock()
}

// TestFailedRowsCarryTheError: the rows of a statement that could not be
// prepared hold nothing and answer its error wherever rows answer one.
func TestFailedRowsCarryTheError(t *testing.T) {
	want := errors.New("not prepared")
	r := failedRows{err: want}
	r.Close()
	_, vErr := r.Values()
	if r.Next() || !errors.Is(r.Err(), want) || !errors.Is(r.Scan(), want) || !errors.Is(vErr, want) ||
		r.CommandTag().String() != "" || r.FieldDescriptions() != nil || r.RawValues() != nil || r.Conn() != nil || r.TypeMap() != nil {
		t.Fatal("failed rows answer more than their error")
	}
}
