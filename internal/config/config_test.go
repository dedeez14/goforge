package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestReplicaEnvVarsResolved pins the Devin Review finding: each new
// field on Database must be listed in allKeys() or viper's
// AutomaticEnv will silently not resolve it. The test writes a
// minimal-but-valid config file, sets the three replica env vars,
// then reloads and asserts Load() picked them up.
func TestReplicaEnvVarsResolved(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(cfg, []byte(`
app:
  name: svc
  env: development
  version: 0.0.0
http:
  port: 8080
database:
  dsn: postgres://a:b@localhost/c
jwt:
  secret: 0123456789abcdef0123456789abcdef
  issuer: svc
  access_ttl: 15m
  refresh_ttl: 24h
log:
  level: info
security: {}
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("GOFORGE_DATABASE_REPLICA_DSN", "postgres://replica:pw@r/c")
	t.Setenv("GOFORGE_DATABASE_REPLICA_MIN_CONNS", "3")
	t.Setenv("GOFORGE_DATABASE_REPLICA_MAX_CONNS", "17")

	c, err := Load(cfg)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got, want := c.Database.ReplicaDSN, "postgres://replica:pw@r/c"; got != want {
		t.Fatalf("ReplicaDSN = %q, want %q (env var not resolved — did you forget allKeys()?)", got, want)
	}
	if got, want := c.Database.ReplicaMinConns, int32(3); got != want {
		t.Fatalf("ReplicaMinConns = %d, want %d", got, want)
	}
	if got, want := c.Database.ReplicaMaxConns, int32(17); got != want {
		t.Fatalf("ReplicaMaxConns = %d, want %d", got, want)
	}
}
