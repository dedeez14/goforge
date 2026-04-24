// Package paginate provides a tiny, reusable request/response pagination helper.
package paginate

import "strconv"

const (
	DefaultPage     = 1
	DefaultPageSize = 20
	MaxPageSize     = 100
)

// Params is a normalised pagination input.
type Params struct {
	Page     int
	PageSize int
}

// Offset returns the SQL OFFSET for the current page.
func (p Params) Offset() int { return (p.Page - 1) * p.PageSize }

// Limit returns the SQL LIMIT for the current page.
func (p Params) Limit() int { return p.PageSize }

// FromStrings parses raw query values, clamps them to sane bounds, and
// substitutes defaults for empty or invalid inputs.
func FromStrings(page, pageSize string) Params {
	p := Params{Page: DefaultPage, PageSize: DefaultPageSize}
	if v, err := strconv.Atoi(page); err == nil && v > 0 {
		p.Page = v
	}
	if v, err := strconv.Atoi(pageSize); err == nil && v > 0 {
		if v > MaxPageSize {
			v = MaxPageSize
		}
		p.PageSize = v
	}
	return p
}
