// Package config is the single declaration of every tunable of the service
// (CFG-001). The Config struct IS the declaration: each leaf field carries its
// key (koanf tag), default, bounds (validate tag), description and whether it
// is a secret. Loading layers defaults, an optional YAML file and environment
// variables; validation refuses unknown keys, out-of-bounds values and
// cross-field violations, listing every violation (CFG-003).
//
// This is the only package allowed to spell a duration literal; a lint test
// enforces that (DEV-043).
package config

import "time"

// EnvPrefix is the prefix of every environment variable the service reads.
const EnvPrefix = "VFAPI_"

// FileEnv names the environment variable holding the optional YAML file path.
const FileEnv = EnvPrefix + "CONFIG_FILE"

// Config is the complete configuration of the service.
type Config struct {
	Server     Server     `koanf:"server"`
	HTTP       HTTP       `koanf:"http"`
	DB         DB         `koanf:"db"`
	Deadline   Deadline   `koanf:"deadline"`
	Contract   Contract   `koanf:"contract"`
	Health     Health     `koanf:"health"`
	Log        Log        `koanf:"log"`
	Docs       Docs       `koanf:"docs"`
	Endpoints  Endpoints  `koanf:"endpoints"`
	Access     Access     `koanf:"access"`
	Auth       Auth       `koanf:"auth"`
	Audit      Audit      `koanf:"audit"`
	Sources    Sources    `koanf:"sources"`
	Geo        Geo        `koanf:"geo"`
	Pagination Pagination `koanf:"pagination"`
	Compose    Compose    `koanf:"compose"`
}

// Server holds the public HTTP listener settings.
type Server struct {
	ListenAddr        string        `koanf:"listen_addr" default:"localhost:8080" validate:"required,hostname_port" desc:"Address of the public listener, host:port. Behind the TLS edge in production."`
	PublicBaseURL     string        `koanf:"public_base_url" default:"https://api.viafrei.de" validate:"required,url,startswith=https://" desc:"Absolute base URL used in links, problem type URIs and the OpenAPI servers entry. https only."`
	ReadHeaderTimeout time.Duration `koanf:"read_header_timeout" default:"2s" validate:"min=100ms,max=300s" desc:"Time allowed to read the request headers."`
	ReadTimeout       time.Duration `koanf:"read_timeout" default:"5s" validate:"min=100ms,max=300s" desc:"Time allowed to read the whole request."`
	WriteTimeout      time.Duration `koanf:"write_timeout" default:"5s" validate:"min=100ms,max=300s" desc:"Time allowed to write the response."`
	IdleTimeout       time.Duration `koanf:"idle_timeout" default:"60s" validate:"min=100ms,max=300s" desc:"Time an idle keep-alive connection is kept open."`
	MaxHeaderBytes    int           `koanf:"max_header_bytes" default:"16384" validate:"min=4096,max=1048576" desc:"Largest accepted request header block, in bytes (431 above)."`
	MaxURIBytes       int           `koanf:"max_uri_bytes" default:"8192" validate:"min=2048,max=65536" desc:"Longest accepted request URI, in bytes (414 above)."`
	ShutdownGrace     time.Duration `koanf:"shutdown_grace" default:"10s" validate:"min=1s,max=60s" desc:"Time in-flight requests get to finish on shutdown."`
}

// HTTP holds the response-header settings (NFR-SEC-001).
type HTTP struct {
	HSTSMaxAge time.Duration `koanf:"hsts_max_age" default:"8760h" validate:"min=0s,max=17520h" desc:"max-age of Strict-Transport-Security on every answer, sent in whole seconds; 0s sends max-age=0, which tells browsers to forget the policy."`
}

// DB holds the storage settings of the read-only role.
type DB struct {
	URL                   Secret        `koanf:"url" secret:"true" desc:"Connection URL of the service's read-only role. Required to serve. Secret: environment or *_FILE only."`
	PoolMax               int           `koanf:"pool_max" default:"4" validate:"min=1,max=16" desc:"Largest number of pooled connections."`
	PoolMin               int           `koanf:"pool_min" default:"1" validate:"min=0,max=16" desc:"Connections kept open when idle. At most pool_max."`
	ConnectTimeout        time.Duration `koanf:"connect_timeout" default:"2s" validate:"min=100ms,max=30s" desc:"Time allowed to open one connection."`
	MaxConnLifetime       time.Duration `koanf:"max_conn_lifetime" default:"30m" validate:"min=1m,max=24h" desc:"A connection older than this is closed after use."`
	MaxConnIdle           time.Duration `koanf:"max_conn_idle" default:"5m" validate:"min=10s,max=24h" desc:"An idle connection older than this is closed."`
	StatementTimeoutFloor time.Duration `koanf:"statement_timeout_floor" default:"50ms" validate:"min=10ms,max=1s" desc:"Smallest useful remaining budget; below it a read is refused without touching storage."`
	CancelGrace           time.Duration `koanf:"cancel_grace" default:"100ms" validate:"min=10ms,max=1s" desc:"Time a cancel request gets to land before the connection is abandoned."`
	ApplicationName       string        `koanf:"application_name" default:"viafrei-api" validate:"required,max=63" desc:"application_name reported to the database."`
}

// Deadline holds the request budget.
type Deadline struct {
	Request time.Duration `koanf:"request" default:"2s" validate:"min=200ms,max=2s" desc:"Hard deadline of every request (NFR-PERF-001)."`
	Reserve time.Duration `koanf:"reserve" default:"50ms" validate:"min=0s,max=500ms" desc:"Margin kept back from the deadline when the statement timeout is set. Less than deadline.request."`
}

// Contract holds the read-contract compatibility settings.
type Contract struct {
	Expected string `koanf:"expected" default:"" validate:"omitempty,contract_version" desc:"Read-contract version (major.minor) this instance requires; empty means the version built in. Readiness fails on another major or an older minor."`
}

// Health holds the probe settings.
type Health struct {
	ReadyTimeout time.Duration `koanf:"ready_timeout" default:"500ms" validate:"min=50ms,max=2s" desc:"Time the readiness probe gives storage to answer. At most deadline.request."`
	ProbeTimeout time.Duration `koanf:"probe_timeout" default:"1s" validate:"min=100ms,max=10s" desc:"Time the healthcheck subcommand waits for /healthz."`
}

// Log holds the logging settings.
type Log struct {
	Level  string `koanf:"level" default:"info" validate:"oneof=debug info warn error" desc:"Lowest level written: debug, info, warn or error."`
	Format string `koanf:"format" default:"json" validate:"oneof=json text" desc:"Log line format on standard output: json or text."`
}

// Docs holds the interface-description settings.
type Docs struct {
	Enabled bool `koanf:"enabled" default:"true" desc:"Serve the OpenAPI document at /v1/openapi.json and /openapi.json."`
}

// Endpoints holds the operational switches for operations.
type Endpoints struct {
	Disabled []string `koanf:"disabled" default:"" desc:"Operation ids switched off: they answer 404 feature-disabled and leave the OpenAPI document. Comma-separated in the environment."`
}

// Access holds the settings of the access store: the API's own small store of
// plans, keys, terms acceptances and audit records.
type Access struct {
	URL     Secret `koanf:"url" secret:"true" desc:"Connection URL of the API's own access-store role. Without it every keyed operation answers 404 feature-disabled. Secret: environment or *_FILE only."`
	PoolMax int    `koanf:"pool_max" default:"2" validate:"min=1,max=8" desc:"Largest number of pooled access-store connections."`
}

// Auth holds the API-key settings (Spec: Authentication and Authorisation).
type Auth struct {
	Environment           string        `koanf:"environment" default:"live" validate:"oneof=live test" desc:"Which keys this instance accepts: live (production) or test (sandbox). A key of the other environment is invalid here (SEC-004)."`
	KeyPrefixLive         string        `koanf:"key_prefix_live" default:"vf_live_" validate:"key_prefix" desc:"Prefix of a production key: two groups of 1 to 5 lowercase letters or digits, each closed by an underscore."`
	KeyPrefixTest         string        `koanf:"key_prefix_test" default:"vf_test_" validate:"key_prefix" desc:"Prefix of a sandbox key, in the same form. Differs from auth.key_prefix_live."`
	Pepper                Secret        `koanf:"pepper" secret:"true" desc:"Server-side MAC pepper, at least 32 bytes. Required with access.url. Held outside the access store (SEC-010). Secret: environment or *_FILE only."`
	PepperPrevious        Secret        `koanf:"pepper_previous" secret:"true" desc:"The previous pepper during a pepper rotation: a key that matches under it is re-MACed under auth.pepper on first use (SEC-011). At least 32 bytes when set."`
	KeyCacheTTL           time.Duration `koanf:"key_cache_ttl" default:"60s" validate:"min=1s,max=10m" desc:"How long a key read from the access store is kept in memory. A plan change or a revocation is seen at the latest after this time (SEC-022)."`
	NegativeCacheTTL      time.Duration `koanf:"negative_cache_ttl" default:"10s" validate:"min=0s,max=5m" desc:"How long an unknown key id is remembered as unknown, to blunt floods of random keys (SEC-050). 0s turns it off."`
	KeyCacheSize          int           `koanf:"key_cache_size" default:"10000" validate:"min=10,max=1000000" desc:"Largest number of key ids held in the key cache, known and unknown together; when full, expired entries are dropped first, then any one."`
	RotationOverlap       time.Duration `koanf:"rotation_overlap" default:"168h" validate:"min=0s,max=720h" desc:"How long the previous secret stays valid after a rotation (SEC-041)."`
	DefaultExpiry         time.Duration `koanf:"default_expiry" default:"8760h" validate:"min=24h,max=26280h" desc:"Lifetime of a key issued without an explicit expiry (SEC-040). An explicit expiry is held to 26280h (1 095 days) too."`
	RevocationPropagation time.Duration `koanf:"revocation_propagation" default:"5s" validate:"min=1s,max=60s" desc:"Interval at which the service asks the access store for keys changed since its last look and drops them from its cache (SEC-042). At most auth.key_cache_ttl."`
	ExpiryWarningDays     int           `koanf:"expiry_warning_days" default:"14" validate:"min=0,max=90" desc:"Days before expiry from which an answer carries the key_expiring warning (SEC-043). 0 turns the warning off."`
	FailureLimit          int           `koanf:"failure_limit" default:"10" validate:"min=1,max=1000" desc:"Authentication failures one client address may have per auth.failure_window before it is answered 429 rate-limited (SEC-052)."`
	FailureWindow         time.Duration `koanf:"failure_window" default:"1m" validate:"min=1s,max=1h" desc:"The window auth.failure_limit counts in."`
	FailureTrackSize      int           `koanf:"failure_track_size" default:"10000" validate:"min=10,max=1000000" desc:"Largest number of client addresses whose failures are counted at once; when full, ended windows are dropped first, then any one."`
}

// Audit holds the audit-log settings.
type Audit struct {
	RetentionDays int `koanf:"retention_days" default:"395" validate:"min=30,max=1825" desc:"Days an audit record is kept; keys prune removes older ones (SEC-045)."`
}

// Sources holds the licence-gate switches.
type Sources struct {
	Disabled []string `koanf:"disabled" default:"" desc:"Source families the licence gate always denies, whatever the plan (SEC-030). Comma-separated in the environment."`
}

// Geo holds the settings of position searches.
type Geo struct {
	DefaultRadiusM int `koanf:"default_radius_m" default:"2000" validate:"min=1,max=50000" desc:"Search radius in metres when a near search names none. At most the radius_m maximum of the interface, 50000."`
}

// Pagination holds the settings of list answers.
type Pagination struct {
	DefaultLimit int `koanf:"default_limit" default:"20" validate:"min=1,max=100" desc:"Items a list answer returns when the request names no limit (ENV-021). At most the limit maximum of the interface, 100."`
}

// Compose holds the settings of composed answers.
type Compose struct {
	PartDeadline time.Duration `koanf:"part_deadline" default:"800ms" validate:"min=100ms,max=2s" desc:"Sub-deadline of each part of a composed answer (FR-BRIEF-003); a part that does not finish in it is reported timeout."`
	PerPartLimit int           `koanf:"per_part_limit" default:"5" validate:"min=1,max=20" desc:"Items per part of a composed answer when the request names no per_part_limit."`
}

// Day is the length of the day counts of this configuration
// (auth.expiry_warning_days, audit.retention_days).
const Day = 24 * time.Hour

// MaxKeyLifetime is the longest lifetime a key may be issued with: 1 095
// days, the upper bound of auth.default_expiry (SEC-040).
const MaxKeyLifetime = 1095 * Day
