package dto

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/dedeez14/goforge/internal/domain/menu"
)

// CreateMenuRequest is the JSON input for POST /menus.
type CreateMenuRequest struct {
	TenantID               *uuid.UUID      `json:"tenant_id"`
	ParentID               *uuid.UUID      `json:"parent_id"`
	Code                   string          `json:"code"                       validate:"required,min=2,max=100"`
	Label                  string          `json:"label"                      validate:"required,min=1,max=128"`
	Icon                   string          `json:"icon"                       validate:"max=64"`
	Path                   string          `json:"path"                       validate:"max=255"`
	SortOrder              int             `json:"sort_order"`
	RequiredPermissionCode *string         `json:"required_permission_code"   validate:"omitempty,max=100"`
	IsVisible              bool            `json:"is_visible"`
	Metadata               json.RawMessage `json:"metadata"`
}

// UpdateMenuRequest is the JSON input for PATCH /menus/:id.
//
// `unset_parent` and `unset_required_permission` are explicit "set
// this field back to NULL" flags because JSON cannot express the
// difference between "field missing" and "field is null" cleanly
// after Go's pointer-omitempty conventions.
type UpdateMenuRequest struct {
	ParentID                *uuid.UUID      `json:"parent_id"`
	UnsetParent             bool            `json:"unset_parent"`
	Label                   string          `json:"label"                      validate:"omitempty,min=1,max=128"`
	Icon                    string          `json:"icon"                       validate:"max=64"`
	Path                    string          `json:"path"                       validate:"max=255"`
	SortOrder               int             `json:"sort_order"`
	RequiredPermissionCode  *string         `json:"required_permission_code"   validate:"omitempty,max=100"`
	UnsetRequiredPermission bool            `json:"unset_required_permission"`
	IsVisible               bool            `json:"is_visible"`
	Metadata                json.RawMessage `json:"metadata"`
}

// MenuResponse is the JSON shape of a single menu node (no children).
type MenuResponse struct {
	ID                     uuid.UUID       `json:"id"`
	TenantID               *uuid.UUID      `json:"tenant_id"`
	ParentID               *uuid.UUID      `json:"parent_id"`
	Code                   string          `json:"code"`
	Label                  string          `json:"label"`
	Icon                   string          `json:"icon"`
	Path                   string          `json:"path"`
	SortOrder              int             `json:"sort_order"`
	RequiredPermissionCode *string         `json:"required_permission_code"`
	IsVisible              bool            `json:"is_visible"`
	Metadata               json.RawMessage `json:"metadata"`
	CreatedAt              time.Time       `json:"created_at"`
	UpdatedAt              time.Time       `json:"updated_at"`
}

// MenuNodeResponse is MenuResponse plus its children.
type MenuNodeResponse struct {
	MenuResponse
	Children []MenuNodeResponse `json:"children"`
}

// MenuFromDomain maps domain → DTO.
func MenuFromDomain(m *menu.Menu) MenuResponse {
	meta := m.Metadata
	if len(meta) == 0 {
		meta = json.RawMessage(`{}`)
	}
	return MenuResponse{
		ID:                     m.ID,
		TenantID:               m.TenantID,
		ParentID:               m.ParentID,
		Code:                   m.Code,
		Label:                  m.Label,
		Icon:                   m.Icon,
		Path:                   m.Path,
		SortOrder:              m.SortOrder,
		RequiredPermissionCode: m.RequiredPermissionCode,
		IsVisible:              m.IsVisible,
		Metadata:               meta,
		CreatedAt:              m.CreatedAt,
		UpdatedAt:              m.UpdatedAt,
	}
}

// MenuTreeFromDomain maps a tree of *menu.Node into the JSON shape.
func MenuTreeFromDomain(nodes []*menu.Node) []MenuNodeResponse {
	out := make([]MenuNodeResponse, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, MenuNodeResponse{
			MenuResponse: MenuFromDomain(n.Menu),
			Children:     MenuTreeFromDomain(n.Children),
		})
	}
	return out
}
