package cache

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
)

// Redis is a Cache backed by a single-instance go-redis client. It
// works with Redis, Valkey, KeyDB and any RESP-compatible server.
type Redis struct {
	c *redis.Client
}

// RedisConfig configures the connection. Use either Addr (host:port)
// or URL (redis://...); URL wins when both are set.
type RedisConfig struct {
	Addr     string
	URL      string
	Password string
	DB       int
	TLS      bool
}

// NewRedis returns a Redis cache. The client lazy-connects, so the
// constructor cannot fail on a wrong DSN; call Ping yourself if you
// need eager validation.
func NewRedis(cfg RedisConfig) (*Redis, error) {
	var opts *redis.Options
	if cfg.URL != "" {
		var err error
		opts, err = redis.ParseURL(cfg.URL)
		if err != nil {
			return nil, err
		}
	} else {
		opts = &redis.Options{
			Addr:     cfg.Addr,
			Password: cfg.Password,
			DB:       cfg.DB,
		}
	}
	return &Redis{c: redis.NewClient(opts)}, nil
}

// Get implements Cache.
func (r *Redis) Get(ctx context.Context, key string) ([]byte, error) {
	b, err := r.c.Get(ctx, key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrMiss
	}
	return b, err
}

// Set implements Cache.
func (r *Redis) Set(ctx context.Context, key string, value []byte, ttl time.Duration) error {
	return r.c.Set(ctx, key, value, ttl).Err()
}

// SetNX implements Cache.
func (r *Redis) SetNX(ctx context.Context, key string, value []byte, ttl time.Duration) (bool, error) {
	return r.c.SetNX(ctx, key, value, ttl).Result()
}

// Del implements Cache.
func (r *Redis) Del(ctx context.Context, keys ...string) error {
	if len(keys) == 0 {
		return nil
	}
	return r.c.Del(ctx, keys...).Err()
}

// Incr implements Cache. We use Redis' atomic INCR; when the key is
// new, a separate EXPIRE applies the TTL. The two-command sequence is
// transactional via a small Lua script so the EXPIRE cannot race with
// another writer's reset.
func (r *Redis) Incr(ctx context.Context, key string, ttl time.Duration) (int64, error) {
	if ttl <= 0 {
		return r.c.Incr(ctx, key).Result()
	}
	res, err := incrWithTTL.Run(ctx, r.c,
		[]string{key},
		strconv.FormatInt(int64(ttl/time.Millisecond), 10),
	).Int64()
	if err != nil {
		return 0, err
	}
	return res, nil
}

// Ping implements Cache.
func (r *Redis) Ping(ctx context.Context) error { return r.c.Ping(ctx).Err() }

// Close implements Cache.
func (r *Redis) Close() error { return r.c.Close() }

// incrWithTTL atomically increments key and applies pttl on first
// write. Subsequent writers see the existing TTL and don't reset it,
// so the rate-limit window is honest.
var incrWithTTL = redis.NewScript(`
local n = redis.call("INCR", KEYS[1])
if n == 1 then
  redis.call("PEXPIRE", KEYS[1], ARGV[1])
end
return n`)
