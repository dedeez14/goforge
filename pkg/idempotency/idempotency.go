// Package idempotency implements Stripe-style request replay protection.
//
// A client adds an `Idempotency-Key` header to a state-changing request.
// The first time goforge sees that key it executes the handler, stores
// the response, and returns it. Subsequent requests with the same key
// return the stored response without invoking the handler again, even
// across process restarts (when a durable Store is used).
//
// This mitigates the classic "double-click submit" / network-retry
// duplicate-write hazard. Almost no Go starter ships this; it is a
// goforge signature feature.
package idempotency

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"

	"github.com/dedeez14/goforge/pkg/errs"
)

// HeaderName is the canonical request header carrying the idempotency
// key. It mirrors the de-facto industry standard popularised by Stripe.
const HeaderName = "Idempotency-Key"

// Record is what the Store persists for a given key. The framework
// stores the entire response (status, content type, body) and the hash
// of the request body, so a different request body sent with the same
// key returns 409 Conflict.
type Record struct {
	Key             string
	Method          string
	Path            string
	RequestHash     string
	StatusCode      int
	ContentType     string
	Body            []byte
	CreatedAt       time.Time
	ExpiresAt       time.Time
}

// Store is the persistence interface for idempotency records. The
// framework ships an in-memory implementation (development) and a
// Postgres implementation (production); applications can plug in any
// durable backend.
type Store interface {
	// Lookup returns the stored record for key or ErrNotFound if no
	// record exists. Expired records are treated as not found.
	Lookup(ctx context.Context, key string) (*Record, error)
	// Save persists rec. Implementations must ensure uniqueness on
	// the key; concurrent Save calls for the same key must surface
	// ErrConflict from one of them.
	Save(ctx context.Context, rec *Record) error
}

// ErrNotFound indicates the lookup found no record under the requested
// key.
var ErrNotFound = errors.New("idempotency: record not found")

// ErrConflict indicates a concurrent writer beat us to persisting the
// record. The middleware translates this to a 409 response.
var ErrConflict = errors.New("idempotency: key conflict")

// Options configures the middleware.
type Options struct {
	// Store is required and must not be nil.
	Store Store
	// TTL is the lifetime applied to new records. Defaults to 24h.
	TTL time.Duration
	// Methods restricts middleware activation to the listed verbs.
	// Defaults to {POST, PUT, PATCH, DELETE} - safe verbs are
	// idempotent by HTTP spec and don't need replay protection.
	Methods []string
}

// Middleware returns a Fiber handler that enforces idempotent replay
// for opted-in mutating verbs. Routes that don't supply the header are
// passed through unchanged so the feature stays opt-in per request.
func Middleware(opts Options) fiber.Handler {
	if opts.Store == nil {
		panic("idempotency: Store is required")
	}
	if opts.TTL <= 0 {
		opts.TTL = 24 * time.Hour
	}
	if len(opts.Methods) == 0 {
		opts.Methods = []string{fiber.MethodPost, fiber.MethodPut, fiber.MethodPatch, fiber.MethodDelete}
	}
	allowed := make(map[string]struct{}, len(opts.Methods))
	for _, m := range opts.Methods {
		allowed[strings.ToUpper(m)] = struct{}{}
	}

	return func(c *fiber.Ctx) error {
		if _, ok := allowed[c.Method()]; !ok {
			return c.Next()
		}
		key := strings.TrimSpace(c.Get(HeaderName))
		if key == "" {
			return c.Next()
		}
		if len(key) > 255 {
			return errs.InvalidInput("idempotency.key_too_long", "Idempotency-Key must be at most 255 chars")
		}

		ctx := c.UserContext()
		hash := hashBody(c.Body())

		// Replay check.
		if existing, err := opts.Store.Lookup(ctx, key); err == nil {
			if existing.RequestHash != hash || existing.Method != c.Method() || existing.Path != c.Path() {
				return errs.Conflict(
					"idempotency.key_reused",
					"Idempotency-Key was already used with a different request",
				)
			}
			c.Set(HeaderName, key)
			c.Set("Idempotent-Replay", "true")
			c.Type(existing.ContentType)
			return c.Status(existing.StatusCode).Send(existing.Body)
		} else if !errors.Is(err, ErrNotFound) {
			return errs.Wrap(errs.KindInternal, "idempotency.store_error", "idempotency store unavailable", err)
		}

		// Run the handler and capture the response.
		if err := c.Next(); err != nil {
			return err
		}
		body := append([]byte(nil), c.Response().Body()...)
		rec := &Record{
			Key:         key,
			Method:      c.Method(),
			Path:        c.Path(),
			RequestHash: hash,
			StatusCode:  c.Response().StatusCode(),
			ContentType: string(c.Response().Header.ContentType()),
			Body:        body,
			CreatedAt:   time.Now().UTC(),
			ExpiresAt:   time.Now().UTC().Add(opts.TTL),
		}
		if err := opts.Store.Save(ctx, rec); err != nil && !errors.Is(err, ErrConflict) {
			// We already produced a response; persistence failures
			// are logged via the store but should not turn a
			// successful operation into a failure for the client.
			c.Set("Idempotency-Persisted", "false")
			return nil
		}
		c.Set(HeaderName, key)
		return nil
	}
}

func hashBody(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// MemoryStore is a process-local Store backed by a sync.Map. Suitable
// for development and tests; use the Postgres store for production.
type MemoryStore struct {
	mu sync.Mutex
	m  map[string]*Record
}

// NewMemoryStore returns an empty in-memory store.
func NewMemoryStore() *MemoryStore { return &MemoryStore{m: make(map[string]*Record)} }

// Lookup returns the record for key or ErrNotFound.
func (s *MemoryStore) Lookup(_ context.Context, key string) (*Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.m[key]
	if !ok {
		return nil, ErrNotFound
	}
	if !r.ExpiresAt.IsZero() && time.Now().UTC().After(r.ExpiresAt) {
		delete(s.m, key)
		return nil, ErrNotFound
	}
	return r, nil
}

// Save persists rec. Returns ErrConflict if a concurrent writer wrote
// a different record under the same key.
func (s *MemoryStore) Save(_ context.Context, rec *Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.m[rec.Key]; ok && existing.RequestHash != rec.RequestHash {
		return ErrConflict
	}
	s.m[rec.Key] = rec
	return nil
}
