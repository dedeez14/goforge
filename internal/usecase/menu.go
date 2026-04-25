package usecase

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/dedeez14/goforge/internal/domain/menu"
	"github.com/dedeez14/goforge/pkg/errs"
)

// CreateMenuInput is the payload for creating a Menu.
type CreateMenuInput struct {
	TenantID               *uuid.UUID
	ParentID               *uuid.UUID
	Code                   string
	Label                  string
	Icon                   string
	Path                   string
	SortOrder              int
	RequiredPermissionCode *string
	IsVisible              bool
	Metadata               json.RawMessage
}

// UpdateMenuInput is the payload for updating a Menu. Code is
// immutable; rename instead by deleting and recreating.
type UpdateMenuInput struct {
	ParentID                *uuid.UUID
	Label                   string
	Icon                    string
	Path                    string
	SortOrder               int
	RequiredPermissionCode  *string
	IsVisible               bool
	Metadata                json.RawMessage
	UnsetParent             bool // explicit "make this a root node"
	UnsetRequiredPermission bool // explicit "remove the permission gate"
}

// MenuUseCase orchestrates menu CRUD and visibility filtering.
type MenuUseCase struct {
	repo  menu.Repository
	audit Auditor
	log   zerolog.Logger
}

// NewMenuUseCase constructs the use case.
func NewMenuUseCase(repo menu.Repository, audit Auditor, log zerolog.Logger) *MenuUseCase {
	return &MenuUseCase{repo: repo, audit: audit, log: log}
}

// Create validates and persists a new menu node.
func (uc *MenuUseCase) Create(ctx context.Context, in CreateMenuInput, actor *uuid.UUID) (*menu.Menu, error) {
	if err := validateMenuCode(in.Code); err != nil {
		return nil, err
	}
	if strings.TrimSpace(in.Label) == "" {
		return nil, errs.InvalidInput("menu.label_required", "menu label is required")
	}
	if in.ParentID != nil {
		parent, err := uc.repo.FindByID(ctx, *in.ParentID)
		if err != nil {
			return nil, err
		}
		// Tenant must match parent's tenant — prevents cross-tenant
		// trees that would break tenant isolation in /menus/mine.
		if !sameTenant(parent.TenantID, in.TenantID) {
			return nil, errs.InvalidInput("menu.parent_tenant_mismatch", "parent menu belongs to a different tenant")
		}
	}
	m := &menu.Menu{
		TenantID:               in.TenantID,
		ParentID:               in.ParentID,
		Code:                   strings.TrimSpace(in.Code),
		Label:                  strings.TrimSpace(in.Label),
		Icon:                   strings.TrimSpace(in.Icon),
		Path:                   strings.TrimSpace(in.Path),
		SortOrder:              in.SortOrder,
		RequiredPermissionCode: in.RequiredPermissionCode,
		IsVisible:              in.IsVisible,
		Metadata:               in.Metadata,
	}
	if err := uc.repo.Create(ctx, m, actor); err != nil {
		return nil, err
	}
	uc.auditSafe(ctx, actor, "menu.create", m.Code, nil, m)
	return m, nil
}

// Update mutates a menu node in place. Cycle detection prevents a
// node from becoming its own ancestor.
func (uc *MenuUseCase) Update(ctx context.Context, id uuid.UUID, in UpdateMenuInput, actor *uuid.UUID) (*menu.Menu, error) {
	current, err := uc.repo.FindByID(ctx, id)
	if err != nil {
		return nil, err
	}
	updated := *current
	if strings.TrimSpace(in.Label) != "" {
		updated.Label = strings.TrimSpace(in.Label)
	}
	updated.Icon = strings.TrimSpace(in.Icon)
	updated.Path = strings.TrimSpace(in.Path)
	updated.SortOrder = in.SortOrder
	updated.IsVisible = in.IsVisible
	if in.UnsetParent {
		updated.ParentID = nil
	} else if in.ParentID != nil {
		if *in.ParentID == id {
			return nil, menu.ErrCycle
		}
		// Walk ancestors of the proposed parent — if we hit `id`,
		// the move would create a cycle.
		visited := map[uuid.UUID]bool{id: true}
		cursor := *in.ParentID
		for {
			if visited[cursor] {
				return nil, menu.ErrCycle
			}
			visited[cursor] = true
			parent, ferr := uc.repo.FindByID(ctx, cursor)
			if ferr != nil {
				return nil, ferr
			}
			if !sameTenant(parent.TenantID, current.TenantID) {
				return nil, errs.InvalidInput("menu.parent_tenant_mismatch", "parent menu belongs to a different tenant")
			}
			if parent.ParentID == nil {
				break
			}
			cursor = *parent.ParentID
		}
		updated.ParentID = in.ParentID
	}
	if in.UnsetRequiredPermission {
		updated.RequiredPermissionCode = nil
	} else if in.RequiredPermissionCode != nil {
		updated.RequiredPermissionCode = in.RequiredPermissionCode
	}
	if len(in.Metadata) > 0 {
		updated.Metadata = in.Metadata
	}
	if err := uc.repo.Update(ctx, &updated, actor); err != nil {
		return nil, err
	}
	uc.auditSafe(ctx, actor, "menu.update", current.Code, current, updated)
	return &updated, nil
}

// Delete soft-deletes a menu node. Children cascade via FK.
func (uc *MenuUseCase) Delete(ctx context.Context, id uuid.UUID, actor *uuid.UUID) error {
	current, err := uc.repo.FindByID(ctx, id)
	if err != nil {
		return err
	}
	if err := uc.repo.Delete(ctx, id, actor); err != nil {
		return err
	}
	uc.auditSafe(ctx, actor, "menu.delete", current.Code, current, nil)
	return nil
}

// Get returns one menu node.
func (uc *MenuUseCase) Get(ctx context.Context, id uuid.UUID) (*menu.Menu, error) {
	return uc.repo.FindByID(ctx, id)
}

// ListAll returns the entire raw flat list for the tenant.
func (uc *MenuUseCase) ListAll(ctx context.Context, tenantID *uuid.UUID) ([]*menu.Menu, error) {
	return uc.repo.List(ctx, tenantID)
}

// Tree returns the full menu tree for the tenant — admin view, no
// permission filtering.
func (uc *MenuUseCase) Tree(ctx context.Context, tenantID *uuid.UUID) ([]*menu.Node, error) {
	flat, err := uc.repo.List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	return buildTree(flat, nil), nil
}

// VisibleTree returns only the menu nodes the caller can see, given
// the codes set. Filtering rules:
//
//   - is_visible = false  → never shown.
//   - required_permission_code IS NULL → always shown to authenticated users.
//   - required_permission_code != NULL → shown only when the user
//     holds that code (or is in superCodes, e.g. "rbac.manage").
//
// If a parent node is hidden, its children are hidden too — there's
// no way to "skip" a denied parent and still expose its descendants.
func (uc *MenuUseCase) VisibleTree(ctx context.Context, tenantID *uuid.UUID, userCodes []string) ([]*menu.Node, error) {
	flat, err := uc.repo.List(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]struct{}, len(userCodes))
	for _, c := range userCodes {
		allowed[c] = struct{}{}
	}
	canSee := func(m *menu.Menu) bool {
		if !m.IsVisible {
			return false
		}
		if m.RequiredPermissionCode == nil {
			return true
		}
		_, ok := allowed[*m.RequiredPermissionCode]
		return ok
	}
	visible := make([]*menu.Menu, 0, len(flat))
	for _, m := range flat {
		if canSee(m) {
			visible = append(visible, m)
		}
	}
	// A child whose parent was filtered out must also disappear, so
	// rebuild the parent index from the survivors and prune any
	// child whose parent is missing.
	idx := make(map[uuid.UUID]struct{}, len(visible))
	for _, m := range visible {
		idx[m.ID] = struct{}{}
	}
	pruned := make([]*menu.Menu, 0, len(visible))
	for _, m := range visible {
		if m.ParentID == nil {
			pruned = append(pruned, m)
			continue
		}
		if _, ok := idx[*m.ParentID]; ok {
			pruned = append(pruned, m)
		}
	}
	return buildTree(pruned, nil), nil
}

// validateMenuCode enforces a sane code shape: short, no
// whitespace, no path separators.
func validateMenuCode(code string) error {
	c := strings.TrimSpace(code)
	if c == "" {
		return errs.InvalidInput("menu.code_required", "menu code is required")
	}
	if len(c) > 100 {
		return errs.InvalidInput("menu.code_long", "menu code must be at most 100 characters")
	}
	for _, r := range c {
		if r == ' ' || r == '/' || r == '\\' {
			return errs.InvalidInput("menu.code_invalid", "menu code may not contain whitespace or slashes")
		}
	}
	return nil
}

// sameTenant returns true when both pointers identify the same
// (tenant or global) scope.
func sameTenant(a, b *uuid.UUID) bool {
	if a == nil && b == nil {
		return true
	}
	if a == nil || b == nil {
		return false
	}
	return *a == *b
}

// buildTree assembles a parent-id-keyed tree from a flat ordered
// slice. It is recursion-free; nodes are linked by mutating the
// parent's Children slice as we encounter them.
func buildTree(flat []*menu.Menu, parent *uuid.UUID) []*menu.Node {
	idx := make(map[uuid.UUID]*menu.Node, len(flat))
	for _, m := range flat {
		idx[m.ID] = &menu.Node{Menu: m}
	}
	roots := make([]*menu.Node, 0)
	for _, m := range flat {
		node := idx[m.ID]
		switch {
		case m.ParentID == nil && parent == nil:
			roots = append(roots, node)
		case m.ParentID != nil:
			if p, ok := idx[*m.ParentID]; ok {
				p.Children = append(p.Children, node)
			} else if parent == nil {
				// Orphan (parent missing or filtered out) → treat as root.
				roots = append(roots, node)
			}
		}
	}
	// Sort each level by SortOrder/label so output is deterministic.
	sortChildren(roots)
	return roots
}

func sortChildren(nodes []*menu.Node) {
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].SortOrder != nodes[j].SortOrder {
			return nodes[i].SortOrder < nodes[j].SortOrder
		}
		return nodes[i].Label < nodes[j].Label
	})
	for _, n := range nodes {
		sortChildren(n.Children)
	}
}

func (uc *MenuUseCase) auditSafe(ctx context.Context, actor *uuid.UUID, action, resource string, before, after any) {
	if uc.audit == nil {
		return
	}
	if err := uc.audit.Log(ctx, actor, action, resource, before, after); err != nil {
		uc.log.Warn().Err(err).Str("action", action).Msg("audit log failed")
	}
}
