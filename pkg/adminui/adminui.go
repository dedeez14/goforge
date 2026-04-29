// Package adminui bundles a client-side admin SPA and exposes a
// Mount helper that serves it as static assets on a Fiber app.
//
// The UI is a pure vanilla-JS single-page application (no build
// step, no framework). It talks to the goforge JSON API at
// /api/v1/* and enforces nothing itself - every permission gate
// lives on the server. The package exists so applications get a
// working back-office out of the box: one function call on boot
// and the admin panel is reachable at /panel/ (configurable).
//
// Design notes:
//
//   - No authentication sits in front of the static assets. The UI
//     is just HTML + JS; anyone can load it. All privileged calls
//     go through /api/v1/* which is permission-guarded.
//   - The assets are embedded via go:embed so the deployable
//     artefact is still a single binary. Operators do not install
//     Node, run a bundler, or copy a dist/ directory.
//   - Keeping adminui in pkg/ (not internal/) means third-party
//     goforge-based apps can opt in explicitly, and the package
//     can later be published standalone if it grows.
package adminui

import (
	"embed"
	"net/http"
	"path"
	"strings"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
)

//go:embed all:assets
var files embed.FS

// Config controls how Mount serves the UI.
type Config struct {
	// Enabled toggles the entire UI. When false, Mount is a no-op.
	Enabled bool

	// Path is the URL prefix the SPA is served from. Defaults to
	// "/panel". A leading slash is added if missing.
	Path string
}

// Mount serves the embedded admin SPA on app at cfg.Path. When
// cfg.Enabled is false (or Mount is called with an empty Config{})
// the function is a no-op - the API still works, just without a
// bundled UI.
//
// Two routes are registered:
//
//   - GET <prefix>          -> 308 redirect to <prefix>/ so users
//     who type /panel (no trailing slash) hit the SPA root.
//   - ANY <prefix>/*         -> filesystem middleware over the
//     embedded FS, with index.html as the SPA fallback so hash
//     routes (#/users, #/roles …) resolve to the bootstrap page.
func Mount(app *fiber.App, cfg Config) {
	if !cfg.Enabled {
		return
	}
	prefix := cfg.Path
	if prefix == "" {
		prefix = "/panel"
	}
	prefix = "/" + strings.Trim(prefix, "/")
	redirectTarget := path.Clean(prefix+"/") + "/"

	// Redirect `/panel` (no trailing slash) to `/panel/`. Fiber runs
	// routes in non-strict mode by default so `/panel` and `/panel/`
	// share one handler; we guard on the raw path so the redirect
	// only fires for the no-slash variant and everything else falls
	// through to the filesystem middleware registered below.
	app.Get(prefix, func(c *fiber.Ctx) error {
		if strings.HasSuffix(c.Path(), "/") {
			return c.Next()
		}
		return c.Redirect(redirectTarget, fiber.StatusPermanentRedirect)
	})

	app.Use(prefix+"/", filesystem.New(filesystem.Config{
		Root:         http.FS(files),
		PathPrefix:   "assets",
		Browse:       false,
		Index:        "index.html",
		NotFoundFile: "assets/index.html",
		MaxAge:       300,
	}))
}
