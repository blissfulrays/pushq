// Package config loads server configuration with precedence flags > env
// (PUSHQ_*) > YAML file > defaults.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Role string

const (
	RoleAdmin  Role = "admin"
	RoleWriter Role = "writer"
)

type APIKey struct {
	Key  string `yaml:"key"`
	Role Role   `yaml:"role"`
}

type Dispatcher struct {
	GlobalConcurrency int           `yaml:"global_concurrency"`
	PerQueueClaimCap  int           `yaml:"per_queue_claim_cap"`
	ReconcileInterval time.Duration `yaml:"reconcile_interval"`
	DrainTimeout      time.Duration `yaml:"drain_timeout"`
	SweepInterval     time.Duration `yaml:"sweep_interval"`
	CleanupInterval   time.Duration `yaml:"cleanup_interval"`
	LeaseGrace        time.Duration `yaml:"lease_grace"`
}

type Limits struct {
	MaxBodyBytes     int64         `yaml:"max_body_bytes"`
	MaxHeadersBytes  int64         `yaml:"max_headers_bytes"`
	MaxScheduleAhead time.Duration `yaml:"max_schedule_ahead"`
	DedupWindow      time.Duration `yaml:"dedup_window"`
}

type Retention struct {
	Succeeded time.Duration `yaml:"succeeded"`
	Failed    time.Duration `yaml:"failed"`
}

type Metrics struct {
	Public bool `yaml:"public"`
}

type Log struct {
	Level  string `yaml:"level"`  // debug|info|warn|error
	Format string `yaml:"format"` // text|json
}

// WorkOS switches pushq into SaaS mode when both ClientID and APIKey are set:
// WorkOS AuthKit becomes the only sign-in (it handles sign-up too), teams are
// WorkOS Organizations, and each team's queues, tasks and API keys are
// isolated. Without it, pushq is self-hosted with local accounts.
type WorkOS struct {
	ClientID string `yaml:"client_id"` // WORKOS_CLIENT_ID
	APIKey   string `yaml:"api_key"`   // WORKOS_API_KEY (prefer the env var)
	// RedirectURI is where WorkOS sends users back; it must be listed under
	// Redirects in the WorkOS dashboard. Default: <request origin>/v1/auth/workos/callback.
	RedirectURI string `yaml:"redirect_uri"` // WORKOS_REDIRECT_URI
	// APIBase overrides https://api.workos.com (tests).
	APIBase string `yaml:"api_base"` // WORKOS_API_BASE
}

// Enabled reports whether WorkOS sign-in is configured.
func (w WorkOS) Enabled() bool { return w.ClientID != "" && w.APIKey != "" }

type Config struct {
	DB         string     `yaml:"db"`
	Listen     string     `yaml:"listen"`
	APIKeys    []APIKey   `yaml:"api_keys"`
	WorkOS     WorkOS     `yaml:"workos"`
	Dispatcher Dispatcher `yaml:"dispatcher"`
	Limits     Limits     `yaml:"limits"`
	Retention  Retention  `yaml:"retention"`
	Metrics    Metrics    `yaml:"metrics"`
	Log        Log        `yaml:"log"`
}

func Default() Config {
	return Config{
		DB:     "sqlite:pushq.db",
		Listen: ":8080",
		Dispatcher: Dispatcher{
			GlobalConcurrency: 512,
			PerQueueClaimCap:  100,
			ReconcileInterval: 30 * time.Second,
			DrainTimeout:      30 * time.Second,
			SweepInterval:     15 * time.Second,
			CleanupInterval:   60 * time.Second,
			LeaseGrace:        60 * time.Second,
		},
		Limits: Limits{
			MaxBodyBytes:     1 << 20, // 1 MiB
			MaxHeadersBytes:  16 << 10,
			MaxScheduleAhead: 8760 * time.Hour, // 1 year
			DedupWindow:      time.Hour,
		},
		Retention: Retention{
			Succeeded: 24 * time.Hour,
			Failed:    7 * 24 * time.Hour,
		},
		Metrics: Metrics{Public: true},
		Log:     Log{Level: "info", Format: "text"},
	}
}

// Load builds the config: defaults, then the YAML file (if path != ""), then
// environment variables (PUSHQ_*, WORKOS_*). Variables from a .env file in
// the working directory (or PUSHQ_ENV_FILE) are loaded first, without
// overriding ones already set. Flag overrides are applied by the caller.
func Load(path string) (Config, error) {
	cfg := Default()
	envFile := os.Getenv("PUSHQ_ENV_FILE")
	if envFile == "" {
		envFile = ".env"
	}
	if err := LoadDotEnv(envFile); err != nil {
		return cfg, err
	}
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return cfg, fmt.Errorf("read config: %w", err)
		}
		dec := yaml.NewDecoder(strings.NewReader(string(b)))
		dec.KnownFields(true)
		if err := dec.Decode(&cfg); err != nil {
			return cfg, fmt.Errorf("parse config %s: %w", path, err)
		}
	}
	if err := applyEnv(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

func applyEnv(cfg *Config) error {
	str := func(key string, dst *string) {
		if v, ok := os.LookupEnv(key); ok {
			*dst = v
		}
	}
	dur := func(key string, dst *time.Duration) error {
		if v, ok := os.LookupEnv(key); ok {
			d, err := time.ParseDuration(v)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			*dst = d
		}
		return nil
	}
	num := func(key string, dst *int) error {
		if v, ok := os.LookupEnv(key); ok {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			*dst = n
		}
		return nil
	}
	num64 := func(key string, dst *int64) error {
		if v, ok := os.LookupEnv(key); ok {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			*dst = n
		}
		return nil
	}

	str("WORKOS_CLIENT_ID", &cfg.WorkOS.ClientID)
	str("WORKOS_API_KEY", &cfg.WorkOS.APIKey)
	str("WORKOS_REDIRECT_URI", &cfg.WorkOS.RedirectURI)
	str("WORKOS_API_BASE", &cfg.WorkOS.APIBase)
	str("PUSHQ_DB", &cfg.DB)
	str("PUSHQ_LISTEN", &cfg.Listen)
	str("PUSHQ_LOG_LEVEL", &cfg.Log.Level)
	str("PUSHQ_LOG_FORMAT", &cfg.Log.Format)

	// PUSHQ_API_KEYS="admin:pq_xxx,writer:pq_yyy"
	if v, ok := os.LookupEnv("PUSHQ_API_KEYS"); ok {
		var keys []APIKey
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			role, key, found := strings.Cut(part, ":")
			if !found {
				return fmt.Errorf("PUSHQ_API_KEYS: entry %q must be role:key", part)
			}
			keys = append(keys, APIKey{Key: key, Role: Role(role)})
		}
		cfg.APIKeys = keys
	}

	for _, e := range []error{
		num("PUSHQ_GLOBAL_CONCURRENCY", &cfg.Dispatcher.GlobalConcurrency),
		num("PUSHQ_PER_QUEUE_CLAIM_CAP", &cfg.Dispatcher.PerQueueClaimCap),
		dur("PUSHQ_RECONCILE_INTERVAL", &cfg.Dispatcher.ReconcileInterval),
		dur("PUSHQ_DRAIN_TIMEOUT", &cfg.Dispatcher.DrainTimeout),
		num64("PUSHQ_MAX_BODY_BYTES", &cfg.Limits.MaxBodyBytes),
		num64("PUSHQ_MAX_HEADERS_BYTES", &cfg.Limits.MaxHeadersBytes),
		dur("PUSHQ_MAX_SCHEDULE_AHEAD", &cfg.Limits.MaxScheduleAhead),
		dur("PUSHQ_DEDUP_WINDOW", &cfg.Limits.DedupWindow),
		dur("PUSHQ_RETENTION_SUCCEEDED", &cfg.Retention.Succeeded),
		dur("PUSHQ_RETENTION_FAILED", &cfg.Retention.Failed),
	} {
		if e != nil {
			return e
		}
	}
	if v, ok := os.LookupEnv("PUSHQ_METRICS_PUBLIC"); ok {
		cfg.Metrics.Public = v == "1" || strings.EqualFold(v, "true")
	}
	return nil
}

// LoadDotEnv sets variables from a KEY=VALUE file (comments, blank lines,
// "export " prefixes and surrounding quotes allowed). Variables that are
// already set are left alone, so the real environment always wins. A missing
// file is not an error.
func LoadDotEnv(path string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" || strings.ContainsAny(key, " \t") {
			return fmt.Errorf("%s:%d: expected KEY=VALUE", path, i+1)
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 && (val[0] == '"' || val[0] == '\'') && val[len(val)-1] == val[0] {
			val = val[1 : len(val)-1]
		}
		if _, set := os.LookupEnv(key); !set {
			os.Setenv(key, val)
		}
	}
	return nil
}

func (c Config) Validate() error {
	if (c.WorkOS.ClientID == "") != (c.WorkOS.APIKey == "") {
		return fmt.Errorf("workos: set both WORKOS_CLIENT_ID and WORKOS_API_KEY (or neither)")
	}
	for i, k := range c.APIKeys {
		if k.Key == "" {
			return fmt.Errorf("api_keys[%d]: empty key", i)
		}
		if k.Role != RoleAdmin && k.Role != RoleWriter {
			return fmt.Errorf("api_keys[%d]: role must be admin or writer, got %q", i, k.Role)
		}
	}
	if !strings.HasPrefix(c.DB, "sqlite:") && !strings.HasPrefix(c.DB, "postgres://") && !strings.HasPrefix(c.DB, "postgresql://") {
		return fmt.Errorf("db must be sqlite:<path> or postgres://…, got %q", c.DB)
	}
	if c.Dispatcher.GlobalConcurrency < 1 {
		return fmt.Errorf("dispatcher.global_concurrency must be >= 1")
	}
	if c.Dispatcher.PerQueueClaimCap < 1 {
		return fmt.Errorf("dispatcher.per_queue_claim_cap must be >= 1")
	}
	if c.Limits.MaxBodyBytes < 1 {
		return fmt.Errorf("limits.max_body_bytes must be >= 1")
	}
	return nil
}
