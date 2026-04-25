package usecase

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/dedeez14/goforge/internal/domain/apikey"
	keytoken "github.com/dedeez14/goforge/pkg/apikey"
	"github.com/dedeez14/goforge/pkg/errs"
)

// APIKeyUseCase orchestrates API-key lifecycle operations: create,
// list, revoke, and authenticate. The plaintext returned by Create
// is shown to the caller exactly once - it is never stored, only
// the SHA-256 hash is.
type APIKeyUseCase struct {
	repo  apikey.Repo
	clock func() time.Time
	env   string // tag baked into newly issued keys (e.g. "live", "test")
}

// NewAPIKeyUseCase returns a use case wired to the given repo. env
// is the short tag baked into prefixes ("live" / "test" / "dev");
// pass an empty string to default to "live".
func NewAPIKeyUseCase(repo apikey.Repo, env string) *APIKeyUseCase {
	if env == "" {
		env = "live"
	}
	return &APIKeyUseCase{
		repo:  repo,
		clock: time.Now,
		env:   env,
	}
}

// CreateInput captures the operator-supplied fields when minting a
// new key. UserID/TenantID may be nil for tenant-wide system keys
// (the deployment uses pkg/authz to gate who is allowed to do that).
type CreateInput struct {
	Name      string
	UserID    *uuid.UUID
	TenantID  *uuid.UUID
	Scopes    []string
	ExpiresAt *time.Time
	CreatedBy *uuid.UUID
}

// CreateResult is what the handler returns to the client. Plaintext
// is the only field that contains the raw secret - the storage
// layer holds only the SHA-256 hash.
type CreateResult struct {
	Key       *apikey.Key
	Plaintext string
}

// Create mints a new API key, persists its hash, and returns the
// plaintext (which the caller MUST display only once).
func (u *APIKeyUseCase) Create(ctx context.Context, in CreateInput) (*CreateResult, error) {
	if in.Name == "" {
		return nil, errs.InvalidInput("apikey.name_required", "API key name is required")
	}
	gen, err := keytoken.Generate(u.env)
	if err != nil {
		return nil, errs.Wrap(errs.KindInternal, "apikey.generate", "failed to mint api key", err)
	}
	k := &apikey.Key{
		ID:        uuid.New(),
		Prefix:    gen.Prefix,
		Hash:      gen.Hash,
		Name:      in.Name,
		UserID:    in.UserID,
		TenantID:  in.TenantID,
		Scopes:    sanitiseScopes(in.Scopes),
		ExpiresAt: in.ExpiresAt,
		CreatedBy: in.CreatedBy,
	}
	if err := u.repo.Create(ctx, k); err != nil {
		return nil, err
	}
	return &CreateResult{Key: k, Plaintext: gen.Plaintext}, nil
}

// List returns every key belonging to userID, sorted newest first.
func (u *APIKeyUseCase) List(ctx context.Context, userID uuid.UUID) ([]*apikey.Key, error) {
	return u.repo.ListByUser(ctx, userID)
}

// Revoke marks id as revoked when the key is owned by ownerID. Any
// other situation (key does not exist, already revoked, or owned by
// a different user) collapses into a single NotFound to defeat
// IDOR enumeration attempts. by is the actor recorded on the
// updated_by audit column and may equal ownerID for self-service.
func (u *APIKeyUseCase) Revoke(ctx context.Context, id, ownerID uuid.UUID, by *uuid.UUID) error {
	if err := u.repo.Revoke(ctx, id, ownerID, by, u.clock()); err != nil {
		return apikey.MapNotFound(err)
	}
	return nil
}

// Authenticate verifies a presented bearer token. Returns the
// matching key when valid, or an *errs.Error suitable for the HTTP
// adapter to render. Side-effects: bumps last_used_at on success
// (best-effort; failures are swallowed since the auth itself
// already succeeded).
func (u *APIKeyUseCase) Authenticate(ctx context.Context, plaintext string) (*apikey.Key, error) {
	parsed, err := keytoken.Parse(plaintext)
	if err != nil {
		return nil, errs.Unauthorized("apikey.malformed", "API key is malformed")
	}
	k, err := u.repo.GetByPrefix(ctx, parsed.Prefix)
	if err != nil {
		// Whether not-found or DB failure, return the same opaque
		// 401 to avoid an oracle for which prefixes exist.
		return nil, errs.Unauthorized("apikey.invalid", "API key is invalid")
	}
	if !keytoken.VerifyHash(plaintext, k.Hash) {
		return nil, errs.Unauthorized("apikey.invalid", "API key is invalid")
	}
	if !k.IsActive(u.clock()) {
		return nil, errs.Unauthorized("apikey.inactive", "API key is revoked or expired")
	}
	// Best-effort touch; this should never fail authentication.
	_ = u.repo.UpdateLastUsed(ctx, k.ID, u.clock())
	return k, nil
}

// sanitiseScopes drops empty entries and de-duplicates while
// preserving order. The wildcard "*" is allowed (operators use it
// for trust-bypass keys; the deployment is responsible for gating
// who may issue those).
func sanitiseScopes(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
