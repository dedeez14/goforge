// Package menu is the domain package for the dynamic menu tree.
//
// A Menu node may be a top-level entry (ParentID == nil) or a child
// of another node. Each node may declare RequiredPermissionCode; when
// non-nil the node is only visible to users that hold the named
// permission. A nil RequiredPermissionCode makes the node visible to
// every authenticated user.
//
// The domain layer knows nothing about how visibility is computed —
// it merely defines the data shape. The use-case layer is responsible
// for filtering by the caller's permission set.
package menu

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/dedeez14/goforge/pkg/errs"
)

// Menu is one node of the menu tree.
type Menu struct {
	ID                     uuid.UUID
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

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Node decorates a Menu with its resolved children for tree
// transport. Use-cases build it; repositories never see it.
type Node struct {
	*Menu
	Children []*Node
}

// Domain errors.
var (
	ErrNotFound = errs.NotFound("menu.not_found", "menu not found")
	ErrTaken    = errs.Conflict("menu.code_taken", "menu code already exists in this tenant")
	ErrCycle    = errs.InvalidInput("menu.cycle", "moving this menu under the chosen parent would create a cycle")
)
