package dbx

import (
	"context"

	"github.com/google/uuid"
)

// actorKey is the unexported key under which the current actor's UUID
// is stored on the request context. Middlewares (auth, jobs runner,
// CLI shells) call WithActor; repositories call ActorFromContext.
type actorKey struct{}

// WithActor returns a context that carries the supplied actor id.
// Pass uuid.Nil to clear it (we treat that as "no actor").
func WithActor(ctx context.Context, id uuid.UUID) context.Context {
	if id == uuid.Nil {
		return ctx
	}
	return context.WithValue(ctx, actorKey{}, id)
}

// ActorFromContext returns the actor id previously stored with
// WithActor. The boolean is false when no actor is set, in which case
// audit columns are written as NULL (e.g. unauthenticated registration,
// system-initiated migrations).
func ActorFromContext(ctx context.Context) (uuid.UUID, bool) {
	v, ok := ctx.Value(actorKey{}).(uuid.UUID)
	return v, ok && v != uuid.Nil
}
