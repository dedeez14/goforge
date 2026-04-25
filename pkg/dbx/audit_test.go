package dbx

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestAudit_CreateStampsBoth(t *testing.T) {
	t.Parallel()
	actor := uuid.New()
	ctx := WithActor(context.Background(), actor)
	var a Audit
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	a.Create(ctx, now)
	if !a.CreatedAt.Equal(now) || !a.UpdatedAt.Equal(now) {
		t.Fatalf("Create did not stamp both timestamps: %#v", a)
	}
	if a.CreatedBy == nil || *a.CreatedBy != actor {
		t.Fatalf("CreatedBy: %v", a.CreatedBy)
	}
	if a.UpdatedBy == nil || *a.UpdatedBy != actor {
		t.Fatalf("UpdatedBy: %v", a.UpdatedBy)
	}
}

func TestAudit_TouchOnlyChangesUpdated(t *testing.T) {
	t.Parallel()
	a := Audit{CreatedAt: time.Unix(1000, 0)}
	a.Touch(context.Background(), time.Unix(2000, 0))
	if a.CreatedAt.Unix() != 1000 {
		t.Fatalf("CreatedAt mutated by Touch: %v", a.CreatedAt)
	}
	if a.UpdatedAt.Unix() != 2000 {
		t.Fatalf("UpdatedAt not advanced by Touch: %v", a.UpdatedAt)
	}
}

func TestAudit_SoftDeleteAttributesActor(t *testing.T) {
	t.Parallel()
	actor := uuid.New()
	ctx := WithActor(context.Background(), actor)
	var a Audit
	if a.IsDeleted() {
		t.Fatal("zero Audit should not be deleted")
	}
	a.SoftDelete(ctx, time.Unix(3000, 0))
	if !a.IsDeleted() {
		t.Fatal("after SoftDelete IsDeleted must be true")
	}
	if a.DeletedBy == nil || *a.DeletedBy != actor {
		t.Fatalf("DeletedBy: %v", a.DeletedBy)
	}
}

func TestActorFromContext_NilTreatedAsAbsent(t *testing.T) {
	t.Parallel()
	ctx := WithActor(context.Background(), uuid.Nil)
	if _, ok := ActorFromContext(ctx); ok {
		t.Fatal("uuid.Nil actor must not be reported as present")
	}
}
