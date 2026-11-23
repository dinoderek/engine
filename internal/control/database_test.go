package control

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
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
			if _, err = c.ExecContext(t.Context(), "INSERT INTO records VALUES('forbidden','0')"); err == nil {
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
	if err = tx.QueryRow("SELECT count(*) FROM records").Scan(&before); err != nil {
		t.Fatal(err)
	}
	if err = s.modify(func(st *state) error { st.Next = 42; return nil }); err != nil {
		t.Fatal(err)
	}
	if err = tx.QueryRow("SELECT count(*) FROM records").Scan(&after); err != nil || before != after {
		t.Fatalf("snapshot %d -> %d: %v", before, after, err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err = s.readers.QueryRow("SELECT count(*) FROM records").Scan(&after); err != nil || after != before+1 {
		t.Fatalf("new snapshot %d -> %d: %v", before, after, err)
	}
}

func TestEveryRecordKindSurvivesReopen(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	s := service(t).Store
	err := s.modify(func(st *state) error {
		st.Next = 10
		st.Hosts["devbox"] = HostRecord{TmuxStart: 1234}
		st.PassAt = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		st.Upgrade = UpgradeRecord{Start: st.PassAt, End: st.PassAt.Add(time.Minute)}
		st.Items["r1"] = Item{ID: "r1", Kind: "repo", Title: "repo"}
		st.Attempts["a2"] = Attempt{ID: "a2", Item: "r1", State: "ended"}
		st.Updates = []Update{{ID: 3, Item: "r1", Body: "persisted", At: st.PassAt}}
		st.Queue["t4"] = QueueEntry{Task: "t4", Repo: "r1", Seq: 3, Phase: QueueWaiting}
		st.Auth["token"] = Actor{Name: "a2", Role: "worker", Attempt: "a2"}
		st.Receipts["request"] = receipt{Digest: "digest", Response: Response{Items: []Item{{ID: "r1"}}}}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := s.snapshot()
	path := s.path
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	got, err := reopened.snapshot()
	if err != nil || !reflect.DeepEqual(want, got) {
		t.Fatalf("reopen: %+v, want %+v (%v)", got, want, err)
	}
}

func TestCommandReadsDoNotQuerySQLite(t *testing.T) {
	s := service(t)
	_, plan := repoPlan(t, s)
	call(t, s, op, Request{Op: "say", ID: plan.ID, Body: "one"})
	if err := s.Store.closeDatabases(); err != nil {
		t.Fatal(err)
	}
	for _, r := range []Request{{Op: "get", ID: plan.ID}, {Op: "list"}, {Op: "updates", ID: plan.ID}, {Op: "session-view"}, {Op: "inbox"}} {
		call(t, s, op, r)
	}
	if _, err := s.Store.snapshot(); err != nil {
		t.Fatal(err)
	}
}
