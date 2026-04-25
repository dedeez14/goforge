package postgres

import (
	"strconv"

	"github.com/google/uuid"
)

// actorOrNil normalises an actor pointer for SQL parameter binding:
// pgx wants a typed NULL, not Go's typed-nil-pointer-to-uuid.
func actorOrNil(a *uuid.UUID) any {
	if a == nil {
		return nil
	}
	return *a
}

// itoa is the local i64-free strconv replacement used by query
// builders to turn $-placeholder indices into strings.
func itoa(i int) string { return strconv.Itoa(i) }
