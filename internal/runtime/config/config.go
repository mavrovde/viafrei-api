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

import (
	"time"
	// The zone database is built in, so quota.reset_tz resolves in an image
	// without one (the distroless runtime image ships none).
	_ "time/tzdata"
)

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
	Charging   Charging   `koanf:"charging"`
	Compose    Compose    `koanf:"compose"`
	RateLimit  RateLimit  `koanf:"ratelimit"`
	Quota      Quota      `koanf:"quota"`
	Tiers      Tiers      `koanf:"tiers"`
	Admission  Admission  `koanf:"admission"`
	Breaker    Breaker    `koanf:"breaker"`
	Metering   Metering   `koanf:"metering"`
	Cache      Cache      `koanf:"cache"`
	Admin      Admin      `koanf:"admin"`
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
	ShutdownGrace     time.Duration `koanf:"shutdown_grace" default:"10s" validate:"min=1s,max=60s" desc:"Time in-flight requests get to finish on shutdown; the final metering flush runs after it."`
	TrustedProxies    []string      `koanf:"trusted_proxies" default:"" validate:"dive,cidr" desc:"Networks (CIDR) of the reverse proxies whose X-Forwarded-For header is believed; the client address is then the nearest address in that header outside these networks. Empty: the immediate peer is the client (RATE-001). Comma-separated in the environment."`
}

// HTTP holds the response-header settings (NFR-SEC-001).
type HTTP struct {
	HSTSMaxAge       time.Duration `koanf:"hsts_max_age" default:"8760h" validate:"min=0s,max=17520h" desc:"max-age of Strict-Transport-Security on every answer, sent in whole seconds; 0s sends max-age=0, which tells browsers to forget the policy."`
	CompressMinBytes int           `koanf:"compress_min_bytes" default:"1024" validate:"min=0,max=1048576" desc:"Smallest answer body, in bytes, that is compressed (br or gzip, as the client accepts); 0 compresses every body."`
	CompressLevel    int           `koanf:"compress_level" default:"5" validate:"min=1,max=9" desc:"Compression level of br and gzip, from 1 (fastest) to 9 (smallest)."`
}

// DB holds the storage settings of the read-only role.
type DB struct {
	URL                    Secret        `koanf:"url" secret:"true" desc:"Connection URL of the service's read-only role. Required to serve. Secret: environment or *_FILE only."`
	PoolMax                int           `koanf:"pool_max" default:"4" validate:"min=1,max=16" desc:"Largest number of pooled connections."`
	PoolMin                int           `koanf:"pool_min" default:"1" validate:"min=0,max=16" desc:"Connections kept open when idle. At most pool_max."`
	ConnectTimeout         time.Duration `koanf:"connect_timeout" default:"2s" validate:"min=100ms,max=30s" desc:"Time allowed to open one connection."`
	MaxConnLifetime        time.Duration `koanf:"max_conn_lifetime" default:"30m" validate:"min=1m,max=24h" desc:"A connection older than this is closed after use."`
	MaxConnIdle            time.Duration `koanf:"max_conn_idle" default:"5m" validate:"min=10s,max=24h" desc:"An idle connection older than this is closed."`
	StatementTimeoutFloor  time.Duration `koanf:"statement_timeout_floor" default:"50ms" validate:"min=10ms,max=1s" desc:"Smallest useful remaining budget; below it a read is refused without touching storage."`
	CancelGrace            time.Duration `koanf:"cancel_grace" default:"100ms" validate:"min=10ms,max=1s" desc:"Time a cancel request gets to land before the connection is abandoned."`
	ApplicationName        string        `koanf:"application_name" default:"viafrei-api" validate:"required,max=63" desc:"application_name reported to the database."`
	QueryExecMode          string        `koanf:"query_exec_mode" default:"cache_statement" validate:"oneof=cache_statement cache_describe describe_exec exec simple_protocol" desc:"How statements are sent: cache_statement prepares each statement once per connection and reuses it; the others are the driver's fallbacks for a pooler that cannot keep prepared statements."`
	StatementCacheCapacity int           `koanf:"statement_cache_capacity" default:"128" validate:"min=1,max=4096" desc:"Prepared statements (or descriptions) cached per connection under cache_statement and cache_describe."`
	SharedBudget           SharedBudget  `koanf:"shared_budget"`
}

// SharedBudget is the storage concurrency budget this service shares with
// the other readers of the store, the MCP server first (NFR-CAP-002): a read
// runs only while it holds one of the budget's slots, a transaction-scoped
// advisory lock in the store, so every reader that takes its slots from the
// same lock space counts against one number.
type SharedBudget struct {
	Slots            int           `koanf:"slots" default:"0" validate:"min=0,max=1024" desc:"Statements all readers sharing db.shared_budget.lock_space may run at once; each read of this service holds one slot to the end of its transaction. 0 switches the shared budget off."`
	LockSpace        int           `koanf:"lock_space" default:"7301" validate:"min=1,max=2147483647" desc:"First key of the two-key advisory locks the slots are (the second is the slot number, 1 to db.shared_budget.slots). Every reader sharing the budget names the same value."`
	CriticalReserved int           `koanf:"critical_reserved" default:"1" validate:"min=0,max=1024" desc:"The highest-numbered slots, which only a read of the critical tier may take. Less than db.shared_budget.slots."`
	MaxWait          time.Duration `koanf:"max_wait" default:"200ms" validate:"min=0s,max=2s" desc:"Longest time a read waits for a free slot before it is refused overloaded with Retry-After. Less than deadline.request."`
	RetryInterval    time.Duration `koanf:"retry_interval" default:"10ms" validate:"min=1ms,max=500ms" desc:"Pause between two attempts to take a slot while every slot is held, spread at random over ±50 %."`
	RetryAfter       time.Duration `koanf:"retry_after" default:"1s" validate:"min=1s,max=60s" desc:"Retry-After of a read refused because no slot came free in db.shared_budget.max_wait."`
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

// Charging holds the settings of charging answers.
type Charging struct {
	StatusMaxAge time.Duration `koanf:"status_max_age" default:"30m" validate:"min=1m,max=24h" desc:"How old a charge point's reported state may be and still count; an older one, or one older than its family's freshness threshold, is answered as stale and not counted as available. The default is the MCP capability's own limit, 30 minutes. The parity harness runs on its own defaults, and its question charging-status-age-limit is asked 38 minutes after a report, so it fails only when the default is raised above 38 minutes."`
}

// Compose holds the settings of composed answers.
type Compose struct {
	PartDeadline time.Duration `koanf:"part_deadline" default:"800ms" validate:"min=100ms,max=2s" desc:"Sub-deadline of each part of a composed answer (FR-BRIEF-003); a part that does not finish in it is reported timeout. The parts run one after the other, so this times the number of parts must not exceed deadline.request minus deadline.reserve."`
	PerPartLimit int           `koanf:"per_part_limit" default:"5" validate:"min=1,max=20" desc:"Items per part of a composed answer when the request names no per_part_limit."`
}

// RateLimit holds the per-address flood control and the rate-limit headers
// (Spec: Rate Limiting, Quotas and Priority, gates 1 and 2).
type RateLimit struct {
	AnonRPS     int           `koanf:"anon_rps" default:"1" validate:"min=0,max=100" desc:"Requests per second one client address may make without presenting a key (service endpoints and keyless requests), as a token bucket (RATE-001, SEC-051). 0 switches the per-address limit off."`
	AnonBurst   int           `koanf:"anon_burst" default:"5" validate:"min=1,max=1000" desc:"Burst of the per-address token bucket: requests a client address may make at once before ratelimit.anon_rps applies."`
	Headers     bool          `koanf:"headers" default:"true" desc:"Send RateLimit-Policy and RateLimit on every keyed answer (RATE-006)."`
	IPStateTTL  time.Duration `koanf:"ip_state_ttl" default:"10m" validate:"min=1m,max=60m" desc:"How long the state of an idle client address is kept; it is held in memory only and never stored (RATE-015)."`
	IPTrackSize int           `koanf:"ip_track_size" default:"100000" validate:"min=100,max=10000000" desc:"Largest number of client addresses tracked at once; when full, idle entries are dropped first, then the least recently seen."`
	IPv6Prefix  int           `koanf:"ipv6_prefix" default:"64" validate:"min=32,max=128" desc:"Prefix length an IPv6 client address is counted by: every address inside one such prefix shares one per-address bucket, since one subscriber is routinely given a whole /64 (RATE-001). 128 counts each address alone."`
}

// Quota holds the quota-window settings (gate 3).
type Quota struct {
	ResetTZ string `koanf:"reset_tz" default:"Europe/Berlin" validate:"required,timezone" desc:"Time zone whose midnight resets the daily quota and whose first of the month resets the monthly quota (RATE-003)."`
}

// Tiers holds the priority-admission tiers (gate 4). The permits are
// db.pool_max (RATE-008).
type Tiers struct {
	Critical       CriticalTier `koanf:"critical"`
	Standard       StandardTier `koanf:"standard"`
	Bulk           BulkTier     `koanf:"bulk"`
	PerKeyInflight int          `koanf:"per_key_inflight" default:"4" validate:"min=1,max=16" desc:"Permits one key may hold at once (RATE-016). At most db.pool_max."`
}

// CriticalTier is the highest tier.
type CriticalTier struct {
	Reserved     int           `koanf:"reserved" default:"1" validate:"min=0,max=16" desc:"Permits kept for the critical tier that lower tiers cannot use (RATE-009)."`
	QueueLen     int           `koanf:"queue_len" default:"32" validate:"min=0,max=1024" desc:"Critical requests that may wait for a permit; one more is shed."`
	QueueMaxWait time.Duration `koanf:"queue_max_wait" default:"800ms" validate:"min=0s,max=2s" desc:"Longest time a critical request waits for a permit before it is shed. Less than deadline.request."`
}

// StandardTier is the middle tier.
type StandardTier struct {
	Reserved     int           `koanf:"reserved" default:"0" validate:"min=0,max=16" desc:"Permits kept for the standard tier (and above) that the bulk tier cannot use."`
	QueueLen     int           `koanf:"queue_len" default:"32" validate:"min=0,max=1024" desc:"Standard requests that may wait for a permit; one more is shed."`
	QueueMaxWait time.Duration `koanf:"queue_max_wait" default:"400ms" validate:"min=0s,max=2s" desc:"Longest time a standard request waits for a permit before it is shed. Less than deadline.request."`
}

// BulkTier is the lowest tier.
type BulkTier struct {
	Reserved     int           `koanf:"reserved" default:"0" validate:"min=0,max=16" desc:"Permits reserved for the bulk tier. No tier is below it, so they keep nothing from anyone; they count against db.pool_max only."`
	QueueLen     int           `koanf:"queue_len" default:"4" validate:"min=0,max=1024" desc:"Bulk requests that may wait for a permit; one more is shed."`
	QueueMaxWait time.Duration `koanf:"queue_max_wait" default:"200ms" validate:"min=0s,max=2s" desc:"Longest time a bulk request waits for a permit before it is shed. Less than deadline.request."`
	MaxInflight  int           `koanf:"max_inflight" default:"1" validate:"min=0,max=16" desc:"Permits the bulk tier may hold at once, however many are free (RATE-009). 0 keeps bulk out of storage. At most db.pool_max."`
}

// Admission holds the admission controller's settings.
type Admission struct {
	AgingAfter time.Duration `koanf:"aging_after" default:"500ms" validate:"min=0s,max=2s" desc:"A waiter older than this is promoted one tier, so no tier starves (RATE-010). 0s promotes every waiter at once."`
	EWMAAlpha  float64       `koanf:"ewma_alpha" default:"0.2" validate:"min=0.01,max=1" desc:"Weight of the newest storage service time in the moving average the wait estimate uses (RATE-011)."`
	ShedStatus int           `koanf:"shed_status" default:"429" validate:"oneof=429 503" desc:"HTTP status of an overloaded refusal: 429 or 503. The problem type is overloaded either way, with Retry-After."`
}

// Breaker holds the settings of the circuit breaker in front of storage
// (NFR-RES-002).
type Breaker struct {
	FailureThreshold int           `koanf:"failure_threshold" default:"5" validate:"min=1,max=100" desc:"Consecutive storage failures, within breaker.window, that open the breaker. Open, it answers 503 upstream-unavailable at once without touching storage."`
	Window           time.Duration `koanf:"window" default:"30s" validate:"min=1s,max=10m" desc:"Span in which the consecutive failures must fall; a failure later than this after the first of a run starts a new run."`
	OpenFor          time.Duration `koanf:"open_for" default:"15s" validate:"min=1s,max=5m" desc:"Time the breaker stays open; Retry-After says what is left of it. Then it admits breaker.half_open_probes trial reads."`
	HalfOpenProbes   int           `koanf:"half_open_probes" default:"2" validate:"min=1,max=16" desc:"Trial reads admitted after breaker.open_for; the first that succeeds closes the breaker, the first that fails opens it again, and other reads are refused meanwhile."`
}

// Metering holds the usage-metering settings (RATE-014).
type Metering struct {
	FlushInterval   time.Duration `koanf:"flush_interval" default:"30s" validate:"min=5s,max=5m" desc:"Interval of the batched usage write to the access store. A crash loses at most one interval of counts; one more flush runs on shutdown."`
	MaxKeysBuffered int           `koanf:"max_keys_buffered" default:"100000" validate:"min=100,max=1000000" desc:"Largest number of (key, hour, operation) counters held between flushes; the counts of a new counter beyond it are dropped and the drop is logged."`
	WriteTimeout    time.Duration `koanf:"write_timeout" default:"1s" validate:"min=100ms,max=10s" desc:"Deadline of one usage write; a failed write keeps its counts for the next flush."`
	RetentionDays   int           `koanf:"retention_days" default:"400" validate:"min=30,max=1825" desc:"Days a usage record is kept; keys prune removes older ones."`
}

// Day is the length of the day counts of this configuration
// (auth.expiry_warning_days, audit.retention_days).
const Day = 24 * time.Hour

// MaxKeyLifetime is the longest lifetime a key may be issued with: 1 095
// days, the upper bound of auth.default_expiry (SEC-040).
const MaxKeyLifetime = 1095 * Day

// Cache holds HTTP caching and the in-process result cache
// (Performance-and-Caching).
type Cache struct {
	Enabled       bool              `koanf:"enabled" default:"true" desc:"Send ETag and Cache-Control on data answers, answer a matching If-None-Match with 304, and keep the result cache. false sends Cache-Control: no-store and caches nothing."`
	MaxBytes      int               `koanf:"max_bytes" default:"33554432" validate:"min=0,max=1073741824" desc:"Byte budget of the result cache's stored bodies; the least recently used entry is dropped first. 0 stores no body (ETag and 304 still work)."`
	MaxEntryBytes int               `koanf:"max_entry_bytes" default:"524288" validate:"min=1024,max=16777216" desc:"Largest body the result cache stores, in bytes. At most cache.max_bytes when that is above 0."`
	MaxEntries    int               `koanf:"max_entries" default:"10000" validate:"min=1,max=1000000" desc:"Most entries the result cache holds, with or without a body; the least recently used entry is dropped first. Each entry also counts against cache.max_bytes with its key, validator, versions and headers."`
	VersionsTTL   time.Duration     `koanf:"versions_ttl" default:"1s" validate:"min=100ms,max=60s" desc:"How long one read of the source families' freshness versions is reused to answer If-None-Match before the handler runs; it is read only while requests arrive."`
	MaxAge        CacheMaxAge       `koanf:"max_age"`
	StaleIfError  CacheStaleIfError `koanf:"stale_if_error"`
	NoStale       []string          `koanf:"no_stale_families" default:"" desc:"Source families whose answers are never served stale: an answer read from any of them is 503 while storage is unavailable, whatever cache.stale_if_error allows its domain. Comma-separated in the environment."`
}

// CacheStaleIfError is the stale allowance of each data domain (NFR-RES-004,
// PROT-047): while storage is unavailable or the deadline runs out, a stored
// answer read no longer than this ago is served with the served_stale warning
// and its true Age instead of 503, and its body is kept this long even past
// its max-age. An answer from several domains takes the smallest; 0s serves
// nothing of that domain stale. It has the fields of CacheMaxAge, so a domain
// is looked up the same way.
type CacheStaleIfError struct {
	Places        time.Duration `koanf:"places" default:"5m" validate:"min=0s,max=1h" desc:"Stale allowance of place answers."`
	Roads         time.Duration `koanf:"roads" default:"5m" validate:"min=0s,max=1h" desc:"Stale allowance of road-condition answers."`
	Energy        time.Duration `koanf:"energy" default:"5m" validate:"min=0s,max=1h" desc:"Stale allowance of charging and fuel answers."`
	Parking       time.Duration `koanf:"parking" default:"5m" validate:"min=0s,max=1h" desc:"Stale allowance of parking answers."`
	Transit       time.Duration `koanf:"transit" default:"5m" validate:"min=0s,max=1h" desc:"Stale allowance of departure and disruption answers."`
	Accessibility time.Duration `koanf:"accessibility" default:"5m" validate:"min=0s,max=1h" desc:"Stale allowance of accessibility answers."`
	Weather       time.Duration `koanf:"weather" default:"5m" validate:"min=0s,max=1h" desc:"Stale allowance of weather answers."`
	Rules         time.Duration `koanf:"rules" default:"5m" validate:"min=0s,max=1h" desc:"Stale allowance of driving-rule answers."`
	Default       time.Duration `koanf:"default" default:"5m" validate:"min=0s,max=1h" desc:"Stale allowance of an answer from a domain not listed above."`
}

// CacheMaxAge is the Cache-Control max-age of each data domain; an answer
// from several domains takes the smallest.
type CacheMaxAge struct {
	Places        time.Duration `koanf:"places" default:"24h" validate:"min=1s,max=168h" desc:"max-age of place answers."`
	Roads         time.Duration `koanf:"roads" default:"60s" validate:"min=1s,max=168h" desc:"max-age of road-condition answers."`
	Energy        time.Duration `koanf:"energy" default:"60s" validate:"min=1s,max=168h" desc:"max-age of charging and fuel answers."`
	Parking       time.Duration `koanf:"parking" default:"60s" validate:"min=1s,max=168h" desc:"max-age of parking answers."`
	Transit       time.Duration `koanf:"transit" default:"30s" validate:"min=1s,max=168h" desc:"max-age of departure and disruption answers."`
	Accessibility time.Duration `koanf:"accessibility" default:"60s" validate:"min=1s,max=168h" desc:"max-age of accessibility answers."`
	Weather       time.Duration `koanf:"weather" default:"300s" validate:"min=1s,max=168h" desc:"max-age of weather answers."`
	Rules         time.Duration `koanf:"rules" default:"24h" validate:"min=1s,max=168h" desc:"max-age of driving-rule answers."`
	Default       time.Duration `koanf:"default" default:"30s" validate:"min=1s,max=168h" desc:"max-age of an answer from a domain not listed above."`
}
