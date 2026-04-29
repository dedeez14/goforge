package adminui

import (
	"encoding/json"

	"github.com/gofiber/fiber/v2"
)

// Resource describes a CRUD-backed resource the generic admin SPA
// should render under a dedicated navigation entry.
//
// The SPA uses this metadata purely to drive a declarative list /
// create / edit / delete UI - every call still goes through the
// usual /api/v1/<path> endpoints, and every permission decision
// stays on the server. Operators cannot access a resource the
// server refuses to serve, even if its Resource entry was hand-
// crafted.
//
// Resources are typically produced by `forge gen --with-admin`,
// which emits a companion file exporting a function that returns
// the corresponding Resource, and wired at Mount time via
// WithResources.
type Resource struct {
	// Name is the stable machine identifier used in hash routes
	// and API paths. Lowercase plural form, e.g. "orders".
	Name string `json:"name"`

	// Label is the human-facing label shown in the navigation.
	Label string `json:"label"`

	// APIPath is the REST path prefix (no leading slash), e.g.
	// "orders" -> /api/v1/orders. Defaults to Name.
	APIPath string `json:"api_path,omitempty"`

	// Permission is the permission code the SPA uses to hide the
	// navigation entry before calling the server. The actual
	// authorization check still happens on the server; this is a
	// UX-only hint. Empty means "show to everyone" - anonymous
	// users still get redirected to login because every API call
	// is authenticated.
	Permission string `json:"permission,omitempty"`

	// Fields describes columns shown in the list view and inputs
	// rendered on the create / edit form. The first field is the
	// primary display column; the rest follow in order.
	Fields []Field `json:"fields"`

	// Searchable, when true, shows a free-text filter above the
	// list. The SPA forwards it as ?q=... to the backend.
	Searchable bool `json:"searchable,omitempty"`
}

// Field describes one column / form input on a generic resource
// page. All fields end up in the JSON served to the SPA, so any
// change here is a wire-format change.
type Field struct {
	// Name is the JSON key used in API request/response bodies
	// (and the corresponding DTO field in Go). Keep it snake_case
	// so it matches goforge's DTO convention.
	Name string `json:"name"`

	// Label is the form label. When empty the SPA falls back to
	// a Title-cased version of Name.
	Label string `json:"label,omitempty"`

	// Type is one of text, number, textarea, checkbox, date, email.
	// Unknown values degrade to text so older SPA builds do not
	// reject new kinds.
	Type string `json:"type,omitempty"`

	// Required renders the input with HTML5 `required`. Validation
	// still happens server-side.
	Required bool `json:"required,omitempty"`

	// ListHidden omits the field from the list view (still shown
	// on the form). Useful for long text / password fields.
	ListHidden bool `json:"list_hidden,omitempty"`

	// FormHidden omits the field from the create/edit form.
	// Useful for server-computed fields like timestamps.
	FormHidden bool `json:"form_hidden,omitempty"`
}

// Option mutates Mount's configuration. Use WithResources to
// register generic admin resources.
type Option func(*options)

type options struct {
	resources []Resource
}

// WithResources registers one or more Resource entries the SPA
// will render as navigation items. Zero resources keeps the
// default navigation unchanged, which is backwards-compatible
// with callers pre-dating the Resource API.
func WithResources(rs ...Resource) Option {
	return func(o *options) {
		o.resources = append(o.resources, rs...)
	}
}

// serveResourceManifest installs a GET <prefix>/_resources.json
// endpoint returning the registered resources. The SPA fetches
// this once on boot to extend its route table.
func serveResourceManifest(app *fiber.App, prefix string, resources []Resource) {
	// Normalise a nil slice to an empty one so the wire format is
	// always `{"items":[]}` rather than `{"items":null}`. The
	// distinction matters because this endpoint is part of the
	// public pkg/adminui contract; any external consumer should be
	// able to `for item := range payload.items` without a nil guard.
	items := resources
	if items == nil {
		items = []Resource{}
	}
	// Marshal once at Mount time; the manifest is effectively
	// immutable for the life of the process.
	body, err := json.Marshal(struct {
		Items []Resource `json:"items"`
	}{Items: items})
	if err != nil {
		// We control the inputs here, so marshal cannot fail in
		// practice; return an empty array rather than panicking.
		body = []byte(`{"items":[]}`)
	}

	app.Get(prefix+"/_resources.json", func(c *fiber.Ctx) error {
		c.Set(fiber.HeaderContentType, fiber.MIMEApplicationJSONCharsetUTF8)
		c.Set(fiber.HeaderCacheControl, "no-store")
		return c.Send(body)
	})
}
