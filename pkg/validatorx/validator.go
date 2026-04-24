// Package validatorx wraps go-playground/validator with a singleton,
// struct-tag-driven validator whose errors are rendered as a single
// client-safe message listing every offending field.
package validatorx

import (
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"

	"github.com/dedeez14/goforge/pkg/errs"
)

var (
	once     sync.Once
	instance *validator.Validate
)

// V returns the singleton validator. It's safe for concurrent use.
func V() *validator.Validate {
	once.Do(func() {
		v := validator.New(validator.WithRequiredStructEnabled())
		// Prefer `json` tag names in field errors for a client-friendly surface.
		v.RegisterTagNameFunc(func(fld reflect.StructField) string {
			name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
			if name == "" || name == "-" {
				return fld.Name
			}
			return name
		})
		instance = v
	})
	return instance
}

// Struct validates s and returns an *errs.Error on failure whose Meta
// contains a per-field breakdown the client can surface in a form UI.
func Struct(s any) error {
	if err := V().Struct(s); err != nil {
		var verrs validator.ValidationErrors
		if ok := asValidationErrors(err, &verrs); !ok {
			return errs.Wrap(errs.KindInvalidInput, "validation", "invalid request", err)
		}
		fields := make(map[string]any, len(verrs))
		for _, fe := range verrs {
			fields[fe.Field()] = describe(fe)
		}
		return errs.InvalidInput("validation", "invalid request").With("fields", fields)
	}
	return nil
}

func asValidationErrors(err error, dst *validator.ValidationErrors) bool {
	ve, ok := err.(validator.ValidationErrors)
	if !ok {
		return false
	}
	*dst = ve
	return true
}

func describe(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "is required"
	case "email":
		return "must be a valid email"
	case "min":
		return fmt.Sprintf("must be at least %s", fe.Param())
	case "max":
		return fmt.Sprintf("must be at most %s", fe.Param())
	case "oneof":
		return fmt.Sprintf("must be one of [%s]", fe.Param())
	case "uuid", "uuid4":
		return "must be a valid uuid"
	default:
		if fe.Param() != "" {
			return fmt.Sprintf("failed %s=%s", fe.Tag(), fe.Param())
		}
		return fmt.Sprintf("failed %s", fe.Tag())
	}
}
