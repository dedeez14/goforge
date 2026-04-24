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
}

type JWT struct {
	Secret     string        `mapstructure:"secret"      validate:"required,min=32"`
	Issuer     string        `mapstructure:"issuer"      validate:"required"`
	AccessTTL  time.Duration `mapstructure:"access_ttl"  validate:"required"`
	RefreshTTL time.Duration `mapstructure:"refresh_ttl" validate:"required"`
}

type Log struct {
	Level  string `mapstructure:"level" validate:"oneof=trace debug info warn error fatal panic disabled"`
	Pretty bool   `mapstructure:"pretty"`
}

type Security struct {
	CORSAllowOrigins string `mapstructure:"cors_allow_origins"`
	RateLimitPerMin  int    `mapstructure:"rate_limit_per_min"`
	TrustXForwarded  bool   `mapstructure:"trust_x_forwarded"`
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
		"jwt.secret", "jwt.issuer", "jwt.access_ttl", "jwt.refresh_ttl",
		"log.level", "log.pretty",
		"security.cors_allow_origins", "security.rate_limit_per_min", "security.trust_x_forwarded",
	}
}
