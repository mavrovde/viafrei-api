// Package baseseed is the test seed of the platform's tables: rows in the
// read contract's own names, written as
//
//	insert into vf_seed.<relation> (<contract columns>) values (...);
//
// land in the tables the workers write (base_seed.sql), so a test reads
// them back with the API's own statements, not through a seed of the
// database image. It is for test databases only: base_seed.sql refuses any
// database whose name does not start with test_.
package baseseed

import (
	_ "embed"
	"regexp"
	"strings"
)

//go:embed base_seed.sql
var triggers string

// contractInsert starts a statement of the fixture set (internal/fixtures),
// which writes in the contract's own names: its recorded checksum, which
// the parity bundles pin, stays the same while the rows go to the staging
// tables.
var contractInsert = regexp.MustCompile(`(?m)^insert into contract\.`)

// Script is the whole seed of rows, named name, as one script to run in one
// transaction: the staging tables and their triggers, the one-seed guard,
// the rows (a row written to contract.<relation> at a line's start goes to
// vf_seed.<relation>), and the read-back of each row through a contract view
// that still exists.
func Script(name, rows string) string {
	return triggers + "\nselect vf_test.contract_seed_begin('" + strings.ReplaceAll(name, "'", "''") + "');\n" +
		contractInsert.ReplaceAllString(rows, "insert into vf_seed.") + "\nselect vf_test.contract_seed_end();\n"
}

// Triggers is the seed's staging tables and their triggers alone, to
// install in a database that rows are planted into later; installing it
// again changes nothing.
func Triggers() string { return triggers }
