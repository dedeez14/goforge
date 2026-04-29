package retention

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/dedeez14/goforge/pkg/partition"
)

// --- fakes ---

type fakePartitionOps struct {
	parts       []partition.Partition
	partsErr    error
	dropErr     error
	dropCalls   []dropCall
	dropReturns [][]string // nth call returns dropReturns[n]
	mu          sync.Mutex
}

type dropCall struct {
	parent string
	cutoff time.Time
}

func (f *fakePartitionOps) Partitions(_ context.Context, _ string) ([]partition.Partition, error) {
	if f.partsErr != nil {
		return nil, f.partsErr
	}
	return f.parts, nil
}

func (f *fakePartitionOps) DropBefore(_ context.Context, s partition.Spec, cutoff time.Time) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := len(f.dropCalls)
	f.dropCalls = append(f.dropCalls, dropCall{parent: s.Parent, cutoff: cutoff})
	if f.dropErr != nil {
		return nil, f.dropErr
	}
	if i < len(f.dropReturns) {
		return f.dropReturns[i], nil
	}
	return nil, nil
}

type fakeArchiver struct {
	mu       sync.Mutex
	archived []string
	rowsMap  map[string]int64
	err      error
}

func (f *fakeArchiver) ArchivePartition(_ context.Context, name string, sink Sink) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	f.archived = append(f.archived, name)
	// Write a stub payload so the sink is exercised end-to-end.
	if err := sink.WriteAll(context.Background(), []byte("csv-payload for "+name), "application/gzip"); err != nil {
		return 0, err
	}
	return f.rowsMap[name], nil
}

// memStorage is a tiny storage.Storage stand-in. Only Put is used
// by Runner.
type memStorage struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

func newMemStorage() *memStorage                           { return &memStorage{blobs: map[string][]byte{}} }
func (m *memStorage) Delete(context.Context, string) error { return nil }
func (m *memStorage) PresignPut(context.Context, string, time.Duration, string) (string, error) {
	return "", nil
}
func (m *memStorage) PresignGet(context.Context, string, time.Duration) (string, error) {
	return "", nil
}
func (m *memStorage) List(context.Context, string, int) ([]string, error) { return nil, nil }

func (m *memStorage) Put(_ context.Context, key string, body io.Reader, _ int64, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	buf := &bytes.Buffer{}
	if _, err := io.Copy(buf, body); err != nil {
		return err
	}
	m.blobs[key] = buf.Bytes()
	return nil
}

func (m *memStorage) Get(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.blobs[key]
	if !ok {
		return nil, fmt.Errorf("missing: %s", key)
	}
	return io.NopCloser(bytes.NewReader(b)), nil
}

// --- helpers ---

func mkParts(t *testing.T, parent string, months ...string) []partition.Partition {
	t.Helper()
	out := make([]partition.Partition, 0, len(months))
	for _, m := range months {
		if m == "default" {
			out = append(out, partition.Partition{Name: parent + "_default", IsDefault: true})
			continue
		}
		var y, mo int
		if _, err := fmt.Sscanf(m, "%d-%d", &y, &mo); err != nil {
			t.Fatalf("bad month fixture %q: %v", m, err)
		}
		from := time.Date(y, time.Month(mo), 1, 0, 0, 0, 0, time.UTC)
		out = append(out, partition.Partition{
			Name: fmt.Sprintf("%s_y%04dm%02d", parent, y, mo),
			From: from,
			To:   from.AddDate(0, 1, 0),
		})
	}
	return out
}

func newTestRunner(t *testing.T, mgr *fakePartitionOps, arc *fakeArchiver, now time.Time, plans ...Plan) (*Runner, *memStorage) {
	t.Helper()
	store := newMemStorage()
	r := NewRunner(mgr, arc, store, zerolog.Nop(), Options{
		Plans: plans,
		Now:   func() time.Time { return now },
	})
	return r, store
}

// --- tests ---

func TestNewRunner_ValidatesInputs(t *testing.T) {
	t.Parallel()
	okPlan := Plan{Spec: partition.Spec{Parent: "audit_log", Column: "occurred_at"}, Retain: 24 * time.Hour}
	okOpts := Options{Plans: []Plan{okPlan}}

	cases := []struct {
		name  string
		build func()
	}{
		{"nil manager", func() {
			NewRunner(nil, &fakeArchiver{}, newMemStorage(), zerolog.Nop(), okOpts)
		}},
		{"nil archiver", func() {
			NewRunner(&fakePartitionOps{}, nil, newMemStorage(), zerolog.Nop(), okOpts)
		}},
		{"nil storage", func() {
			NewRunner(&fakePartitionOps{}, &fakeArchiver{}, nil, zerolog.Nop(), okOpts)
		}},
		{"no plans", func() {
			NewRunner(&fakePartitionOps{}, &fakeArchiver{}, newMemStorage(), zerolog.Nop(), Options{})
		}},
		{"zero retain", func() {
			bad := Plan{Spec: partition.Spec{Parent: "x", Column: "y"}, Retain: 0}
			NewRunner(&fakePartitionOps{}, &fakeArchiver{}, newMemStorage(), zerolog.Nop(), Options{Plans: []Plan{bad}})
		}},
		{"duplicate parent", func() {
			dup := []Plan{okPlan, okPlan}
			NewRunner(&fakePartitionOps{}, &fakeArchiver{}, newMemStorage(), zerolog.Nop(), Options{Plans: dup})
		}},
		{"empty parent", func() {
			bad := Plan{Spec: partition.Spec{Parent: "", Column: "y"}, Retain: time.Hour}
			NewRunner(&fakePartitionOps{}, &fakeArchiver{}, newMemStorage(), zerolog.Nop(), Options{Plans: []Plan{bad}})
		}},
		{"empty column", func() {
			bad := Plan{Spec: partition.Spec{Parent: "x", Column: ""}, Retain: time.Hour}
			NewRunner(&fakePartitionOps{}, &fakeArchiver{}, newMemStorage(), zerolog.Nop(), Options{Plans: []Plan{bad}})
		}},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			defer func() {
				if r := recover(); r == nil {
					t.Fatalf("%s: expected panic", c.name)
				}
			}()
			c.build()
		})
	}
}

func TestRun_ArchivesEligibleThenDrops(t *testing.T) {
	t.Parallel()
	// Cutoff at 2026-04-01. Partitions 01/02/03 are eligible (To
	// <= cutoff), partition 04 (To=2026-05-01) and 05 (To=2026-06-01)
	// are still live.
	now := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	retain := 24 * time.Hour // cutoff = now - 24h = 2026-03-31 12:00

	mgr := &fakePartitionOps{
		parts:       mkParts(t, "audit_log", "2026-01", "2026-02", "2026-03", "2026-04", "default"),
		dropReturns: [][]string{{"audit_log_y2026m01", "audit_log_y2026m02"}},
	}
	arc := &fakeArchiver{rowsMap: map[string]int64{
		"audit_log_y2026m01": 100,
		"audit_log_y2026m02": 200,
	}}
	r, store := newTestRunner(t, mgr, arc, now,
		Plan{Spec: partition.Spec{Parent: "audit_log", Column: "occurred_at"}, Retain: retain, StoragePrefix: "archive/audit_log"},
	)

	rep := r.Run(context.Background())
	if err := rep.Err(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 2026-01 (To=02-01) and 2026-02 (To=03-01) are the only ones
	// with To <= cutoff(2026-03-31 12:00). 2026-03 has To=04-01, which
	// is after cutoff.
	wantArchived := []string{"audit_log_y2026m01", "audit_log_y2026m02"}
	if !equalStrings(rep.Archived, wantArchived) {
		t.Fatalf("Archived = %v, want %v", rep.Archived, wantArchived)
	}
	if !equalStrings(arc.archived, wantArchived) {
		t.Fatalf("archiver.archived = %v, want %v", arc.archived, wantArchived)
	}

	// Exactly one DropBefore call with cutoff = now - Retain.
	if got := len(mgr.dropCalls); got != 1 {
		t.Fatalf("DropBefore calls = %d, want 1", got)
	}
	wantCutoff := now.Add(-retain)
	if !mgr.dropCalls[0].cutoff.Equal(wantCutoff) {
		t.Fatalf("DropBefore cutoff = %v, want %v", mgr.dropCalls[0].cutoff, wantCutoff)
	}
	if got, want := mgr.dropCalls[0].parent, "audit_log"; got != want {
		t.Fatalf("DropBefore parent = %q, want %q", got, want)
	}

	// Storage received the archive blobs under the expected keys.
	for _, name := range wantArchived {
		key := "archive/audit_log/" + name + ".csv.gz"
		if _, ok := store.blobs[key]; !ok {
			t.Errorf("storage missing blob %q", key)
		}
	}
}

func TestRun_DefaultPartitionNeverTouched(t *testing.T) {
	t.Parallel()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	mgr := &fakePartitionOps{
		parts: mkParts(t, "audit_log", "2020-01", "default"),
	}
	arc := &fakeArchiver{}
	r, _ := newTestRunner(t, mgr, arc, now,
		Plan{Spec: partition.Spec{Parent: "audit_log", Column: "occurred_at"}, Retain: 24 * time.Hour},
	)

	rep := r.Run(context.Background())
	if err := rep.Err(); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, name := range arc.archived {
		if name == "audit_log_default" {
			t.Fatalf("default partition was archived: %v", arc.archived)
		}
	}
}

func TestRun_ArchiveErrorStopsDropForThatPlan(t *testing.T) {
	t.Parallel()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	mgr := &fakePartitionOps{
		parts: mkParts(t, "audit_log", "2020-01", "2020-02"),
	}
	arc := &fakeArchiver{err: errors.New("s3 down")}
	r, _ := newTestRunner(t, mgr, arc, now,
		Plan{Spec: partition.Spec{Parent: "audit_log", Column: "occurred_at"}, Retain: 24 * time.Hour},
	)

	rep := r.Run(context.Background())
	if rep.Err() == nil {
		t.Fatalf("expected archive error")
	}
	// Drop must NOT run for this plan when archive failed — otherwise
	// we lose data.
	if len(mgr.dropCalls) != 0 {
		t.Fatalf("DropBefore called despite archive failure: %d times", len(mgr.dropCalls))
	}
}

func TestRun_ContinuesAcrossPlansAfterFailure(t *testing.T) {
	t.Parallel()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	mgr1 := &fakePartitionOps{partsErr: errors.New("pg down")}
	mgr2 := &fakePartitionOps{parts: mkParts(t, "jobs", "2020-01")}
	// Combine into a single PartitionOps that routes by parent.
	mgr := &routingOps{byParent: map[string]*fakePartitionOps{
		"audit_log": mgr1,
		"jobs":      mgr2,
	}}
	arc := &fakeArchiver{}
	store := newMemStorage()
	r := NewRunner(mgr, arc, store, zerolog.Nop(), Options{
		Plans: []Plan{
			{Spec: partition.Spec{Parent: "audit_log", Column: "occurred_at"}, Retain: 24 * time.Hour},
			{Spec: partition.Spec{Parent: "jobs", Column: "created_at"}, Retain: 24 * time.Hour},
		},
		Now: func() time.Time { return now },
	})

	rep := r.Run(context.Background())
	if len(rep.Errors) != 1 {
		t.Fatalf("errors = %d, want 1", len(rep.Errors))
	}
	// Second plan still ran.
	if len(arc.archived) == 0 || arc.archived[0] != "jobs_y2020m01" {
		t.Fatalf("second plan did not archive: %v", arc.archived)
	}
}

func TestRun_DropErrorPropagatesAfterArchiveSucceeds(t *testing.T) {
	t.Parallel()
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	mgr := &fakePartitionOps{
		parts:   mkParts(t, "audit_log", "2020-01"),
		dropErr: errors.New("lock timeout"),
	}
	arc := &fakeArchiver{}
	r, store := newTestRunner(t, mgr, arc, now,
		Plan{Spec: partition.Spec{Parent: "audit_log", Column: "occurred_at"}, Retain: 24 * time.Hour},
	)

	rep := r.Run(context.Background())
	if rep.Err() == nil {
		t.Fatalf("expected drop error")
	}
	// Archive still landed in storage (so retry is idempotent).
	if len(store.blobs) != 1 {
		t.Fatalf("storage should still have the archive: got %d blobs", len(store.blobs))
	}
}

func TestHandler_PayloadIgnored(t *testing.T) {
	t.Parallel()
	mgr := &fakePartitionOps{parts: mkParts(t, "audit_log", "2020-01")}
	arc := &fakeArchiver{}
	r, _ := newTestRunner(t, mgr, arc,
		time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
		Plan{Spec: partition.Spec{Parent: "audit_log", Column: "occurred_at"}, Retain: 24 * time.Hour},
	)
	h := r.Handler()
	if err := h(context.Background(), []byte(`{"ignored":true}`)); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(arc.archived) != 1 {
		t.Fatalf("handler did not run plan: archived=%v", arc.archived)
	}
}

func TestObjectKey_PrefixHandling(t *testing.T) {
	t.Parallel()
	cases := []struct {
		prefix, part, want string
	}{
		{"", "p", "p.csv.gz"},
		{"a", "p", "a/p.csv.gz"},
		{"a/", "p", "a/p.csv.gz"},
		{"archive/audit_log", "audit_log_y2026m04", "archive/audit_log/audit_log_y2026m04.csv.gz"},
	}
	for _, c := range cases {
		got := objectKey(c.prefix, c.part)
		if got != c.want {
			t.Errorf("objectKey(%q,%q) = %q, want %q", c.prefix, c.part, got, c.want)
		}
	}
}

// routingOps dispatches Partitions/DropBefore to the per-parent
// fakePartitionOps in byParent — lets a single Runner exercise
// independent fakes.
type routingOps struct {
	byParent map[string]*fakePartitionOps
}

func (r *routingOps) Partitions(ctx context.Context, parent string) ([]partition.Partition, error) {
	op, ok := r.byParent[parent]
	if !ok {
		return nil, fmt.Errorf("routingOps: no fake for %q", parent)
	}
	return op.Partitions(ctx, parent)
}

func (r *routingOps) DropBefore(ctx context.Context, s partition.Spec, cutoff time.Time) ([]string, error) {
	op, ok := r.byParent[s.Parent]
	if !ok {
		return nil, fmt.Errorf("routingOps: no fake for %q", s.Parent)
	}
	return op.DropBefore(ctx, s, cutoff)
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
