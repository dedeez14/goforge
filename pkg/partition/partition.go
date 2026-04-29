// Package partition manages monthly RANGE partitions on PostgreSQL
// partitioned tables.
//
// Goforge uses declarative partitioning to keep high-churn, append-only
// tables — audit_log today, jobs/refresh_tokens/webhook_deliveries as
// their PK constraints are revisited — from degrading into a single
// multi-hundred-million-row heap. Each logical table becomes a parent
// (`PARTITION BY RANGE (column)`) plus one child table per month.
//
// This package owns the housekeeping: it creates partitions a few
// months into the future before workloads land in them, and (on
// request) drops partitions that have aged out of the retention
// window. The Maintainer type wraps a Manager as a daily job so
// operators do not have to remember to run the rollover manually.
//
// # Naming convention
//
// Child partitions are named `<parent>_yYYYYmMM` — e.g.
// `audit_log_y2026m04`. The convention lets Manager enumerate
// existing partitions without joining `pg_inherits`: it scans
// `pg_tables` with a `LIKE` predicate and parses the suffix.
//
// A default partition named `<parent>_default` catches inserts
// outside the pre-created range (for example, historical rows with
// back-dated timestamps). Default partitions are intentionally not
// dropped by the Manager; the operator must detach them first.
//
// # Safety
//
// Every DDL statement the Manager emits uses `CREATE TABLE IF NOT
// EXISTS` / `DROP TABLE IF EXISTS`, so running the Maintainer twice
// — including across a zero-downtime rolling deploy — is safe.
package partition

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// Executor is the minimum surface Manager needs from a pgx pool. Any
// *pgxpool.Pool, *pgx.Conn, or pgx.Tx satisfies it. Tests can supply
// an in-memory fake.
type Executor interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

// Spec describes a partitioned parent table and the column that the
// partitioning range is declared against. Both fields are required.
type Spec struct {
	// Parent is the unqualified parent table name (e.g. "audit_log").
	// Manager quotes it with pgx.Identifier.Sanitize, so SQL
	// injection via Parent is not possible, but callers should still
	// treat Spec as trusted configuration.
	Parent string

	// Column is the name of the partition key column (e.g.
	// "occurred_at"). Manager uses it only in CREATE TABLE ... FOR
	// VALUES clauses, where the name matters only for documentation —
	// PostgreSQL does not re-check the column is the parent's
	// partition key at CREATE time because partition-of queries
	// resolve it from the parent. Still required for clarity.
	Column string
}

// Partition is a single child table belonging to a partitioned parent.
// From/To are inclusive/exclusive as in PostgreSQL's FOR VALUES syntax.
type Partition struct {
	// Name is the child table name (e.g. "audit_log_y2026m04").
	Name string
	// From is the inclusive lower bound of the partition's range.
	From time.Time
	// To is the exclusive upper bound of the partition's range.
	To time.Time
	// IsDefault is true for the `<parent>_default` overflow partition.
	// Such partitions have no From/To and are not dropped by DropBefore.
	IsDefault bool
}

// Manager creates and drops monthly partitions. It is safe for
// concurrent use: every method is stateless and delegates ordering
// to PostgreSQL.
type Manager struct {
	db Executor
}

// NewManager constructs a Manager backed by db. Panics if db is nil —
// this package is only useful with a real connection, and a nil
// Executor is always a programming error.
func NewManager(db Executor) *Manager {
	if db == nil {
		panic("partition: NewManager requires a non-nil Executor")
	}
	return &Manager{db: db}
}

// EnsureMonths makes sure partitions exist for every month in the
// window [now - past, now + future] (inclusive on both ends). It is
// idempotent: months that already have a partition are skipped
// silently via `CREATE TABLE IF NOT EXISTS`.
//
// Returns the names of partitions created during this call. Existing
// partitions are NOT included — this lets callers log rollover
// activity without being noisy on every run.
//
// EnsureMonths also creates the default overflow partition
// `<parent>_default` if it does not already exist.
func (m *Manager) EnsureMonths(ctx context.Context, s Spec, past, future int) ([]string, error) {
	if err := validateSpec(s); err != nil {
		return nil, err
	}
	if past < 0 || future < 0 {
		return nil, fmt.Errorf("partition: past and future must be >= 0, got past=%d future=%d", past, future)
	}

	// Snapshot the set of existing partition names before we start
	// issuing DDL. We diff against this at the end to decide which
	// partitions are genuinely new — PostgreSQL's CommandTag for
	// `CREATE TABLE IF NOT EXISTS` is `CREATE TABLE` whether or not
	// the table was actually created, so we cannot use the tag as a
	// signal. Any partition name that appears in the post-state but
	// not in this snapshot is counted as "created this call".
	existing, err := m.partitionNames(ctx, s.Parent)
	if err != nil {
		return nil, err
	}

	base := firstOfMonth(time.Now().UTC())
	created := make([]string, 0, past+future+1)

	for offset := -past; offset <= future; offset++ {
		from := base.AddDate(0, offset, 0)
		to := from.AddDate(0, 1, 0)
		name := partitionName(s.Parent, from)

		sql := fmt.Sprintf(
			"CREATE TABLE IF NOT EXISTS %s PARTITION OF %s FOR VALUES FROM (%s) TO (%s)",
			pgIdent(name),
			pgIdent(s.Parent),
			pgTimestamp(from),
			pgTimestamp(to),
		)
		if _, err := m.db.Exec(ctx, sql); err != nil {
			return created, fmt.Errorf("partition: create %s: %w", name, err)
		}
		if _, seen := existing[name]; !seen {
			created = append(created, name)
		}
	}

	// Default partition — catch anything outside the window so
	// mis-clocked inserts do not error out.
	defaultName := s.Parent + "_default"
	defaultSQL := fmt.Sprintf(
		"CREATE TABLE IF NOT EXISTS %s PARTITION OF %s DEFAULT",
		pgIdent(defaultName),
		pgIdent(s.Parent),
	)
	if _, err := m.db.Exec(ctx, defaultSQL); err != nil {
		return created, fmt.Errorf("partition: create default %s: %w", defaultName, err)
	}
	if _, seen := existing[defaultName]; !seen {
		created = append(created, defaultName)
	}
	return created, nil
}

// partitionNames loads the current set of child table names for
// parent from pg_tables. It is the pre-DDL snapshot EnsureMonths
// diffs against to report genuinely-created partitions.
func (m *Manager) partitionNames(ctx context.Context, parent string) (map[string]struct{}, error) {
	rows, err := m.db.Query(ctx,
		`SELECT tablename
		   FROM pg_tables
		  WHERE schemaname = current_schema()
		    AND tablename LIKE $1`,
		parent+"_%",
	)
	if err != nil {
		return nil, fmt.Errorf("partition: list %s: %w", parent, err)
	}
	defer rows.Close()

	out := make(map[string]struct{}, 16)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("partition: scan: %w", err)
		}
		out[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("partition: iterate: %w", err)
	}
	return out, nil
}

// Partitions returns the child partitions belonging to parent,
// ordered chronologically (default partition last). It parses the
// `<parent>_yYYYYmMM` naming convention rather than consulting
// `pg_inherits`, so any partition that does not follow the naming
// convention is ignored.
func (m *Manager) Partitions(ctx context.Context, parent string) ([]Partition, error) {
	if parent == "" {
		return nil, errors.New("partition: parent is required")
	}
	rows, err := m.db.Query(ctx,
		`SELECT tablename
		   FROM pg_tables
		  WHERE schemaname = current_schema()
		    AND tablename LIKE $1`,
		parent+"_%",
	)
	if err != nil {
		return nil, fmt.Errorf("partition: list %s: %w", parent, err)
	}
	defer rows.Close()

	out := make([]Partition, 0, 12)
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("partition: scan: %w", err)
		}
		if name == parent+"_default" {
			out = append(out, Partition{Name: name, IsDefault: true})
			continue
		}
		from, ok := parsePartitionName(parent, name)
		if !ok {
			continue
		}
		out = append(out, Partition{
			Name: name,
			From: from,
			To:   from.AddDate(0, 1, 0),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("partition: iterate: %w", err)
	}

	sort.Slice(out, func(i, j int) bool {
		// Default partition goes last.
		if out[i].IsDefault != out[j].IsDefault {
			return !out[i].IsDefault
		}
		return out[i].From.Before(out[j].From)
	})
	return out, nil
}

// DropBefore drops every non-default partition whose upper bound
// (`To`) is on or before cutoff. Returns the names dropped.
//
// The default overflow partition is never dropped: detaching it
// requires an explicit operator decision because it may still hold
// rows that do not fit any remaining partition.
//
// Callers that archive data elsewhere (S3, cold storage) should do so
// BEFORE calling DropBefore — this method is a destructive operation.
func (m *Manager) DropBefore(ctx context.Context, s Spec, cutoff time.Time) ([]string, error) {
	if err := validateSpec(s); err != nil {
		return nil, err
	}
	parts, err := m.Partitions(ctx, s.Parent)
	if err != nil {
		return nil, err
	}
	dropped := make([]string, 0, len(parts))
	for _, p := range parts {
		if p.IsDefault {
			continue
		}
		if !p.To.After(cutoff) {
			sql := fmt.Sprintf("DROP TABLE IF EXISTS %s", pgIdent(p.Name))
			if _, err := m.db.Exec(ctx, sql); err != nil {
				return dropped, fmt.Errorf("partition: drop %s: %w", p.Name, err)
			}
			dropped = append(dropped, p.Name)
		}
	}
	return dropped, nil
}

// --- helpers ---

func validateSpec(s Spec) error {
	if s.Parent == "" {
		return errors.New("partition: Parent is required")
	}
	if s.Column == "" {
		return errors.New("partition: Column is required")
	}
	return nil
}

func firstOfMonth(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), 1, 0, 0, 0, 0, time.UTC)
}

func partitionName(parent string, t time.Time) string {
	return fmt.Sprintf("%s_y%04dm%02d", parent, t.Year(), int(t.Month()))
}

// parsePartitionName extracts the "from" timestamp encoded in a child
// partition name. Returns false if the name does not follow the
// `<parent>_yYYYYmMM` convention.
func parsePartitionName(parent, name string) (time.Time, bool) {
	prefix := parent + "_y"
	if !strings.HasPrefix(name, prefix) {
		return time.Time{}, false
	}
	rest := name[len(prefix):]
	if len(rest) != 7 || rest[4] != 'm' {
		return time.Time{}, false
	}
	var year, month int
	if _, err := fmt.Sscanf(rest, "%04dm%02d", &year, &month); err != nil {
		return time.Time{}, false
	}
	if month < 1 || month > 12 {
		return time.Time{}, false
	}
	return time.Date(year, time.Month(month), 1, 0, 0, 0, 0, time.UTC), true
}

// pgIdent quotes an identifier for use in dynamic SQL. It never
// embeds user input (inputs come from Spec.Parent and generated
// month names), but we still route it through pgx.Identifier.Sanitize
// to catch configuration typos.
func pgIdent(name string) string {
	return pgx.Identifier{name}.Sanitize()
}

// pgTimestamp renders a timestamptz literal suitable for inclusion
// in FOR VALUES clauses. We format as ISO-8601 UTC and wrap in single
// quotes — PostgreSQL parses this unambiguously.
func pgTimestamp(t time.Time) string {
	return "'" + t.UTC().Format("2006-01-02 15:04:05") + "+00'"
}
