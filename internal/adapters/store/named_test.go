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
	"an internal setting": regexp.MustCompile(`(?i)\b(subscription_var|runtime_config|source_schedule|control\.[a-z_]+)\b`),
}

// readableControl is the one control relation a statement may read: the
// platform's feed status view, which the digest, the MCP and the API read
// alike (owner-rights, kept when the contract views go); every other control
// name stays an internal setting.
var readableControl = regexp.MustCompile(`(?i)\bcontrol\.data_status\b`)

// refusals names what of refusedReads the text sql reads.
func refusals(sql string) []string {
	var out []string
	sql = readableControl.ReplaceAllString(sql, "")
	for kind, re := range refusedReads {
		if re.MatchString(sql) {
			out = append(out, kind)
		}
	}
	return out
}

// withoutTrafficEvents is sql without the traffic events relation, read
// whole or around its rule match.
func withoutTrafficEvents(sql string) string {
	before, after, _ := strings.Cut(relTrafficEvent, rulesPerEvent)
	for _, part := range []string{relTrafficEvent, before, after} {
		sql = strings.ReplaceAll(sql, part, "")
	}
	return sql
}

// TestNamedStatementsReadNoRefusedColumn: no named statement reads a whole
// row, a contract view or a column the platform keeps from the API.
//
// The traffic events relation alone (and its form with the rules matched
// per family) reads the Autobahn payload, to derive an event's fields from
// it as the platform does; it never answers it.
func TestNamedStatementsReadNoRefusedColumn(t *testing.T) {
	for _, s := range namedList() {
		if r := refusals(withoutTrafficEvents(s.SQL)); len(r) > 0 {
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
		"select * from t":                                                   "every column",
		"select distinct * from t":                                          "every column",
		"select t.* from t":                                                 "every column",
		"select a from contract.place":                                      "a contract view",
		"select e.raw from autobahn_events":                                 "a raw payload",
		"select payload from road_events":                                   "a raw payload",
		"select operator_tokens from s":                                     "a contact column",
		"select service_phone from s":                                       "a contact column",
		"select subscription_var from c":                                    "an internal setting",
		"select k from control.budgets":                                     "an internal setting",
		"select k from control.feed_state":                                  "an internal setting",
		"select k from control.data_status_x":                               "an internal setting",
		"select k from control.data_status join control.refusals r on true": "an internal setting",
	} {
		if got := refusals(sql); len(got) != 1 || got[0] != want {
			t.Errorf("%q: %v, want %s", sql, got, want)
		}
	}
	if got := refusals("select d.cadence_s from control.data_status d"); len(got) != 0 {
		t.Errorf("the feed status view refused: %v", got)
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

// someRows hold a row that cannot be scanned.
type someRows struct{ failedRows }

func (someRows) Next() bool { return true }

// TestOneRowAnswersTheScanError: a row that cannot be scanned answers the
// scan's error, not a missing row.
func TestOneRowAnswersTheScanError(t *testing.T) {
	want := errors.New("cannot scan")
	if err := (oneRow{someRows{failedRows{err: want}}}).Scan(); !errors.Is(err, want) {
		t.Fatalf("Scan = %v, want %v", err, want)
	}
}

// TestDeparturesOfTripsKeepTheRelation: the trip keys are put before the
// departure relation's own columns, which stay as they are.
func TestDeparturesOfTripsKeepTheRelation(t *testing.T) {
	if !strings.HasPrefix(relDeparture, "\n SELECT ") ||
		departuresOfTrips != "\n SELECT t.gtfs_feed_key AS board_feed, t.id AS board_trip, t.headsign AS board_headsign,"+relDeparture[len("\n SELECT"):] {
		t.Fatal("the departure relation no longer begins with its select list")
	}
}

// TestTrafficEventsOfFamiliesMatchRulesOnce: the per-event rule match is
// found in the traffic event relation and replaced.
func TestTrafficEventsOfFamiliesMatchRulesOnce(t *testing.T) {
	if strings.Count(relTrafficEvent, rulesPerEvent) != 1 || strings.Contains(trafficEventsOfFamilies, rulesPerEvent) {
		t.Fatal("the traffic event relation's rule match changed")
	}
}
