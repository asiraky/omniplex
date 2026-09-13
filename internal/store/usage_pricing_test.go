package store

import (
	"context"
	"github.com/asiraky/omniplex/internal/proto"
	"path/filepath"
	"testing"
)

func TestUsagePricingSurvivesReopenAndQueryIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := st.CreateSession(ctx, SessionMeta{ID: "s", Harness: "codex", Phase: "idle"}); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Append(ctx, "s", proto.Emit(proto.SessionCreated, proto.SessionCreatedPayload{Model: "gpt-5.4"})); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if _, err := st.Append(ctx, "s", proto.Emit(proto.UsageUpdated, proto.UsageUpdatedPayload{Input: int64(i * 100)})); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate a previously assigned catalogue version with a different rate.
	if _, err := st.db.Exec(`UPDATE usage_pricing SET pricing='{"Version":"original","Rates":{"Input":7}}'`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.Exec(`UPDATE events SET created_at=1 WHERE seq<21`); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	rows, err := st.UsageEvents(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("query loaded full history: %d rows", len(rows))
	}
	if rows[2].Pricing == nil || rows[2].Pricing.Version != "original" || *rows[2].Pricing.Rates.Input != 7 {
		t.Fatalf("reopen repriced history: %+v", rows[2])
	}
}
