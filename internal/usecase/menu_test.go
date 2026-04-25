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

// TestVisibleTree_PrunesDeeplyNestedDescendants verifies that when a
// hidden ancestor disappears, every descendant disappears too — not
// just direct children. Regression test for the
// single-pass-pruning bug where grandchildren leaked as orphan roots.
func TestVisibleTree_PrunesDeeplyNestedDescendants(t *testing.T) {
	repo := &stubMenuRepo{}
	uc := NewMenuUseCase(repo, nil, zerolog.Nop())
	ctx := context.Background()

	rootID, _ := uuid.NewRandom()
	childID, _ := uuid.NewRandom()
	grandID, _ := uuid.NewRandom()
	greatID, _ := uuid.NewRandom()

	// Hidden root with three layers of visible descendants. Expected
	// outcome: empty tree — the entire branch is gone.
	root := &menu.Menu{ID: rootID, Code: "root", Label: "Root", IsVisible: true,
		RequiredPermissionCode: ptrStr("ops.admin")}
	child := &menu.Menu{ID: childID, Code: "child", Label: "Child", IsVisible: true, ParentID: &rootID}
	grand := &menu.Menu{ID: grandID, Code: "grand", Label: "Grand", IsVisible: true, ParentID: &childID}
	great := &menu.Menu{ID: greatID, Code: "great", Label: "Great", IsVisible: true, ParentID: &grandID}

	for _, m := range []*menu.Menu{root, child, grand, great} {
		_ = repo.Create(ctx, m, nil)
	}

	tree, err := uc.VisibleTree(ctx, nil, []string{"menu.view"})
	if err != nil {
		t.Fatalf("VisibleTree: %v", err)
	}
	if len(tree) != 0 {
		t.Fatalf("expected empty tree (root denied → all descendants pruned), got %d roots", len(tree))
	}
}

// TestUpdate_PartialPATCH_DoesNotZeroFields verifies that a partial
// PATCH (e.g. only sending {"label": "..."}) leaves untouched
// columns alone instead of overwriting them with Go zero values.
// Regression test for the bug where omitting `is_visible` would
// silently hide menus.
func TestUpdate_PartialPATCH_DoesNotZeroFields(t *testing.T) {
	repo := &stubMenuRepo{}
	uc := NewMenuUseCase(repo, nil, zerolog.Nop())
	ctx := context.Background()

	id, _ := uuid.NewRandom()
	icon := "settings"
	path := "/dashboard"
	original := &menu.Menu{
		ID: id, Code: "dash", Label: "Dashboard",
		Icon: icon, Path: path, SortOrder: 7,
		IsVisible: true,
	}
	if err := repo.Create(ctx, original, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}

	// Only label is provided. Everything else must survive.
	newLabel := "Home"
	updated, err := uc.Update(ctx, id, UpdateMenuInput{Label: &newLabel}, nil)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Label != "Home" {
		t.Fatalf("label not applied: %q", updated.Label)
	}
	if !updated.IsVisible {
		t.Fatalf("is_visible was overwritten to false on partial PATCH")
	}
	if updated.SortOrder != 7 {
		t.Fatalf("sort_order was overwritten on partial PATCH: got %d", updated.SortOrder)
	}
	if updated.Icon != icon || updated.Path != path {
		t.Fatalf("icon/path were overwritten on partial PATCH: icon=%q path=%q", updated.Icon, updated.Path)
	}
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
	_, err := uc.Update(ctx, rootID, UpdateMenuInput{ParentID: &grandID}, nil)
	if err == nil {
		t.Fatalf("expected cycle error")
	}
	if !errors.Is(err, menu.ErrCycle) {
		t.Fatalf("expected ErrCycle, got %v", err)
	}
}
