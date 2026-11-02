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
	Server    Server    `koanf:"server"`
	HTTP      HTTP      `koanf:"http"`
	DB        DB        `koanf:"db"`
	Deadline  Deadline  `koanf:"deadline"`
	Contract  Contract  `koanf:"contract"`
	Health    Health    `koanf:"health"`
	Log       Log       `koanf:"log"`
	Docs      Docs      `koanf:"docs"`
	Endpoints Endpoints `koanf:"endpoints"`
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
