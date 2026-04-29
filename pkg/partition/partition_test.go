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
// records every Exec'd statement and serves pre-programmed rows for
// Query. We deliberately do NOT parse SQL — we assert on substrings
// so the tests remain readable while exercising real formatting.
type fakeExec struct {
	execs []string
	rows  [][]string
	// execErr, if non-nil, is returned from every Exec call.
	execErr error
	// createdMap maps statement->tag. Default "CREATE TABLE" so
	// EnsureMonths sees every call as a fresh CREATE.
	createdMap map[string]string
}

func (f *fakeExec) Exec(_ context.Context, sql string, _ ...any) (pgconn.CommandTag, error) {
	f.execs = append(f.execs, sql)
	if f.execErr != nil {
		return pgconn.CommandTag{}, f.execErr
	}
	if tag, ok := f.createdMap[sql]; ok {
		return pgconn.NewCommandTag(tag), nil
	}
	return pgconn.NewCommandTag("CREATE TABLE"), nil
}

func (f *fakeExec) Query(_ context.Context, _ string, _ ...any) (pgx.Rows, error) {
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

func TestEnsureMonths_Idempotent(t *testing.T) {
	t.Parallel()
	// Simulate "already exists" by returning CREATE TABLE tag for
	// first run, "0" CommandTag for second run.
	fe := &fakeExec{createdMap: map[string]string{}}
	m := NewManager(fe)

	created1, err := m.EnsureMonths(context.Background(),
		Spec{Parent: "audit_log", Column: "occurred_at"}, 0, 0)
	if err != nil {
		t.Fatalf("first EnsureMonths: %v", err)
	}
	if len(created1) != 2 { // current month + default
		t.Fatalf("first run created %d, want 2", len(created1))
	}

	// Second run: tell fakeExec to report "0" for every SQL.
	for _, s := range fe.execs {
		fe.createdMap[s] = ""
	}
	created2, err := m.EnsureMonths(context.Background(),
		Spec{Parent: "audit_log", Column: "occurred_at"}, 0, 0)
	if err != nil {
		t.Fatalf("second EnsureMonths: %v", err)
	}
	if len(created2) != 0 {
		t.Fatalf("second run created %d, want 0 (idempotent)", len(created2))
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
