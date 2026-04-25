package audit

import (
	"context"
	"testing"
)

func TestMemory_RecordsActions(t *testing.T) {
	t.Parallel()
	m := NewMemory()
	if err := m.Log(context.Background(), Entry{
		Action:  "user.delete",
		Subject: "alice",
		Object:  "user/bob",
	}); err != nil {
		t.Fatalf("Log: %v", err)
	}
	got := m.Snapshot()
	if len(got) != 1 || got[0].Action != "user.delete" {
		t.Fatalf("got %+v", got)
	}
}
