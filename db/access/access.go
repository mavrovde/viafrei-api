// Package access embeds the access store's schema: the SQL that builds it and
// the API's own role in it. The operator applies them; tests build them in a
// throwaway database.
package access

import _ "embed"

//go:embed 0001_access.sql
var schemaSQL string

//go:embed 0002_access_role.sql
var roleSQL string

// Role is the name of the API's own access-store role.
const Role = "api_access"

// Version is the schema version this build expects: the number of the newest
// migration.
const Version = 1

// SchemaSQL returns the statements that build the access store.
func SchemaSQL() string { return schemaSQL }

// RoleSQL returns the statements that create the API's role. The caller sets
// the role's password separately.
func RoleSQL() string { return roleSQL }
