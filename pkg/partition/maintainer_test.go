package partition

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog"
)

func TestNewMaintainer_NilManagerPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("NewMaintainer(nil, ...) did not panic")
		}
	}()
	NewMaintainer(nil, zerolog.Nop(),
		MaintenancePlan{Spec: Spec{Parent: "t", Column: "c"}})
}

func TestNewMaintainer_EmptyPlansPanics(t *testing.T) {
	t.Parallel()
	mgr := NewManager(&fakeExec{})
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("NewMaintainer(mgr, logger) did not panic without plans")
		}
	}()
	NewMaintainer(mgr, zerolog.Nop())
}

func TestMaintainer_Run_CreatesAndDrops(t *testing.T) {
	t.Parallel()
	fe := &fakeExec{rows: [][]string{
		{"audit_log_y2020m01"},
		{"audit_log_y2020m02"},
	}}
	mgr := NewManager(fe)
	m := NewMaintainer(mgr, zerolog.Nop(), MaintenancePlan{
		Spec:      Spec{Parent: "audit_log", Column: "occurred_at"},
		Past:      1,
		Future:    1,
		DropAfter: 30 * 24 * time.Hour,
	})

	if err := m.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	ensureCount := 0
	dropCount := 0
	for _, stmt := range fe.execs {
		switch {
		case strings.HasPrefix(stmt, "CREATE"):
			ensureCount++
		case strings.HasPrefix(stmt, "DROP"):
			dropCount++
		}
	}
	if ensureCount != 4 {
		t.Errorf("CREATE calls = %d, want 4 (3 months + 1 default)", ensureCount)
	}
	if dropCount != 2 {
		t.Errorf("DROP calls = %d, want 2", dropCount)
	}
}

// onceFailExec fails on first Exec, then falls through to fakeExec.
type onceFailExec struct {
	inner  *fakeExec
	failed bool
}

func (o *onceFailExec) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	if !o.failed {
		o.failed = true
		o.inner.execs = append(o.inner.execs, sql)
		return pgconn.CommandTag{}, errors.New("boom")
	}
	return o.inner.Exec(ctx, sql, args...)
}

func (o *onceFailExec) Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error) {
	return o.inner.Query(ctx, sql, args...)
}

func TestMaintainer_Run_ContinuesAfterPlanFailure(t *testing.T) {
	t.Parallel()
	fe := &fakeExec{}
	ofe := &onceFailExec{inner: fe}
	mgr := NewManager(ofe)
	m := NewMaintainer(mgr, zerolog.Nop(),
		MaintenancePlan{Spec: Spec{Parent: "audit_log", Column: "occurred_at"}, Past: 0, Future: 0},
		MaintenancePlan{Spec: Spec{Parent: "jobs", Column: "created_at"}, Past: 0, Future: 0},
	)

	err := m.Run(context.Background())
	if err == nil {
		t.Fatalf("expected error from first plan")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error should wrap underlying: %v", err)
	}
	// Second plan still executed (current month + default).
	seenJobs := false
	for _, stmt := range fe.execs {
		if strings.Contains(stmt, `"jobs"`) {
			seenJobs = true
		}
	}
	if !seenJobs {
		t.Errorf("second plan did not run after first plan failure: %v", fe.execs)
	}
}

func TestMaintenanceHandler_IgnoresPayload(t *testing.T) {
	t.Parallel()
	fe := &fakeExec{}
	mgr := NewManager(fe)
	m := NewMaintainer(mgr, zerolog.Nop(), MaintenancePlan{
		Spec:   Spec{Parent: "audit_log", Column: "occurred_at"},
		Past:   0,
		Future: 0,
	})

	h := m.MaintenanceHandler()
	if err := h(context.Background(), []byte(`{"anything":"ignored"}`)); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(fe.execs) == 0 {
		t.Fatalf("handler did not invoke Manager")
	}
}
