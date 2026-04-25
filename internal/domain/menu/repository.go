package menu

import (
	"context"

	"github.com/google/uuid"
)

// Repository is the persistence port for Menu.
type Repository interface {
	Create(ctx context.Context, m *Menu, actor *uuid.UUID) error
	Update(ctx context.Context, m *Menu, actor *uuid.UUID) error
	Delete(ctx context.Context, id uuid.UUID, actor *uuid.UUID) error
	FindByID(ctx context.Context, id uuid.UUID) (*Menu, error)
	List(ctx context.Context, tenantID *uuid.UUID) ([]*Menu, error)
}
