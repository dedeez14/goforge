package module

import (
	"context"
	"io/fs"

	"github.com/gofiber/fiber/v2"
)

// BaseModule is a zero-cost embeddable struct that supplies sensible
// no-op defaults for every optional Module method. Modules need only
// override what they actually use, e.g.
//
//	type MyModule struct{ module.BaseModule }
//
//	func (MyModule) Name() string         { return "my" }
//	func (m MyModule) Routes(r fiber.Router) { r.Get("/foo", m.handler) }
type BaseModule struct{}

func (BaseModule) Init(context.Context, *Context) error { return nil }
func (BaseModule) Routes(fiber.Router)                  {}
func (BaseModule) Migrations() (fs.FS, string)          { return nil, "" }
func (BaseModule) Workers() []Worker                    { return nil }
func (BaseModule) Subscriptions(EventBus)               {}
func (BaseModule) Health(context.Context) error         { return nil }
func (BaseModule) Shutdown(context.Context) error       { return nil }
