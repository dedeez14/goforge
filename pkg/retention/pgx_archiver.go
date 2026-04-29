package retention

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgxArchiver streams a partition's rows into gzip-compressed CSV
// via PostgreSQL's `COPY ... TO STDOUT (FORMAT csv, HEADER true)`
// protocol. It acquires a pooled connection per partition — the
// copy stream must run on a single connection, which pgxpool.Pool
// does not expose otherwise.
//
// CSV was chosen over JSONL because:
//   - It is replayable with `COPY ... FROM STDIN` if operators need
//     to pull archives back into a staging table for analytics.
//   - It is denser on disk than JSONL for row-shaped data.
//   - It avoids adding a Parquet dependency to the framework.
//
// Archives are gzip-compressed before upload; empirical audit_log
// rows compress ~5x at default level, which is worth the CPU on
// a daily-or-slower schedule.
type PgxArchiver struct {
	Pool *pgxpool.Pool
}

// NewPgxArchiver constructs a PgxArchiver. Panics if pool is nil —
// the archiver is useless without a backing connection.
func NewPgxArchiver(pool *pgxpool.Pool) *PgxArchiver {
	if pool == nil {
		panic("retention: NewPgxArchiver requires non-nil pgxpool.Pool")
	}
	return &PgxArchiver{Pool: pool}
}

// ArchivePartition implements Archiver. It runs `COPY (SELECT *
// FROM partition) TO STDOUT (FORMAT csv, HEADER true)` and streams
// the result through gzip into the sink.
//
// The partition name is embedded in a format string but the only
// caller in this package is Runner, which passes a name parsed from
// pg_tables via pkg/partition. External callers that construct
// PgxArchiver directly should ensure the name is a trusted identifier.
func (a *PgxArchiver) ArchivePartition(ctx context.Context, partitionName string, sink Sink) (int64, error) {
	if partitionName == "" {
		return 0, fmt.Errorf("retention: partition name is required")
	}
	conn, err := a.Pool.Acquire(ctx)
	if err != nil {
		return 0, fmt.Errorf("retention: acquire: %w", err)
	}
	defer conn.Release()

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)

	quoted := pgx.Identifier{partitionName}.Sanitize()
	sql := fmt.Sprintf(
		"COPY (SELECT * FROM %s) TO STDOUT WITH (FORMAT csv, HEADER true)",
		quoted,
	)
	tag, err := conn.Conn().PgConn().CopyTo(ctx, gz, sql)
	if err != nil {
		return 0, fmt.Errorf("retention: copy %s: %w", partitionName, err)
	}
	if err := gz.Close(); err != nil {
		return 0, fmt.Errorf("retention: gzip close: %w", err)
	}

	if err := sink.WriteAll(ctx, buf.Bytes(), "application/gzip"); err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
