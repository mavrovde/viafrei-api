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

// allowedReads are the forms of a refused name a statement may read, cut
// from its text before refusedReads is applied: a named field of a provider
// payload (a jsonb path of literal keys, never the payload whole), whether
// one is stored (is [not] null), the comparison of two payloads' hashes, and
// the platform's data status (the freshness the read contract publishes).
var allowedReads = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:[a-z_][a-z0-9_]*\.)?(?:raw|payload)(?:\s*(?:->>?|#>>?)\s*'[^']*')+`),
	regexp.MustCompile(`(?i)\b(?:[a-z_][a-z0-9_]*\.)?(?:raw|payload)\s+is\s+(?:not\s+)?null\b`),
	regexp.MustCompile(`(?i)\b[a-z_][a-z0-9_]*\.raw_hash\s*=\s*[a-z_][a-z0-9_]*\.raw_hash\b`),
	regexp.MustCompile(`(?i)\bcontrol\.data_status\b`),
}

// refusals names what of refusedReads the text sql reads.
func refusals(sql string) []string {
	for _, re := range allowedReads {
		sql = re.ReplaceAllString(sql, " ")
	}
	var out []string
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
	before, after, _ := strings.Cut(relTrafficEvent, rulesPerRow("b.source_ref"))
	// the form that reads the stored Land, cut around its rules (their $1 is made opaque)
	landBefore, landAfter, _ := strings.Cut(trafficEventsOfFamilies, rulesOfFamilies("b.source_ref"))
	for _, part := range []string{relTrafficEvent, before, after, landBefore, landAfter} {
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
	// One statement a subtest, side by side: each one's patterns are its
	// own work.
	for _, s := range namedList() {
		t.Run(s.Name, func(t *testing.T) {
			t.Parallel()
			if r := refusals(withoutTrafficEvents(s.SQL)); len(r) > 0 {
				t.Errorf("%s reads %s", s.Name, strings.Join(r, ", "))
			}
			if !strings.HasPrefix(s.Name, "api_") {
				t.Errorf("%s: a statement's name starts with api_", s.Name)
			}
		})
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
		"select raw from autobahn_events":                                   "a raw payload",
		"select e.raw->>'a', e.raw from e":                                  "a raw payload",
		"select coalesce(e.raw, r.raw) from e":                              "a raw payload",
		"select e.raw_hash from e":                                          "a raw payload",
		"select k from control.data_status_x":                               "an internal setting",
		"select operator_tokens from s":                                     "a contact column",
		"select service_phone from s":                                       "a contact column",
		"select subscription_var from c":                                    "an internal setting",
		"select k from control.budgets":                                     "an internal setting",
		"select k from control.feed_state":                                  "an internal setting",
		"select k from control.data_status join control.refusals r on true": "an internal setting",
	} {
		if got := refusals(sql); len(got) != 1 || got[0] != want {
			t.Errorf("%q: %v, want %s", sql, got, want)
		}
	}
	for _, sql := range []string{
		"select count(*), p.name from gazetteer_places p",
		"select e.raw->>'isBlocked', e.raw -> 'description', r.raw->'a'->>'b', raw #>> '{a,b}' from e",
		"select 1 from e, r where e.raw is null and r.raw is not null and r.raw_hash = e.raw_hash",
		"select d.key from control.data_status d",
	} {
		if got := refusals(sql); len(got) != 0 {
			t.Errorf("%q refused: %v", sql, got)
		}
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
	if strings.Count(relTrafficEvent, rulesPerRow("b.source_ref")) != 1 || strings.Contains(trafficEventsOfFamilies, rulesPerRow("b.source_ref")) {
		t.Fatal("the traffic event relation's rule match changed")
	}
}

// TestTrafficEventsOfFamiliesReadTheStoredLand: each fragment the stored
// Land replaces is found once in the relation, and none is left.
func TestTrafficEventsOfFamiliesReadTheStoredLand(t *testing.T) {
	for _, f := range []string{autobahnBranchEnd, datexBranchEnd, landPerEvent, autobahnCarriesLand} {
		if strings.Count(relTrafficEvent, f) != 1 {
			t.Fatalf("the traffic event relation changed: %q", f)
		}
	}
	for _, f := range []string{autobahnBranchEnd, datexBranchEnd, landPerEvent} {
		if strings.Contains(trafficEventsOfFamilies, f) {
			t.Fatalf("not replaced: %q", f)
		}
	}
	if strings.Count(trafficEventsOfFamilies, "e.land_code") != 2 || !strings.Contains(trafficEventsOfFamilies, "SELECT b.land_code AS code") {
		t.Fatal("the stored Land is not read")
	}
}

// TestStationDisruptionOfHourReadsOneRange: the per-ref probe and its limit
// are found in the station disruption relation and replaced.
func TestStationDisruptionOfHourReadsOneRange(t *testing.T) {
	if strings.Count(relStationDisruption, callsPerRef) != 1 || strings.Contains(stationDisruptionOfHour, callsPerRef) ||
		strings.Contains(stationDisruptionOfHour, "LIMIT 500") || !strings.Contains(stationDisruptionOfHour, "ON c_1.stop_ref = r.ref") ||
		strings.Count(relStationDisruption, freshPerCall) != 1 || strings.Contains(stationDisruptionOfHour, freshPerCall) ||
		strings.Count(relStationDisruption, rulesPerRow("c.source_feed")) != 1 || strings.Contains(stationDisruptionOfHour, rulesPerRow("c.source_feed")) {
		t.Fatal("the station disruption relation's read of the calls changed")
	}
}
