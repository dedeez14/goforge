package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/dedeez14/goforge/internal/domain/rbac"
	"github.com/dedeez14/goforge/pkg/errs"
)

// stubPermissionRepo is the minimum needed to drive PermissionUseCase
// in-memory.
type stubPermissionRepo struct {
	rows []*rbac.Permission
}

func (s *stubPermissionRepo) Create(_ context.Context, p *rbac.Permission, _ *uuid.UUID) error {
	for _, r := range s.rows {
		if r.Code == p.Code {
			return rbac.ErrPermissionTaken
		}
	}
	if p.ID == uuid.Nil {
		p.ID = uuid.New()
	}
	s.rows = append(s.rows, p)
	return nil
}
func (s *stubPermissionRepo) Update(_ context.Context, p *rbac.Permission, _ *uuid.UUID) error {
	for i, r := range s.rows {
		if r.ID == p.ID {
			s.rows[i] = p
			return nil
		}
	}
	return rbac.ErrPermissionNotFound
}
func (s *stubPermissionRepo) Delete(_ context.Context, id uuid.UUID, _ *uuid.UUID) error {
	for i, r := range s.rows {
		if r.ID == id {
			s.rows = append(s.rows[:i], s.rows[i+1:]...)
			return nil
		}
	}
	return rbac.ErrPermissionNotFound
}
func (s *stubPermissionRepo) FindByID(_ context.Context, id uuid.UUID) (*rbac.Permission, error) {
	for _, r := range s.rows {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, rbac.ErrPermissionNotFound
}
func (s *stubPermissionRepo) FindByCode(_ context.Context, code string) (*rbac.Permission, error) {
	for _, r := range s.rows {
		if r.Code == code {
			return r, nil
		}
	}
	return nil, rbac.ErrPermissionNotFound
}
func (s *stubPermissionRepo) List(_ context.Context, _ rbac.PermissionFilter) ([]*rbac.Permission, error) {
	return s.rows, nil
}

// TestPermissionUseCase_Create_RejectsBadCode covers the validation
// shape: empty, too long, illegal characters.
func TestPermissionUseCase_Create_RejectsBadCode(t *testing.T) {
	uc := NewPermissionUseCase(&stubPermissionRepo{}, nil, zerolog.Nop())
	cases := []struct {
		name string
		code string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"with space", "users read"},
		{"with slash", "users/read"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := uc.Create(context.Background(), CreatePermissionInput{
				Code: tc.code, Resource: "users", Action: "read",
			}, nil)
			var e *errs.Error
			if !errors.As(err, &e) || e.Kind != errs.KindInvalidInput {
				t.Fatalf("expected invalid-input error, got %v", err)
			}
		})
	}
}

// TestPermissionUseCase_Create_NormalisesAndStores verifies that
// input strings are trimmed + lowercased before persistence.
func TestPermissionUseCase_Create_NormalisesAndStores(t *testing.T) {
	repo := &stubPermissionRepo{}
	uc := NewPermissionUseCase(repo, nil, zerolog.Nop())
	p, err := uc.Create(context.Background(), CreatePermissionInput{
		Code: "  Users.READ  ", Resource: "USERS", Action: "Read", Description: "  read records ",
	}, nil)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.Code != "users.read" || p.Resource != "users" || p.Action != "read" || p.Description != "read records" {
		t.Fatalf("normalisation failed: %+v", p)
	}
}
