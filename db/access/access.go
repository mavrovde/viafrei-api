// Package access embeds the access store's schema: the SQL that builds it and
// the API's own role in it. The operator applies them in file order; tests
// build them in a throwaway database.
package access

import _ "embed"

//go:embed 0001_access.sql
var schemaSQL string

//go:embed 0002_access_role.sql
var roleSQL string

//go:embed 0003_limits.sql
var limitsSQL string

//go:embed 0004_console.sql
var consoleSQL string

// Role is the name of the API's own access-store role.
const Role = "api_access"

// Version is the schema version this build expects.
const Version = 3

// SchemaSQL returns the statements that build the access store.
func SchemaSQL() string { return schemaSQL }

// RoleSQL returns the statements that create the API's role. The caller sets
// the role's password separately.
func RoleSQL() string { return roleSQL }

// LimitsSQL returns the statements of schema version 2: the plans' limits
// and the usage records. They run after RoleSQL, whose role they grant to.
func LimitsSQL() string { return limitsSQL }

// ConsoleSQL returns the statements of schema version 3: the owner console's
// passkeys and enrolment codes. They run after LimitsSQL.
func ConsoleSQL() string { return consoleSQL }
