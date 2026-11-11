package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/asiraky/omniplex/internal/proto"
)

// queryer is the part of *sql.DB and *sql.Conn a pragma read needs.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func pragmaInt(t *testing.T, q queryer, name string) int64 {
	t.Helper()
	var v int64
	if err := q.QueryRowContext(context.Background(), "PRAGMA "+name).Scan(&v); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return v
}

// churn fills table with rows of ~4KB each and then deletes all but keep of
// them, leaving the pages they used on the freelist.
func churn(t *testing.T, db *sql.DB, table string, rows, keep int) {
	t.Helper()
	ctx := context.Background()
	if _, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+table+` (id INTEGER PRIMARY KEY, body BLOB NOT NULL)`); err != nil {
		t.Fatalf("create %s: %v", table, err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(strings.Repeat("x", 4096))
	for i := 1; i <= rows; i++ {
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+table+` (id, body) VALUES (?, ?)`, i, body); err != nil {
			t.Fatalf("insert into %s: %v", table, err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM `+table+` WHERE id > ?`, keep); err != nil {
		t.Fatalf("delete from %s: %v", table, err)
	}
}

// synchronous=NORMAL must hold on every connection the pool hands out, not
// just the one that happened to run the schema. Holding the connections open
// at once forces the pool to open distinct ones.
func TestEveryPooledConnectionRunsWithSynchronousNormal(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	const n = 4
	conns := make([]*sql.Conn, 0, n)
	t.Cleanup(func() {
		for _, c := range conns {
			_ = c.Close()
		}
	})
	for i := 0; i < n; i++ {
		c, err := s.db.Conn(ctx)
		if err != nil {
			t.Fatalf("check out connection %d: %v", i, err)
		}
		conns = append(conns, c)
	}
	for i, c := range conns {
		if got := pragmaInt(t, c, "synchronous"); got != 1 {
			t.Fatalf("connection %d of %d: synchronous = %d, want 1 (NORMAL)", i, n, got)
		}
	}
}

// A database created before incremental auto-vacuum has auto_vacuum=0, which
// can only be changed by a full VACUUM. Open does that once: the file comes
// out in incremental mode, the freelist it had built up is gone, and the
// rows it held are still there.
func TestOpenConvertsALegacyDatabaseToIncrementalVacuumAndKeepsItsRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	raw, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(schema); err != nil {
		t.Fatalf("apply legacy schema: %v", err)
	}
	const keep = 50
	churn(t, raw, "scratch", 3000, keep)
	if got := pragmaInt(t, raw, "auto_vacuum"); got != 0 {
		t.Fatalf("legacy fixture auto_vacuum = %d, want 0 (NONE)", got)
	}
	freeBefore := pragmaInt(t, raw, "freelist_count")
	pagesBefore := pragmaInt(t, raw, "page_count")
	if freeBefore < 1000 {
		t.Fatalf("legacy fixture freelist_count = %d, want a large freelist to reclaim", freeBefore)
	}
	raw.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if got := pragmaInt(t, s.db, "auto_vacuum"); got != 2 {
		t.Fatalf("auto_vacuum after open = %d, want 2 (INCREMENTAL)", got)
	}
	if got := pragmaInt(t, s.db, "freelist_count"); got != 0 {
		t.Fatalf("freelist_count after open = %d, want 0 (was %d)", got, freeBefore)
	}
	if got := pragmaInt(t, s.db, "page_count"); got > pagesBefore/2 {
		t.Fatalf("page_count after open = %d, want well under the %d it had before", got, pagesBefore)
	}

	var count int
	var total int64
	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(length(body)), 0) FROM scratch`).Scan(&count, &total); err != nil {
		t.Fatalf("read surviving rows: %v", err)
	}
	if count != keep || total != int64(keep*4096) {
		t.Fatalf("surviving rows = %d (%d bytes), want %d (%d bytes)", count, total, keep, keep*4096)
	}

	// The schema Open applies works on the converted file.
	mustCreateThread(t, s, "after-vacuum")
	if got := listIDs(t, s); len(got) != 1 || got[0] != "after-vacuum" {
		t.Fatalf("threads after open = %v, want [after-vacuum]", got)
	}

	// Reopening an already converted database leaves it as it is.
	s.Close()
	s2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })
	if got := pragmaInt(t, s2.db, "auto_vacuum"); got != 2 {
		t.Fatalf("auto_vacuum after reopen = %d, want 2 (INCREMENTAL)", got)
	}
	if got := listIDs(t, s2); len(got) != 1 || got[0] != "after-vacuum" {
		t.Fatalf("threads after reopen = %v, want [after-vacuum]", got)
	}
}

// Deleted rows leave their pages on the freelist; Maintain hands them back to
// the filesystem so the file shrinks.
func TestMaintainReturnsFreedPagesToTheFilesystem(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	const keep = 10
	churn(t, s.db, "scratch", 3000, keep)
	freeBefore := pragmaInt(t, s.db, "freelist_count")
	pagesBefore := pragmaInt(t, s.db, "page_count")
	if freeBefore < 1000 {
		t.Fatalf("freelist_count after churn = %d, want a large freelist to reclaim", freeBefore)
	}

	if err := s.Maintain(ctx); err != nil {
		t.Fatalf("maintain: %v", err)
	}

	if got := pragmaInt(t, s.db, "freelist_count"); got != 0 {
		t.Fatalf("freelist_count after maintain = %d, want 0 (was %d)", got, freeBefore)
	}
	pagesAfter := pragmaInt(t, s.db, "page_count")
	if pagesAfter > pagesBefore-freeBefore/2 {
		t.Fatalf("page_count after maintain = %d, want it to drop by most of the %d free pages from %d", pagesAfter, freeBefore, pagesBefore)
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM scratch`).Scan(&count); err != nil {
		t.Fatalf("count surviving rows: %v", err)
	}
	if count != keep {
		t.Fatalf("surviving rows after maintain = %d, want %d", count, keep)
	}

	// A second pass with nothing to reclaim is a no-op, not an error.
	if err := s.Maintain(ctx); err != nil {
		t.Fatalf("second maintain: %v", err)
	}
}

// Command results are only kept long enough to dedupe a client's retry.
// Maintain drops the ones older than commandRetention; one still inside the
// window keeps replaying its result.
func TestMaintainPrunesCommandsOlderThanRetentionAndKeepsRecentOnes(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	for _, id := range []string{"cmd-old", "cmd-recent"} {
		if _, done, err := s.ClaimCommand(ctx, id, "t1"); err != nil || done {
			t.Fatalf("claim %s: done=%v err=%v", id, done, err)
		}
		if err := s.CompleteCommand(ctx, id, map[string]string{"id": id}); err != nil {
			t.Fatalf("complete %s: %v", id, err)
		}
	}
	now := proto.NowMillis()
	backdate := func(id string, age time.Duration) {
		t.Helper()
		if _, err := s.db.ExecContext(ctx, `UPDATE commands SET created_at = ? WHERE command_id = ?`, now-age.Milliseconds(), id); err != nil {
			t.Fatalf("backdate %s: %v", id, err)
		}
	}
	backdate("cmd-old", commandRetention+24*time.Hour)
	backdate("cmd-recent", commandRetention-time.Hour)

	if err := s.Maintain(ctx); err != nil {
		t.Fatalf("maintain: %v", err)
	}

	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM commands WHERE command_id = 'cmd-old'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("cmd-old rows after maintain = %d, want 0", n)
	}
	// The pruned id is free again: claiming it is a fresh claim, not a replay.
	if res, done, err := s.ClaimCommand(ctx, "cmd-old", "t1"); err != nil || done || res != nil {
		t.Fatalf("reclaim pruned cmd-old: res=%s done=%v err=%v, want a fresh claim", res, done, err)
	}

	res, done, err := s.ClaimCommand(ctx, "cmd-recent", "t1")
	if err != nil || !done {
		t.Fatalf("claim cmd-recent after maintain: done=%v err=%v, want its stored result replayed", done, err)
	}
	var got map[string]string
	if err := json.Unmarshal(res, &got); err != nil || got["id"] != "cmd-recent" {
		t.Fatalf("cmd-recent replayed %s (err %v), want {\"id\":\"cmd-recent\"}", res, err)
	}
}
