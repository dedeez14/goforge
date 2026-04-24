// Package errs defines the framework's canonical error taxonomy.
//
// Domain and use-case layers return *Error values. The HTTP adapter maps
// them to status codes once, in a single place (see pkg/httpx).
// Each Error carries a machine-readable Code plus an optional Cause chain.
package errs

import (
	"errors"
	"fmt"
)

// Kind classifies an error for transport-layer mapping.
type Kind uint8

const (
	KindUnknown Kind = iota
	KindInvalidInput
	KindUnauthorized
	KindForbidden
	KindNotFound
	KindConflict
	KindRateLimited
	KindInternal
	KindUnavailable
)

// String returns a short kind label, used in logs and responses.
func (k Kind) String() string {
	switch k {
	case KindInvalidInput:
		return "invalid_input"
	case KindUnauthorized:
		return "unauthorized"
	case KindForbidden:
		return "forbidden"
	case KindNotFound:
		return "not_found"
	case KindConflict:
		return "conflict"
	case KindRateLimited:
		return "rate_limited"
	case KindInternal:
		return "internal"
	case KindUnavailable:
		return "unavailable"
	default:
		return "unknown"
	}
}

// Error is the canonical application error.
type Error struct {
	Kind    Kind
	Code    string // stable machine code, e.g. "user.email_taken"
	Message string // safe-for-client human message
	Cause   error  // optional wrapped error (never leaked to client)
	Meta    map[string]any
}

func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.Cause }

// New constructs a new *Error.
func New(kind Kind, code, message string) *Error {
	return &Error{Kind: kind, Code: code, Message: message}
}

// Wrap attaches a cause to a new *Error without exposing the cause to clients.
func Wrap(kind Kind, code, message string, cause error) *Error {
	return &Error{Kind: kind, Code: code, Message: message, Cause: cause}
}

// With attaches metadata that is safe to expose.
func (e *Error) With(k string, v any) *Error {
	if e.Meta == nil {
		e.Meta = make(map[string]any, 1)
	}
	e.Meta[k] = v
	return e
}

// As unwraps err into a *Error if possible.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// Is reports whether err is an *Error with the given kind.
func Is(err error, kind Kind) bool {
	e, ok := As(err)
	return ok && e.Kind == kind
}

// Common constructors for reuse.
func InvalidInput(code, msg string) *Error { return New(KindInvalidInput, code, msg) }
func Unauthorized(code, msg string) *Error { return New(KindUnauthorized, code, msg) }
func Forbidden(code, msg string) *Error    { return New(KindForbidden, code, msg) }
func NotFound(code, msg string) *Error     { return New(KindNotFound, code, msg) }
func Conflict(code, msg string) *Error     { return New(KindConflict, code, msg) }
func Internal(code, msg string) *Error     { return New(KindInternal, code, msg) }
