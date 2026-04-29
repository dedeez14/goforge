package partition

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog"
)

// MaintenancePlan describes the rollover policy for a single
// partitioned table. A Maintainer executes one plan per tick.
type MaintenancePlan struct {
	// Spec is the partitioned table being maintained.
	Spec Spec

	// Past is the number of historical months to keep pre-created.
	// Typical value: 1 — gives backfills and clock-drift a buffer.
	Past int

	// Future is the number of months to keep pre-created ahead of
	// now. Typical value: 3 — plenty of runway even if the
	// maintenance cron is paused for a week.
	Future int

	// DropAfter, when non-zero, drops partitions whose upper bound
	// is more than DropAfter older than now. Use with care: data in
	// those partitions is gone. Operators that need cold-storage
	// archival should archive via a sibling job BEFORE the drop.
	//
	// Zero value = never drop (the default).
	DropAfter time.Duration
}

// Maintainer applies one or more MaintenancePlans on every tick.
// Construct via NewMaintainer and either call Run directly or wire
// MaintenanceHandler into pkg/jobs as a scheduled job.
type Maintainer struct {
	mgr    *Manager
	plans  []MaintenancePlan
	logger zerolog.Logger
}

// NewMaintainer wires a Manager and a set of plans together. Panics
// if no plans are supplied — an empty Maintainer is never what the
// caller intended.
func NewMaintainer(mgr *Manager, logger zerolog.Logger, plans ...MaintenancePlan) *Maintainer {
	if mgr == nil {
		panic("partition: NewMaintainer requires non-nil Manager")
	}
	if len(plans) == 0 {
		panic("partition: NewMaintainer requires at least one MaintenancePlan")
	}
	return &Maintainer{mgr: mgr, plans: plans, logger: logger}
}

// Run executes every plan in order. It returns the first error
// encountered, but only after attempting every plan: partial
// rollover is better than none when a single table is wedged.
func (m *Maintainer) Run(ctx context.Context) error {
	var firstErr error
	for _, p := range m.plans {
		if err := m.runPlan(ctx, p); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m *Maintainer) runPlan(ctx context.Context, p MaintenancePlan) error {
	created, err := m.mgr.EnsureMonths(ctx, p.Spec, p.Past, p.Future)
	if err != nil {
		m.logger.Error().Err(err).Str("parent", p.Spec.Parent).
			Msg("partition: ensure months failed")
		return fmt.Errorf("partition: ensure %s: %w", p.Spec.Parent, err)
	}
	if len(created) > 0 {
		m.logger.Info().Str("parent", p.Spec.Parent).Strs("created", created).
			Msg("partition: created")
	}

	if p.DropAfter > 0 {
		cutoff := time.Now().UTC().Add(-p.DropAfter)
		dropped, err := m.mgr.DropBefore(ctx, p.Spec, cutoff)
		if err != nil {
			m.logger.Error().Err(err).Str("parent", p.Spec.Parent).
				Msg("partition: drop before failed")
			return fmt.Errorf("partition: drop %s: %w", p.Spec.Parent, err)
		}
		if len(dropped) > 0 {
			m.logger.Warn().Str("parent", p.Spec.Parent).Strs("dropped", dropped).
				Msg("partition: dropped (retention)")
		}
	}
	return nil
}

// MaintenanceHandler adapts a Maintainer to the pkg/jobs Handler
// signature. Register as a schedule (e.g. interval=24h) so partitions
// are rolled forward daily without human intervention.
//
// The payload is ignored; the schedule's cadence defines the cadence.
func (m *Maintainer) MaintenanceHandler() func(ctx context.Context, payload json.RawMessage) error {
	return func(ctx context.Context, _ json.RawMessage) error {
		return m.Run(ctx)
	}
}
