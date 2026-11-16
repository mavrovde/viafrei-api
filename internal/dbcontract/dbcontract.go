// Package dbcontract names what this build of the service expects of the
// database: the read-contract version, the two roles it connects as and the
// access store's schema version. The database itself, its read contract
// (schema contract) and its access store (schema access) are built by the
// viafrei-database release database.pin names; this repository runs no DDL.
package dbcontract

// ContractVersion is the read-contract version this build expects, in
// major.minor form: contract.contract_info must state it.
const ContractVersion = "0.3"

// ReaderRole is the read-only role the service reads the contract as.
const ReaderRole = "api_reader"

// AccessRole is the role the service reads and writes its access store as.
const AccessRole = "api_access"

// AccessSchemaVersion is the newest access.schema_version this build expects.
const AccessSchemaVersion = 3

// ReaderConnectionLimit is the connection limit the database gives
// ReaderRole: the largest pool the configuration accepts must fit in it.
const ReaderConnectionLimit = 16
