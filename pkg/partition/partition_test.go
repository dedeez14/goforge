package partition

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// fakeExec is a minimal Executor substitute for unit tests. It
// records every Exec'd statement, serves pre-programmed rows for
// Query, and mimics real PostgreSQL's DDL behaviour — in particular,
// CREATE TABLE IF NOT EXISTS always returns the CommandTag
// "CREATE TABLE" regardless of whether the table existed already.
// Detection of "did I create it?" is the caller's responsibility,
// and we deliberately DO NOT give tests a way to fake the old
// behaviour, because the real driver cannot do it either.
type fakeExec struct {
	execs []string

	// initialRows is returned from the first Query call (the
	// pre-DDL partition-name snapshot EnsureMonths takes). After
	// the first call, any subsequent Query gets `rows` (the user's
	// explicit programming for Partitions / DropBefore tests).
	// If initialRows is nil, `rows` is served on every call.
	initialRows [][]string
	rows        [][]string
	queryCalls  int

	// execErr, if non-nil, is returned from every Exec call.
	execErr error
}

func (f *fakeExec) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	f.execs = append(f.execs, sql)
	if f.execErr != nil {
		return pgconn.CommandTag{}, f.execErr
	}
	return pgconn.NewCommandTag("CREATE TABLE"), nil
}

func (f *fakeExec) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
	f.queryCalls++
	if f.initialRows != nil && f.queryCalls == 1 {
		return &fakeRows{data: f.initialRows}, nil
	}
	return &fakeRows{data: f.rows}, nil
}

type fakeRows struct {
	data [][]string
	i    int
}

func (r *fakeRows) Close()                                       {}
func (r *fakeRows) Err() error                                   { return nil }
func (r *fakeRows) CommandTag() pgconn.CommandTag                { return pgconn.CommandTag{} }
func (r *fakeRows) FieldDescriptions() []pgconn.FieldDescription { return nil }
func (r *fakeRows) Conn() *pgx.Conn                              { return nil }
func (r *fakeRows) RawValues() [][]byte                          { return nil }
func (r *fakeRows) Values() ([]any, error)                       { return nil, nil }
func (r *fakeRows) Next() bool                                   { return r.i < len(r.data) }
func (r *fakeRows) Scan(dest ...any) error {
	row := r.data[r.i]
	r.i++
	for i, d := range dest {
		*(d.(*string)) = row[i]
	}
	return nil
}

func TestNewManager_NilPanics(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("NewManager(nil) did not panic")
		}
	}()
	NewManager(nil)
}

func TestEnsureMonths_ValidatesSpec(t *testing.T) {
	t.Parallel()
	m := NewManager(&fakeExec{})
	ctx := context.Background()

	if _, err := m.EnsureMonths(ctx, Spec{}, 0, 0); err == nil {
		t.Fatalf("expected error for empty Parent")
	}
	if _, err := m.EnsureMonths(ctx, Spec{Parent: "t"}, 0, 0); err == nil {
		t.Fatalf("expected error for empty Column")
	}
	if _, err := m.EnsureMonths(ctx, Spec{Parent: "t", Column: "c"}, -1, 0); err == nil {
		t.Fatalf("expected error for negative past")
	}
	if _, err := m.EnsureMonths(ctx, Spec{Parent: "t", Column: "c"}, 0, -1); err == nil {
		t.Fatalf("expected error for negative future")
	}
}

func TestEnsureMonths_EmitsCorrectDDL(t *testing.T) {
	t.Parallel()
	// Empty snapshot — every partition in the window is newly created.
	fe := &fakeExec{}
	m := NewManager(fe)

	created, err := m.EnsureMonths(context.Background(),
		Spec{Parent: "audit_log", Column: "occurred_at"}, 1, 2)
	if err != nil {
		t.Fatalf("EnsureMonths: %v", err)
	}

	// 1 past + 1 current + 2 future = 4 range partitions, + 1 default.
	if got, want := len(fe.execs), 5; got != want {
		t.Fatalf("Exec call count = %d, want %d", got, want)
	}
	if got, want := len(created), 5; got != want {
		t.Fatalf("created count = %d, want %d", got, want)
	}

	re := regexp.MustCompile(`CREATE TABLE IF NOT EXISTS "audit_log_y\d{4}m\d{2}" PARTITION OF "audit_log" FOR VALUES FROM \('\d{4}-\d{2}-\d{2} 00:00:00\+00'\) TO \('\d{4}-\d{2}-\d{2} 00:00:00\+00'\)`)
	for i, stmt := range fe.execs[:4] {
		if !re.MatchString(stmt) {
			t.Errorf("Exec[%d] does not match expected DDL: %s", i, stmt)
		}
	}
	if !strings.Contains(fe.execs[4], `PARTITION OF "audit_log" DEFAULT`) {
		t.Errorf("final Exec not default-partition DDL: %s", fe.execs[4])
	}
}

func TestEnsureMonths_Idempotent_ViaPreDDLSnapshot(t *testing.T) {
	t.Parallel()
	// Real PostgreSQL returns CommandTag "CREATE TABLE" for
	// CREATE TABLE IF NOT EXISTS regardless of whether the table
	// existed. The Manager must therefore detect novelty via the
	// pre-DDL pg_tables snapshot, not the CommandTag. This test
	// pins that behaviour: first run sees no existing partitions,
	// second run sees them, so the `created` slice is empty on
	// the second call even though the Exec tags are identical.
	now := time.Now().UTC()
	current := partitionName("audit_log", firstOfMonth(now))
	defaultName := "audit_log_default"

	// First run: pre-DDL snapshot is empty — no partitions exist yet.
	fe1 := &fakeExec{initialRows: nil, rows: nil}
	m1 := NewManager(fe1)
	created1, err := m1.EnsureMonths(context.Background(),
		Spec{Parent: "audit_log", Column: "occurred_at"}, 0, 0)
	if err != nil {
		t.Fatalf("first EnsureMonths: %v", err)
	}
	if got, want := len(created1), 2; got != want {
		t.Fatalf("first run created %d names, want %d: %v", got, want, created1)
	}

	// Second run: pre-DDL snapshot already contains both names —
	// Exec still returns "CREATE TABLE" (as real PG does) but the
	// diff sees zero new names.
	fe2 := &fakeExec{initialRows: [][]string{
		{current},
		{defaultName},
	}}
	m2 := NewManager(fe2)
	created2, err := m2.EnsureMonths(context.Background(),
		Spec{Parent: "audit_log", Column: "occurred_at"}, 0, 0)
	if err != nil {
		t.Fatalf("second EnsureMonths: %v", err)
	}
	if got, want := len(created2), 0; got != want {
		t.Fatalf("second run created %d names, want %d (idempotent): %v", got, want, created2)
	}
}

func TestEnsureMonths_MixedPartialReturnsOnlyMissing(t *testing.T) {
	t.Parallel()
	// Regression: if the snapshot already has SOME of the target
	// partitions but not all, EnsureMonths must report only the
	// genuinely-missing ones in `created`. This mirrors the real
	// operational pattern where the maintainer fills in months as
	// they age into the Past window.
	now := time.Now().UTC()
	base := firstOfMonth(now)
	current := partitionName("audit_log", base)
	next := partitionName("audit_log", base.AddDate(0, 1, 0))

	// Current month already exists; next month + default don't.
	fe := &fakeExec{initialRows: [][]string{{current}}}
	m := NewManager(fe)

	created, err := m.EnsureMonths(context.Background(),
		Spec{Parent: "audit_log", Column: "occurred_at"}, 0, 1)
	if err != nil {
		t.Fatalf("EnsureMonths: %v", err)
	}
	if got, want := len(created), 2; got != want {
		t.Fatalf("created %d, want %d: %v", got, want, created)
	}
	seen := map[string]bool{}
	for _, n := range created {
		seen[n] = true
	}
	if !seen[next] {
		t.Errorf("expected %q in created; got %v", next, created)
	}
	if !seen["audit_log_default"] {
		t.Errorf("expected audit_log_default in created; got %v", created)
	}
	if seen[current] {
		t.Errorf("did NOT expect %q in created (already existed); got %v", current, created)
	}
}

func TestEnsureMonths_ExecError(t *testing.T) {
	t.Parallel()
	fe := &fakeExec{execErr: errors.New("boom")}
	m := NewManager(fe)

	_, err := m.EnsureMonths(context.Background(),
		Spec{Parent: "audit_log", Column: "occurred_at"}, 0, 0)
	if err == nil {
		t.Fatalf("expected error propagating exec failure")
	}
	if !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error does not wrap underlying: %v", err)
	}
}

func TestPartitions_ParsesNaming(t *testing.T) {
	t.Parallel()
	fe := &fakeExec{rows: [][]string{
		{"audit_log_y2026m04"},
		{"audit_log_y2026m03"},
		{"audit_log_default"},
		{"audit_log_malformed"}, // ignored silently
	}}
	m := NewManager(fe)

	parts, err := m.Partitions(context.Background(), "audit_log")
	if err != nil {
		t.Fatalf("Partitions: %v", err)
	}
	if got, want := len(parts), 3; got != want {
		t.Fatalf("Partitions returned %d, want %d", got, want)
	}
	// Chronological: 2026-03, 2026-04, default.
	if parts[0].Name != "audit_log_y2026m03" {
		t.Errorf("parts[0] = %s, want audit_log_y2026m03", parts[0].Name)
	}
	if parts[1].Name != "audit_log_y2026m04" {
		t.Errorf("parts[1] = %s, want audit_log_y2026m04", parts[1].Name)
	}
	if !parts[2].IsDefault {
		t.Errorf("parts[2] should be default partition")
	}
	want := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if !parts[0].From.Equal(want) {
		t.Errorf("parts[0].From = %v, want %v", parts[0].From, want)
	}
	if !parts[0].To.Equal(want.AddDate(0, 1, 0)) {
		t.Errorf("parts[0].To = %v, want one month after From", parts[0].To)
	}
}

func TestDropBefore_OnlyDropsRangePartitions(t *testing.T) {
	t.Parallel()
	fe := &fakeExec{rows: [][]string{
		{"audit_log_y2026m01"},
		{"audit_log_y2026m02"},
		{"audit_log_y2026m03"},
		{"audit_log_default"},
	}}
	m := NewManager(fe)

	// Cutoff = March 1 — drops anything whose upper bound is <= March 1.
	// y2026m01 ends Feb 1 → drop. y2026m02 ends Mar 1 → drop.
	// y2026m03 ends Apr 1 > cutoff → keep.
	cutoff := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	dropped, err := m.DropBefore(context.Background(),
		Spec{Parent: "audit_log", Column: "occurred_at"}, cutoff)
	if err != nil {
		t.Fatalf("DropBefore: %v", err)
	}
	if got, want := len(dropped), 2; got != want {
		t.Fatalf("dropped %d, want %d: %v", got, want, dropped)
	}
	for _, name := range dropped {
		if strings.Contains(name, "default") {
			t.Errorf("default partition should never be dropped: %s", name)
		}
	}
	// Verify the DROP statements were issued.
	drops := 0
	for _, stmt := range fe.execs {
		if strings.HasPrefix(stmt, "DROP TABLE IF EXISTS ") {
			drops++
		}
	}
	if drops != 2 {
		t.Errorf("DROP count = %d, want 2", drops)
	}
}

func TestDropBefore_EmptyWhenNothingOldEnough(t *testing.T) {
	t.Parallel()
	fe := &fakeExec{rows: [][]string{
		{"audit_log_y2030m12"},
		{"audit_log_default"},
	}}
	m := NewManager(fe)

	cutoff := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	dropped, err := m.DropBefore(context.Background(),
		Spec{Parent: "audit_log", Column: "occurred_at"}, cutoff)
	if err != nil {
		t.Fatalf("DropBefore: %v", err)
	}
	if len(dropped) != 0 {
		t.Errorf("dropped %v, want none", dropped)
	}
}

func TestParsePartitionName_Table(t *testing.T) {
	t.Parallel()
	cases := []struct {
		parent string
		name   string
		want   time.Time
		ok     bool
	}{
		{"audit_log", "audit_log_y2026m04", time.Date(2026, 4, 1, 0, 0, 0, 0, time.UTC), true},
		{"audit_log", "audit_log_y2026m12", time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC), true},
		{"audit_log", "audit_log_default", time.Time{}, false},
		{"audit_log", "audit_log_y2026m13", time.Time{}, false}, // invalid month
		{"audit_log", "audit_log_y0000m00", time.Time{}, false}, // invalid month
		{"audit_log", "something_else", time.Time{}, false},
		{"jobs", "audit_log_y2026m04", time.Time{}, false},
	}
	for _, c := range cases {
		got, ok := parsePartitionName(c.parent, c.name)
		if ok != c.ok {
			t.Errorf("parsePartitionName(%q,%q) ok=%v, want %v", c.parent, c.name, ok, c.ok)
			continue
		}
		if ok && !got.Equal(c.want) {
			t.Errorf("parsePartitionName(%q,%q) = %v, want %v", c.parent, c.name, got, c.want)
		}
	}
}

func TestPartitionName_Format(t *testing.T) {
	t.Parallel()
	got := partitionName("audit_log", time.Date(2026, 4, 15, 12, 0, 0, 0, time.UTC))
	if want := "audit_log_y2026m04"; got != want {
		t.Fatalf("partitionName = %q, want %q", got, want)
	}
}
