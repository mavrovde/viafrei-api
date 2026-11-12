// Package contract embeds the read-contract stub: the SQL that builds a
// database shaped like the logical read model, and the read-only role the
// service connects as. Tests and the local development stack use it; sqlc
// compiles the queries against it.
package contract

import (
	_ "embed"
	"strings"
)

//go:embed 0001_contract.sql
var schemaSQL string

//go:embed 0003_physical.sql
var physicalSQL string

//go:embed 0002_reader_role.sql
var readerRoleSQL string

//go:embed VERSION
var version string

// ReaderRole is the name of the service's read-only database role.
const ReaderRole = "api_reader"

// Version is the read-contract version this build of the service expects,
// in major.minor form.
func Version() string { return strings.TrimSpace(version) }

// SchemaSQL returns the statements that build the contract stub: its
// relations, then their partitions and indexes.
func SchemaSQL() string { return schemaSQL + "\n" + physicalSQL }

// ReaderRoleSQL returns the statements that create the read-only role. The
// caller sets the role's password separately.
func ReaderRoleSQL() string { return readerRoleSQL }
