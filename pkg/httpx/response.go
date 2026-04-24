// Package httpx provides the HTTP response envelope and error mapper.
//
// Every handler emits either OK(...) for success or handles errors via
// RespondError(...). This is the only place in the codebase that turns
// domain *errs.Error values into HTTP responses, keeping the mapping
// centralised and consistent.
package httpx

import (
	"errors"
	"net/http"

	"github.com/gofiber/fiber/v2"
	"github.com/dedeez14/goforge/pkg/errs"
)

// Envelope is the canonical JSON body for all responses.
type Envelope struct {
	Success   bool            `json:"success"`
	Data      any             `json:"data,omitempty"`
	Error     *ErrorPayload   `json:"error,omitempty"`
	Meta      *Meta           `json:"meta,omitempty"`
	RequestID string          `json:"request_id,omitempty"`
}

type ErrorPayload struct {
	Code    string         `json:"code"`
	Message string         `json:"message"`
	Meta    map[string]any `json:"meta,omitempty"`
}

type Meta struct {
	Page       int   `json:"page,omitempty"`
	PageSize   int   `json:"page_size,omitempty"`
	Total      int64 `json:"total,omitempty"`
	TotalPages int   `json:"total_pages,omitempty"`
}

// OK writes a 200 success envelope.
func OK(c *fiber.Ctx, data any) error {
	return c.Status(fiber.StatusOK).JSON(Envelope{
		Success:   true,
		Data:      data,
		RequestID: requestID(c),
	})
}

// Created writes a 201 success envelope.
func Created(c *fiber.Ctx, data any) error {
	return c.Status(fiber.StatusCreated).JSON(Envelope{
		Success:   true,
		Data:      data,
		RequestID: requestID(c),
	})
}

// NoContent writes a 204 response.
func NoContent(c *fiber.Ctx) error {
	return c.SendStatus(fiber.StatusNoContent)
}

// Paginated writes a 200 envelope with pagination meta.
func Paginated(c *fiber.Ctx, data any, page, pageSize int, total int64) error {
	totalPages := 0
	if pageSize > 0 {
		totalPages = int((total + int64(pageSize) - 1) / int64(pageSize))
	}
	return c.Status(fiber.StatusOK).JSON(Envelope{
		Success: true,
		Data:    data,
		Meta: &Meta{
			Page:       page,
			PageSize:   pageSize,
			Total:      total,
			TotalPages: totalPages,
		},
		RequestID: requestID(c),
	})
}

// RespondError maps any error to the canonical JSON envelope + HTTP status.
// Only *errs.Error messages are exposed to clients; everything else is
// collapsed to a generic "internal" error to avoid leaking internals.
func RespondError(c *fiber.Ctx, err error) error {
	status, payload := mapError(err)
	return c.Status(status).JSON(Envelope{
		Success:   false,
		Error:     payload,
		RequestID: requestID(c),
	})
}

func mapError(err error) (int, *ErrorPayload) {
	var e *errs.Error
	if !errors.As(err, &e) {
		return http.StatusInternalServerError, &ErrorPayload{
			Code:    "internal",
			Message: "internal server error",
		}
	}
	return statusForKind(e.Kind), &ErrorPayload{
		Code:    e.Code,
		Message: e.Message,
		Meta:    e.Meta,
	}
}

func statusForKind(k errs.Kind) int {
	switch k {
	case errs.KindInvalidInput:
		return http.StatusBadRequest
	case errs.KindUnauthorized:
		return http.StatusUnauthorized
	case errs.KindForbidden:
		return http.StatusForbidden
	case errs.KindNotFound:
		return http.StatusNotFound
	case errs.KindConflict:
		return http.StatusConflict
	case errs.KindRateLimited:
		return http.StatusTooManyRequests
	case errs.KindUnavailable:
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func requestID(c *fiber.Ctx) string {
	if v := c.Locals("requestid"); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}
