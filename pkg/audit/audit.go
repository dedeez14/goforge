// Package audit is goforge's append-only activity log.
//
// Every privileged action in a goforge app should emit an audit row.
// The contract is intentionally tiny so calling it does not become a
// burden:
//
//	audit.Log(ctx, audit.Entry{
//	    Action:  "user.role.grant",
//	    Subject: actor,
//	    Object:  "user/" + targetID,
//	    Before:  oldRoles,
//	    After:   newRoles,
//	})
//
// Rows are written to a dedicated audit_log table that is never
// updated or deleted by application code. Operators can answer
// "what changed, when, by whom?" without joining half a dozen
// domain tables.
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ActorKind classifies the principal — most rows are 'user', but
// system jobs and webhooks deserve their own label so an operator
// can filter them out.
type ActorKind string

const (
	ActorUser    ActorKind = "user"
	ActorSystem  ActorKind = "system"
	ActorService ActorKind = "service"
)

// Entry is a single audit row in memory.
type Entry struct {
	OccurredAt time.Time
	TenantID   string
	Subject    string
	ActorKind  ActorKind
	Action     string
	Object     string
	RequestID  string
	IP         string
	UserAgent  string
	Before     any
	After      any
	Metadata   map[string]any
}

// Logger is the storage abstraction. Implementations live alongside
// the concrete database; the interface is what the rest of goforge
// consumes.
type Logger interface {
	Log(ctx context.Context, e Entry) error
}

// Postgres is the only Logger goforge ships, but the interface lets
// tests substitute in-memory implementations.
type Postgres struct{ Pool *pgxpool.Pool }

// NewPostgres wraps a pgxpool.
func NewPostgres(p *pgxpool.Pool) *Postgres { return &Postgres{Pool: p} }

// Log implements Logger. Marshalling errors are propagated rather
// than swallowed: an audit miss is a security regression.
func (p *Postgres) Log(ctx context.Context, e Entry) error {
	if e.Action == "" {
		return errors.New("audit: action is required")
	}
	if e.OccurredAt.IsZero() {
		e.OccurredAt = time.Now().UTC()
	}
	if e.ActorKind == "" {
		e.ActorKind = ActorUser
	}
	beforeJSON, err := encode(e.Before)
	if err != nil {
		return err
	}
	afterJSON, err := encode(e.After)
	if err != nil {
		return err
	}
	metaJSON, err := encode(e.Metadata)
	if err != nil {
		return err
	}
	if metaJSON == nil {
		metaJSON = []byte("{}")
	}

	_, err = p.Pool.Exec(ctx, `
		INSERT INTO audit_log
			(occurred_at, tenant_id, actor, actor_kind, action, resource,
			 request_id, ip, user_agent, before, after, metadata)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`,
		e.OccurredAt, nullableText(e.TenantID), nullableText(e.Subject), string(e.ActorKind),
		e.Action, nullableText(e.Object), nullableText(e.RequestID),
		nullableText(e.IP), nullableText(e.UserAgent),
		beforeJSON, afterJSON, metaJSON,
	)
	return err
}

func encode(v any) ([]byte, error) {
	if v == nil {
		return nil, nil
	}
	return json.Marshal(v)
}

func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}
