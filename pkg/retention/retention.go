// Package retention archives aged-out rows from monthly-partitioned
// tables to object storage and then drops the empty partitions.
//
// It is the companion to pkg/partition: partitions keep the working
// set small; retention keeps the archive intact while freeing the
// database of data that is never read online.
//
// # Lifecycle
//
// For each Plan, Runner.Run does, in order:
//
//  1. Enumerate the plan's partitions via pkg/partition.Manager.
//  2. Pick partitions whose upper bound is older than Retain.
//  3. For each candidate, stream its rows through the Archiver to the
//     configured Storage, under a deterministic key.
//  4. Only after a successful archive, DROP the partition.
//
// The order matters: archive first, drop second. A crash between
// steps 3 and 4 leaves a duplicate archive on the next run (the
// storage key is deterministic, so the write is idempotent) but the
// database partition is still there. A crash between 2 and 3 leaves
// nothing on disk and no data loss. At no point is a partition
// dropped before its data lands in durable storage.
//
// # Archive format
//
// The default PgxArchiver writes one blob per partition using
// PostgreSQL's `COPY ... TO STDOUT (FORMAT csv, HEADER true)`. CSV
// is portable, gzipable, and replayable with `COPY ... FROM STDIN`.
// Applications that prefer JSONL, Parquet, or a custom layout can
// implement the Archiver interface directly.
//
// # Wiring
//
// Runner.Handler() adapts the runner to pkg/jobs so operators can
// schedule it as a daily job. Run it immediately after the
// partition maintainer's rollover so each day's window of work is
// bounded.
package retention

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/dedeez14/goforge/pkg/partition"
	"github.com/dedeez14/goforge/pkg/storage"
)

// Plan describes one partitioned table's retention policy.
type Plan struct {
	// Spec is the partitioned table being maintained. Parent must
	// match the parent used by pkg/partition.
	Spec partition.Spec

	// Retain is the minimum age before a partition becomes eligible
	// for archival. A partition whose upper bound (`To`) is on or
	// before `now - Retain` is archived and then dropped.
	//
	// Retain <= 0 is rejected at construction time — a zero
	// retention would archive the currently-live partition, which
	// is never what the operator wants.
	Retain time.Duration

	// StoragePrefix is the key prefix under which archives are
	// written. The full key is `<StoragePrefix>/<partition>.csv.gz`.
	// A trailing slash in StoragePrefix is tolerated.
	StoragePrefix string
}

// Archiver streams the rows of a single partition into an io.Writer.
// The returned count is the number of rows written; it is advisory
// (used for logging) and MAY be -1 if the backend cannot report it.
//
// Implementations MUST be safe to retry with the same partition name
// — Runner writes the resulting blob to a deterministic storage key,
// so a partial/failed first archive is overwritten cleanly on retry.
type Archiver interface {
	// ArchivePartition reads every row in the named child partition
	// table and writes a serialised representation to dst. The
	// context is honoured for cancellation and timeouts.
	ArchivePartition(ctx context.Context, partition string, dst Sink) (rows int64, err error)
}

// Sink is what an Archiver writes to — a Storage-like contract narrow
// enough to be trivially faked in tests. The default implementation
// wraps a storage.Storage behind a key that Runner generates.
type Sink interface {
	// Key returns the object key this Sink represents. Archivers
	// use it only for logging/metadata; the writer is the primary
	// contract.
	Key() string

	// WriteAll commits the archive contents in one shot. Sinks are
	// intentionally not io.Writer: CSV/gzip streams need a Close()
	// signal to flush trailers, and WriteAll encapsulates that as
	// a single call-site.
	WriteAll(ctx context.Context, body []byte, contentType string) error
}

// PartitionOps is the narrow subset of *pkg/partition.Manager that
// Runner depends on. Using an interface lets tests drive Runner
// without a live database; pkg/partition.Manager satisfies it.
// Applications normally pass a *pkg/partition.Manager directly.
type PartitionOps interface {
	Partitions(ctx context.Context, parent string) ([]partition.Partition, error)
	DropBefore(ctx context.Context, s partition.Spec, cutoff time.Time) ([]string, error)
}

// Runner executes Plans. It is safe for concurrent use once
// constructed.
type Runner struct {
	mgr      PartitionOps
	archiver Archiver
	store    storage.Storage
	logger   zerolog.Logger
	plans    []Plan
	now      func() time.Time
}

// Options configures a Runner.
type Options struct {
	// Plans is the list of retention policies. At least one is
	// required; duplicate Parent names within the list are
	// rejected.
	Plans []Plan

	// Now overrides time.Now for tests. Leave nil in production.
	Now func() time.Time
}

// NewRunner constructs a Runner. Panics on programmer errors
// (nil manager/archiver/storage, empty/duplicate/invalid plans)
// because misconfiguration here is never recoverable at request
// time.
func NewRunner(mgr PartitionOps, archiver Archiver, store storage.Storage, logger zerolog.Logger, opts Options) *Runner {
	if mgr == nil {
		panic("retention: NewRunner requires non-nil Manager")
	}
	if archiver == nil {
		panic("retention: NewRunner requires non-nil Archiver")
	}
	if store == nil {
		panic("retention: NewRunner requires non-nil Storage")
	}
	if len(opts.Plans) == 0 {
		panic("retention: NewRunner requires at least one Plan")
	}

	seen := make(map[string]struct{}, len(opts.Plans))
	for i, p := range opts.Plans {
		if p.Spec.Parent == "" {
			panic(fmt.Sprintf("retention: plans[%d].Spec.Parent is empty", i))
		}
		if p.Spec.Column == "" {
			panic(fmt.Sprintf("retention: plans[%d].Spec.Column is empty", i))
		}
		if p.Retain <= 0 {
			panic(fmt.Sprintf("retention: plans[%d].Retain must be > 0 (got %s)", i, p.Retain))
		}
		if _, ok := seen[p.Spec.Parent]; ok {
			panic(fmt.Sprintf("retention: duplicate plan for parent %q", p.Spec.Parent))
		}
		seen[p.Spec.Parent] = struct{}{}
	}

	now := opts.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}

	return &Runner{
		mgr:      mgr,
		archiver: archiver,
		store:    store,
		logger:   logger,
		plans:    opts.Plans,
		now:      now,
	}
}

// Report summarises the work a single Run performed.
type Report struct {
	Archived []string
	Dropped  []string
	// Errors collects per-plan failures. Run keeps going after an
	// individual plan error so one bad table does not stall the rest.
	Errors []error
}

// Run iterates every Plan in order, archiving eligible partitions
// and then dropping them. A per-plan error is recorded in the
// report but does not abort later plans.
func (r *Runner) Run(ctx context.Context) Report {
	var rep Report
	for _, p := range r.plans {
		if err := r.runPlan(ctx, p, &rep); err != nil {
			rep.Errors = append(rep.Errors, err)
			r.logger.Error().Err(err).Str("parent", p.Spec.Parent).
				Msg("retention: plan failed")
		}
	}
	return rep
}

// Err returns the first error across all plans, or nil if every plan
// succeeded. Used to give the pkg/jobs runner a non-nil error so the
// job is retried.
func (rep Report) Err() error {
	if len(rep.Errors) == 0 {
		return nil
	}
	return rep.Errors[0]
}

// Handler adapts a Runner to the pkg/jobs Handler signature.
// Payload is ignored — the plans are fixed at construction time.
func (r *Runner) Handler() func(ctx context.Context, _ []byte) error {
	return func(ctx context.Context, _ []byte) error {
		return r.Run(ctx).Err()
	}
}

func (r *Runner) runPlan(ctx context.Context, p Plan, rep *Report) error {
	parts, err := r.mgr.Partitions(ctx, p.Spec.Parent)
	if err != nil {
		return fmt.Errorf("retention: list %s: %w", p.Spec.Parent, err)
	}
	cutoff := r.now().Add(-p.Retain)

	// Two-phase: archive every eligible partition first, then drop
	// them all in a single DropBefore call. The phases are separated
	// so an archival crash never leaves a partition dropped without
	// its data in storage, and so the drop step is O(1) DDL instead
	// of one DROP per partition.
	for _, part := range parts {
		if part.IsDefault {
			// The default partition is deliberately never archived
			// or dropped: its contents are out-of-band historical
			// imports that the operator must handle explicitly.
			continue
		}
		if part.To.After(cutoff) {
			continue
		}

		key := objectKey(p.StoragePrefix, part.Name)
		sink := &storageSink{store: r.store, key: key}

		rows, err := r.archiver.ArchivePartition(ctx, part.Name, sink)
		if err != nil {
			return fmt.Errorf("retention: archive %s: %w", part.Name, err)
		}
		r.logger.Info().
			Str("parent", p.Spec.Parent).
			Str("partition", part.Name).
			Str("key", key).
			Int64("rows", rows).
			Msg("retention: archived partition")
		rep.Archived = append(rep.Archived, part.Name)
	}

	dropped, err := r.mgr.DropBefore(ctx, p.Spec, cutoff)
	if err != nil {
		// Archives are already in storage; surface the drop error
		// so the job is retried on the next tick. On retry the
		// already-archived partitions are re-archived under their
		// deterministic keys (idempotent overwrite) before the
		// drop is re-attempted.
		return fmt.Errorf("retention: drop before %s for %s: %w",
			cutoff.Format(time.RFC3339), p.Spec.Parent, err)
	}
	rep.Dropped = append(rep.Dropped, dropped...)
	return nil
}

// objectKey deterministically joins a prefix + partition name. A
// trailing slash in prefix is tolerated; an empty prefix produces
// a bare key.
func objectKey(prefix, partition string) string {
	suffix := partition + ".csv.gz"
	if prefix == "" {
		return suffix
	}
	if prefix[len(prefix)-1] == '/' {
		return prefix + suffix
	}
	return prefix + "/" + suffix
}

// --- Sink implementation ---

type storageSink struct {
	store storage.Storage
	key   string
}

func (s *storageSink) Key() string { return s.key }

func (s *storageSink) WriteAll(ctx context.Context, body []byte, contentType string) error {
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	if err := s.store.Put(ctx, s.key, bytes.NewReader(body), int64(len(body)), contentType); err != nil {
		return fmt.Errorf("retention: sink write %q: %w", s.key, err)
	}
	return nil
}
