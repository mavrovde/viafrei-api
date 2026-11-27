package store

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/mavrovde/viafrei-api/internal/runtime/config"
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
// one is stored (is [not] null), the comparison of two payloads' hashes, a
// jsonb path query of a literal path (the fields it names, never the payload
// whole), and the platform's data status (the freshness the read contract
// publishes).
var allowedReads = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\bjsonb_path_(?:query|query_first|query_array|exists)\(\s*(?:[a-z_][a-z0-9_]*\.)?[a-z_][a-z0-9_]*\s*,\s*'[^']*'`),
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
	// the motorway answer's sweep rows and plan rule, which derive fields from the payload
	motorway := []string{motorwaySweepRows("$3", "true"), motorwaySweepRows("$6", "true"), planInForce,
		motorwaySweepRows("$10", "$11::boolean"), planSuspended, strategyPayloadColumn, strategyPayloadOnce}
	for _, part := range append([]string{relTrafficEvent, before, after, landBefore, landAfter}, motorway...) {
		sql = strings.ReplaceAll(sql, part, "")
	}
	return withoutTwinRule(sql)
}

// twinWords are the forms in which the place-grouped charging read reads
// the operator's comparable words: compared between two sites (the
// register's twin rule), and carried to that comparison in the column
// lists of the sites read.
var twinWords = regexp.MustCompile(`\b(?:[a-z_]+\.)?operator_tokens\s*&&\s*[a-z_]+\.operator_tokens\b|` +
	`(?:\(c\.site\)|\bs)\.operator_tokens,|\bgeom, operator_tokens,`)

// withoutTwinRule cuts the twin rule's reads of the operator's words from
// the place-grouped charging read, whose answered columns (its last select
// list) never name them.
func withoutTwinRule(sql string) string {
	if sql != chargingPlacesNear.SQL {
		return sql
	}
	return twinWords.ReplaceAllString(sql, " ")
}

// TestTheTwinRuleAnswersNoOperatorWords: the place-grouped charging read
// answers no operator words, and the cut leaves every other read of them.
func TestTheTwinRuleAnswersNoOperatorWords(t *testing.T) {
	head, _, _ := strings.Cut(chargingPlacesNear.SQL[strings.LastIndex(chargingPlacesNear.SQL, "select k.source_feed"):], "(select (select eps.slug")
	if strings.Contains(head, "operator_tokens") {
		t.Errorf("the answered columns name the operator words: %s", head)
	}
	if got := withoutTwinRule("select operator_tokens from s"); got != "select operator_tokens from s" {
		t.Errorf("another statement was cut: %s", got)
	}
}

// TestNamedStatementsReadNoRefusedColumn: no named statement reads a whole
// row, a contract view or a column the platform keeps from the API.
//
// The traffic events relation alone (and its form with the rules matched
// per family, and the motorway answer's sweep rows and plan rule) reads the
// Autobahn payload, to derive an event's fields from
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
	namedSealed = false
	defer func() { namedSealed = true }()
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

// TestDeparturesOfTripsKeepTheRelation: the trip and stop keys are put before the
// departure relation's own columns, which stay as they are.
func TestDeparturesOfTripsKeepTheRelation(t *testing.T) {
	if !strings.HasPrefix(relDeparture, "\n SELECT ") ||
		departuresOfTrips != "\n SELECT t.gtfs_feed_key AS board_feed, t.id AS board_trip, t.headsign AS board_headsign, s.gtfs_feed_key AS board_stop_feed, s.id AS board_stop,"+relDeparture[len("\n SELECT"):] {
		t.Fatal("the departure relation no longer begins with its select list")
	}
}

// TestDeparturesOfWindowReplaceOnce: the stop times' join and the real-time
// call's freshness check are each found once in the departure relation and
// replaced, by the join in the window's times of day and the fresh sources
// read once.
func TestDeparturesOfWindowReplaceOnce(t *testing.T) {
	w := departuresOfWindow("$2::timestamptz", "$3::timestamptz")
	if strings.Count(departuresOfTrips, stopTimesOfStop) != 1 || strings.Count(departuresOfTrips, siriFresh) != 1 ||
		strings.Contains(w, siriFresh) ||
		!strings.Contains(w, "CROSS JOIN ("+timesOfDay("$2::timestamptz", "$3::timestamptz")+") tod\n     "+stopTimesOfStop+
			" AND "+stopTimeOfDay+" >= tod.lo AND "+stopTimeOfDay+" < tod.hi\n") ||
		!strings.Contains(w, "(c.source_feed = ANY (ARRAY( SELECT k_1.feed_key") {
		t.Fatal("the departure relation's stop times join or real-time freshness check is not replaced once")
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

// TestStationDisruptionOfHourProbesEachRef: the per-ref probe (kept a probe
// by OFFSET 0, of the families only) and its limit, the stations' read
// through the index view and the per-call source test are found in the
// station disruption relation and replaced, and the calls are picked and
// grouped by byte order.
func TestStationDisruptionOfHourProbesEachRef(t *testing.T) {
	if strings.Count(relStationDisruption, callsPerRef) != 1 || strings.Contains(stationDisruptionOfHour, callsPerRef) ||
		strings.Contains(stationDisruptionOfHour, "LIMIT 500") || !strings.Contains(stationDisruptionOfHour, "OFFSET 0) c_1") ||
		!strings.Contains(stationDisruptionOfHour, "WHERE x.stop_ref = r.ref AND x.source_feed = any($1::text[]) AND ") ||
		strings.Count(relStationDisruption, stationsOfIndex) != 1 || strings.Contains(stationDisruptionOfHour, stationsOfIndex) ||
		!strings.Contains(stationDisruptionOfHour, stationsWithSlug) || strings.Count(relStationDisruption, freshPerCall) != 1 || strings.Contains(stationDisruptionOfHour, freshPerCall) ||
		strings.Count(relStationDisruption, rulesPerRow("c.source_feed")) != 1 || strings.Contains(stationDisruptionOfHour, rulesPerRow("c.source_feed")) {
		t.Fatal("the station disruption relation's read of the calls changed")
	}
	for from, to := range map[string]string{callsDistinct: callsDistinctBytes, callsNewestFirst: callsNewestFirstBytes, callsLineKey: callsLineKeyBytes} {
		if strings.Count(relStationDisruption, from) != 1 || strings.Contains(stationDisruptionOfHour, from) || strings.Count(stationDisruptionOfHour, to) != 1 {
			t.Fatalf("the calls are not compared by byte order: %q", from)
		}
	}
}

// TestNamedIsSealedOnceThePackageLoaded: every statement is built when the
// package loads; one named later (built per call, so planned on every call)
// panics.
func TestNamedIsSealedOnceThePackageLoaded(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("a statement named after the package loaded: no panic")
		}
	}()
	named("api_test_per_call", "select $1::int", "int")
}

// TestStatementCacheCoversEveryStatement: the default statement cache holds
// every distinct statement of the store, the named ones and the texts sent
// as they are, so none is evicted and prepared (and planned) again on the
// same connection.
func TestStatementCacheCoversEveryStatement(t *testing.T) {
	cfg, err := config.Load(config.Options{})
	if err != nil {
		t.Fatal(err)
	}
	n := len(namedList())
	for file, texts := range rawAllowed {
		if file != "named.go" { // run's own call sends a named statement
			n += len(texts)
		}
	}
	if n < 100 || cfg.DB.StatementCacheCapacity < n {
		t.Errorf("db.statement_cache_capacity %d for %d statements", cfg.DB.StatementCacheCapacity, n)
	}
}

// TestAMotorwayRowThatCannotBeScannedFailsTheList: the motorway list answers
// the scan's error, with no roads and no families counted.
func TestAMotorwayRowThatCannotBeScannedFailsTheList(t *testing.T) {
	want := errors.New("cannot scan")
	ms, counted, err := collectMotorways(someRows{failedRows{err: want}}, nil)
	if !errors.Is(err, want) || ms != nil || counted != nil {
		t.Fatalf("collectMotorways = %v, %v, %v; want the scan's error alone", ms, counted, err)
	}
}
