package control

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestDriverRunsWithoutSQLiteExecutable(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	s := service(t)
	_, plan := repoPlan(t, s)
	call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "in process"})
	backup := filepath.Join(t.TempDir(), "backup.db")
	if err := s.Store.Backup(backup); err != nil {
		t.Fatal(err)
	}
	copy, err := Open(backup)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = copy.Close() }()
	got, err := copy.snapshot()
	if err != nil || got.Updates[len(got.Updates)-1].Body != "in process" {
		t.Fatalf("backup: %+v %v", got, err)
	}
}

func TestEveryConnectionHasPragmasAndReadersAreReadOnly(t *testing.T) {
	s := service(t).Store
	if s.writer.Stats().MaxOpenConnections != 1 || s.readers.Stats().MaxOpenConnections != 4 {
		t.Fatal("unexpected pool sizes")
	}
	// Hold every reader connection to force the pool to create each one, then
	// discard them and check their replacements as well.
	for range 2 {
		var conns []*sql.Conn
		for range 4 {
			c, err := s.readers.Conn(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			conns = append(conns, c)
			checkPragmas(t, c)
			if _, err = c.ExecContext(t.Context(), "INSERT INTO marks VALUES('forbidden','0')"); err == nil {
				t.Fatal("reader wrote to database")
			}
		}
		for _, c := range conns {
			_ = c.Close()
		}
		s.readers.SetMaxIdleConns(0)
		s.readers.SetMaxIdleConns(4)
	}
	c, err := s.writer.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()
	checkPragmas(t, c)
}

func checkPragmas(t *testing.T, c *sql.Conn) {
	t.Helper()
	for name, want := range map[string]any{"journal_mode": "wal", "synchronous": int64(1), "foreign_keys": int64(1), "busy_timeout": int64(5000)} {
		var got any
		if err := c.QueryRowContext(context.Background(), "PRAGMA "+name).Scan(&got); err != nil || got != want {
			t.Fatalf("%s=%v, want %v (%v)", name, got, want, err)
		}
	}
}

func TestWALReaderKeepsItsSnapshotBesideWriter(t *testing.T) {
	s := service(t).Store
	tx, err := s.readers.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	var before, after int
	if err = tx.QueryRow("SELECT count(*) FROM marks").Scan(&before); err != nil {
		t.Fatal(err)
	}
	edit(t, s, func(tx *Tx) { tx.SetPassAt(time.Now()) })
	if err = tx.QueryRow("SELECT count(*) FROM marks").Scan(&after); err != nil || before != after {
		t.Fatalf("snapshot %d -> %d: %v", before, after, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = s.readers.QueryRow("SELECT count(*) FROM marks").Scan(&after); err != nil || after != before+1 {
		t.Fatalf("new snapshot %d -> %d: %v", before, after, err)
	}
}

func TestEveryRecordKindSurvivesReopen(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	s := service(t).Store
	pass := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	edit(t, s, func(tx *Tx) {
		tx.exec("UPDATE counters SET value=10 WHERE name='next'")
		tx.PutHost("devbox", HostRecord{TmuxStart: 1234})
		tx.SetPassAt(pass)
		tx.SetUpgrade(UpgradeRecord{Start: pass, End: pass.Add(time.Minute)})
		tx.PutItem(Item{ID: "r1", Kind: "repo", Title: "repo", Depends: []string{"r0"}, Requires: []string{"gpu"}})
		tx.PutAttempt(Attempt{ID: "a2", Item: "r1", State: "ended"})
		tx.PutUpdate(Update{ID: 3, Item: "r1", Body: "persisted", At: pass})
		tx.PutQueueEntry(QueueEntry{Task: "t4", Repo: "r1", Seq: 3, Phase: QueueWaiting})
		tx.PutCredential("token", Actor{Name: "a2", Role: "worker", Attempt: "a2"})
		tx.PutReceipt("request", receipt{Digest: "digest", Response: Response{Items: []Item{{ID: "r1"}}}})
	})
	want, _ := s.snapshot()
	path := s.path
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	got, err := reopened.snapshot()
	if err != nil || fingerprint(want) != fingerprint(got) || got.Next != 10 || len(got.Items) != 1 {
		t.Fatalf("reopen: %+v, want %+v (%v)", got, want, err)
	}
	rows, err := reopened.sql("SELECT depends FROM item_deps WHERE item='r1'; SELECT cap FROM item_requires WHERE item='r1'")
	if err != nil || string(rows) != `[{"depends":"r0"}][{"cap":"gpu"}]` {
		t.Fatalf("an item's dependencies and capabilities: %s %v", rows, err)
	}
}
