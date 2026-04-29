package handler

import (
	"github.com/gofiber/fiber/v2"

	"github.com/dedeez14/goforge/internal/usecase"
)

// sessionContextFromRequest captures the device hints (User-Agent
// header, best-effort client IP) to attach to a freshly-created
// session row. The IP is taken from c.IP() which already honours
// the trusted-proxy configuration; we deliberately do NOT read
// X-Forwarded-For here because at this layer the framework can't
// distinguish a legitimate proxy hop from a spoofed header.
func sessionContextFromRequest(c *fiber.Ctx) usecase.SessionContext {
	return usecase.SessionContext{
		UserAgent: c.Get(fiber.HeaderUserAgent),
		IP:        c.IP(),
	}
}
