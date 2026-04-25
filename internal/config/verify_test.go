package config

import (
	"strings"
	"testing"
	"time"
)

// baseProdConfig returns a hypothetically-valid production Config.
// Tests then mutate one field at a time and assert Verify() fails
// for that specific reason, so a regression in one check never
// hides regressions in other checks.
func baseProdConfig() *Config {
	return &Config{
		App: App{Name: "svc", Env: "production", Version: "1.0.0"},
		HTTP: HTTP{
			Host: "0.0.0.0", Port: 8080, BodyLimitBytes: 1 << 20,
			ReadTimeout: 10 * time.Second, WriteTimeout: 15 * time.Second,
			TrustedProxies: []string{"10.0.0.0/8"},
		},
		Database: Database{
			DSN:      "postgres://app:pw@db.internal:5432/app",
			MinConns: 2, MaxConns: 20,
		},
		JWT: JWT{
			Secret:     "0123456789abcdef0123456789abcdef", // 32 distinct chars
			Issuer:     "my-svc",
			AccessTTL:  15 * time.Minute,
			RefreshTTL: 24 * time.Hour,
		},
		Log: Log{Level: "info"},
		Security: Security{
			CORSAllowOrigins: "https://app.example.com",
			RateLimitPerMin:  120,
			TrustXForwarded:  true,
			ArgonMemoryKiB:   64 * 1024,
			ArgonIters:       3,
			ArgonParallel:    2,
		},
		Platform: Platform{AdminToken: "long-enough-admin-token-1234567890"},
	}
}

func TestVerify_HappyPath(t *testing.T) {
	if err := baseProdConfig().Verify(); err != nil {
		t.Fatalf("base production config should pass Verify: %v", err)
	}
}

func TestVerify_FailsForWeakJWTSecret(t *testing.T) {
	cases := map[string]string{
		"too short":          "short",
		"known weak":         "changeme",
		"all same":           strings.Repeat("a", 64),
		"empty":              "",
		"32 of zero":         "00000000000000000000000000000000",
		"env.example sample": "change-me-to-a-very-long-random-string-of-32+chars",
		"docker-compose":     "please-change-me-to-a-very-long-random-secret-key",
	}
	for name, secret := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := baseProdConfig()
			cfg.JWT.Secret = secret
			err := cfg.Verify()
			if err == nil {
				t.Fatalf("expected Verify to fail for %q", secret)
			}
			if !strings.Contains(err.Error(), "jwt.secret") {
				t.Fatalf("expected jwt.secret problem; got %v", err)
			}
		})
	}
}

func TestVerify_ProductionRejectsOpenCORS(t *testing.T) {
	cfg := baseProdConfig()
	cfg.Security.CORSAllowOrigins = "*"
	err := cfg.Verify()
	if err == nil || !strings.Contains(err.Error(), "cors_allow_origins") {
		t.Fatalf("expected CORS-wildcard error in production; got %v", err)
	}
}

func TestVerify_ProductionRejectsLocalhostDSN(t *testing.T) {
	cfg := baseProdConfig()
	cfg.Database.DSN = "postgres://app:pw@localhost:5432/app"
	err := cfg.Verify()
	if err == nil || !strings.Contains(err.Error(), "localhost") {
		t.Fatalf("expected localhost-DSN error in production; got %v", err)
	}
}

func TestVerify_RejectsTrustedProxiesMisconfig(t *testing.T) {
	cfg := baseProdConfig()
	cfg.HTTP.TrustedProxies = nil
	cfg.Security.TrustXForwarded = true
	err := cfg.Verify()
	if err == nil || !strings.Contains(err.Error(), "trust_x_forwarded") {
		t.Fatalf("expected trusted-proxies error; got %v", err)
	}
}

func TestVerify_DevelopmentIsLenient(t *testing.T) {
	// Same problematic configuration but in development - only
	// universal checks (weak JWT secret, argon params) should fire.
	cfg := baseProdConfig()
	cfg.App.Env = "development"
	cfg.Security.CORSAllowOrigins = "*"
	cfg.Database.DSN = "postgres://app:pw@localhost:5432/app"
	cfg.Platform.AdminToken = ""
	if err := cfg.Verify(); err != nil {
		t.Fatalf("development config should be lenient; got %v", err)
	}
}

func TestVerify_RejectsRefreshTTLSmallerThanAccess(t *testing.T) {
	cfg := baseProdConfig()
	cfg.JWT.RefreshTTL = time.Minute
	cfg.JWT.AccessTTL = time.Hour
	err := cfg.Verify()
	if err == nil || !strings.Contains(err.Error(), "refresh_ttl") {
		t.Fatalf("expected refresh-ttl ordering error; got %v", err)
	}
}

func TestVerify_RejectsArgonBelowOWASP(t *testing.T) {
	cfg := baseProdConfig()
	cfg.Security.ArgonMemoryKiB = 1024 // 1 MiB - way below 19 MiB minimum
	err := cfg.Verify()
	if err == nil || !strings.Contains(err.Error(), "argon_memory_kib") {
		t.Fatalf("expected argon_memory_kib error; got %v", err)
	}
}
