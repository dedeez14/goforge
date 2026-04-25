package usecase

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/dedeez14/goforge/internal/domain/menu"
)

// stubMenuRepo is a minimal in-memory menu.Repository that
// preserves insertion order so tests can assert on tree shape.
type stubMenuRepo struct {
	rows []*menu.Menu
}

func (s *stubMenuRepo) Create(_ context.Context, m *menu.Menu, _ *uuid.UUID) error {
	if m.ID == uuid.Nil {
		m.ID = uuid.New()
	}
	s.rows = append(s.rows, m)
	return nil
}
func (s *stubMenuRepo) Update(_ context.Context, m *menu.Menu, _ *uuid.UUID) error {
	for i, r := range s.rows {
		if r.ID == m.ID {
			s.rows[i] = m
			return nil
		}
	}
	return menu.ErrNotFound
}
func (s *stubMenuRepo) Delete(_ context.Context, id uuid.UUID, _ *uuid.UUID) error {
	for i, r := range s.rows {
		if r.ID == id {
			s.rows = append(s.rows[:i], s.rows[i+1:]...)
			return nil
		}
	}
	return menu.ErrNotFound
}
func (s *stubMenuRepo) FindByID(_ context.Context, id uuid.UUID) (*menu.Menu, error) {
	for _, r := range s.rows {
		if r.ID == id {
			return r, nil
		}
	}
	return nil, menu.ErrNotFound
}
func (s *stubMenuRepo) List(_ context.Context, tenantID *uuid.UUID) ([]*menu.Menu, error) {
	out := make([]*menu.Menu, 0, len(s.rows))
	for _, r := range s.rows {
		if !sameTenant(r.TenantID, tenantID) {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

func ptrStr(s string) *string { return &s }

// TestVisibleTree_FiltersByPermission verifies the central use-case
// behaviour: nodes whose required permission is missing are dropped,
// and any orphaned children (whose parent was dropped) are dropped
// too — never elevated to roots.
func TestVisibleTree_FiltersByPermission(t *testing.T) {
	repo := &stubMenuRepo{}
	uc := NewMenuUseCase(repo, nil, zerolog.Nop())
	ctx := context.Background()

	dashID, err := uuid.NewRandom()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	settingsID, err := uuid.NewRandom()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	dash := &menu.Menu{ID: dashID, Code: "dashboard", Label: "Dashboard", IsVisible: true}
	settings := &menu.Menu{
		ID: settingsID, Code: "settings", Label: "Settings", IsVisible: true,
		RequiredPermissionCode: ptrStr("rbac.manage"),
	}
	usersChild := &menu.Menu{
		Code: "settings.users", Label: "Users", IsVisible: true, ParentID: &settings.ID,
		RequiredPermissionCode: ptrStr("users.manage"),
	}

	for _, m := range []*menu.Menu{dash, settings, usersChild} {
		if err := repo.Create(ctx, m, nil); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	t.Run("plain user sees only dashboard", func(t *testing.T) {
		tree, err := uc.VisibleTree(ctx, nil, []string{"menu.view"})
		if err != nil {
			t.Fatalf("VisibleTree: %v", err)
		}
		if len(tree) != 1 || tree[0].Code != "dashboard" {
			t.Fatalf("expected only dashboard, got %+v", tree)
		}
	})

	t.Run("admin without users.manage sees Settings but not Users child", func(t *testing.T) {
		tree, err := uc.VisibleTree(ctx, nil, []string{"rbac.manage"})
		if err != nil {
			t.Fatalf("VisibleTree: %v", err)
		}
		if len(tree) != 2 {
			t.Fatalf("expected 2 roots, got %d", len(tree))
		}
		var settingsNode *menu.Node
		for _, n := range tree {
			if n.Code == "settings" {
				settingsNode = n
			}
		}
		if settingsNode == nil {
			t.Fatalf("Settings node missing")
		}
		if len(settingsNode.Children) != 0 {
			t.Fatalf("expected Users child to be filtered out, got %d children", len(settingsNode.Children))
		}
	})

	t.Run("super admin sees full tree", func(t *testing.T) {
		tree, err := uc.VisibleTree(ctx, nil, []string{"rbac.manage", "users.manage"})
		if err != nil {
			t.Fatalf("VisibleTree: %v", err)
		}
		if len(tree) != 2 {
			t.Fatalf("expected 2 roots, got %d", len(tree))
		}
		for _, n := range tree {
			if n.Code == "settings" && len(n.Children) != 1 {
				t.Fatalf("expected Users child in Settings, got %d", len(n.Children))
			}
		}
	})

	t.Run("hidden node is invisible regardless of permissions", func(t *testing.T) {
		hiddenID, err := uuid.NewRandom()
		if err != nil {
			t.Fatalf("uuid: %v", err)
		}
		hidden := &menu.Menu{ID: hiddenID, Code: "hidden", Label: "Hidden", IsVisible: false}
		if err := repo.Create(ctx, hidden, nil); err != nil {
			t.Fatalf("seed: %v", err)
		}
		tree, err := uc.VisibleTree(ctx, nil, []string{"rbac.manage", "users.manage"})
		if err != nil {
			t.Fatalf("VisibleTree: %v", err)
		}
		for _, n := range tree {
			if n.Code == "hidden" {
				t.Fatalf("hidden node leaked into tree")
			}
		}
	})
}

// TestUpdate_RejectsCycle verifies that moving a node under one of
// its descendants is refused.
func TestUpdate_RejectsCycle(t *testing.T) {
	repo := &stubMenuRepo{}
	uc := NewMenuUseCase(repo, nil, zerolog.Nop())
	ctx := context.Background()

	rootID, _ := uuid.NewRandom()
	childID, _ := uuid.NewRandom()
	grandID, _ := uuid.NewRandom()
	root := &menu.Menu{ID: rootID, Code: "root", Label: "Root", IsVisible: true}
	child := &menu.Menu{ID: childID, Code: "child", Label: "Child", IsVisible: true, ParentID: &rootID}
	grand := &menu.Menu{ID: grandID, Code: "grand", Label: "Grand", IsVisible: true, ParentID: &childID}
	for _, m := range []*menu.Menu{root, child, grand} {
		_ = repo.Create(ctx, m, nil)
	}

	// Trying to move root under grand → cycle.
	_, err := uc.Update(ctx, rootID, UpdateMenuInput{ParentID: &grandID, IsVisible: true}, nil)
	if err == nil {
		t.Fatalf("expected cycle error")
	}
	if !errors.Is(err, menu.ErrCycle) {
		t.Fatalf("expected ErrCycle, got %v", err)
	}
}
