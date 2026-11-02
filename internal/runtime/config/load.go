package config

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/v2"

	"github.com/mavrovde/viafrei-api/internal/invariant"
)

// ExitCode is the process exit status for a configuration error.
const ExitCode = 78 // EX_CONFIG from sysexits.h

// Options controls one load.
type Options struct {
	// Environ is the environment, in os.Environ form. Nil means no variables.
	Environ []string
	// File is a YAML file path; empty means the value of VFAPI_CONFIG_FILE,
	// if set.
	File string
	// RequireSecrets makes a missing required secret (db.url) a violation.
	// Commands that never touch storage (openapi, config docs) leave it off.
	RequireSecrets bool
}

// Errors lists every violation found by one load (CFG-003).
type Errors []string

func (e Errors) Error() string {
	return "configuration refused:\n  - " + strings.Join(e, "\n  - ")
}

// Load builds the configuration from defaults, the optional file and the
// environment, then validates it. On any violation it returns Errors naming
// every key at fault; the values of secrets never appear in them.
func Load(opts Options) (*Config, error) {
	fields := Fields()
	known := make(map[string]Field, len(fields))
	byEnv := make(map[string]Field, len(fields))
	for _, f := range fields {
		known[f.Key] = f
		byEnv[f.Env] = f
	}
	var errs Errors
	k := koanf.New(".")

	defaults := make(map[string]any, len(fields))
	for _, f := range fields {
		if f.Type != "secret" {
			defaults[f.Key] = f.Default
		}
	}
	// A map provider without a parser cannot fail.
	invariant.Check(k.Load(confmap.Provider(defaults, "."), nil))

	env := parseEnviron(opts.Environ)
	path := opts.File
	if path == "" {
		path = env[FileEnv]
	}
	if path != "" {
		kf := koanf.New(".")
		if err := kf.Load(file.Provider(path), yaml.Parser()); err != nil {
			errs = append(errs, fmt.Sprintf("config file: cannot read %q: %v", path, err))
		} else {
			for _, key := range kf.Keys() {
				f, ok := known[key]
				switch {
				case !ok:
					errs = append(errs, fmt.Sprintf("%s: unknown key in the config file", key))
				case f.Secret:
					errs = append(errs, fmt.Sprintf("%s: is a secret and may not be set in the config file; use %s or %s_FILE", key, f.Env, f.Env))
				default:
					_ = k.Set(key, kf.Get(key))
				}
			}
		}
	}

	secrets := map[string]string{}
	names := make([]string, 0, len(env))
	for name := range env {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if !strings.HasPrefix(name, EnvPrefix) || name == FileEnv {
			continue
		}
		value := env[name]
		if f, ok := byEnv[name]; ok {
			if f.Secret {
				secrets[f.Key] = value
			} else {
				_ = k.Set(f.Key, value)
			}
			continue
		}
		if base, isFile := strings.CutSuffix(name, "_FILE"); isFile {
			if f, ok := byEnv[base]; ok && f.Secret {
				b, err := os.ReadFile(value) // #nosec G304 -- the operator names the secret file
				if err != nil {
					errs = append(errs, fmt.Sprintf("%s: cannot read the secret file named by %s", f.Key, name))
					continue
				}
				secrets[f.Key] = strings.TrimSpace(string(b))
				continue
			}
		}
		errs = append(errs, fmt.Sprintf("%s: unknown environment variable", name))
	}

	cfg := &Config{}
	root := reflect.ValueOf(cfg).Elem()
	unparsed := map[string]bool{}
	for _, f := range fields {
		dst := root.FieldByIndex(f.index)
		if f.Secret {
			dst.Set(reflect.ValueOf(NewSecret(secrets[f.Key])))
			continue
		}
		if err := assign(dst, f.Type, k.Get(f.Key)); err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", f.Key, err))
			unparsed[f.Key] = true
		}
	}

	// Bounds are checked for every key that parsed; a key that did not parse
	// is already named, and its zero value would only add noise.
	for _, e := range validate(cfg, known) {
		if key, _, _ := strings.Cut(e, ":"); !unparsed[key] {
			errs = append(errs, e)
		}
	}
	if len(unparsed) == 0 {
		errs = append(errs, crossField(cfg)...)
	}
	if opts.RequireSecrets && !cfg.DB.URL.IsSet() {
		errs = append(errs, fmt.Sprintf("db.url: required; set %s or %s_FILE", EnvName("db.url"), EnvName("db.url")))
	}
	if len(errs) > 0 {
		return nil, errs
	}
	return cfg, nil
}

// Defaults returns the configuration with every default and no secret.
func Defaults() *Config {
	// TestDefaultsValidate holds the declaration to this.
	return invariant.Must(Load(Options{}))
}

func parseEnviron(environ []string) map[string]string {
	env := make(map[string]string, len(environ))
	for _, kv := range environ {
		if name, value, ok := strings.Cut(kv, "="); ok {
			env[name] = value
		}
	}
	return env
}

func assign(dst reflect.Value, typ string, raw any) error {
	switch typ {
	case "duration":
		var d time.Duration
		switch v := raw.(type) {
		case string:
			parsed, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("%q is not a duration (for example 500ms, 2s, 5m)", v)
			}
			d = parsed
		default:
			return fmt.Errorf("%v is not a duration; write it with a unit, for example 2s", raw)
		}
		dst.SetInt(int64(d))
	case "int":
		switch v := raw.(type) {
		case string:
			n, err := strconv.Atoi(strings.TrimSpace(v))
			if err != nil {
				return fmt.Errorf("%q is not a whole number", v)
			}
			dst.SetInt(int64(n))
		case int:
			dst.SetInt(int64(v))
		case float64:
			if v != float64(int64(v)) {
				return fmt.Errorf("%v is not a whole number", v)
			}
			dst.SetInt(int64(v))
		default:
			return fmt.Errorf("%v is not a whole number", raw)
		}
	case "bool":
		switch v := raw.(type) {
		case string:
			b, err := strconv.ParseBool(strings.TrimSpace(v))
			if err != nil {
				return fmt.Errorf("%q is not true or false", v)
			}
			dst.SetBool(b)
		case bool:
			dst.SetBool(v)
		default:
			return fmt.Errorf("%v is not true or false", raw)
		}
	case "list":
		var items []string
		switch v := raw.(type) {
		case string:
			for item := range strings.SplitSeq(v, ",") {
				if item = strings.TrimSpace(item); item != "" {
					items = append(items, item)
				}
			}
		case []any:
			for _, item := range v {
				s, ok := item.(string)
				if !ok {
					return fmt.Errorf("list item %v is not text", item)
				}
				items = append(items, s)
			}
		default:
			return fmt.Errorf("%v is not a list", raw)
		}
		dst.Set(reflect.ValueOf(items))
	default:
		s, ok := raw.(string)
		if !ok {
			return fmt.Errorf("%v is not text", raw)
		}
		dst.SetString(s)
	}
	return nil
}

var contractVersionPattern = regexp.MustCompile(`^[0-9]+\.[0-9]+$`)

func newValidator() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	v.RegisterTagNameFunc(func(f reflect.StructField) string { return f.Tag.Get("koanf") })
	_ = v.RegisterValidation("contract_version", func(fl validator.FieldLevel) bool {
		return contractVersionPattern.MatchString(fl.Field().String())
	})
	return v
}

func validate(cfg *Config, known map[string]Field) Errors {
	err := newValidator().Struct(cfg)
	if err == nil {
		return nil
	}
	// Struct on a *Config fails only with ValidationErrors; anything else
	// would be a broken invariant, not a configuration fault.
	var verrs validator.ValidationErrors
	invariant.Check(asValidation(err, &verrs))
	var out Errors
	for _, fe := range verrs {
		key := fe.Namespace()
		if _, rest, ok := strings.Cut(key, "."); ok {
			key = rest
		}
		f := known[key]
		out = append(out, fmt.Sprintf("%s: %s (bounds: %s)", key, describe(fe), Bounds(f)))
	}
	return out
}

func asValidation(err error, verrs *validator.ValidationErrors) error {
	if errors.As(err, verrs) {
		return nil
	}
	return err
}

func describe(fe validator.FieldError) string {
	switch fe.Tag() {
	case "min":
		return "below the minimum " + fe.Param()
	case "max":
		return "above the maximum " + fe.Param()
	case "oneof":
		return "not one of " + fe.Param()
	case "required":
		return "required"
	default:
		return "fails the " + fe.Tag() + " rule"
	}
}

// crossField checks the rules between keys (CFG-004).
func crossField(c *Config) Errors {
	var out Errors
	if c.DB.PoolMin > c.DB.PoolMax {
		out = append(out, fmt.Sprintf("db.pool_min: %d is larger than db.pool_max %d", c.DB.PoolMin, c.DB.PoolMax))
	}
	if c.Deadline.Reserve >= c.Deadline.Request {
		out = append(out, fmt.Sprintf("deadline.reserve: %s must be less than deadline.request %s", c.Deadline.Reserve, c.Deadline.Request))
	}
	if c.Health.ReadyTimeout > c.Deadline.Request {
		out = append(out, fmt.Sprintf("health.ready_timeout: %s is longer than deadline.request %s", c.Health.ReadyTimeout, c.Deadline.Request))
	}
	if c.DB.StatementTimeoutFloor >= c.Deadline.Request-c.Deadline.Reserve {
		out = append(out, fmt.Sprintf("db.statement_timeout_floor: %s leaves no budget inside deadline.request minus deadline.reserve", c.DB.StatementTimeoutFloor))
	}
	return out
}
