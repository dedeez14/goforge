package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Verify runs additional, environment-aware sanity checks on top of
// the struct validator. The struct validator catches "field missing
// or out of range"; Verify catches "the values together would be
// dangerous in production".
//
// The checks are deliberately conservative: they only fail-fast for
// configurations that are almost certainly mistakes in production
// (e.g. wide-open CORS combined with a default JWT secret). Lower
// environments are allowed to be sloppy.
//
// Returns an aggregate error joining every problem so operators see
// the full picture, not just the first failure.
func (c *Config) Verify() error {
	var problems []string

	check := func(cond bool, msg string) {
		if cond {
			problems = append(problems, msg)
		}
	}

	prod := c.IsProduction()

	// JWT secret entropy / known-bad values.
	check(isWeakSecret(c.JWT.Secret),
		"jwt.secret looks weak: use at least 32 random bytes (e.g. `openssl rand -base64 48`)")

	if prod {
		// Production must not run with the dev/sample defaults.
		check(strings.EqualFold(c.JWT.Issuer, "goforge"),
			"jwt.issuer is the default 'goforge'; set it to your service name in production")
		check(c.Security.CORSAllowOrigins == "*",
			"security.cors_allow_origins='*' is unsafe in production; pin to known origins")
		check(c.HTTP.Host == "0.0.0.0" && c.Platform.AdminToken == "",
			"platform.admin_token is empty while http binds 0.0.0.0; admin endpoints would be reachable without auth")
		check(c.Security.RateLimitPerMin <= 0,
			"security.rate_limit_per_min must be > 0 in production")
		check(c.Security.TrustXForwarded && len(c.HTTP.TrustedProxies) == 0,
			"security.trust_x_forwarded=true with no http.trusted_proxies set lets clients spoof their IP")
		check(c.Database.MaxConns < 4,
			"database.max_conns < 4 is too low for production load")

		// DSN must not point at localhost in production - almost
		// always a misconfigured deploy.
		if host := dsnHost(c.Database.DSN); host != "" {
			h, _, _ := net.SplitHostPort(host)
			if h == "" {
				h = host
			}
			check(h == "localhost" || h == "127.0.0.1" || h == "::1",
				"database.dsn points at localhost in production - check your secrets wiring")
		}
	}

	// Argon2id minimums (OWASP 2023 interactive: m=64MiB, t=3, p=2).
	check(c.Security.ArgonMemoryKiB < 19*1024,
		"security.argon_memory_kib below 19 MiB falls under OWASP's 2023 minimum for interactive login")
	check(c.Security.ArgonIters < 2,
		"security.argon_iters < 2 is below OWASP's 2023 minimum")
	check(c.Security.ArgonParallel == 0,
		"security.argon_parallel must be >= 1")

	// JWT TTLs - a refresh token shorter than its access token would
	// rotate the session into the ground; warn loudly.
	check(c.JWT.RefreshTTL <= c.JWT.AccessTTL,
		"jwt.refresh_ttl must be longer than jwt.access_ttl, otherwise refresh would expire before access")

	// Body limit zero would let a single client OOM the API.
	check(c.HTTP.BodyLimitBytes <= 0,
		"http.body_limit_bytes must be > 0 to prevent unbounded request bodies")

	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("config: %d problem(s):\n  - %s",
		len(problems), strings.Join(problems, "\n  - "))
}

// knownWeakSecrets catalogues the strings the framework, its README
// or its sample configs use - none of these should ever ship to
// production. The list is intentionally small: detecting "weakness"
// in arbitrary strings is the password-strength rabbit hole, so we
// only catch the obvious copy/paste cases plus a length check.
var knownWeakSecrets = map[string]struct{}{
	"changeme": {},
	"secret":   {},
	"password": {},
	"goforge":  {},
	"please-change-me-in-production-32-chars!": {},
	"00000000000000000000000000000000":         {},
}

func isWeakSecret(s string) bool {
	if len(s) < 32 {
		return true
	}
	if _, bad := knownWeakSecrets[s]; bad {
		return true
	}
	// All-same-character secrets ("aaaa...") have ~zero entropy.
	if len(s) > 0 {
		first := s[0]
		same := true
		for i := 1; i < len(s); i++ {
			if s[i] != first {
				same = false
				break
			}
		}
		if same {
			return true
		}
	}
	return false
}

// dsnHost extracts the host:port from a Postgres connection string.
// Best-effort: returns "" when the DSN is in an unrecognised shape,
// which simply skips the localhost-in-prod check above.
func dsnHost(dsn string) string {
	if strings.Contains(dsn, "://") {
		u, err := url.Parse(dsn)
		if err != nil || u.Host == "" {
			return ""
		}
		return u.Host
	}
	// key=value form: scan for host=...
	for _, part := range strings.Fields(dsn) {
		if strings.HasPrefix(part, "host=") {
			return strings.TrimPrefix(part, "host=")
		}
	}
	return ""
}
