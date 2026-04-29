// Package config loads and validates runtime configuration.
//
// Precedence (lowest -> highest): code defaults -> config file -> env vars.
// Environment variables use the prefix "GOFORGE_" and underscores for nesting,
// e.g. GOFORGE_HTTP_PORT, GOFORGE_DATABASE_DSN.
package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// Config is the fully-resolved application configuration.
type Config struct {
	App      App      `mapstructure:"app"      validate:"required"`
	HTTP     HTTP     `mapstructure:"http"     validate:"required"`
	Database Database `mapstructure:"database" validate:"required"`
	JWT      JWT      `mapstructure:"jwt"      validate:"required"`
	Log      Log      `mapstructure:"log"      validate:"required"`
	Security Security `mapstructure:"security" validate:"required"`
	Platform Platform `mapstructure:"platform"`
}

type App struct {
	Name    string `mapstructure:"name"    validate:"required"`
	Env     string `mapstructure:"env"     validate:"required,oneof=development staging production"`
	Version string `mapstructure:"version"`
}

type HTTP struct {
	Host            string        `mapstructure:"host"`
	Port            int           `mapstructure:"port"             validate:"required,min=1,max=65535"`
	ReadTimeout     time.Duration `mapstructure:"read_timeout"`
	WriteTimeout    time.Duration `mapstructure:"write_timeout"`
	IdleTimeout     time.Duration `mapstructure:"idle_timeout"`
	ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
	BodyLimitBytes  int           `mapstructure:"body_limit_bytes"`
	Prefork         bool          `mapstructure:"prefork"`
	TrustedProxies  []string      `mapstructure:"trusted_proxies"`
}

type Database struct {
	DSN             string        `mapstructure:"dsn" validate:"required"`
	MinConns        int32         `mapstructure:"min_conns"`
	MaxConns        int32         `mapstructure:"max_conns"`
	MaxConnLifetime time.Duration `mapstructure:"max_conn_lifetime"`
	MaxConnIdleTime time.Duration `mapstructure:"max_conn_idle_time"`
	ConnectTimeout  time.Duration `mapstructure:"connect_timeout"`
	StatementCache  bool          `mapstructure:"statement_cache"`
	// ReplicaDSN, when set, enables read-replica routing via
	// pkg/db.Router. Code that asks for Read() will hit the replica;
	// writes and read-your-writes reads (db.WithPrimary) still go to
	// the primary. Leave empty to run single-primary (the default
	// and what every existing deployment keeps doing).
	ReplicaDSN      string `mapstructure:"replica_dsn"`
	ReplicaMinConns int32  `mapstructure:"replica_min_conns"`
	ReplicaMaxConns int32  `mapstructure:"replica_max_conns"`
}

type JWT struct {
	Secret string `mapstructure:"secret" validate:"required,min=32"`
	// NextSecrets is the list of additional HS256 secrets the
	// verifier accepts during a key rotation. New tokens are always
	// signed with Secret; NextSecrets is verify-only. Provide either
	// the legacy secret being rotated out or the upcoming secret
	// being rotated in.
	NextSecrets []string      `mapstructure:"next_secrets"`
	Issuer      string        `mapstructure:"issuer"      validate:"required"`
	AccessTTL   time.Duration `mapstructure:"access_ttl"  validate:"required"`
	RefreshTTL  time.Duration `mapstructure:"refresh_ttl" validate:"required"`
}

type Log struct {
	Level  string `mapstructure:"level" validate:"oneof=trace debug info warn error fatal panic disabled"`
	Pretty bool   `mapstructure:"pretty"`
}

// Platform groups the optional platform-feature toggles. Each module
// is opt-in and can be wired off independently in environments that
// don't need it (e.g. realtime SSE in batch jobs).
type Platform struct {
	IdempotencyEnabled bool   `mapstructure:"idempotency_enabled"`
	IdempotencyTTL     string `mapstructure:"idempotency_ttl"`
	OutboxEnabled      bool   `mapstructure:"outbox_enabled"`
	OutboxBatchSize    int    `mapstructure:"outbox_batch_size"`
	OutboxIntervalMs   int    `mapstructure:"outbox_interval_ms"`
	RealtimeEnabled    bool   `mapstructure:"realtime_enabled"`
	OpenAPIEnabled     bool   `mapstructure:"openapi_enabled"`
	MetricsEnabled     bool   `mapstructure:"metrics_enabled"`
	TenantHeader       string `mapstructure:"tenant_header"`
	AdminToken         string `mapstructure:"admin_token"`

	// OpenTelemetry — when OtelEndpoint is non-empty, the process
	// installs an OTLP/HTTP exporter and wraps requests, outbox
	// dispatches and DB calls in spans. Empty endpoint means the
	// global TracerProvider is no-op (zero overhead).
	OtelEndpoint    string  `mapstructure:"otel_endpoint"`
	OtelInsecure    bool    `mapstructure:"otel_insecure"`
	OtelSampleRatio float64 `mapstructure:"otel_sample_ratio"`
}

type Security struct {
	CORSAllowOrigins string `mapstructure:"cors_allow_origins"`
	RateLimitPerMin  int    `mapstructure:"rate_limit_per_min"`
	TrustXForwarded  bool   `mapstructure:"trust_x_forwarded"`
	// Argon2id tunables. Safe defaults (64 MiB / t=3 / p=2) match
	// OWASP's 2023 recommendations for interactive login.
	ArgonMemoryKiB uint32 `mapstructure:"argon_memory_kib"`
	ArgonIters     uint32 `mapstructure:"argon_iters"`
	ArgonParallel  uint8  `mapstructure:"argon_parallel"`
}

// IsProduction reports whether the configured environment is production-like.
func (c *Config) IsProduction() bool { return c.App.Env == "production" }

// Load reads configuration from the optional config file at path,
// overlays environment variables, applies defaults, and validates.
// If path is empty, only defaults + env are used.
func Load(path string) (*Config, error) {
	// Best-effort .env so local dev "just works" without shell sourcing.
	_ = godotenv.Overload(".env")

	v := viper.New()
	v.SetConfigType("yaml")
	setDefaults(v)

	if path != "" {
		v.SetConfigFile(path)
		if err := v.ReadInConfig(); err != nil {
			return nil, fmt.Errorf("config: read %q: %w", path, err)
		}
	}

	v.SetEnvPrefix("GOFORGE")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// BindEnv registers every configuration key so viper.AutomaticEnv()
	// actually resolves it even when no default or config-file value exists.
	for _, key := range allKeys() {
		_ = v.BindEnv(key)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("config: unmarshal: %w", err)
	}

	if err := validator.New().Struct(&cfg); err != nil {
		return nil, fmt.Errorf("config: validate: %w", err)
	}

	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("app.name", "goforge")
	v.SetDefault("app.env", "development")
	v.SetDefault("app.version", "0.1.0")

	v.SetDefault("http.host", "0.0.0.0")
	v.SetDefault("http.port", 8080)
	v.SetDefault("http.read_timeout", "10s")
	v.SetDefault("http.write_timeout", "15s")
	v.SetDefault("http.idle_timeout", "60s")
	v.SetDefault("http.shutdown_timeout", "15s")
	v.SetDefault("http.body_limit_bytes", 1<<20)
	v.SetDefault("http.prefork", false)

	v.SetDefault("database.min_conns", 2)
	v.SetDefault("database.max_conns", 20)
	v.SetDefault("database.max_conn_lifetime", "30m")
	v.SetDefault("database.max_conn_idle_time", "5m")
	v.SetDefault("database.connect_timeout", "5s")
	v.SetDefault("database.statement_cache", true)

	v.SetDefault("jwt.issuer", "goforge")
	v.SetDefault("jwt.access_ttl", "15m")
	v.SetDefault("jwt.refresh_ttl", "168h")

	v.SetDefault("log.level", "info")
	v.SetDefault("log.pretty", false)

	v.SetDefault("security.cors_allow_origins", "*")
	v.SetDefault("security.rate_limit_per_min", 120)
	v.SetDefault("security.trust_x_forwarded", false)
	v.SetDefault("security.argon_memory_kib", 64*1024)
	v.SetDefault("security.argon_iters", 3)
	v.SetDefault("security.argon_parallel", 2)

	v.SetDefault("platform.idempotency_enabled", true)
	v.SetDefault("platform.idempotency_ttl", "24h")
	v.SetDefault("platform.outbox_enabled", true)
	v.SetDefault("platform.outbox_batch_size", 100)
	v.SetDefault("platform.outbox_interval_ms", 1000)
	v.SetDefault("platform.realtime_enabled", true)
	v.SetDefault("platform.openapi_enabled", true)
	v.SetDefault("platform.metrics_enabled", true)
	v.SetDefault("platform.tenant_header", "X-Tenant-ID")
	v.SetDefault("platform.admin_token", "")
}

// allKeys enumerates every configuration key. Used to bind env vars
// explicitly so BindEnv works independently of defaults and config files.
func allKeys() []string {
	return []string{
		"app.name", "app.env", "app.version",
		"http.host", "http.port", "http.read_timeout", "http.write_timeout",
		"http.idle_timeout", "http.shutdown_timeout", "http.body_limit_bytes",
		"http.prefork", "http.trusted_proxies",
		"database.dsn", "database.min_conns", "database.max_conns",
		"database.max_conn_lifetime", "database.max_conn_idle_time",
		"database.connect_timeout", "database.statement_cache",
		"database.replica_dsn", "database.replica_min_conns", "database.replica_max_conns",
		"jwt.secret", "jwt.next_secrets", "jwt.issuer", "jwt.access_ttl", "jwt.refresh_ttl",
		"log.level", "log.pretty",
		"security.cors_allow_origins", "security.rate_limit_per_min", "security.trust_x_forwarded",
		"security.argon_memory_kib", "security.argon_iters", "security.argon_parallel",
		"platform.idempotency_enabled", "platform.idempotency_ttl",
		"platform.outbox_enabled", "platform.outbox_batch_size", "platform.outbox_interval_ms",
		"platform.realtime_enabled", "platform.openapi_enabled", "platform.metrics_enabled",
		"platform.tenant_header", "platform.admin_token",
		"platform.otel_endpoint", "platform.otel_insecure", "platform.otel_sample_ratio",
	}
}
